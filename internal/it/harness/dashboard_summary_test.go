//go:build integration

package harness_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/banking"
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
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
	"github.com/npmulder/ledgerly/internal/reports"
)

func TestDashboardSummaryHarnessScenario(t *testing.T) {
	ctx := context.Background()
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 7, 6, 9, 0, 0, 0, time.UTC)})
	company := fixtures.Company(t, h)
	fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{
		time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC): "0.8500",
		time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC): "0.8500",
		time.Date(2025, 7, 6, 0, 0, 0, 0, time.UTC): "0.8500",
	}))

	invoiceService := newInvoiceService(t, h)
	contoso := fixtures.Contoso(t, h)
	fabrikam := fixtures.Fabrikam(t, h)
	eurInvoice := sendDashboardInvoice(t, invoiceService, contoso.ID, "line-eur", time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC), invoicing.Money{Amount: 100_000, Currency: "EUR"})
	gbpInvoice := sendDashboardInvoice(t, invoiceService, fabrikam.ID, "line-gbp", time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 7, 31, 0, 0, 0, 0, time.UTC), invoicing.Money{Amount: 200_000, Currency: "GBP"})

	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	bankingService := banking.NewService(h.BankingPool, ledgerService)
	gbpAccount, eurAccount := seedDashboardBankAccounts(t, ctx, h, bankingService)
	postDashboardCashBalance(t, ctx, h, ledgerService, gbpAccount, 100_000, 100_000)
	postDashboardCashBalance(t, ctx, h, ledgerService, eurAccount, 200_000, 170_000)
	postDashboardDLARepayment(t, h, gbpAccount.LedgerAccountCode, 25_000)
	seedDashboardReviewQueue(t, ctx, h, bankingService, gbpAccount, eurAccount, eurInvoice, gbpInvoice)

	response := performDashboardHarnessRequest(t, h, true)
	if response.StatusCode != nethttp.StatusOK {
		t.Fatalf("dashboard summary status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusOK, response.BodyString())
	}
	body := decodeDashboardHarnessSummary(t, response)
	if len(body.Errors) != 0 {
		t.Fatalf("dashboard errors = %+v, want none", body.Errors)
	}

	assertDashboardGreeting(t, body.Greeting, "Owner", company.TradingName)
	assertDashboardRate(t, h, body.Rate)
	assertDashboardCash(t, ctx, h, body.Cash, ledgerService, gbpAccount, eurAccount)
	assertDashboardOutstanding(t, ctx, body.Outstanding, invoiceService)
	assertDashboardRecentInvoices(t, ctx, body.RecentInvoices, invoiceService, gbpInvoice, eurInvoice)
	assertDashboardDLA(t, h, body.DLA)
	assertDashboardHeadroom(t, ctx, h, body.DividendHeadroom, invoiceService)
	assertDashboardReconciliation(t, ctx, body.ToReconcile, bankingService, gbpAccount, eurAccount)

	avgLatency := measureDashboardWarmLatency(t, h, 20)
	t.Logf("dashboard summary warm DB latency: avg=%s over 20 sequential requests", avgLatency)
	if avgLatency >= 150*time.Millisecond {
		t.Fatalf("dashboard summary warm DB latency avg = %s, want <150ms", avgLatency)
	}
}

