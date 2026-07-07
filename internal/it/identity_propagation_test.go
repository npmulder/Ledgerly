//go:build integration

package it_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	nethttp "net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/dividends"
	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
	"github.com/npmulder/ledgerly/internal/reports"
)

const identityPropagationCacheControl = "private, max-age=31536000, immutable"

func TestIdentityPropagation(t *testing.T) {
	ctx := context.Background()
	h := harness.New(t, harness.Options{
		ClockStart: time.Date(2025, time.July, 1, 9, 0, 0, 0, time.UTC),
	})
	fixtures.Company(t, h)
	fixtures.Rates(t, h)
	contoso := fixtures.Contoso(t, h)

	identityAPI := identity.NewTransactionalProfileService(testdb.AsModule(t, "identity"), h.Bus, identity.WithDataDir(h.IdentityDataDir))
	assetStore := identityPropagationAssetStore{
		writer:  identity.NewAssetWriter(testdb.AsModule(t, "identity"), h.IdentityDataDir),
		profile: identityAPI,
	}
	invoiceEngine := identityPropagationInvoicePDFEngine{}
	invoiceService := newIdentityPropagationInvoiceService(t, h, identityAPI, invoiceEngine, assetStore)
	dividendEngine := identityPropagationDividendPDFEngine{}
	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	dividendService := newIdentityPropagationDividendService(t, h, identityAPI, invoiceService, ledgerService, dividendEngine, assetStore)

	oldLogo := putIdentityPropagationLogo(t, h, "old-logo.png", identityPropagationPNG(t, color.RGBA{R: 19, G: 67, B: 116, A: 255}))
	oldLogoBytes, oldLogoHeader := getIdentityPropagationAsset(t, h, oldLogo.AssetURL)
	oldLogoDataURI := identityPropagationLogoDataURI(oldLogoBytes)
	assertIdentityPropagationCacheHeaders(t, oldLogoHeader)

	oldInvoice := sendIdentityPropagationInvoice(t, ctx, invoiceService, contoso.ID, "old-retainer", 450_000)
	oldInvoice = waitForIdentityPropagationInvoicePDFAsset(t, ctx, invoiceService, oldInvoice.ID)
	if oldInvoice.PDFAsset == nil {
		t.Fatal("old invoice PDFAsset = nil after wait")
	}
	oldInvoiceBytes, _ := getIdentityPropagationAsset(t, h, *oldInvoice.PDFAsset)

	postIdentityPropagationRetainedEarnings(t, ctx, h, ledgerService, 2_000_000)
	oldDeclaration := declareIdentityPropagationDividend(t, ctx, dividendService, 300_000)
	oldDeclaration = waitForIdentityPropagationDividendAssets(t, ctx, dividendService, oldDeclaration.ID)
	oldVoucherBytes, _ := getIdentityPropagationAsset(t, h, identityPropagationAssetURL(*oldDeclaration.VoucherAsset))
	oldMinutesBytes, _ := getIdentityPropagationAsset(t, h, identityPropagationAssetURL(*oldDeclaration.MinutesAsset))
	beforeHashes := map[string]string{
		"invoice_pdf": sha256String(oldInvoiceBytes),
		"voucher":     sha256String(oldVoucherBytes),
		"minutes":     sha256String(oldMinutesBytes),
	}

	profileUpdated := subscribeIdentityPropagationProfileUpdated(t, h)
	newTradingName := "NPM Digital Studio"
	patchedProfile := patchIdentityPropagationTradingName(t, h, newTradingName)
	if patchedProfile.TradingName != newTradingName {
		t.Fatalf("PATCH profile trading_name = %q, want %q", patchedProfile.TradingName, newTradingName)
	}
	newLogoBytes := identityPropagationPNG(t, color.RGBA{R: 24, G: 140, B: 96, A: 255})
	newLogoDataURI := identityPropagationLogoDataURI(newLogoBytes)
	newLogo := putIdentityPropagationLogo(t, h, "new-logo.png", newLogoBytes)
	assertIdentityPropagationProfileUpdatedEvents(t, profileUpdated, 2)
	if newLogo.AssetURL == oldLogo.AssetURL {
		t.Fatalf("new logo asset URL = old URL %s, want replacement asset", newLogo.AssetURL)
	}

	profile := getIdentityPropagationProfile(t, h)
	if profile.TradingName != newTradingName {
		t.Fatalf("profile trading_name = %q, want %q", profile.TradingName, newTradingName)
	}
	if profile.LogoAssetURL == nil || *profile.LogoAssetURL != newLogo.AssetURL {
		t.Fatalf("profile logo_asset_url = %v, want %s", profile.LogoAssetURL, newLogo.AssetURL)
	}
	dashboard := getIdentityPropagationDashboard(t, h)
	if dashboard.Greeting == nil || dashboard.Greeting.TradingName != newTradingName {
		t.Fatalf("dashboard greeting = %#v, want trading_name %q", dashboard.Greeting, newTradingName)
	}

	newInvoice := sendIdentityPropagationInvoice(t, ctx, invoiceService, contoso.ID, "new-retainer", 125_000)
	newInvoice = waitForIdentityPropagationInvoicePDFAsset(t, ctx, invoiceService, newInvoice.ID)
	newInvoiceBytes, _ := getIdentityPropagationAsset(t, h, *newInvoice.PDFAsset)
	assertIdentityPropagationPDFText(t, newInvoiceBytes, "new invoice PDF", newTradingName, newLogo.AssetURL)

	newDeclaration := declareIdentityPropagationDividend(t, ctx, dividendService, 200_000)
	newDeclaration = waitForIdentityPropagationDividendAssets(t, ctx, dividendService, newDeclaration.ID)
	newVoucherBytes, _ := getIdentityPropagationAsset(t, h, identityPropagationAssetURL(*newDeclaration.VoucherAsset))
	newMinutesBytes, _ := getIdentityPropagationAsset(t, h, identityPropagationAssetURL(*newDeclaration.MinutesAsset))
	assertIdentityPropagationPDFText(t, newVoucherBytes, "new dividend voucher", newTradingName, newLogo.AssetURL, newLogoDataURI)
	assertIdentityPropagationPDFText(t, newMinutesBytes, "new board minutes", newTradingName, newLogo.AssetURL, newLogoDataURI)

	assertIdentityPropagationAssetHash(t, h, *oldInvoice.PDFAsset, beforeHashes["invoice_pdf"], "old invoice PDF")
	assertIdentityPropagationAssetHash(t, h, identityPropagationAssetURL(*oldDeclaration.VoucherAsset), beforeHashes["voucher"], "old dividend voucher")
	assertIdentityPropagationAssetHash(t, h, identityPropagationAssetURL(*oldDeclaration.MinutesAsset), beforeHashes["minutes"], "old board minutes")

	oldPreview := getIdentityPropagationDividendPrintPayload(t, h, oldDeclaration.ID)
	oldCompany := oldPreview.Declaration.CompanySnapshot
	if oldCompany == nil {
		t.Fatal("old declaration preview company_snapshot = nil")
	}
	if oldCompany.TradingName != "NPM Limited" {
		t.Fatalf("old declaration preview trading_name = %q, want declaration-time NPM Limited", oldCompany.TradingName)
	}
	if oldCompany.LogoAssetURL == nil || *oldCompany.LogoAssetURL != oldLogo.AssetURL {
		t.Fatalf("old declaration preview logo_asset_url = %v, want %s", oldCompany.LogoAssetURL, oldLogo.AssetURL)
	}
	if oldCompany.LogoDataURI == nil || *oldCompany.LogoDataURI != oldLogoDataURI {
		t.Fatalf("old declaration preview logo_data_uri = %v, want declaration-time old logo data URI", oldCompany.LogoDataURI)
	}

	assertIdentityPropagationNoCopies(t, ctx, h)

	oldLogoAfter, oldLogoAfterHeader := getIdentityPropagationAsset(t, h, oldLogo.AssetURL)
	assertIdentityPropagationCacheHeaders(t, oldLogoAfterHeader)
	if !bytes.Equal(oldLogoAfter, oldLogoBytes) {
		t.Fatal("old logo asset bytes changed after replacement")
	}
}

