//go:build integration

package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/npmulder/ledgerly/internal/dividends"
	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/it"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
	"github.com/npmulder/ledgerly/internal/reports"
)

func TestDividendsHeadroomHarnessBreakdownScenarios(t *testing.T) {
	tests := []struct {
		name          string
		packRateLine  string
		seed          func(testing.TB, *harness.Harness)
		wantLines     []dividends.MoneyLine
		distributable bool
	}{
		{
			name: "prior-year retained balance only",
			seed: func(t testing.TB, h *harness.Harness) {
				postRetainedEarnings(t, h, "2025-03-31", 1_200_000)
			},
			wantLines: []dividends.MoneyLine{
				moneyLine("Retained earnings b/fwd", 1_200_000),
				moneyLine("Profit YTD (after expenses)", 0),
				moneyLine("Corporation tax provision at 0%", 0),
				moneyLine("Dividends already declared YTD", 0),
				moneyLine("Available to distribute", 1_200_000),
			},
			distributable: true,
		},
		{
			name: "YTD profit increases headroom",
			seed: func(t testing.TB, h *harness.Harness) {
				postRetainedEarnings(t, h, "2025-03-31", 1_200_000)
				postSales(t, h, "2025-05-10", 500_000)
			},
			wantLines: []dividends.MoneyLine{
				moneyLine("Retained earnings b/fwd", 1_200_000),
				moneyLine("Profit YTD (after expenses)", 500_000),
				moneyLine("Corporation tax provision at 0%", 0),
				moneyLine("Dividends already declared YTD", 0),
				moneyLine("Available to distribute", 1_700_000),
			},
			distributable: true,
		},
		{
			name: "mid-year declaration reduces headroom",
			seed: func(t testing.TB, h *harness.Harness) {
				postRetainedEarnings(t, h, "2025-03-31", 1_200_000)
				postSales(t, h, "2025-05-10", 500_000)
				insertDeclaration(t, h, "div-2025-06-01", "2025-06-01", 300_000)
			},
			wantLines: []dividends.MoneyLine{
				moneyLine("Retained earnings b/fwd", 1_200_000),
				moneyLine("Profit YTD (after expenses)", 500_000),
				moneyLine("Corporation tax provision at 0%", 0),
				moneyLine("Dividends already declared YTD", -300_000),
				moneyLine("Available to distribute", 1_400_000),
			},
			distributable: true,
		},
		{
			name: "loss year is negative and non-distributable",
			seed: func(t testing.TB, h *harness.Harness) {
				postExpense(t, h, "2025-05-10", 100_000)
			},
			wantLines: []dividends.MoneyLine{
				moneyLine("Retained earnings b/fwd", 0),
				moneyLine("Profit YTD (after expenses)", -100_000),
				moneyLine("Corporation tax provision at 0%", 0),
				moneyLine("Dividends already declared YTD", 0),
				moneyLine("Available to distribute", -100_000),
			},
			distributable: false,
		},
		{
			name:         "corporate tax provision follows 10 percent fixture pack",
			packRateLine: "standard_rate: \"0.1\"",
			seed: func(t testing.TB, h *harness.Harness) {
				postSales(t, h, "2025-05-10", 1_000_000)
			},
			wantLines: []dividends.MoneyLine{
				moneyLine("Retained earnings b/fwd", 0),
				moneyLine("Profit YTD (after expenses)", 900_000),
				moneyLine("Corporation tax provision at 10%", -90_000),
				moneyLine("Dividends already declared YTD", 0),
				moneyLine("Available to distribute", 810_000),
			},
			distributable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newDividendsHarness(t)
			loadDividendsPack(t, tt.packRateLine)
			tt.seed(t, h)

			breakdown, err := newDividendsService(t, h).Headroom(context.Background())
			if err != nil {
				t.Fatalf("Headroom() error = %v", err)
			}
			if breakdown.FinancialYear != "2025-26" {
				t.Fatalf("FinancialYear = %q, want 2025-26", breakdown.FinancialYear)
			}
			assertDateString(t, breakdown.AsOf, "2025-07-01")
			assertHeadroomLines(t, breakdown.Lines, tt.wantLines)
			if breakdown.Available != tt.wantLines[len(tt.wantLines)-1].Amount {
				t.Fatalf("Available = %+v, want final line %+v", breakdown.Available, tt.wantLines[len(tt.wantLines)-1].Amount)
			}
			if breakdown.Distributable != tt.distributable {
				t.Fatalf("Distributable = %v, want %v", breakdown.Distributable, tt.distributable)
			}
		})
	}
}

