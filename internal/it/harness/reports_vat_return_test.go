//go:build integration

package harness_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/reports"
)

func TestReportsVATReturnBoxesDomesticReverseChargeAndManualInputVAT(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	invoiceService := newInvoiceService(t, h)
	reportService := newReportsService(t, h, invoiceService)
	ctx := context.Background()

	fabrikam := fixtures.Fabrikam(t, h)
	contoso := fixtures.Contoso(t, h)

	domestic := createInvoiceDraftForReports(t, invoiceService, fabrikam.ID, invoicing.Money{Amount: 10_000, Currency: string(invoicing.CurrencyGBP)})
	if _, err := invoiceService.Send(ctx, domestic.ID); err != nil {
		t.Fatalf("Send(domestic) error = %v", err)
	}

	reverseCharge := createInvoiceDraftForReports(t, invoiceService, contoso.ID, invoicing.Money{Amount: 450_000, Currency: string(invoicing.CurrencyEUR)})
	if _, err := invoiceService.Send(ctx, reverseCharge.ID); err != nil {
		t.Fatalf("Send(reverse charge) error = %v", err)
	}

	postManualInputVATAdjustment(t, h, time.Date(2025, 6, 30, 0, 0, 0, 0, time.UTC), 4_120)

	figures, err := reportService.VATReturn(ctx, reports.VATQuarterForDate(time.Date(2025, 5, 15, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("VATReturn() error = %v", err)
	}

	assertMoney(t, figures.Box1, 2_000, "GBP")
	assertMoney(t, figures.Box4, 4_120, "GBP")
	assertMoney(t, figures.Box6, 392_500, "GBP")
	assertMoney(t, figures.NetPosition, -2_120, "GBP")
}

func TestReportsVATReturnQuarterBoundaries(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 6, 30, 9, 0, 0, 0, time.UTC)})
	invoiceService := newInvoiceService(t, h)
	reportService := newReportsService(t, h, invoiceService)
	ctx := context.Background()

	fabrikam := fixtures.Fabrikam(t, h)
	lastDay := createInvoiceDraftForReports(t, invoiceService, fabrikam.ID, invoicing.Money{Amount: 10_000, Currency: string(invoicing.CurrencyGBP)})
	if _, err := invoiceService.Send(ctx, lastDay.ID); err != nil {
		t.Fatalf("Send(last day) error = %v", err)
	}

	h.Clock.Set(time.Date(2025, 7, 1, 9, 0, 0, 0, time.UTC))
	firstNext := createInvoiceDraftForReports(t, invoiceService, fabrikam.ID, invoicing.Money{Amount: 10_000, Currency: string(invoicing.CurrencyGBP)})
	if _, err := invoiceService.Send(ctx, firstNext.ID); err != nil {
		t.Fatalf("Send(first next) error = %v", err)
	}

	q2, err := reportService.VATReturn(ctx, reports.Period{
		From: time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2025, 6, 30, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("VATReturn(Q2) error = %v", err)
	}
	assertMoney(t, q2.Box1, 2_000, "GBP")
	assertMoney(t, q2.Box6, 10_000, "GBP")

	q3, err := reportService.VATReturn(ctx, reports.Period{
		From: time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2025, 9, 30, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("VATReturn(Q3) error = %v", err)
	}
	assertMoney(t, q3.Box1, 2_000, "GBP")
	assertMoney(t, q3.Box6, 10_000, "GBP")
}

func TestReportsVATReturnBox1UsesFixturePackRateChange(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 7, 1, 9, 0, 0, 0, time.UTC)})
	if err := jurisdiction.LoadActiveFromFS(os.DirFS("../../.."), "testland@0.1"); err != nil {
		t.Fatalf("LoadActiveFromFS(testland) error = %v", err)
	}
	t.Cleanup(func() {
		if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
			t.Fatalf("restore default jurisdiction pack: %v", err)
		}
	})

	invoiceService := newInvoiceService(t, h)
	reportService := newReportsService(t, h, invoiceService)
	ctx := context.Background()

	fabrikam := fixtures.Fabrikam(t, h)
	domestic := createInvoiceDraftForReports(t, invoiceService, fabrikam.ID, invoicing.Money{Amount: 10_000, Currency: string(invoicing.CurrencyGBP)})
	if _, err := invoiceService.Send(ctx, domestic.ID); err != nil {
		t.Fatalf("Send(domestic testland) error = %v", err)
	}

	figures, err := reportService.VATReturn(ctx, reports.VATQuarterForDate(time.Date(2025, 7, 15, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("VATReturn() error = %v", err)
	}

	assertMoney(t, figures.Box1, 1_700, "GBP")
	assertMoney(t, figures.Box6, 10_000, "GBP")
}

func TestReportsVATReturnBox4UsesOnlySignedManualInputVATAdjustments(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 6, 20, 9, 0, 0, 0, time.UTC)})
	invoiceService := newInvoiceService(t, h)
	reportService := newReportsService(t, h, invoiceService)
	ctx := context.Background()
	date := time.Date(2025, 6, 20, 0, 0, 0, 0, time.UTC)

	entryID := postManualInputVATAdjustment(t, h, date, 2_500)
	reverseLedgerEntry(t, h, entryID, "manual input VAT correction")
	postVATLiabilityPayment(t, h, date, 1_750)

	figures, err := reportService.VATReturn(ctx, reports.VATQuarterForDate(date))
	if err != nil {
		t.Fatalf("VATReturn() error = %v", err)
	}

	assertMoney(t, figures.Box4, 0, "GBP")
	assertMoney(t, figures.NetPosition, 0, "GBP")
}