type identityPropagationLogoResponse struct {
	AssetID  identity.AssetID `json:"asset_id"`
	AssetURL string           `json:"asset_url"`
}

type identityPropagationProfileResponse struct {
	TradingName  string            `json:"trading_name"`
	LogoAssetID  *identity.AssetID `json:"logo_asset_id"`
	LogoAssetURL *string           `json:"logo_asset_url"`
}

type identityPropagationDashboardResponse struct {
	Greeting *struct {
		TradingName string `json:"trading_name"`
	} `json:"greeting"`
}

type identityPropagationSendResponse struct {
	Invoice invoicing.Invoice `json:"invoice"`
	Number  string            `json:"number"`
}

type identityPropagationAssetStore struct {
	writer  *identity.AssetWriter
	profile interface {
		Asset(context.Context, identity.AssetID) (identity.Asset, error)
	}
}

func (s identityPropagationAssetStore) StoreInvoicePDF(ctx context.Context, pdf []byte) (string, error) {
	id, err := s.storePDF(ctx, pdf)
	if err != nil {
		return "", err
	}
	return identityPropagationAssetURL(id), nil
}

func (s identityPropagationAssetStore) LoadInvoicePDF(ctx context.Context, assetURL string) ([]byte, error) {
	id, err := identityPropagationAssetIDFromURL(assetURL)
	if err != nil {
		return nil, err
	}
	asset, err := s.profile.Asset(ctx, id)
	if err != nil {
		return nil, err
	}
	return append([]byte{}, asset.Bytes...), nil
}

