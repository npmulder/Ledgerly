//go:build integration

package harness_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
	"github.com/npmulder/ledgerly/internal/reports"
)

func TestReportsProfitAndLossHarnessScenario(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Company(t, h)
	fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{
		time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC): "0.8500",
		time.Date(2025, 5, 2, 0, 0, 0, 0, time.UTC): "0.8600",
	}))
	invoiceService := newInvoiceService(t, h)
	ctx := context.Background()

	contosoSent, err := invoiceService.Send(ctx, createEURInvoiceDraft(t, h, invoiceService, 450_000).ID)
	if err != nil {
		t.Fatalf("Send(Contoso) error = %v", err)
	}
	if _, err := markSettledFromBankingTx(t, h, invoiceService, contosoSent.ID, "bank-contoso", time.Date(2025, 5, 2, 0, 0, 0, 0, time.UTC), invoicing.Money{Amount: 450_000, Currency: "EUR"}); err != nil {
		t.Fatalf("MarkSettled(Contoso) error = %v", err)
	}

	fabrikamSent := sendFabrikamGBPInvoice(t, h, invoiceService, 360_000)
	if fabrikamSent.Number == nil {
		t.Fatal("Fabrikam invoice number = nil")
	}

	postRecodedExpense(t, h, "2025-05-12", "5010-software", 25_000)
	postRecodedExpense(t, h, "2025-05-20", "5020-travel", 10_000)

	service := reports.New(
		ledger.New(h.LedgerPool),
		identity.NewTransactionalProfileService(testdb.AsModule(t, "identity"), h.Bus),
		invoiceService,
	)
	pl, err := service.ProfitAndLoss(ctx, reports.Period{
		From: time.Date(2025, time.May, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2025, time.May, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ProfitAndLoss() error = %v", err)
	}

	gotIncome := map[string]int64{}
	for _, line := range pl.Income {
		gotIncome[line.ClientName+"/"+line.Currency] = line.Amount.Amount
	}
	wantIncome := map[string]int64{
		"Contoso GmbH/EUR": 382_500,
		"Fabrikam Ltd/GBP": 360_000,
	}
	if !reflect.DeepEqual(gotIncome, wantIncome) {
		t.Fatalf("income = %#v, want %#v; lines=%#v", gotIncome, wantIncome, pl.Income)
	}
	assertMoney(t, pl.IncomeTotal, 747_000, "GBP")
	assertMoney(t, pl.RealisedFXGains.Amount, 4_500, "GBP")

	gotExpenses := map[ledger.AccountCode]int64{}
	for _, line := range pl.Expenses {
		gotExpenses[line.AccountCode] = line.Amount.Amount
	}
	wantExpenses := map[ledger.AccountCode]int64{
		"5010-software": 25_000,
		"5020-travel":   10_000,
	}
	if !reflect.DeepEqual(gotExpenses, wantExpenses) {
		t.Fatalf("expenses = %#v, want %#v; lines=%#v", gotExpenses, wantExpenses, pl.Expenses)
	}
	assertMoney(t, pl.ExpenseTotal, 35_000, "GBP")
	assertMoney(t, pl.ProfitBeforeTax, 712_000, "GBP")
	if pl.CorporateTax.Label != "IoM income tax at 0%" || pl.CorporateTax.Rate != "0.0" {
		t.Fatalf("CorporateTax = %#v, want IoM zero-rate line from pack", pl.CorporateTax)
	}
	assertMoney(t, pl.CorporateTax.Amount, 0, "GBP")
	assertMoney(t, pl.NetProfit, 712_000, "GBP")

	ytd, err := service.ProfitYTD(ctx, "2025-26")
	if err != nil {
		t.Fatalf("ProfitYTD() error = %v", err)
	}
	if ytd != pl.NetProfit {
		t.Fatalf("ProfitYTD = %+v, ProfitAndLoss net = %+v", ytd, pl.NetProfit)
	}
}

func sendFabrikamGBPInvoice(t testing.TB, h *harness.Harness, service *invoicing.Service, amount int64) invoicing.Invoice {
	t.Helper()

	fabrikam := fixtures.Fabrikam(t, h)
	draft, err := service.CreateDraft(context.Background(), fabrikam.ID)
	if err != nil {
		t.Fatalf("CreateDraft(Fabrikam) error = %v", err)
	}
	lines := []invoicing.InvoiceLineInput{{
		Description: "Delivery",
		Qty:         invoicing.MustQuantity("1"),
		UnitPrice:   invoicing.Money{Amount: amount, Currency: string(invoicing.CurrencyGBP)},
	}}
	updated, err := service.UpdateDraft(context.Background(), draft.ID, invoicing.DraftPatch{Lines: &lines})
	if err != nil {
		t.Fatalf("UpdateDraft(Fabrikam lines) error = %v", err)
	}
	sent, err := service.Send(context.Background(), updated.ID)
	if err != nil {
		t.Fatalf("Send(Fabrikam) error = %v", err)
	}
	return sent
}

func postRecodedExpense(t testing.TB, h *harness.Harness, date string, account ledger.AccountCode, amount int64) {
	t.Helper()

	ctx := context.Background()
	entryDate, err := time.ParseInLocation(time.DateOnly, date, time.UTC)
	if err != nil {
		t.Fatalf("parse expense date %q: %v", date, err)
	}
	service := ledger.New(h.LedgerPool, h.Bus)
	tx, err := h.LedgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin ledger expense tx: %v", err)
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
		Description:  "Reports recoded expense " + string(account),
		SourceModule: "reports-test",
		SourceRef:    "expense:" + string(account) + ":" + date,
		Postings: []ledger.NewPosting{
			{AccountCode: account, Amount: money.Money{Amount: amount, Currency: "GBP"}, AmountGBP: money.Money{Amount: amount, Currency: "GBP"}},
			{AccountCode: "1000-cash-gbp", Amount: money.Money{Amount: -amount, Currency: "GBP"}, AmountGBP: money.Money{Amount: -amount, Currency: "GBP"}},
		},
	}); err != nil {
		t.Fatalf("post recoded expense: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit ledger expense tx: %v", err)
	}
	committed = true
}

func ensureCashAccount(t testing.TB, ctx context.Context, service *ledger.Service, tx db.Tx) {
	t.Helper()

	currency := "GBP"
	if _, err := service.EnsureAccount(ctx, tx, ledger.AccountSpec{
		Code:     "1000-cash-gbp",
		Name:     "Fixture cash GBP",
		Type:     ledger.AccountTypeAsset,
		Currency: &currency,
	}); err != nil {
		t.Fatalf("ensure cash account: %v", err)
	}
}