func TestDividendsDeclaredInYearHarnessUsesCompanyYearEndBoundary(t *testing.T) {
	h := newDividendsHarness(t)
	loadDividendsPack(t, "")
	service := newDividendsService(t, h)

	insertDeclaration(t, h, "div-2026-03-31", "2026-03-31", 100_000)
	insertDeclaration(t, h, "div-2026-04-01", "2026-04-01", 200_000)

	declared2025, err := service.DeclaredInYear(context.Background(), "2025-26")
	if err != nil {
		t.Fatalf("DeclaredInYear(2025-26) error = %v", err)
	}
	assertHarnessMoney(t, declared2025, 100_000)

	declared2026, err := service.DeclaredInYear(context.Background(), "2026-27")
	if err != nil {
		t.Fatalf("DeclaredInYear(2026-27) error = %v", err)
	}
	assertHarnessMoney(t, declared2026, 200_000)

	history, err := service.History(context.Background())
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	gotIDs := []dividends.DeclarationID{history[0].ID, history[1].ID}
	wantIDs := []dividends.DeclarationID{"div-2026-04-01", "div-2026-03-31"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("History IDs = %#v, want newest first %#v", gotIDs, wantIDs)
	}
	if history[0].VoucherAsset != nil || history[0].MinutesAsset != nil {
		t.Fatalf("History nullable assets = voucher %v minutes %v, want nil", history[0].VoucherAsset, history[0].MinutesAsset)
	}
}

func TestDividendsDeclareHarnessWritesDeclarationLedgerDLAAndEvent(t *testing.T) {
	ctx := context.Background()
	h := newDividendsHarness(t)
	loadDividendsPack(t, "")
	postRetainedEarnings(t, h, "2025-03-31", 2_000_000)
	service := newDividendsService(t, h)
	events := subscribeDividendDeclared(t, h)

	declaration, err := service.Declare(ctx, harnessMoney(123_400))
	if err != nil {
		t.Fatalf("Declare() error = %v", err)
	}

	if declaration.Amount != harnessMoney(123_400) {
		t.Fatalf("Declaration.Amount = %+v, want 123400 GBP", declaration.Amount)
	}
	if declaration.PerShare.Amount*declaration.Shares != declaration.Amount.Amount {
		t.Fatalf("per-share total = %d * %d, want %d", declaration.PerShare.Amount, declaration.Shares, declaration.Amount.Amount)
	}
	if declaration.ShareholderName != "N. Meyer" || declaration.Shares != 100 {
		t.Fatalf("shareholder snapshot = %q/%d, want N. Meyer/100", declaration.ShareholderName, declaration.Shares)
	}

	history, err := service.History(ctx)
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history) != 1 || history[0].ID != declaration.ID {
		t.Fatalf("History() = %#v, want declaration %s", history, declaration.ID)
	}

	sourceRef := "dividends:" + string(declaration.ID)
	assertDividendLedgerEntry(t, ctx, h, sourceRef, 123_400)
	assertDividendDLAEntryAndConsistency(t, ctx, h, sourceRef, 123_400)
	assertDividendDeclaredEvent(t, events, declaration.ID, 123_400)
	it.AssertLedgerBalanced(t, h)
}

func TestDividendsDeclareSchedulesDocumentRenderAfterCommitWithRetry(t *testing.T) {
	ctx := context.Background()
	h := newDividendsHarness(t)
	loadDividendsPack(t, "")
	postRetainedEarnings(t, h, "2025-03-31", 2_000_000)
	engine := newRecordingDividendDocumentPDFEngine(1)
	assets := &recordingDividendDocumentAssetStore{}
	var logs bytes.Buffer
	service := newDividendsService(
		t,
		h,
		dividends.WithDocumentPDFEngine(engine),
		dividends.WithDocumentAssetStore(assets),
		dividends.WithDocumentRetryBackoff(0),
		dividends.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))),
	)

	declaration, err := service.Declare(ctx, harnessMoney(300_000))
	if err != nil {
		t.Fatalf("Declare() error = %v", err)
	}
	if declaration.VoucherAsset != nil || declaration.MinutesAsset != nil {
		t.Fatalf("Declare() assets = voucher %v minutes %v, want async render after response", declaration.VoucherAsset, declaration.MinutesAsset)
	}

	waitForDividendDocumentAssets(t, service, declaration.ID, "test-dividend-document-1", "test-dividend-document-2", func() string {
		return fmt.Sprintf("attempts=%d asset_calls=%d logs=%q", engine.attemptCount(), assets.callCount(), logs.String())
	})
	if got := engine.attemptCount(); got != 3 {
		t.Fatalf("document render attempts = %d, want voucher failure then voucher/minutes retry", got)
	}
	if got := assets.callCount(); got != 2 {
		t.Fatalf("document asset store calls = %d, want voucher and minutes", got)
	}
	if !strings.Contains(logs.String(), "dividend document render failed") {
		t.Fatalf("logs = %q, want render failure log", logs.String())
	}
}