func TestDashboardSummaryHarnessRequiresAuthentication(t *testing.T) {
	h := harness.New(t, harness.Options{})

	response := performDashboardHarnessRequest(t, h, false)
	if response.StatusCode != nethttp.StatusUnauthorized {
		t.Fatalf("dashboard summary status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusUnauthorized, response.BodyString())
	}
	if got := response.Header.Get("Content-Type"); got != httpserver.ProblemContentType {
		t.Fatalf("Content-Type = %q, want %s", got, httpserver.ProblemContentType)
	}
}

func sendDashboardInvoice(
	t testing.TB,
	service *invoicing.Service,
	clientID string,
	lineID string,
	issueDate time.Time,
	dueDate time.Time,
	amount invoicing.Money,
) invoicing.Invoice {
	t.Helper()

	draft, err := service.CreateDraft(context.Background(), clientID)
	if err != nil {
		t.Fatalf("CreateDraft(%s) error = %v", clientID, err)
	}
	currency := invoicing.Currency(amount.Currency)
	lines := []invoicing.InvoiceLineInput{{
		ID:          lineID,
		Description: "Dashboard scenario",
		Qty:         invoicing.MustQuantity("1"),
		UnitPrice:   amount,
	}}
	updated, err := service.UpdateDraft(context.Background(), draft.ID, invoicing.DraftPatch{
		IssueDate: &issueDate,
		DueDate:   &dueDate,
		Currency:  &currency,
		Lines:     &lines,
	})
	if err != nil {
		t.Fatalf("UpdateDraft(%s) error = %v", draft.ID, err)
	}
	sent, err := service.Send(context.Background(), updated.ID)
	if err != nil {
		t.Fatalf("Send(%s) error = %v", updated.ID, err)
	}
	return sent
}

func seedDashboardBankAccounts(
	t *testing.T,
	ctx context.Context,
	h *harness.Harness,
	service *banking.Service,
) (banking.BankAccount, banking.BankAccount) {
	t.Helper()

	gbpAccount, err := service.CreateAccount(ctx, banking.AccountInput{
		Name:     "Operating GBP",
		Provider: banking.ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount(GBP) error = %v", err)
	}
	eurAccount, err := service.CreateAccount(ctx, banking.AccountInput{
		Name:     "Operating EUR",
		Provider: banking.ProviderRevolut,
		Currency: "EUR",
	})
	if err != nil {
		t.Fatalf("CreateAccount(EUR) error = %v", err)
	}

	if h.BankingPool == nil {
		t.Fatal("harness BankingPool is nil")
	}
	return gbpAccount, eurAccount
}

func postDashboardCashBalance(
	t *testing.T,
	ctx context.Context,
	h *harness.Harness,
	service *ledger.Service,
	account banking.BankAccount,
	amount int64,
	amountGBP int64,
) {
	t.Helper()

	currency := account.Currency
	contra := ledger.AccountCode("2999-dashboard-" + strings.ToLower(currency) + "-contra")
	ensureLedgerAccount(t, ctx, h.LedgerPool, service, ledger.AccountSpec{
		Code:     contra,
		Name:     "Dashboard " + currency + " contra",
		Type:     ledger.AccountTypeLiability,
		Currency: &currency,
	})

	tx, err := h.LedgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin dashboard cash tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	if _, err := service.Post(ctx, tx, ledger.NewJournalEntry{
		Date:         time.Date(2025, 7, 6, 0, 0, 0, 0, time.UTC),
		Description:  "Dashboard cash seed " + account.Currency,
		SourceModule: "dashboard-test",
		SourceRef:    "cash:" + account.Currency,
		Postings: []ledger.NewPosting{
			{
				AccountCode: account.LedgerAccountCode,
				Amount:      money.Money{Amount: amount, Currency: account.Currency},
				AmountGBP:   money.Money{Amount: amountGBP, Currency: "GBP"},
			},
			{
				AccountCode: contra,
				Amount:      money.Money{Amount: -amount, Currency: account.Currency},
				AmountGBP:   money.Money{Amount: -amountGBP, Currency: "GBP"},
			},
		},
	}); err != nil {
		t.Fatalf("post dashboard cash balance: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit dashboard cash tx: %v", err)
	}
	committed = true
}

func postDashboardDLARepayment(t *testing.T, h *harness.Harness, cashAccount ledger.AccountCode, amount int64) {
	t.Helper()

	response := performDLARequest(t, h, nethttp.MethodPost, "/api/dla/entries", mustJSON(t, map[string]any{
		"date":              "2025-07-06",
		"kind":              string(dla.EntryKindRepayment),
		"description":       "Dashboard DLA credit fixture",
		"amount":            map[string]any{"amount_minor": amount, "currency": "GBP"},
		"cash_account_code": string(cashAccount),
		"source_ref":        "manual:dashboard-dla-credit",
	}), true)
	if response.StatusCode != nethttp.StatusCreated {
		t.Fatalf("DLA repayment status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusCreated, response.BodyString())
	}
}

func seedDashboardReviewQueue(
	t *testing.T,
	ctx context.Context,
	h *harness.Harness,
	service *banking.Service,
	gbpAccount banking.BankAccount,
	eurAccount banking.BankAccount,
	eurInvoice invoicing.Invoice,
	gbpInvoice invoicing.Invoice,
) {
	t.Helper()

	invoiceTxn := importDashboardBankTxn(t, ctx, h, service, gbpAccount.ID, dashboardBankTxn{
		ID:        "dash-invoice",
		Date:      time.Date(2025, 7, 5, 0, 0, 0, 0, time.UTC),
		Payee:     "Contoso GmbH",
		Reference: "invoice match",
		Amount:    money.Money{Amount: 100_000, Currency: "GBP"},
	})
	dlaTxn := importDashboardBankTxn(t, ctx, h, service, gbpAccount.ID, dashboardBankTxn{
		ID:        "dash-dla",
		Date:      time.Date(2025, 7, 4, 0, 0, 0, 0, time.UTC),
		Payee:     "Director",
		Reference: "director transfer",
		Amount:    money.Money{Amount: -25_000, Currency: "GBP"},
	})
	ruleTxn := importDashboardBankTxn(t, ctx, h, service, gbpAccount.ID, dashboardBankTxn{
		ID:        "dash-rule",
		Date:      time.Date(2025, 7, 3, 0, 0, 0, 0, time.UTC),
		Payee:     "SaaS Tools",
		Reference: "software",
		Amount:    money.Money{Amount: -4_000, Currency: "GBP"},
	})
	importDashboardBankTxn(t, ctx, h, service, gbpAccount.ID, dashboardBankTxn{
		ID:        "dash-unreconciled-gbp",
		Date:      time.Date(2025, 7, 2, 0, 0, 0, 0, time.UTC),
		Payee:     "Coffee Shop",
		Reference: "coffee",
		Amount:    money.Money{Amount: -1_000, Currency: "GBP"},
	})
	importDashboardBankTxn(t, ctx, h, service, eurAccount.ID, dashboardBankTxn{
		ID:        "dash-unreconciled-eur",
		Date:      time.Date(2025, 7, 2, 0, 0, 0, 0, time.UTC),
		Payee:     "Euro Client",
		Reference: "euro receipt",
		Amount:    money.Money{Amount: 200_000, Currency: "EUR"},
	})

	invoiceTarget := ""
	if eurInvoice.Number != nil {
		invoiceTarget = *eurInvoice.Number
	}
	mustRecordDashboardSuggestion(t, ctx, service, invoiceTxn, banking.SuggestionKindInvoiceMatch, 0.98, invoiceTarget, "amount + payee + date")
	mustRecordDashboardSuggestion(t, ctx, service, dlaTxn, banking.SuggestionKindDLA, 0.88, "manual:dashboard-dla-credit", "director repayment")
	gbpTarget := ""
	if gbpInvoice.Number != nil {
		gbpTarget = *gbpInvoice.Number
	}
	mustRecordDashboardSuggestion(t, ctx, service, ruleTxn, banking.SuggestionKindPayeeRule, 0.72, gbpTarget, "matched payee rule")
}

type dashboardBankTxn struct {
	ID        string
	Date      time.Time
	Payee     string
	Reference string
	Amount    money.Money
}

func importDashboardBankTxn(
	t *testing.T,
	ctx context.Context,
	h *harness.Harness,
	service *banking.Service,
	accountID banking.AccountID,
	txn dashboardBankTxn,
) banking.TransactionID {
	t.Helper()

	_, err := service.ImportCSV(ctx, accountID, banking.ImportFile{
		Filename: txn.ID + ".csv",
		Reader:   bytes.NewReader(dashboardRevolutCSV(txn)),
	})
	if err != nil {
		t.Fatalf("ImportCSV(%s) error = %v", txn.ID, err)
	}
	var id int64
	if err := h.BankingPool.QueryRow(ctx, `
SELECT id
FROM transactions
WHERE account_id = $1
	AND reference = $2
ORDER BY id DESC
LIMIT 1`, int64(accountID), txn.Reference).Scan(&id); err != nil {
		t.Fatalf("load dashboard transaction %q: %v", txn.Reference, err)
	}
	return banking.TransactionID(id)
}

func dashboardRevolutCSV(txn dashboardBankTxn) []byte {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	if err := writer.Write([]string{
		"Date started (UTC)",
		"Date completed (UTC)",
		"ID",
		"Type",
		"Description",
		"Reference",
		"Amount",
		"Fee",
		"Currency",
		"State",
		"Balance",
	}); err != nil {
		panic(err)
	}
	if err := writer.Write([]string{
		txn.Date.Format("2006-01-02 15:04:05"),
		txn.Date.Format("2006-01-02 15:04:05"),
		txn.ID,
		"CARD_PAYMENT",
		txn.Payee,
		txn.Reference,
		formatDashboardAmount(txn.Amount),
		"0.00",
		txn.Amount.Currency,
		"COMPLETED",
		formatDashboardAmount(txn.Amount),
	}); err != nil {
		panic(err)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func formatDashboardAmount(amount money.Money) string {
	sign := ""
	value := amount.Amount
	if value < 0 {
		sign = "-"
		value = -value
	}
	return fmt.Sprintf("%s%d.%02d", sign, value/100, value%100)
}

func mustRecordDashboardSuggestion(
	t *testing.T,
	ctx context.Context,
	service *banking.Service,
	txnID banking.TransactionID,
	kind banking.SuggestionKind,
	confidence float64,
	target string,
	explanation string,
) {
	t.Helper()
	if _, err := service.RecordSuggestion(ctx, banking.SuggestionInput{
		TransactionID: txnID,
		Kind:          kind,
		Confidence:    confidence,
		Target:        target,
		Explanation:   explanation,
		CreatedBy:     "dashboard-test",
	}); err != nil {
		t.Fatalf("RecordSuggestion(%d, %s) error = %v", txnID, kind, err)
	}
}

func assertDashboardGreeting(t testing.TB, got *dashboardGreetingBody, wantUser string, wantTradingName string) {
	t.Helper()
	if got == nil {
		t.Fatal("greeting = nil")
	}
	if got.UserName != wantUser || got.TradingName != wantTradingName {
		t.Fatalf("greeting = %+v, want user %q trading %q", got, wantUser, wantTradingName)
	}
}

func assertDashboardRate(t testing.TB, h *harness.Harness, got *dashboardRateBody) {
	t.Helper()
	if got == nil {
		t.Fatal("rate = nil")
	}
	service := moneyfx.NewService(moneyfx.NewStore(testdb.AsModule(t, moneyfx.ModuleName)), h.Clock)
	rate, fetchedAt, err := service.TodayRate(context.Background(), "EUR", "GBP")
	if err != nil {
		t.Fatalf("TodayRate(EUR,GBP) error = %v", err)
	}
	if got.Rate != rate.Value || got.RateDate != rate.RateDate.Format(time.DateOnly) || !got.FetchedAt.Equal(fetchedAt.UTC()) {
		t.Fatalf("rate = %+v, want rate %s date %s fetched %s", got, rate.Value, rate.RateDate.Format(time.DateOnly), fetchedAt.UTC())
	}
}

func assertDashboardCash(
	t testing.TB,
	ctx context.Context,
	h *harness.Harness,
	got *dashboardCashBody,
	ledgerService *ledger.Service,
	gbpAccount banking.BankAccount,
	eurAccount banking.BankAccount,
) {
	t.Helper()
	if got == nil {
		t.Fatal("cash = nil")
	}
	if len(got.Accounts) != 2 {
		t.Fatalf("cash accounts = %+v, want 2", got.Accounts)
	}
	fxService := moneyfx.NewService(moneyfx.NewStore(testdb.AsModule(t, moneyfx.ModuleName)), h.Clock)
	wantByCode := make(map[ledger.AccountCode]dashboardCashAccountBody)
	var wantTotal money.Money = money.Zero("GBP")
	for _, account := range []banking.BankAccount{gbpAccount, eurAccount} {
		balance, err := ledgerService.AccountBalance(ctx, account.LedgerAccountCode, time.Date(2025, 7, 6, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("AccountBalance(%s) error = %v", account.LedgerAccountCode, err)
		}
		native := balance.Native[0]
		gbp, err := fxService.ToGBP(ctx, native, time.Date(2025, 7, 6, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("ToGBP(%+v) error = %v", native, err)
		}
		wantTotal, err = wantTotal.Add(gbp)
		if err != nil {
			t.Fatalf("cash total add error = %v", err)
		}
		wantByCode[account.LedgerAccountCode] = dashboardCashAccountBody{
			ID:                int64(account.ID),
			Name:              account.Name,
			Currency:          account.Currency,
			LedgerAccountCode: account.LedgerAccountCode,
			NativeBalance:     native,
			GBPBalance:        gbp,
		}
	}
	assertDashboardMoney(t, got.TotalGBP, wantTotal.Amount, wantTotal.Currency)
	for _, account := range got.Accounts {
		want, ok := wantByCode[account.LedgerAccountCode]
		if !ok {
			t.Fatalf("unexpected cash account %+v", account)
		}
		if account.ID != want.ID || account.Name != want.Name || account.Currency != want.Currency {
			t.Fatalf("cash account = %+v, want %+v", account, want)
		}
		assertDashboardMoney(t, account.NativeBalance, want.NativeBalance.Amount, want.NativeBalance.Currency)
		assertDashboardMoney(t, account.GBPBalance, want.GBPBalance.Amount, want.GBPBalance.Currency)
	}
}

func assertDashboardOutstanding(t testing.TB, ctx context.Context, got *dashboardOutstandingBody, service *invoicing.Service) {
	t.Helper()
	if got == nil {
		t.Fatal("outstanding = nil")
	}
	filter := invoicing.InvoiceListFilter{Statuses: []invoicing.InvoiceStatus{invoicing.InvoiceStatusSent, invoicing.InvoiceStatusOverdue}}
	totals, err := service.Totals(ctx, filter)
	if err != nil {
		t.Fatalf("Totals(outstanding) error = %v", err)
	}
	list, err := service.List(ctx, invoicing.InvoiceListFilter{Statuses: filter.Statuses, Limit: invoicing.MaxInvoiceListLimit})
	if err != nil {
		t.Fatalf("List(outstanding) error = %v", err)
	}
	if len(got.Totals) != len(totals.Subtotals) {
		t.Fatalf("outstanding totals = %+v, want %+v", got.Totals, totals.Subtotals)
	}
	for i := range totals.Subtotals {
		assertDashboardMoney(t, got.Totals[i], totals.Subtotals[i].Amount, totals.Subtotals[i].Currency)
	}
	assertDashboardMoney(t, got.TotalGBP, totals.TotalGBP.Amount, totals.TotalGBP.Currency)
	earliest := earliestDashboardDueDate(t, list.Invoices)
	if got.EarliestDueDate == nil || *got.EarliestDueDate != earliest {
		t.Fatalf("earliest_due_date = %v, want %s", got.EarliestDueDate, earliest)
	}
}

func earliestDashboardDueDate(t testing.TB, invoices []invoicing.InvoiceListItem) string {
	t.Helper()
	if len(invoices) == 0 {
		return ""
	}
	dates := make([]time.Time, 0, len(invoices))
	for _, invoice := range invoices {
		dates = append(dates, invoice.DueDate.UTC())
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	return dates[0].Format(time.DateOnly)
}

func assertDashboardRecentInvoices(
	t testing.TB,
	ctx context.Context,
	got *[]dashboardRecentInvoiceBody,
	service *invoicing.Service,
	first invoicing.Invoice,
	second invoicing.Invoice,
) {
	t.Helper()
	if got == nil {
		t.Fatal("recentInvoices = nil")
	}
	list, err := service.List(ctx, invoicing.InvoiceListFilter{Limit: 5})
	if err != nil {
		t.Fatalf("List(recent) error = %v", err)
	}
	if len(*got) != len(list.Invoices) || len(*got) != 2 {
		t.Fatalf("recent invoices = %+v, want 2 from list %+v", *got, list.Invoices)
	}
	gotFirst := (*got)[0]
	if gotFirst.ID != first.ID {
		t.Fatalf("recent first id = %q, want %q", gotFirst.ID, first.ID)
	}
	if first.Number == nil || gotFirst.Number == nil || *gotFirst.Number != *first.Number {
		t.Fatalf("recent first number = %v, want %v", gotFirst.Number, first.Number)
	}
	assertDashboardMoney(t, gotFirst.Amount, first.Totals.Total.Amount, first.Totals.Total.Currency)
	gotSecond := (*got)[1]
	if gotSecond.ID != second.ID {
		t.Fatalf("recent second id = %q, want %q", gotSecond.ID, second.ID)
	}
	if second.Number == nil || gotSecond.Number == nil || *gotSecond.Number != *second.Number {
		t.Fatalf("recent second number = %v, want %v", gotSecond.Number, second.Number)
	}
	if gotSecond.DaysOverdue == nil || *gotSecond.DaysOverdue != 21 {
		t.Fatalf("recent overdue days = %v, want 21", gotSecond.DaysOverdue)
	}
}

func assertDashboardDLA(t *testing.T, h *harness.Harness, got *dashboardDLABody) {
	t.Helper()
	if got == nil {
		t.Fatal("dla = nil")
	}
	response := performDLARequest(t, h, nethttp.MethodGet, "/api/dla/balance", nil, true)
	if response.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET /api/dla/balance status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusOK, response.BodyString())
	}
	underlying := decodeDLABalanceResponse(t, response)
	assertDashboardMoney(t, got.Balance, underlying.Balance.AmountMinor, underlying.Balance.Currency)
	if got.Status != underlying.Status {
		t.Fatalf("dla status = %q, want %q", got.Status, underlying.Status)
	}
}

func assertDashboardHeadroom(t testing.TB, ctx context.Context, h *harness.Harness, got *dashboardDividendHeadroomBody, invoiceService *invoicing.Service) {
	t.Helper()
	if got == nil {
		t.Fatal("dividendHeadroom = nil")
	}
	identityService := identity.NewTransactionalProfileService(testdb.AsModule(t, "identity"), h.Bus)
	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	reportsService := reports.New(ledgerService, identityService, invoiceService, reports.WithClock(h.Clock))
	dividendService := dividends.New(h.DividendsPool, ledgerService, reportsService, identityService, dividends.WithClock(h.Clock))
	headroom, err := dividendService.Headroom(ctx)
	if err != nil {
		t.Fatalf("Headroom() error = %v", err)
	}
	assertDashboardMoney(t, got.Available, headroom.Available.Amount, headroom.Available.Currency)
	if got.Distributable != headroom.Distributable {
		t.Fatalf("distributable = %v, want %v", got.Distributable, headroom.Distributable)
	}
}

func assertDashboardReconciliation(
	t testing.TB,
	ctx context.Context,
	got *dashboardToReconcileBody,
	service *banking.Service,
	gbpAccount banking.BankAccount,
	eurAccount banking.BankAccount,
) {
	t.Helper()
	if got == nil {
		t.Fatal("toReconcile = nil")
	}
	if len(got.Accounts) != 2 {
		t.Fatalf("toReconcile accounts = %+v, want 2", got.Accounts)
	}
	wantCounts := map[int64]int{}
	for _, account := range []banking.BankAccount{gbpAccount, eurAccount} {
		count, err := service.UnreconciledCount(ctx, account.ID)
		if err != nil {
			t.Fatalf("UnreconciledCount(%d) error = %v", account.ID, err)
		}
		wantCounts[int64(account.ID)] = count
	}
	for _, account := range got.Accounts {
		if account.UnreconciledCount != wantCounts[account.ID] {
			t.Fatalf("account %d unreconciled_count = %d, want %d", account.ID, account.UnreconciledCount, wantCounts[account.ID])
		}
	}
	queue, err := service.ReviewQueue(ctx)
	if err != nil {
		t.Fatalf("ReviewQueue() error = %v", err)
	}
	if len(got.ReviewQueue) != 3 || len(queue.InvoiceMatches)+len(queue.DLA)+len(queue.PayeeRules) != 3 {
		t.Fatalf("review queue = %+v, underlying = %+v", got.ReviewQueue, queue)
	}
	if got.ReviewQueue[0].Kind != banking.SuggestionKindInvoiceMatch || got.ReviewQueue[0].Payee != "Contoso GmbH" || got.ReviewQueue[0].Confidence != 0.98 {
		t.Fatalf("top review item = %+v, want invoice match for Contoso", got.ReviewQueue[0])
	}
}

func assertDashboardMoney(t testing.TB, got money.Money, wantAmount int64, wantCurrency string) {
	t.Helper()
	if got.Amount != wantAmount || got.Currency != wantCurrency {
		t.Fatalf("money = %+v, want %d %s", got, wantAmount, wantCurrency)
	}
}

type dashboardHarnessResponse struct {
	StatusCode int
	Header     nethttp.Header
	Body       []byte
}

func (r dashboardHarnessResponse) BodyString() string {
	return string(r.Body)
}

func performDashboardHarnessRequest(t testing.TB, h *harness.Harness, authenticated bool) dashboardHarnessResponse {
	t.Helper()

	target := "/api/dashboard/summary"
	if !authenticated {
		target = h.BaseURL + target
	}
	request, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("create GET /api/dashboard/summary request: %v", err)
	}

	var response *nethttp.Response
	if authenticated {
		response, err = h.Do(request)
	} else {
		response, err = nethttp.DefaultClient.Do(request)
	}
	if err != nil {
		t.Fatalf("GET /api/dashboard/summary: %v", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read dashboard summary response: %v", err)
	}
	return dashboardHarnessResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header,
		Body:       body,
	}
}

func measureDashboardWarmLatency(t testing.TB, h *harness.Harness, iterations int) time.Duration {
	t.Helper()
	if iterations <= 0 {
		t.Fatalf("iterations must be positive")
	}
	warm := performDashboardHarnessRequest(t, h, true)
	if warm.StatusCode != nethttp.StatusOK {
		t.Fatalf("warm dashboard request status = %d, want %d; body=%s", warm.StatusCode, nethttp.StatusOK, warm.BodyString())
	}
	start := time.Now()
	for i := 0; i < iterations; i++ {
		response := performDashboardHarnessRequest(t, h, true)
		if response.StatusCode != nethttp.StatusOK {
			t.Fatalf("dashboard latency request %d status = %d, want %d; body=%s", i, response.StatusCode, nethttp.StatusOK, response.BodyString())
		}
	}
	return time.Since(start) / time.Duration(iterations)
}

func decodeDashboardHarnessSummary(t testing.TB, response dashboardHarnessResponse) dashboardHarnessSummary {
	t.Helper()
	var body dashboardHarnessSummary
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatalf("decode dashboard summary: %v; body=%s", err, response.BodyString())
	}
	return body
}

type dashboardHarnessSummary struct {
	Cash             *dashboardCashBody             `json:"cash"`
	Outstanding      *dashboardOutstandingBody      `json:"outstanding"`
	DLA              *dashboardDLABody              `json:"dla"`
	DividendHeadroom *dashboardDividendHeadroomBody `json:"dividendHeadroom"`
	RecentInvoices   *[]dashboardRecentInvoiceBody  `json:"recentInvoices"`
	ToReconcile      *dashboardToReconcileBody      `json:"toReconcile"`
	Rate             *dashboardRateBody             `json:"rate"`
	Greeting         *dashboardGreetingBody         `json:"greeting"`
	Errors           []dashboardErrorBody           `json:"errors"`
}

type dashboardCashBody struct {
	Accounts []dashboardCashAccountBody `json:"accounts"`
	TotalGBP money.Money                `json:"total_gbp"`
}

type dashboardCashAccountBody struct {
	ID                int64              `json:"id"`
	Name              string             `json:"name"`
	Currency          string             `json:"currency"`
	LedgerAccountCode ledger.AccountCode `json:"ledger_account_code"`
	NativeBalance     money.Money        `json:"native_balance"`
	GBPBalance        money.Money        `json:"gbp_balance"`
}

type dashboardOutstandingBody struct {
	Totals          []money.Money `json:"totals"`
	TotalGBP        money.Money   `json:"total_gbp"`
	EarliestDueDate *string       `json:"earliest_due_date"`
}

type dashboardDLABody struct {
	Balance money.Money `json:"balance"`
	Status  string      `json:"status"`
}

type dashboardDividendHeadroomBody struct {
	Available     money.Money `json:"available"`
	Distributable bool        `json:"distributable"`
}

type dashboardRecentInvoiceBody struct {
	ID          string      `json:"id"`
	Number      *string     `json:"number"`
	Client      string      `json:"client"`
	Amount      money.Money `json:"amount"`
	Status      string      `json:"status"`
	DaysOverdue *int        `json:"days_overdue"`
}

type dashboardToReconcileBody struct {
	Accounts    []dashboardReconcileAccountBody `json:"accounts"`
	ReviewQueue []dashboardReviewQueueItemBody  `json:"review_queue"`
}

type dashboardReconcileAccountBody struct {
	ID                int64              `json:"id"`
	LedgerAccountCode ledger.AccountCode `json:"ledger_account_code"`
	UnreconciledCount int                `json:"unreconciled_count"`
}

type dashboardReviewQueueItemBody struct {
	Kind       banking.SuggestionKind `json:"kind"`
	Payee      string                 `json:"payee"`
	Amount     money.Money            `json:"amount"`
	Confidence float64                `json:"confidence"`
}

type dashboardRateBody struct {
	Rate      string    `json:"rate"`
	RateDate  string    `json:"rate_date"`
	FetchedAt time.Time `json:"fetched_at"`
}

type dashboardGreetingBody struct {
	UserName    string `json:"user_name"`
	TradingName string `json:"trading_name"`
}

type dashboardErrorBody struct {
	Section string `json:"section"`
	Detail  string `json:"detail"`
}