func TestReportsVATReturnNetsRevertedInvoiceSend(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 6, 20, 9, 0, 0, 0, time.UTC)})
	invoiceService := newInvoiceService(t, h)
	reportService := newReportsService(t, h, invoiceService)
	ctx := context.Background()

	fabrikam := fixtures.Fabrikam(t, h)
	invoice := createInvoiceDraftForReports(t, invoiceService, fabrikam.ID, invoicing.Money{Amount: 10_000, Currency: string(invoicing.CurrencyGBP)})
	sent, err := invoiceService.Send(ctx, invoice.ID)
	if err != nil {
		t.Fatalf("Send(domestic) error = %v", err)
	}
	if sent.SendLedgerEntryID == nil {
		t.Fatal("SendLedgerEntryID = nil, want persisted send entry")
	}
	if _, err := invoiceService.RevertToDraft(ctx, invoice.ID); err != nil {
		t.Fatalf("RevertToDraft() error = %v", err)
	}

	figures, err := reportService.VATReturn(ctx, reports.VATQuarterForDate(time.Date(2025, 6, 20, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("VATReturn() error = %v", err)
	}

	assertMoney(t, figures.Box1, 0, "GBP")
	assertMoney(t, figures.Box6, 0, "GBP")
	assertMoney(t, figures.NetPosition, 0, "GBP")
}

func newReportsService(t testing.TB, h *harness.Harness, invoiceService *invoicing.Service) *reports.Service {
	t.Helper()

	service, err := reports.New(reports.Config{
		Ledger:           ledger.New(h.LedgerPool, h.Bus),
		InvoiceVATReader: invoiceService,
		Clock:            h.Clock,
	})
	if err != nil {
		t.Fatalf("reports.New() error = %v", err)
	}
	return service
}

func createInvoiceDraftForReports(t testing.TB, service *invoicing.Service, clientID string, amount invoicing.Money) invoicing.Invoice {
	t.Helper()

	draft, err := service.CreateDraft(context.Background(), clientID)
	if err != nil {
		t.Fatalf("CreateDraft() error = %v", err)
	}
	lines := []invoicing.InvoiceLineInput{{
		Description: "VAT return fixture",
		Qty:         invoicing.MustQuantity("1"),
		UnitPrice:   amount,
	}}
	updated, err := service.UpdateDraft(context.Background(), draft.ID, invoicing.DraftPatch{Lines: &lines})
	if err != nil {
		t.Fatalf("UpdateDraft(lines) error = %v", err)
	}
	return updated
}

func postManualInputVATAdjustment(t testing.TB, h *harness.Harness, date time.Time, amount int64) ledger.EntryID {
	t.Helper()

	ctx := context.Background()
	tx, err := h.LedgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin manual VAT adjustment tx: %v", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	entryID, err := ledgerService.Post(ctx, tx, ledger.NewJournalEntry{
		Date:         date,
		Description:  "Manual quarterly input VAT adjustment",
		SourceModule: reports.ModuleName,
		SourceRef:    "manual-input-vat:" + date.Format(time.DateOnly),
		Postings: []ledger.NewPosting{
			{
				AccountCode: "2200-vat-control",
				Amount:      money.Money{Amount: amount, Currency: "GBP"},
				AmountGBP:   money.Money{Amount: amount, Currency: "GBP"},
			},
			{
				AccountCode: "5030-office",
				Amount:      money.Money{Amount: -amount, Currency: "GBP"},
				AmountGBP:   money.Money{Amount: -amount, Currency: "GBP"},
			},
		},
	})
	if err != nil {
		t.Fatalf("post manual VAT adjustment: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit manual VAT adjustment: %v", err)
	}
	return entryID
}

func reverseLedgerEntry(t testing.TB, h *harness.Harness, entryID ledger.EntryID, reason string) {
	t.Helper()

	ctx := context.Background()
	tx, err := h.LedgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin reverse ledger entry tx: %v", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	if _, err = ledgerService.Reverse(ctx, tx, entryID, reason); err != nil {
		t.Fatalf("reverse ledger entry: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit reverse ledger entry: %v", err)
	}
}

func postVATLiabilityPayment(t testing.TB, h *harness.Harness, date time.Time, amount int64) {
	t.Helper()

	ctx := context.Background()
	tx, err := h.LedgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin VAT payment tx: %v", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	gbp := "GBP"
	if _, err = ledgerService.EnsureAccount(ctx, tx, ledger.AccountSpec{
		Code:     "1000-cash-gbp",
		Name:     "Fixture cash GBP",
		Type:     ledger.AccountTypeAsset,
		Currency: &gbp,
	}); err != nil {
		t.Fatalf("ensure VAT payment cash account: %v", err)
	}
	if _, err = ledgerService.Post(ctx, tx, ledger.NewJournalEntry{
		Date:         date,
		Description:  "VAT liability payment",
		SourceModule: ledger.ModuleName,
		SourceRef:    "vat-payment:" + date.Format(time.DateOnly),
		Postings: []ledger.NewPosting{
			{
				AccountCode: "2200-vat-control",
				Amount:      money.Money{Amount: amount, Currency: "GBP"},
				AmountGBP:   money.Money{Amount: amount, Currency: "GBP"},
			},
			{
				AccountCode: "1000-cash-gbp",
				Amount:      money.Money{Amount: -amount, Currency: "GBP"},
				AmountGBP:   money.Money{Amount: -amount, Currency: "GBP"},
			},
		},
	}); err != nil {
		t.Fatalf("post VAT payment: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit VAT payment: %v", err)
	}
}