func TestDividendsDocumentRenderFailureLeavesDeclarationAndRecoverySucceeds(t *testing.T) {
	ctx := context.Background()
	h := newDividendsHarness(t)
	loadDividendsPack(t, "")
	postRetainedEarnings(t, h, "2025-03-31", 2_000_000)
	engine := newRecordingDividendDocumentPDFEngine(10)
	assets := &recordingDividendDocumentAssetStore{}
	service := newDividendsService(
		t,
		h,
		dividends.WithDocumentPDFEngine(engine),
		dividends.WithDocumentAssetStore(assets),
		dividends.WithDocumentRetryBackoff(0),
	)

	declaration, err := service.Declare(ctx, harnessMoney(300_000))
	if err != nil {
		t.Fatalf("Declare() error = %v", err)
	}
	waitForDividendDocumentAttempts(t, engine, 3)
	persisted, err := service.Declaration(ctx, declaration.ID)
	if err != nil {
		t.Fatalf("Declaration() after render failures error = %v", err)
	}
	if persisted.VoucherAsset != nil || persisted.MinutesAsset != nil {
		t.Fatalf("Declaration assets after failed renders = voucher %v minutes %v, want nil", persisted.VoucherAsset, persisted.MinutesAsset)
	}

	engine.setFailures(0)
	recovered, err := service.RenderDeclarationDocumentsNow(ctx, declaration.ID)
	if err != nil {
		t.Fatalf("RenderDeclarationDocumentsNow() recovery error = %v", err)
	}
	if recovered.VoucherAsset == nil || *recovered.VoucherAsset != "test-dividend-document-1" ||
		recovered.MinutesAsset == nil || *recovered.MinutesAsset != "test-dividend-document-2" {
		t.Fatalf("recovered assets = voucher %v minutes %v, want test asset ids", recovered.VoucherAsset, recovered.MinutesAsset)
	}
}

func TestDividendsDocumentsAreImmutableAndPreviewPayloadUsesSnapshotsAfterIdentityChange(t *testing.T) {
	ctx := context.Background()
	h := newDividendsHarness(t)
	loadDividendsPack(t, "")
	postRetainedEarnings(t, h, "2025-03-31", 2_000_000)
	engine := newRecordingDividendDocumentPDFEngine(0)
	assets := &recordingDividendDocumentAssetStore{}
	service := newDividendsService(
		t,
		h,
		dividends.WithDocumentPDFEngine(engine),
		dividends.WithDocumentAssetStore(assets),
		dividends.WithDocumentRetryBackoff(0),
	)

	declaration, err := service.Declare(ctx, harnessMoney(300_000))
	if err != nil {
		t.Fatalf("Declare() error = %v", err)
	}
	waitForDividendDocumentAssets(t, service, declaration.ID, "test-dividend-document-1", "test-dividend-document-2", func() string {
		return fmt.Sprintf("attempts=%d asset_calls=%d", engine.attemptCount(), assets.callCount())
	})
	firstVoucher := assets.bytesAt(0)
	firstMinutes := assets.bytesAt(1)

	renamedLegalName := "Renamed NPM Limited"
	renamedTradingName := "Renamed Trading"
	renamedShareholders := []identity.Shareholder{{Name: "Changed Shareholder", Shares: 100, Class: "ordinary £1"}}
	identityService := identity.NewTransactionalProfileService(testdb.AsModule(t, "identity"), h.Bus)
	if err := identityService.UpdateProfile(ctx, identity.UpdateProfilePatch{
		LegalName:    &renamedLegalName,
		TradingName:  &renamedTradingName,
		Shareholders: &renamedShareholders,
	}); err != nil {
		t.Fatalf("UpdateProfile() error = %v", err)
	}

	payload, err := service.DeclarationDocumentPayload(ctx, declaration.ID)
	if err != nil {
		t.Fatalf("DeclarationDocumentPayload() after identity change error = %v", err)
	}
	if payload.Declaration.CompanySnapshot == nil || payload.Declaration.CompanySnapshot.LegalName != "NPM Limited" {
		t.Fatalf("company snapshot = %#v, want original NPM Limited", payload.Declaration.CompanySnapshot)
	}
	if payload.Declaration.ShareholderSnapshot == nil || payload.Declaration.ShareholderSnapshot.Name != "N. Meyer" {
		t.Fatalf("shareholder snapshot = %#v, want original N. Meyer", payload.Declaration.ShareholderSnapshot)
	}

	engine.setVersion("changed-after-identity-update")
	after, err := service.RenderDeclarationDocumentsNow(ctx, declaration.ID)
	if err != nil {
		t.Fatalf("RenderDeclarationDocumentsNow() after identity change error = %v", err)
	}
	if after.VoucherAsset == nil || *after.VoucherAsset != "test-dividend-document-1" ||
		after.MinutesAsset == nil || *after.MinutesAsset != "test-dividend-document-2" {
		t.Fatalf("assets after recovery render = voucher %v minutes %v, want originals", after.VoucherAsset, after.MinutesAsset)
	}
	if got := assets.callCount(); got != 2 {
		t.Fatalf("asset store calls after immutable recovery render = %d, want unchanged 2", got)
	}
	if !bytes.Equal(assets.bytesAt(0), firstVoucher) || !bytes.Equal(assets.bytesAt(1), firstMinutes) {
		t.Fatal("stored dividend document PDF bytes changed after identity update and recovery render")
	}
}

