//go:build integration

package harness_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/mail"
	"github.com/npmulder/ledgerly/internal/reports"
)

func TestReportsExportPackHTTPAssemblesZipAndSharesAttachment(t *testing.T) {
	fakeMailer := mail.NewMemorySender()
	h := harness.New(t, harness.Options{
		ClockStart: time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC),
		MailSender: fakeMailer,
		ReportsPDF: fakeReportsPDFEngine{},
	})
	seedReportsExportQuarter(t, h, fixtures.CompanyVATRegistered(true))

	var pl reportsPLResponse
	getJSON(t, h, "/api/reports/pl?from=2026-04-01&to=2026-06-30", &pl)

	firstLocation := exportLocation(t, h, "/api/reports/export?from=2026-04-01&to=2026-06-30")
	zipBytes := getBytes(t, h, firstLocation)
	files := readZipFiles(t, zipBytes)

	for _, name := range []string{
		"expenses.csv",
		"balance-sheet.csv",
		"pl.csv",
		"pl.pdf",
		"vat.csv",
		"journal.csv",
		"dla.csv",
		"manifest.json",
		"invoices/inv-2026-01.pdf",
	} {
		if _, ok := files[name]; !ok {
			t.Fatalf("export zip missing %s; files=%v", name, sortedKeys(files))
		}
	}
	if len(files) < 7 {
		t.Fatalf("export zip file count = %d, want at least 7", len(files))
	}
	receiptName := firstZipFileWithPrefix(files, "receipts/")
	if receiptName == "" {
		t.Fatalf("export zip missing receipt document; files=%v", sortedKeys(files))
	}
	if !bytes.Equal(files[receiptName], exportReceiptPDF()) {
		t.Fatalf("receipt export %s bytes = %q, want fixture PDF", receiptName, files[receiptName])
	}
	assertJournalCSVBalanced(t, files["journal.csv"])
	assertPLCSVNetProfit(t, files["pl.csv"], pl.NetProfit.AmountMinor)
	assertBalanceSheetCSV(t, files["balance-sheet.csv"], pl.NetProfit.AmountMinor)
	assertExportManifest(t, files["manifest.json"], "2026-04-01", "2026-06-30")

	secondLocation := exportLocation(t, h, "/api/reports/export?from=2026-04-01&to=2026-06-30")
	if secondLocation != firstLocation {
		t.Fatalf("second export location = %q, want same archive %q", secondLocation, firstLocation)
	}

	share := postShareExport(t, h, "accountant@example.test", "2026-04-01", "2026-06-30")
	if share.Status != string(reports.ShareStatusSent) {
		t.Fatalf("share status = %q, want sent; response=%+v", share.Status, share)
	}
	messages := fakeMailer.Messages()
	if len(messages) != 1 {
		t.Fatalf("mail count = %d, want 1", len(messages))
	}
	if got := messages[0].To; got != "accountant@example.test" {
		t.Fatalf("mail To = %q", got)
	}
	if len(messages[0].Attachments) != 1 {
		t.Fatalf("mail attachment count = %d, want 1", len(messages[0].Attachments))
	}
	attachment := messages[0].Attachments[0]
	if attachment.ContentType != "application/zip" || !bytes.Equal(attachment.Bytes, zipBytes) {
		t.Fatalf("share attachment content type/bytes mismatch: %s %d bytes", attachment.ContentType, len(attachment.Bytes))
	}
}

func TestReportsExportPackHTTPOmitsVATWhenNotRegistered(t *testing.T) {
	h := harness.New(t, harness.Options{
		ClockStart: time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC),
		ReportsPDF: fakeReportsPDFEngine{},
	})
	seedReportsExportQuarter(t, h, fixtures.CompanyVATRegistered(false))

	location := exportLocation(t, h, "/api/reports/export?from=2026-04-01&to=2026-06-30")
	files := readZipFiles(t, getBytes(t, h, location))

	if _, ok := files["vat.csv"]; ok {
		t.Fatalf("vat.csv present for non-registered company; files=%v", sortedKeys(files))
	}
	if bytes.Contains(files["manifest.json"], []byte("vat.csv")) {
		t.Fatalf("manifest includes vat.csv for non-registered company: %s", string(files["manifest.json"]))
	}
}