func (s identityPropagationAssetStore) StoreDividendDocumentPDF(ctx context.Context, pdf []byte) (identity.AssetID, error) {
	return s.storePDF(ctx, pdf)
}

func (s identityPropagationAssetStore) storePDF(ctx context.Context, pdf []byte) (identity.AssetID, error) {
	if s.writer == nil {
		return "", fmt.Errorf("identity propagation asset store requires writer")
	}
	return s.writer.StoreAsset(ctx, identity.AssetUpload{
		MIME:  "application/pdf",
		Bytes: pdf,
	})
}

type identityPropagationInvoicePDFEngine struct{}

func (identityPropagationInvoicePDFEngine) RenderInvoicePDF(_ context.Context, payload invoicing.InvoicePrintPayload) ([]byte, error) {
	lines := []string{
		"invoice",
		"trading_name=" + payload.Identity.TradingName,
		"legal_name=" + payload.Identity.LegalName,
	}
	if payload.Identity.LogoAssetURL != nil {
		lines = append(lines, "logo_asset_url="+*payload.Identity.LogoAssetURL)
	}
	return identityPropagationPDF(lines...), nil
}

type identityPropagationDividendPDFEngine struct{}

func (identityPropagationDividendPDFEngine) RenderDividendVoucherPDF(_ context.Context, payload dividends.DividendDocumentPayload) ([]byte, error) {
	return identityPropagationDividendPDF("dividend voucher", payload)
}

func (identityPropagationDividendPDFEngine) RenderBoardMinutesPDF(_ context.Context, payload dividends.DividendDocumentPayload) ([]byte, error) {
	return identityPropagationDividendPDF("board minutes", payload)
}

func identityPropagationDividendPDF(kind string, payload dividends.DividendDocumentPayload) ([]byte, error) {
	company := payload.Declaration.CompanySnapshot
	if company == nil {
		return nil, fmt.Errorf("%s payload missing company snapshot", kind)
	}
	lines := []string{
		kind,
		"trading_name=" + company.TradingName,
		"legal_name=" + company.LegalName,
	}
	if company.LogoAssetURL != nil {
		lines = append(lines, "logo_asset_url="+*company.LogoAssetURL)
	}
	if company.LogoDataURI != nil {
		lines = append(lines, "logo_data_uri="+*company.LogoDataURI)
	}
	return identityPropagationPDF(lines...), nil
}