func TestDividendsDocumentPrintPayloadHTTPUsesDeclarationSnapshots(t *testing.T) {
	ctx := context.Background()
	h := newDividendsHarness(t)
	loadDividendsPack(t, "")
	postRetainedEarnings(t, h, "2025-03-31", 2_000_000)
	service := newDividendsService(t, h)

	declaration, err := service.Declare(ctx, harnessMoney(300_000))
	if err != nil {
		t.Fatalf("Declare() error = %v", err)
	}

	renamedLegalName := "Renamed NPM Limited"
	identityService := identity.NewTransactionalProfileService(testdb.AsModule(t, "identity"), h.Bus)
	if err := identityService.UpdateProfile(ctx, identity.UpdateProfilePatch{LegalName: &renamedLegalName}); err != nil {
		t.Fatalf("UpdateProfile() error = %v", err)
	}

	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, "/api/dividends/declarations/"+string(declaration.ID)+"/print", nil)
	if err != nil {
		t.Fatalf("create print payload request: %v", err)
	}
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("GET dividend print payload: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read print payload body: %v", err)
	}
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET dividend print payload status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}
	var payload dividends.DividendDocumentPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode print payload: %v; body=%s", err, string(body))
	}
	if payload.Declaration.CompanySnapshot == nil || payload.Declaration.CompanySnapshot.LegalName != "NPM Limited" {
		t.Fatalf("HTTP company snapshot = %#v, want declaration-time NPM Limited", payload.Declaration.CompanySnapshot)
	}
	if payload.Declaration.HeadroomSnapshot == nil || payload.Declaration.HeadroomSnapshot.Available.Amount != 2_000_000 {
		t.Fatalf("HTTP headroom snapshot = %#v, want declaration-time £20,000.00", payload.Declaration.HeadroomSnapshot)
	}
}

func TestDividendsDeclareRejectsOverHeadroomWithoutWrites(t *testing.T) {
	ctx := context.Background()
	h := newDividendsHarness(t)
	loadDividendsPack(t, "")
	postRetainedEarnings(t, h, "2025-03-31", 100_000)
	service := newDividendsService(t, h)

	_, err := service.Declare(ctx, harnessMoney(100_001))
	if !errors.Is(err, dividends.ErrOverHeadroom) {
		t.Fatalf("Declare(over headroom) error = %v, want ErrOverHeadroom", err)
	}
	if !strings.Contains(err.Error(), "£1,000.00") {
		t.Fatalf("over-headroom error = %q, want distributable figure", err)
	}

	assertCountWhere(t, ctx, h.DB, "dividends.declarations", "true", 0)
	assertCountWhere(t, ctx, h.DB, "ledger.journal_entries", "source_module = 'dividends'", 0)
	assertCountWhere(t, ctx, h.DB, "dla.dla_entries", "source LIKE 'dividends:%'", 0)
	it.AssertLedgerBalanced(t, h)
}

func TestDividendsDeclareTypedValidationFailures(t *testing.T) {
	ctx := context.Background()
	h := newDividendsHarness(t)
	loadDividendsPack(t, "")
	service := newDividendsService(t, h)

	if _, err := service.Validate(ctx, harnessMoney(0)); !errors.Is(err, dividends.ErrNonPositiveAmount) {
		t.Fatalf("Validate(non-positive) error = %v, want ErrNonPositiveAmount", err)
	}

	postExpense(t, h, "2025-05-10", 100_000)
	_, err := service.Declare(ctx, harnessMoney(1_000))
	if !errors.Is(err, dividends.ErrNonDistributableYear) {
		t.Fatalf("Declare(non-distributable year) error = %v, want ErrNonDistributableYear", err)
	}
	assertCountWhere(t, ctx, h.DB, "dividends.declarations", "true", 0)
	it.AssertLedgerBalanced(t, h)
}