func TestReportsShareExportPackOversizeReturnsManualSend(t *testing.T) {
	fakeMailer := mail.NewMemorySender()
	h := harness.New(t, harness.Options{
		ClockStart:        time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC),
		MailSender:        fakeMailer,
		ReportsPDF:        fakeReportsPDFEngine{},
		ReportsShareLimit: 1,
	})
	seedReportsExportQuarter(t, h, fixtures.CompanyVATRegistered(true))

	share := postShareExport(t, h, "accountant@example.test", "2026-04-01", "2026-06-30")
	if share.Status != string(reports.ShareStatusManualSend) {
		t.Fatalf("share status = %q, want manual-send; response=%+v", share.Status, share)
	}
	if !strings.Contains(share.Message, "Download the zip") {
		t.Fatalf("manual-send message = %q", share.Message)
	}
	if got := len(fakeMailer.Messages()); got != 0 {
		t.Fatalf("mail count = %d, want 0 for oversize", got)
	}
}

func seedReportsExportQuarter(t *testing.T, h *harness.Harness, companyOverrides ...fixtures.CompanyOverride) {
	t.Helper()
	fixtures.Company(t, h, companyOverrides...)
	fixtures.Rates(t, h)
	fabrikam := fixtures.Fabrikam(t, h)

	postSales(t, h, "2026-04-10", 100_000)
	postExpense(t, h, "2026-04-12", 20_000)
	postManualVATReclaim(t, h, "2026-04-20", 4_120)
	seedExportDLAEntry(t, h)
	seedExportInvoicePDF(t, h, fabrikam.ID)
	seedExportBankingReceipt(t, h)
}

func postManualVATReclaim(t testing.TB, h *harness.Harness, date string, amount int64) {
	t.Helper()
	ctx := context.Background()
	entryDate, err := time.ParseInLocation(time.DateOnly, date, time.UTC)
	if err != nil {
		t.Fatalf("parse VAT date: %v", err)
	}
	service := ledger.New(h.LedgerPool, h.Bus)
	tx, err := h.LedgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin VAT tx: %v", err)
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
		Description:  "Q2 VAT reclaim",
		SourceModule: reports.ModuleName,
		SourceRef:    "manual-input-vat:q2-2026",
		Postings: []ledger.NewPosting{
			{AccountCode: "2200-vat-control", Amount: harnessMoney(amount), AmountGBP: harnessMoney(amount)},
			{AccountCode: "1000-cash-gbp", Amount: harnessMoney(-amount), AmountGBP: harnessMoney(-amount)},
		},
	}); err != nil {
		t.Fatalf("post VAT reclaim: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit VAT tx: %v", err)
	}
	committed = true
}

func seedExportDLAEntry(t *testing.T, h *harness.Harness) {
	t.Helper()
	service := dla.NewWithBusAndClock(h.DLAPool, h.Bus, h.Clock, ledger.New(h.LedgerPool, h.Bus))
	if err := service.AddEntry(context.Background(), dla.NewEntry{
		Date:            time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		Kind:            dla.EntryKindRepayment,
		Description:     "Director repaid expenses",
		Amount:          harnessMoney(5_000),
		Source:          "manual:export-repayment",
		CashAccountCode: "1000-cash-gbp",
	}); err != nil {
		t.Fatalf("seed DLA repayment: %v", err)
	}
}

func seedExportInvoicePDF(t *testing.T, h *harness.Harness, clientID string) {
	t.Helper()
	draft := createDraftInvoiceViaHTTP(t, h, clientID)
	patched := performInvoiceRequest(t, h, http.MethodPatch, "/api/invoicing/invoices/"+draft.ID, mustInvoiceJSON(t, map[string]any{
		"issue_date": "2026-06-15",
		"due_date":   "2026-06-30",
		"lines": []map[string]any{{
			"id":          "line-export",
			"description": "Export pack support",
			"qty":         "1",
			"unit_price": map[string]any{
				"amount":   int64(10_000),
				"currency": string(invoicing.CurrencyGBP),
			},
		}},
	}), true)
	if patched.StatusCode != http.StatusOK {
		t.Fatalf("patch export invoice status = %d; body=%s", patched.StatusCode, patched.BodyString())
	}
	send := performInvoiceRequest(t, h, http.MethodPost, "/api/invoicing/invoices/"+draft.ID+"/send", nil, true)
	if send.StatusCode != http.StatusOK {
		t.Fatalf("send export invoice status = %d; body=%s", send.StatusCode, send.BodyString())
	}
	sent := decodeSendInvoiceResponse(t, send)
	storeInvoicePDFAssetForReminderTest(t, h, sent.Invoice.ID, []byte("%PDF-1.4\n% export invoice fixture\n%%EOF\n"))
}