func newIdentityPropagationInvoiceService(
	t testing.TB,
	h *harness.Harness,
	identityAPI identity.Identity,
	engine invoicing.InvoicePDFEngine,
	assetStore invoicing.InvoicePDFAssetStore,
) *invoicing.Service {
	t.Helper()
	moneyFXService := moneyfx.NewService(moneyfx.NewStore(testdb.AsModule(t, moneyfx.ModuleName)), h.Clock)
	rateLocks := lifecycleRateLocker{service: moneyFXService}
	return invoicing.NewService(
		testdb.AsModule(t, invoicing.ModuleName),
		invoicing.Store{},
		invoicing.WithClock(h.Clock),
		invoicing.WithTodayRate(lifecycleTodayRate),
		invoicing.WithRateLocker(rateLocks),
		invoicing.WithRateLockReader(rateLocks),
		invoicing.WithLedger(ledger.New(h.LedgerPool, h.Bus)),
		invoicing.WithEventBus(h.Bus),
		invoicing.WithIdentity(identityAPI),
		invoicing.WithInvoicePDFEngine(engine),
		invoicing.WithInvoicePDFAssetStore(assetStore),
		invoicing.WithInvoicePDFRetryBackoff(0),
	)
}

func newIdentityPropagationDividendService(
	t testing.TB,
	h *harness.Harness,
	identityAPI identity.Identity,
	invoices *invoicing.Service,
	ledgerService *ledger.Service,
	engine dividends.DividendDocumentPDFEngine,
	assetStore dividends.DividendDocumentAssetStore,
) *dividends.Service {
	t.Helper()
	reportsService := reports.New(ledgerService, identityAPI, invoices, reports.WithClock(h.Clock))
	dlaService := dla.NewWithBusAndClock(h.DLAPool, h.Bus, h.Clock, ledgerService)
	return dividends.New(
		testdb.AsModule(t, dividends.ModuleName),
		ledgerService,
		reportsService,
		identityAPI,
		dividends.WithClock(h.Clock),
		dividends.WithDLA(dlaService),
		dividends.WithBus(h.Bus),
		dividends.WithDocumentPDFEngine(engine),
		dividends.WithDocumentAssetStore(assetStore),
		dividends.WithDocumentRetryBackoff(0),
	)
}