func TestDividendsValidateRejectsNonUniformPerShareAmount(t *testing.T) {
	ctx := context.Background()
	h := newDividendsHarness(t)
	loadDividendsPack(t, "")
	postRetainedEarnings(t, h, "2025-03-31", 200_000)
	service := newDividendsService(t, h)

	_, err := service.Validate(ctx, harnessMoney(100_001))
	if !errors.Is(err, dividends.ErrInvalidDeclaration) {
		t.Fatalf("Validate(non-uniform per-share) error = %v, want ErrInvalidDeclaration", err)
	}
	if !strings.Contains(err.Error(), "uniform per-share amount") {
		t.Fatalf("Validate(non-uniform per-share) error = %q, want split detail", err)
	}
	assertCountWhere(t, ctx, h.DB, "dividends.declarations", "true", 0)
	it.AssertLedgerBalanced(t, h)
}

func TestDividendsDeclareConcurrentRaceAllowsOneWinner(t *testing.T) {
	ctx := context.Background()
	h := newDividendsHarness(t)
	loadDividendsPack(t, "")
	postRetainedEarnings(t, h, "2025-03-31", 100_000)
	service := newDividendsService(t, h)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := service.Declare(ctx, harnessMoney(70_000))
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	successes := 0
	overHeadroom := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, dividends.ErrOverHeadroom):
			overHeadroom++
		default:
			t.Fatalf("concurrent Declare() unexpected error = %v", err)
		}
	}
	if successes != 1 || overHeadroom != 1 {
		t.Fatalf("concurrent Declare() successes=%d overHeadroom=%d, want 1/1", successes, overHeadroom)
	}
	assertCountWhere(t, ctx, h.DB, "dividends.declarations", "true", 1)
	assertCountWhere(t, ctx, h.DB, "ledger.journal_entries", "source_module = 'dividends'", 1)
	assertCountWhere(t, ctx, h.DB, "dla.dla_entries", "source LIKE 'dividends:%'", 1)
	dlaService := dla.NewWithBusAndClock(h.DLAPool, h.Bus, h.Clock, ledger.New(h.LedgerPool, h.Bus))
	if report, err := dlaService.CheckConsistency(ctx, h.Clock.Now()); err != nil || !report.Consistent {
		t.Fatalf("DLA CheckConsistency() report=%+v error=%v, want consistent", report, err)
	}
	it.AssertLedgerBalanced(t, h)
}

func TestDividendsValidateMarginalPersonalTaxAcrossAllowanceBoundary(t *testing.T) {
	ctx := context.Background()
	h := newDividendsHarness(t)
	loadDividendsPack(t, "")
	postRetainedEarnings(t, h, "2025-03-31", 2_000_000)
	insertDeclaration(t, h, "div-prior-ytd", "2025-06-01", 1_470_000)
	service := newDividendsService(t, h)

	result, err := service.Validate(ctx, harnessMoney(20_000))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	assertHarnessMoney(t, result.PersonalTax.PriorYTD, 1_470_000)
	assertHarnessMoney(t, result.PersonalTax.WithDividend, 1_490_000)
	assertHarnessMoney(t, result.PersonalTax.PriorEstimate.Total, 0)
	assertHarnessMoney(t, result.PersonalTax.TotalEstimate.Taxable, 15_000)
	assertHarnessMoney(t, result.PersonalTax.TotalEstimate.PerBand[0].Amount, 1_500)
	assertHarnessMoney(t, result.PersonalTax.Marginal, 1_500)
	if !strings.Contains(result.PersonalTax.Message, "£15.00") {
		t.Fatalf("PersonalTax.Message = %q, want £15.00 set-aside", result.PersonalTax.Message)
	}
	if result.Withholding.Policy != "none" || result.Withholding.Applies {
		t.Fatalf("Withholding = %#v, want no withholding informational policy", result.Withholding)
	}
}

func newDividendsHarness(t testing.TB) *harness.Harness {
	t.Helper()

	h := harness.New(t, harness.Options{
		ClockStart: time.Date(2025, time.July, 1, 9, 0, 0, 0, time.UTC),
	})
	fixtures.Company(t, h)
	return h
}