func seedExportBankingReceipt(t *testing.T, h *harness.Harness) {
	t.Helper()
	var account struct {
		ID int64 `json:"id"`
	}
	postJSON(t, h, "/api/banking/accounts", map[string]any{
		"name":     "Revolut Receipts",
		"provider": "revolut",
		"currency": "GBP",
	}, http.StatusCreated, &account)

	statement := fixtures.RevolutCSV(fixtures.RevolutTxn{
		Date:      time.Date(2026, 4, 12, 9, 0, 0, 0, time.UTC),
		ID:        "export-receipt-1",
		Payee:     "Office Supplies Ltd",
		Reference: "receipt export",
		Amount:    money.Money{Amount: -2000, Currency: "GBP"},
		Balance:   money.Money{Amount: -2000, Currency: "GBP"},
	})
	postMultipart(t, h, fmt.Sprintf("/api/banking/accounts/%d/import", account.ID), "file", "receipts.csv", "text/csv", statement, http.StatusOK, nil)

	var txnID int64
	if err := h.BankingPool.QueryRow(context.Background(), `
SELECT id
FROM transactions
WHERE reference = 'receipt export'`).Scan(&txnID); err != nil {
		t.Fatalf("load receipt export transaction: %v", err)
	}
	postMultipart(t, h, fmt.Sprintf("/api/banking/transactions/%d/receipt", txnID), "receipt", "receipt.pdf", "application/pdf", exportReceiptPDF(), http.StatusOK, nil)
}

func exportReceiptPDF() []byte {
	return []byte("%PDF-1.4\n% export receipt fixture\n%%EOF\n")
}

func exportLocation(t *testing.T, h *harness.Harness, path string) string {
	t.Helper()
	client := *h.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, h.BaseURL+path, nil)
	if err != nil {
		t.Fatalf("create export request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET export: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("export status = %d, want 302; body=%s", resp.StatusCode, string(body))
	}
	location := resp.Header.Get("Location")
	if location == "" {
		t.Fatalf("export response missing Location")
	}
	return location
}

func getBytes(t *testing.T, h *harness.Harness, path string) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	if err != nil {
		t.Fatalf("create GET %s: %v", path, err)
	}
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET %s body: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d; body=%s", path, resp.StatusCode, string(body))
	}
	return body
}

func getJSON(t *testing.T, h *harness.Harness, path string, target any) {
	t.Helper()
	body := getBytes(t, h, path)
	if err := json.Unmarshal(body, target); err != nil {
		t.Fatalf("decode %s: %v; body=%s", path, err, string(body))
	}
}

func postJSON(t *testing.T, h *harness.Harness, path string, body map[string]any, wantStatus int, target any) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, path, mustJSON(t, body))
	if err != nil {
		t.Fatalf("create POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST %s response: %v", path, err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST %s status = %d, want %d; body=%s", path, resp.StatusCode, wantStatus, string(responseBody))
	}
	if target != nil {
		if err := json.Unmarshal(responseBody, target); err != nil {
			t.Fatalf("decode POST %s response: %v; body=%s", path, err, string(responseBody))
		}
	}
}

func postMultipart(t *testing.T, h *harness.Harness, path string, field string, filename string, contentType string, data []byte, wantStatus int, target any) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, filename))
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create multipart part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart body: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, path, &body)
	if err != nil {
		t.Fatalf("create multipart POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if strings.Contains(path, "/receipt") {
		req.Method = http.MethodPut
	}
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, path, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read multipart %s response: %v", path, err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; body=%s", req.Method, path, resp.StatusCode, wantStatus, string(responseBody))
	}
	if target != nil {
		if err := json.Unmarshal(responseBody, target); err != nil {
			t.Fatalf("decode multipart %s response: %v; body=%s", path, err, string(responseBody))
		}
	}
}

func postShareExport(t *testing.T, h *harness.Harness, email string, from string, to string) reportsShareResponse {
	t.Helper()
	body := mustJSON(t, map[string]any{
		"email": email,
		"period": map[string]string{
			"from": from,
			"to":   to,
		},
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "/api/reports/share", body)
	if err != nil {
		t.Fatalf("create share request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("POST share: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read share response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("share status = %d; body=%s", resp.StatusCode, string(responseBody))
	}
	var share reportsShareResponse
	if err := json.Unmarshal(responseBody, &share); err != nil {
		t.Fatalf("decode share response: %v; body=%s", err, string(responseBody))
	}
	return share
}

func readZipFiles(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open export zip: %v", err)
	}
	files := map[string][]byte{}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open zip member %s: %v", file.Name, err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read zip member %s: %v", file.Name, err)
		}
		files[file.Name] = body
	}
	return files
}

func firstZipFileWithPrefix(files map[string][]byte, prefix string) string {
	names := sortedKeys(files)
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			return name
		}
	}
	return ""
}

func assertJournalCSVBalanced(t *testing.T, data []byte) {
	t.Helper()
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatalf("read journal.csv: %v", err)
	}
	var debitTotal, creditTotal int64
	for _, row := range rows[1:] {
		debitTotal += parseDecimalMinor(t, row[8])
		creditTotal += parseDecimalMinor(t, row[9])
	}
	if debitTotal != creditTotal {
		t.Fatalf("journal.csv debits = %d, credits = %d", debitTotal, creditTotal)
	}
}