func putIdentityPropagationLogo(t testing.TB, h *harness.Harness, filename string, data []byte) identityPropagationLogoResponse {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="logo"; filename="%s"`, filename))
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create multipart logo field: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart logo field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart logo body: %v", err)
	}

	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodPut, "/api/identity/logo", &body)
	if err != nil {
		t.Fatalf("create PUT /api/identity/logo request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/identity/logo: %v", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read PUT /api/identity/logo body: %v", err)
	}
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("PUT /api/identity/logo status = %d, want 200; body=%s", resp.StatusCode, string(respBody))
	}
	var decoded identityPropagationLogoResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		t.Fatalf("decode logo response: %v; body=%s", err, string(respBody))
	}
	if decoded.AssetID == "" || strings.TrimSpace(decoded.AssetURL) == "" {
		t.Fatalf("logo response missing asset fields: %+v", decoded)
	}
	return decoded
}

func patchIdentityPropagationTradingName(t testing.TB, h *harness.Harness, tradingName string) identityPropagationProfileResponse {
	t.Helper()
	body := identityPropagationJSON(t, h, nethttp.MethodPatch, "/api/identity/profile", map[string]any{
		"trading_name": tradingName,
	}, nethttp.StatusOK)
	var profile identityPropagationProfileResponse
	if err := json.Unmarshal(body, &profile); err != nil {
		t.Fatalf("decode PATCH profile response: %v; body=%s", err, string(body))
	}
	return profile
}

func getIdentityPropagationProfile(t testing.TB, h *harness.Harness) identityPropagationProfileResponse {
	t.Helper()
	body := identityPropagationJSON(t, h, nethttp.MethodGet, "/api/identity/profile", nil, nethttp.StatusOK)
	var profile identityPropagationProfileResponse
	if err := json.Unmarshal(body, &profile); err != nil {
		t.Fatalf("decode profile response: %v; body=%s", err, string(body))
	}
	return profile
}

func getIdentityPropagationDashboard(t testing.TB, h *harness.Harness) identityPropagationDashboardResponse {
	t.Helper()
	body := identityPropagationJSON(t, h, nethttp.MethodGet, "/api/dashboard/summary", nil, nethttp.StatusOK)
	var dashboard identityPropagationDashboardResponse
	if err := json.Unmarshal(body, &dashboard); err != nil {
		t.Fatalf("decode dashboard summary: %v; body=%s", err, string(body))
	}
	return dashboard
}

func identityPropagationJSON(t testing.TB, h *harness.Harness, method string, path string, requestBody any, wantStatus int) []byte {
	t.Helper()
	var reader io.Reader
	if requestBody != nil {
		body, err := json.Marshal(requestBody)
		if err != nil {
			t.Fatalf("marshal %s %s request: %v", method, path, err)
		}
		reader = bytes.NewReader(body)
	}
	req, err := nethttp.NewRequestWithContext(context.Background(), method, path, reader)
	if err != nil {
		t.Fatalf("create %s %s request: %v", method, path, err)
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; body=%s", method, path, resp.StatusCode, wantStatus, string(body))
	}
	return body
}

func getIdentityPropagationAsset(t testing.TB, h *harness.Harness, assetURL string) ([]byte, nethttp.Header) {
	t.Helper()
	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, assetURL, nil)
	if err != nil {
		t.Fatalf("create GET %s request: %v", assetURL, err)
	}
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", assetURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET %s response: %v", assetURL, err)
	}
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET %s status = %d, want 200; body=%s", assetURL, resp.StatusCode, string(body))
	}
	return body, resp.Header.Clone()
}

func sendIdentityPropagationInvoice(
	t testing.TB,
	ctx context.Context,
	service *invoicing.Service,
	clientID string,
	lineID string,
	amount int64,
) invoicing.Invoice {
	t.Helper()
	draft, err := service.CreateDraft(ctx, clientID)
	if err != nil {
		t.Fatalf("CreateDraft(%s) error = %v", clientID, err)
	}
	currency := invoicing.CurrencyEUR
	issueDate := time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC)
	dueDate := time.Date(2025, time.July, 15, 0, 0, 0, 0, time.UTC)
	lines := []invoicing.InvoiceLineInput{{
		ID:          lineID,
		Description: "Identity propagation retainer",
		Qty:         invoicing.MustQuantity("1"),
		UnitPrice:   invoicing.Money{Amount: amount, Currency: string(invoicing.CurrencyEUR)},
	}}
	updated, err := service.UpdateDraft(ctx, draft.ID, invoicing.DraftPatch{
		IssueDate: &issueDate,
		DueDate:   &dueDate,
		Currency:  &currency,
		Lines:     &lines,
	})
	if err != nil {
		t.Fatalf("UpdateDraft(%s) error = %v", draft.ID, err)
	}
	sent, err := service.Send(ctx, updated.ID)
	if err != nil {
		t.Fatalf("Send(%s) error = %v", updated.ID, err)
	}
	return sent
}

func waitForIdentityPropagationInvoicePDFAsset(
	t testing.TB,
	ctx context.Context,
	service *invoicing.Service,
	id string,
) invoicing.Invoice {
	t.Helper()
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		invoice, err := service.Invoice(ctx, id)
		if err != nil {
			t.Fatalf("Invoice(%s) while waiting for PDF asset: %v", id, err)
		}
		if invoice.PDFAsset != nil && strings.TrimSpace(*invoice.PDFAsset) != "" {
			return invoice
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for invoice %s PDF asset", id)
		case <-tick.C:
		}
	}
}

func postIdentityPropagationRetainedEarnings(
	t *testing.T,
	ctx context.Context,
	h *harness.Harness,
	service *ledger.Service,
	amount int64,
) {
	t.Helper()
	tx, err := h.LedgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin retained earnings tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	currency := "GBP"
	cash := ledger.AccountCode("1000-identity-propagation-cash-gbp")
	if _, err := service.EnsureAccount(ctx, tx, ledger.AccountSpec{
		Code:     cash,
		Name:     "Identity propagation fixture cash GBP",
		Type:     ledger.AccountTypeAsset,
		Currency: &currency,
	}); err != nil {
		t.Fatalf("ensure retained earnings cash account: %v", err)
	}
	if _, err := service.Post(ctx, tx, ledger.NewJournalEntry{
		Date:         time.Date(2025, time.March, 31, 0, 0, 0, 0, time.UTC),
		Description:  "identity propagation retained earnings",
		SourceModule: "identity-propagation-test",
		SourceRef:    "retained-earnings",
		Postings: []ledger.NewPosting{
			{AccountCode: cash, Amount: identityPropagationMoney(amount), AmountGBP: identityPropagationMoney(amount)},
			{AccountCode: dividends.RetainedEarningsAccountCode, Amount: identityPropagationMoney(-amount), AmountGBP: identityPropagationMoney(-amount)},
		},
	}); err != nil {
		t.Fatalf("post retained earnings: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit retained earnings tx: %v", err)
	}
	committed = true
}

func declareIdentityPropagationDividend(
	t testing.TB,
	ctx context.Context,
	service *dividends.Service,
	amount int64,
) dividends.Declaration {
	t.Helper()
	declaration, err := service.Declare(ctx, identityPropagationMoney(amount))
	if err != nil {
		t.Fatalf("Declare(%d) error = %v", amount, err)
	}
	return declaration
}

func waitForIdentityPropagationDividendAssets(
	t testing.TB,
	ctx context.Context,
	service *dividends.Service,
	id dividends.DeclarationID,
) dividends.Declaration {
	t.Helper()
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		declaration, err := service.Declaration(ctx, id)
		if err != nil {
			t.Fatalf("Declaration(%s) while waiting for document assets: %v", id, err)
		}
		if declaration.VoucherAsset != nil && *declaration.VoucherAsset != "" &&
			declaration.MinutesAsset != nil && *declaration.MinutesAsset != "" {
			return declaration
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for declaration %s document assets", id)
		case <-tick.C:
		}
	}
}

func getIdentityPropagationDividendPrintPayload(
	t testing.TB,
	h *harness.Harness,
	id dividends.DeclarationID,
) dividends.DividendDocumentPayload {
	t.Helper()
	body := identityPropagationJSON(t, h, nethttp.MethodGet, "/api/dividends/declarations/"+string(id)+"/print", nil, nethttp.StatusOK)
	var payload dividends.DividendDocumentPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode dividend print payload: %v; body=%s", err, string(body))
	}
	return payload
}

func subscribeIdentityPropagationProfileUpdated(t testing.TB, h *harness.Harness) <-chan identity.ProfileUpdated {
	t.Helper()
	events := make(chan identity.ProfileUpdated, 4)
	h.Bus.Subscribe(identity.ProfileUpdatedEventName, func(_ context.Context, _ db.Tx, event bus.Event) error {
		updated, ok := event.(identity.ProfileUpdated)
		if !ok {
			return fmt.Errorf("unexpected identity profile event %T", event)
		}
		events <- updated
		return nil
	})
	return events
}

func assertIdentityPropagationProfileUpdatedEvents(t testing.TB, events <-chan identity.ProfileUpdated, want int) {
	t.Helper()
	for i := 0; i < want; i++ {
		select {
		case <-events:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for identity.ProfileUpdated event %d of %d", i+1, want)
		}
	}
}

func assertIdentityPropagationPDFText(t testing.TB, pdfBytes []byte, label string, wants ...string) {
	t.Helper()
	text, err := extractLifecyclePDFText(pdfBytes)
	if err != nil {
		t.Fatalf("extract %s text: %v", label, err)
	}
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("%s text missing %q:\n%s", label, want, text)
		}
	}
}

func assertIdentityPropagationAssetHash(t testing.TB, h *harness.Harness, assetURL string, want string, label string) {
	t.Helper()
	body, _ := getIdentityPropagationAsset(t, h, assetURL)
	if got := sha256String(body); got != want {
		t.Fatalf("%s hash = %s, want unchanged %s", label, got, want)
	}
}

func assertIdentityPropagationCacheHeaders(t testing.TB, header nethttp.Header) {
	t.Helper()
	if got := header.Get("Cache-Control"); got != identityPropagationCacheControl {
		t.Fatalf("asset Cache-Control = %q, want %q", got, identityPropagationCacheControl)
	}
}

func assertIdentityPropagationNoCopies(t testing.TB, ctx context.Context, h *harness.Harness) {
	t.Helper()
	rows, err := h.DB.Query(ctx, `
SELECT table_schema, table_name, column_name
FROM information_schema.columns
WHERE table_schema NOT IN ('information_schema', 'pg_catalog')
	AND column_name ~* '(trading_name|company_name|logo|company_snapshot)'
ORDER BY table_schema, table_name, column_name`)
	if err != nil {
		t.Fatalf("query information_schema identity copy scan: %v", err)
	}
	defer rows.Close()

	// Declaration snapshots intentionally freeze legal-document inputs at
	// declaration time; they are the only sanctioned non-identity copy.
	allowlist := map[string]string{
		"dividends.declarations.company_snapshot": "declaration-time company identity snapshot for immutable dividend documents",
	}
	var unexpected []string
	for rows.Next() {
		var schema, table, column string
		if err := rows.Scan(&schema, &table, &column); err != nil {
			t.Fatalf("scan information_schema identity copy row: %v", err)
		}
		key := schema + "." + table + "." + column
		if schema == "identity" {
			continue
		}
		if _, ok := allowlist[key]; ok {
			continue
		}
		unexpected = append(unexpected, key)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate information_schema identity copy rows: %v", err)
	}
	if len(unexpected) > 0 {
		t.Fatalf("unexpected identity name/logo copy columns outside identity schema: %v", unexpected)
	}
}

func identityPropagationPDF(lines ...string) []byte {
	var stream strings.Builder
	stream.WriteString("BT\n/F1 12 Tf\n72 740 Td\n")
	for i, line := range lines {
		if i > 0 {
			stream.WriteString("0 -16 Td\n")
		}
		fmt.Fprintf(&stream, "(%s) Tj\n", identityPropagationPDFText(line))
	}
	stream.WriteString("ET\n")

	content := stream.String()
	var pdf bytes.Buffer
	offsets := []int{0}
	writeObject := func(number int, body string) {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", number, body)
	}

	pdf.WriteString("%PDF-1.4\n")
	writeObject(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObject(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObject(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>")
	writeObject(4, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	writeObject(5, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	xrefOffset := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xrefOffset)
	return pdf.Bytes()
}

func identityPropagationPDFText(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`, "\r", " ", "\n", " ")
	return replacer.Replace(value)
}

func identityPropagationPNG(t testing.TB, fill color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.SetRGBA(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return buf.Bytes()
}

func identityPropagationLogoDataURI(data []byte) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}

func identityPropagationMoney(amount int64) money.Money {
	return money.Money{Amount: amount, Currency: "GBP"}
}

func identityPropagationAssetURL(id identity.AssetID) string {
	return "/api/identity/assets/" + string(id)
}

func identityPropagationAssetIDFromURL(assetURL string) (identity.AssetID, error) {
	const prefix = "/api/identity/assets/"
	trimmed := strings.TrimSpace(assetURL)
	if !strings.HasPrefix(trimmed, prefix) {
		return "", fmt.Errorf("asset URL %q does not use %s", assetURL, prefix)
	}
	id := strings.TrimPrefix(trimmed, prefix)
	if id == "" || strings.Contains(id, "/") {
		return "", fmt.Errorf("asset URL %q has invalid id", assetURL)
	}
	return identity.AssetID(id), nil
}

func sha256String(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