func newDividendsService(t testing.TB, h *harness.Harness, opts ...dividends.Option) *dividends.Service {
	t.Helper()

	identityService := identity.NewTransactionalProfileService(testdb.AsModule(t, "identity"), h.Bus)
	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	reportsService := reports.New(ledgerService, identityService, dividendsTestInvoicing{})
	serviceOpts := []dividends.Option{
		dividends.WithDLA(dla.NewWithBusAndClock(h.DLAPool, h.Bus, h.Clock, ledgerService)),
		dividends.WithBus(h.Bus),
		dividends.WithClock(h.Clock),
	}
	serviceOpts = append(serviceOpts, opts...)
	return dividends.New(
		testdb.AsModule(t, dividends.ModuleName),
		ledgerService,
		reportsService,
		identityService,
		serviceOpts...,
	)
}

func postRetainedEarnings(t testing.TB, h *harness.Harness, date string, amount int64) {
	t.Helper()

	postDividendLedgerEntry(t, h, date, "opening retained earnings", "retained:"+date, []ledger.NewPosting{
		{AccountCode: "1000-cash-gbp", Amount: harnessMoney(amount), AmountGBP: harnessMoney(amount)},
		{AccountCode: dividends.RetainedEarningsAccountCode, Amount: harnessMoney(-amount), AmountGBP: harnessMoney(-amount)},
	})
}

func postSales(t testing.TB, h *harness.Harness, date string, amount int64) {
	t.Helper()

	postDividendLedgerEntry(t, h, date, "dividends test sale", "sale:"+date, []ledger.NewPosting{
		{AccountCode: "1000-cash-gbp", Amount: harnessMoney(amount), AmountGBP: harnessMoney(amount)},
		{AccountCode: "4000-sales", Amount: harnessMoney(-amount), AmountGBP: harnessMoney(-amount)},
	})
}

func postExpense(t testing.TB, h *harness.Harness, date string, amount int64) {
	t.Helper()

	postDividendLedgerEntry(t, h, date, "dividends test expense", "expense:"+date, []ledger.NewPosting{
		{AccountCode: "5010-software", Amount: harnessMoney(amount), AmountGBP: harnessMoney(amount)},
		{AccountCode: "1000-cash-gbp", Amount: harnessMoney(-amount), AmountGBP: harnessMoney(-amount)},
	})
}

func postDividendLedgerEntry(t testing.TB, h *harness.Harness, date string, description string, sourceRef string, postings []ledger.NewPosting) {
	t.Helper()

	ctx := context.Background()
	entryDate, err := time.ParseInLocation(time.DateOnly, date, time.UTC)
	if err != nil {
		t.Fatalf("parse ledger date %q: %v", date, err)
	}
	service := ledger.New(h.LedgerPool, h.Bus)
	tx, err := h.LedgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin ledger tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	ensureCashAccount(t, ctx, service, tx)
	if _, err := service.Post(ctx, tx, ledger.NewJournalEntry{
		Date:         entryDate,
		Description:  description,
		SourceModule: dividends.ModuleName + "-test",
		SourceRef:    sourceRef,
		Postings:     postings,
	}); err != nil {
		t.Fatalf("post dividend ledger entry: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit ledger tx: %v", err)
	}
	committed = true
}

func insertDeclaration(t testing.TB, h *harness.Harness, id string, date string, amount int64) dividends.Declaration {
	t.Helper()

	declaredDate, err := time.ParseInLocation(time.DateOnly, date, time.UTC)
	if err != nil {
		t.Fatalf("parse declaration date %q: %v", date, err)
	}
	stored, err := dividends.Store{}.InsertDeclaration(context.Background(), testdb.AsModule(t, dividends.ModuleName), dividends.Declaration{
		ID:              dividends.DeclarationID(id),
		DeclaredDate:    declaredDate,
		Amount:          harnessMoney(amount),
		PerShare:        harnessMoney(amount / 100),
		Shares:          100,
		ShareholderName: "N. Meyer",
	})
	if err != nil {
		t.Fatalf("insert declaration: %v", err)
	}
	return stored
}

func moneyLine(label string, amount int64) dividends.MoneyLine {
	return dividends.MoneyLine{Label: label, Amount: harnessMoney(amount)}
}

func harnessMoney(amount int64) money.Money {
	return money.Money{Amount: amount, Currency: "GBP"}
}