func assertPLCSVNetProfit(t *testing.T, data []byte, wantMinor int64) {
	t.Helper()
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatalf("read pl.csv: %v", err)
	}
	for _, row := range rows[1:] {
		if row[0] == "total" && row[1] == "Net profit" {
			if got := parseDecimalMinor(t, row[2]); got != wantMinor {
				t.Fatalf("pl.csv net profit = %d, want API %d", got, wantMinor)
			}
			return
		}
	}
	t.Fatalf("pl.csv missing Net profit row")
}

func assertBalanceSheetCSV(t *testing.T, data []byte, wantCurrentProfitMinor int64) {
	t.Helper()
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatalf("read balance-sheet.csv: %v", err)
	}
	if len(rows) == 0 || strings.Join(rows[0], ",") != "section,account_code,account_name,amount,currency" {
		t.Fatalf("balance-sheet.csv header = %#v", rows)
	}
	var (
		currentProfitSeen         bool
		totalAssets               int64
		totalLiabilitiesAndEquity int64
	)
	for _, row := range rows[1:] {
		if len(row) != 5 {
			t.Fatalf("balance-sheet.csv row has %d columns: %#v", len(row), row)
		}
		if row[1] == "current-year-profit" {
			currentProfitSeen = true
			if got := parseDecimalMinor(t, row[3]); got != wantCurrentProfitMinor {
				t.Fatalf("balance-sheet.csv current-year profit = %d, want %d", got, wantCurrentProfitMinor)
			}
		}
		if row[0] == "total" && row[2] == "Assets" {
			totalAssets = parseDecimalMinor(t, row[3])
		}
		if row[0] == "total" && row[2] == "Liabilities and equity" {
			totalLiabilitiesAndEquity = parseDecimalMinor(t, row[3])
		}
	}
	if !currentProfitSeen {
		t.Fatalf("balance-sheet.csv missing current-year-profit row: %#v", rows)
	}
	if totalAssets == 0 || totalAssets != totalLiabilitiesAndEquity {
		t.Fatalf("balance-sheet.csv totals assets=%d liabilities+equity=%d", totalAssets, totalLiabilitiesAndEquity)
	}
}

func assertExportManifest(t *testing.T, data []byte, from string, to string) {
	t.Helper()
	var manifest struct {
		Period struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"period"`
		GeneratedAt string `json:"generated_at"`
		AppVersion  string `json:"app_version"`
		Company     struct {
			LegalName string `json:"legal_name"`
			YearEnd   struct {
				Month int `json:"month"`
				Day   int `json:"day"`
			} `json:"year_end"`
		} `json:"company"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v; body=%s", err, string(data))
	}
	if manifest.Period.From != from || manifest.Period.To != to {
		t.Fatalf("manifest period = %+v, want %s to %s", manifest.Period, from, to)
	}
	if manifest.GeneratedAt == "" || manifest.AppVersion != "test" {
		t.Fatalf("manifest generated/app = %q/%q", manifest.GeneratedAt, manifest.AppVersion)
	}
	if manifest.Company.LegalName != "NPM Limited" || manifest.Company.YearEnd.Month != 3 || manifest.Company.YearEnd.Day != 31 {
		t.Fatalf("manifest company = %+v", manifest.Company)
	}
}

func parseDecimalMinor(t *testing.T, value string) int64 {
	t.Helper()
	value = strings.TrimSpace(value)
	sign := int64(1)
	if strings.HasPrefix(value, "-") {
		sign = -1
		value = strings.TrimPrefix(value, "-")
	}
	whole, frac, ok := strings.Cut(value, ".")
	if !ok {
		frac = "00"
	}
	if len(frac) == 1 {
		frac += "0"
	}
	if len(frac) > 2 {
		t.Fatalf("decimal %q has too many places", value)
	}
	major, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		t.Fatalf("parse decimal major %q: %v", value, err)
	}
	minor, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		t.Fatalf("parse decimal minor %q: %v", value, err)
	}
	return sign * (major*100 + minor)
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type fakeReportsPDFEngine struct{}

func (fakeReportsPDFEngine) RenderPLPDF(context.Context, reports.PLPrintPayload) ([]byte, error) {
	return []byte("%PDF-1.4\n% reports pl fixture\n%%EOF\n"), nil
}

type reportsPLResponse struct {
	NetProfit struct {
		AmountMinor int64 `json:"amount_minor"`
	} `json:"net_profit"`
}

type reportsShareResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}