func assertHeadroomLines(t testing.TB, got []dividends.MoneyLine, want []dividends.MoneyLine) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("headroom lines len = %d, want %d; lines=%#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("headroom line %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func assertHarnessMoney(t testing.TB, got money.Money, wantAmount int64) {
	t.Helper()

	want := harnessMoney(wantAmount)
	if got != want {
		t.Fatalf("money = %+v, want %+v", got, want)
	}
}

func assertDateString(t testing.TB, got time.Time, want string) {
	t.Helper()

	if got.Format(time.DateOnly) != want {
		t.Fatalf("date = %s, want %s", got.Format(time.DateOnly), want)
	}
}

func subscribeDividendDeclared(t testing.TB, h *harness.Harness) <-chan dividends.Declared {
	t.Helper()

	events := make(chan dividends.Declared, 4)
	h.Bus.Subscribe(dividends.DeclaredName, func(_ context.Context, _ db.Tx, event bus.Event) error {
		declared, ok := event.(dividends.Declared)
		if !ok {
			return errors.New("unexpected dividend declared event payload")
		}
		events <- declared
		return nil
	})
	return events
}

func assertDividendDeclaredEvent(
	t testing.TB,
	events <-chan dividends.Declared,
	wantID dividends.DeclarationID,
	wantAmount int64,
) {
	t.Helper()

	select {
	case event := <-events:
		if event.DeclarationID != wantID || event.Amount != harnessMoney(wantAmount) {
			t.Fatalf("Declared event = %#v, want id %s amount %d", event, wantID, wantAmount)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dividends.Declared event")
	}
}

func assertDividendLedgerEntry(t testing.TB, ctx context.Context, h *harness.Harness, sourceRef string, amount int64) {
	t.Helper()

	entries, err := ledger.New(h.LedgerPool, h.Bus).Entries(ctx, ledger.EntryFilter{
		SourceModule: dividends.ModuleName,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("ledger Entries() error = %v", err)
	}
	var entry *ledger.JournalEntry
	for i := range entries {
		if entries[i].SourceRef == sourceRef {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		t.Fatalf("dividend ledger entry source %q not found in %#v", sourceRef, entries)
	}
	if entry.Description != "Dividend declared" {
		t.Fatalf("ledger entry description = %q, want Dividend declared", entry.Description)
	}

	postings := map[ledger.AccountCode]ledger.Posting{}
	for _, posting := range entry.Postings {
		postings[posting.AccountCode] = posting
	}
	assertPostingMoney(t, postings[dividends.RetainedEarningsAccountCode], amount)
	assertPostingMoney(t, postings[dla.DLAAccountCode], -amount)
}

func assertPostingMoney(t testing.TB, posting ledger.Posting, wantAmount int64) {
	t.Helper()

	want := harnessMoney(wantAmount)
	if posting.Amount != want || posting.AmountGBP != want {
		t.Fatalf("posting = %+v, want amount and amount_gbp %+v", posting, want)
	}
}

func assertDividendDLAEntryAndConsistency(t testing.TB, ctx context.Context, h *harness.Harness, sourceRef string, amount int64) {
	t.Helper()

	dlaService := dla.NewWithBusAndClock(h.DLAPool, h.Bus, h.Clock, ledger.New(h.LedgerPool, h.Bus))
	entries, err := dlaService.Ledger(ctx, dla.LedgerFilter{})
	if err != nil {
		t.Fatalf("DLA Ledger() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("DLA entries = %#v, want exactly one dividend credit", entries)
	}
	entry := entries[0]
	if entry.Kind != dla.EntryKindExpenseOwed || entry.Source != sourceRef || entry.Description != "Dividend declared" {
		t.Fatalf("DLA entry = %#v, want dividend external credit source %q", entry, sourceRef)
	}
	assertHarnessMoney(t, entry.Amount, amount)
	assertHarnessMoney(t, entry.RunningBalance, amount)
	if entry.BalanceSide != dla.BalanceSideCredit {
		t.Fatalf("DLA balance side = %s, want CR", entry.BalanceSide)
	}
	if report, err := dlaService.CheckConsistency(ctx, h.Clock.Now()); err != nil || !report.Consistent {
		t.Fatalf("DLA CheckConsistency() report=%+v error=%v, want consistent", report, err)
	}
}

func loadDividendsPack(t testing.TB, corporateRateLine string) {
	t.Helper()

	loadDividendsPackNoCleanup(t, corporateRateLine)
	if strings.TrimSpace(corporateRateLine) != "" {
		t.Cleanup(func() {
			loadDividendsPackNoCleanup(t, "")
		})
	}
}

func loadDividendsPackNoCleanup(t testing.TB, corporateRateLine string) {
	t.Helper()

	path := filepath.Join("..", "..", "..", "packs", "isle-of-man", "1.0", "pack.yaml")
	pack, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pack fixture: %v", err)
	}
	if strings.TrimSpace(corporateRateLine) != "" {
		pack = []byte(strings.Replace(string(pack), "standard_rate: \"0.0\"", corporateRateLine, 1))
	}
	files := fstest.MapFS{
		"packs/isle-of-man/1.0/pack.yaml": {Data: pack},
	}
	if err := jurisdiction.LoadActiveFromFS(files, jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("LoadActiveFromFS() error = %v", err)
	}
}

type dividendsTestInvoicing struct{}

func (dividendsTestInvoicing) Invoice(context.Context, string) (invoicing.Invoice, error) {
	return invoicing.Invoice{}, errors.New("unexpected invoice lookup")
}

func (dividendsTestInvoicing) InvoiceByNumber(context.Context, string) (invoicing.Invoice, error) {
	return invoicing.Invoice{}, errors.New("unexpected invoice lookup")
}

func (dividendsTestInvoicing) InvoicesIssuedBetween(context.Context, time.Time, time.Time) ([]invoicing.Invoice, error) {
	return nil, errors.New("unexpected issued invoice lookup")
}

func (dividendsTestInvoicing) InvoiceVATContextBySendEntryID(context.Context, ledger.EntryID) (invoicing.InvoiceVATContext, error) {
	return invoicing.InvoiceVATContext{}, errors.New("unexpected invoice VAT context lookup")
}

func (dividendsTestInvoicing) Client(context.Context, string) (invoicing.Client, error) {
	return invoicing.Client{}, errors.New("unexpected client lookup")
}

type recordingDividendDocumentPDFEngine struct {
	mu       sync.Mutex
	failures int
	attempts int
	version  string
}

func newRecordingDividendDocumentPDFEngine(failures int) *recordingDividendDocumentPDFEngine {
	return &recordingDividendDocumentPDFEngine{
		failures: failures,
		version:  "initial",
	}
}

func (e *recordingDividendDocumentPDFEngine) RenderDividendVoucherPDF(_ context.Context, payload dividends.DividendDocumentPayload) ([]byte, error) {
	return e.render("voucher", payload)
}

func (e *recordingDividendDocumentPDFEngine) RenderBoardMinutesPDF(_ context.Context, payload dividends.DividendDocumentPayload) ([]byte, error) {
	return e.render("minutes", payload)
}

func (e *recordingDividendDocumentPDFEngine) render(kind string, payload dividends.DividendDocumentPayload) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.attempts++
	if e.attempts <= e.failures {
		return nil, errors.New("forced dividend document render failure")
	}
	company := ""
	if payload.Declaration.CompanySnapshot != nil {
		company = payload.Declaration.CompanySnapshot.LegalName
	}
	data := fmt.Sprintf("%%PDF-1.4\n%s\n%s\n%s\n%%%%EOF\n", kind, company, e.version)
	return []byte(data), nil
}

func (e *recordingDividendDocumentPDFEngine) attemptCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.attempts
}

func (e *recordingDividendDocumentPDFEngine) setFailures(failures int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failures = failures
}

func (e *recordingDividendDocumentPDFEngine) setVersion(version string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.version = version
}

type recordingDividendDocumentAssetStore struct {
	mu    sync.Mutex
	bytes [][]byte
}

func (s *recordingDividendDocumentAssetStore) StoreDividendDocumentPDF(_ context.Context, pdf []byte) (identity.AssetID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bytes = append(s.bytes, append([]byte{}, pdf...))
	return identity.AssetID(fmt.Sprintf("test-dividend-document-%d", len(s.bytes))), nil
}

func (s *recordingDividendDocumentAssetStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bytes)
}

func (s *recordingDividendDocumentAssetStore) bytesAt(i int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte{}, s.bytes[i]...)
}

func waitForDividendDocumentAssets(
	t testing.TB,
	service *dividends.Service,
	id dividends.DeclarationID,
	wantVoucher identity.AssetID,
	wantMinutes identity.AssetID,
	debug func() string,
) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		declaration, err := service.Declaration(context.Background(), id)
		if err != nil {
			t.Fatalf("Declaration(%s) while waiting for documents: %v", id, err)
		}
		if declaration.VoucherAsset != nil && *declaration.VoucherAsset == wantVoucher &&
			declaration.MinutesAsset != nil && *declaration.MinutesAsset == wantMinutes {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for document assets %s/%s; declaration assets = %v/%v; %s",
				wantVoucher,
				wantMinutes,
				declaration.VoucherAsset,
				declaration.MinutesAsset,
				debug(),
			)
		case <-tick.C:
		}
	}
}

func waitForDividendDocumentAttempts(t testing.TB, engine *recordingDividendDocumentPDFEngine, want int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if got := engine.attemptCount(); got >= want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %d document render attempts; got %d", want, engine.attemptCount())
		case <-tick.C:
		}
	}
}
