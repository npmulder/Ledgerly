//go:build integration

package it_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/banking"
	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/invoicing"
	it "github.com/npmulder/ledgerly/internal/it"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
	"github.com/npmulder/ledgerly/internal/reports"
)

func TestReportingSuite(t *testing.T) {
	loadReportingJurisdictionPack(t, jurisdiction.DefaultSelector)

	t.Run("vat pl export and pack driven tax", func(t *testing.T) {
		ctx := context.Background()
		issueDate := reportingDay(2025, time.May, 1)
		settleDate := reportingDay(2025, time.May, 2)
		quarter := reports.Period{From: reportingDay(2025, time.April, 1), To: reportingDay(2025, time.June, 30)}
		financialYear := reports.Period{From: reportingDay(2025, time.April, 1), To: reportingDay(2026, time.March, 31)}

		h := harness.New(t, harness.Options{ClockStart: issueDate.Add(9 * time.Hour)})
		fixtures.Company(t, h, fixtures.CompanyYearEnd(time.March, 31), fixtures.CompanyVATRegistered(true))
		fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{
			issueDate:  "0.8500",
			settleDate: "0.8600",
		}))

		invoices := newReportingInvoiceService(t, h)
		bankingService := newReportingBankingService(t, h, invoices)
		archiveStore := newReportingArchiveStore()
		reportService := newReportingReportsService(t, h, invoices, archiveStore)

		contoso := fixtures.Contoso(t, h)
		fabrikam := fixtures.Fabrikam(t, h)

		reverseCharge := sendReportingInvoice(t, invoices, contoso.ID, "Reverse-charge retainer", invoicing.Money{
			Amount:   450_000,
			Currency: string(invoicing.CurrencyEUR),
		})
		reverseResult := settleReportingInvoiceViaBanking(t, h, bankingService, reverseCharge, contoso.Name, "reporting-contoso-paid", settleDate, invoicing.Money{
			Amount:   450_000,
			Currency: string(invoicing.CurrencyEUR),
		})
		assertReportingMoney(t, reverseResult.RealisedFXGBP, 4_500, "GBP")

		domestic := sendReportingInvoice(t, invoices, fabrikam.ID, "Domestic support", invoicing.Money{
			Amount:   10_000,
			Currency: string(invoicing.CurrencyGBP),
		})
		domesticResult := settleReportingInvoiceViaBanking(t, h, bankingService, domestic, fabrikam.Name, "reporting-fabrikam-paid", settleDate, domestic.Totals.Total)
		assertReportingMoney(t, domesticResult.RealisedFXGBP, 0, "GBP")

		postReportingExpense(t, h, reportingDay(2025, time.May, 12), "5010-software", 25_000)
		postReportingExpense(t, h, reportingDay(2025, time.May, 20), "5020-travel", 10_000)
		postReportingManualInputVATAdjustment(t, h, reportingDay(2025, time.June, 30), 4_120)
		seedReportingDLAEntry(t, h)
		attachReportingInvoicePDF(t, h, archiveStore, reverseCharge.ID, "memory://invoice/contoso.pdf", []byte("%PDF-1.4\n% contoso reporting fixture\n%%EOF\n"))
		attachReportingInvoicePDF(t, h, archiveStore, domestic.ID, "memory://invoice/fabrikam.pdf", []byte("%PDF-1.4\n% fabrikam reporting fixture\n%%EOF\n"))

		vat, err := reportService.VATReturn(ctx, quarter)
		if err != nil {
			t.Fatalf("VATReturn() error = %v", err)
		}
		assertReportingMoney(t, vat.Box1, 2_000, "GBP")
		assertReportingMoney(t, vat.Box4, 4_120, "GBP")
		assertReportingMoney(t, vat.Box6, 392_500, "GBP")
		assertReportingMoney(t, vat.NetPosition, -2_120, "GBP")

		pl, err := reportService.ProfitAndLoss(ctx, financialYear)
		if err != nil {
			t.Fatalf("ProfitAndLoss() error = %v", err)
		}
		assertReportingPL(t, ctx, h, pl)

		ytd, err := reportService.ProfitYTD(ctx, "2025-26")
		if err != nil {
			t.Fatalf("ProfitYTD() error = %v", err)
		}
		if ytd != pl.NetProfit {
			t.Fatalf("ProfitYTD = %+v, want P&L net %+v", ytd, pl.NetProfit)
		}

		store := moneyfx.NewStore(testdb.AsModule(t, moneyfx.ModuleName))
		if err := store.StoreECBRates(ctx, []moneyfx.ECBRate{{
			Date:     settleDate,
			Currency: "GBP",
			Rate:     "0.9900",
		}}); err != nil {
			t.Fatalf("move settlement ECB rate: %v", err)
		}
		afterRateMove, err := reportService.ProfitAndLoss(ctx, financialYear)
		if err != nil {
			t.Fatalf("ProfitAndLoss() after rate move error = %v", err)
		}
		if !reflect.DeepEqual(reportingPLFrozenFields(afterRateMove), reportingPLFrozenFields(pl)) {
			t.Fatalf("P&L changed after ECB rate move:\nbefore=%#v\nafter=%#v", reportingPLFrozenFields(pl), reportingPLFrozenFields(afterRateMove))
		}

		quarterPL, err := reportService.ProfitAndLoss(ctx, quarter)
		if err != nil {
			t.Fatalf("ProfitAndLoss(quarter) error = %v", err)
		}
		ref, err := reportService.ExportPack(ctx, quarter)
		if err != nil {
			t.Fatalf("ExportPack() error = %v", err)
		}
		archive := archiveStore.mustAssetBytes(t, ref.URL)
		files := readReportingZipFiles(t, archive)
		assertReportingExportMembers(t, files)
		assertReportingJournalCSVBalanced(t, files["journal.csv"])
		assertReportingPLCSVTotals(t, files["pl.csv"], quarterPL)

		reexport, err := reportService.ExportPack(ctx, quarter)
		if err != nil {
			t.Fatalf("ExportPack() second call error = %v", err)
		}
		secondArchive := archiveStore.mustAssetBytes(t, reexport.URL)
		if reexport.URL != ref.URL || !bytes.Equal(secondArchive, archive) {
			t.Fatalf("re-export archive changed: first=%s second=%s bytesEqual=%v", ref.URL, reexport.URL, bytes.Equal(secondArchive, archive))
		}

		if err := jurisdiction.LoadActiveFromFS(os.DirFS("testdata"), "cit10@1.0"); err != nil {
			t.Fatalf("LoadActiveFromFS(cit10@1.0) error = %v", err)
		}
		t.Cleanup(func() {
			if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
				t.Fatalf("restore default jurisdiction pack: %v", err)
			}
		})
		citPL, err := reportService.ProfitAndLoss(ctx, financialYear)
		if err != nil {
			t.Fatalf("ProfitAndLoss() with CIT pack error = %v", err)
		}
		assertReportingMoney(t, citPL.IncomeTotal, pl.IncomeTotal.Amount, "GBP")
		assertReportingMoney(t, citPL.ExpenseTotal, pl.ExpenseTotal.Amount, "GBP")
		assertReportingMoney(t, citPL.ProfitBeforeTax, pl.ProfitBeforeTax.Amount, "GBP")
		if citPL.CorporateTax.Label != "IoM income tax at 10%" || citPL.CorporateTax.Rate != "0.10" {
			t.Fatalf("CIT pack CorporateTax = %#v, want 10%% line", citPL.CorporateTax)
		}
		assertReportingMoney(t, citPL.CorporateTax.Amount, 36_200, "GBP")
		assertReportingMoney(t, citPL.NetProfit, 325_800, "GBP")

		if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
			t.Fatalf("restore default jurisdiction pack: %v", err)
		}
		it.AssertLedgerBalanced(t, h)
	})

	t.Run("quarter boundaries", func(t *testing.T) {
		ctx := context.Background()
		h := harness.New(t, harness.Options{ClockStart: reportingDay(2025, time.June, 30).Add(9 * time.Hour)})
		fixtures.Company(t, h, fixtures.CompanyYearEnd(time.March, 31), fixtures.CompanyVATRegistered(true))
		invoices := newReportingInvoiceService(t, h)
		reportService := newReportingReportsService(t, h, invoices, newReportingArchiveStore())
		fabrikam := fixtures.Fabrikam(t, h)

		lastDay := sendReportingInvoice(t, invoices, fabrikam.ID, "Quarter close support", invoicing.Money{
			Amount:   10_000,
			Currency: string(invoicing.CurrencyGBP),
		})
		if lastDay.IssueDate.Format(time.DateOnly) != "2025-06-30" {
			t.Fatalf("last-day invoice issue date = %s, want 2025-06-30", lastDay.IssueDate.Format(time.DateOnly))
		}

		h.Clock.Set(reportingDay(2025, time.July, 1).Add(9 * time.Hour))
		firstNext := sendReportingInvoice(t, invoices, fabrikam.ID, "Next quarter support", invoicing.Money{
			Amount:   10_000,
			Currency: string(invoicing.CurrencyGBP),
		})
		if firstNext.IssueDate.Format(time.DateOnly) != "2025-07-01" {
			t.Fatalf("first-next invoice issue date = %s, want 2025-07-01", firstNext.IssueDate.Format(time.DateOnly))
		}

		q2, err := reportService.VATReturn(ctx, reports.Period{
			From: reportingDay(2025, time.April, 1),
			To:   reportingDay(2025, time.June, 30),
		})
		if err != nil {
			t.Fatalf("VATReturn(Q2) error = %v", err)
		}
		assertReportingMoney(t, q2.Box1, 2_000, "GBP")
		assertReportingMoney(t, q2.Box6, 10_000, "GBP")

		q3, err := reportService.VATReturn(ctx, reports.Period{
			From: reportingDay(2025, time.July, 1),
			To:   reportingDay(2025, time.September, 30),
		})
		if err != nil {
			t.Fatalf("VATReturn(Q3) error = %v", err)
		}
		assertReportingMoney(t, q3.Box1, 2_000, "GBP")
		assertReportingMoney(t, q3.Box6, 10_000, "GBP")
		it.AssertLedgerBalanced(t, h)
	})

	t.Run("filing calendar", func(t *testing.T) {
		ctx := context.Background()
		h := harness.New(t, harness.Options{ClockStart: reportingDay(2026, time.July, 5)})
		fixtures.Company(t, h, fixtures.CompanyYearEnd(time.March, 31))
		reportService, err := reports.NewService(reportingIdentity(t, h), reports.WithClock(h.Clock))
		if err != nil {
			t.Fatalf("NewService() error = %v", err)
		}

		calendar, err := reportService.FilingCalendarContext(ctx)
		if err != nil {
			t.Fatalf("FilingCalendarContext() error = %v", err)
		}
		want := []reportingFilingWant{
			{key: "vat_return", label: "VAT return", authority: "Isle of Man Customs & Excise", dueDate: reportingDay(2026, time.July, 30), status: reports.FilingStatusDueSoon},
			{key: "annual_return", label: "Annual return", authority: "IoM Companies Registry", dueDate: reportingDay(2026, time.August, 14), status: reports.FilingStatusUpcoming},
			{key: "personal_tax_return", label: "Personal tax return", authority: "IoM Income Tax Division", dueDate: reportingDay(2026, time.October, 6), status: reports.FilingStatusUpcoming},
			{key: "company_tax_return", label: "Company tax return", dueDate: reportingDay(2027, time.April, 1), status: reports.FilingStatusUpcoming},
		}
		if len(calendar) != len(want) {
			t.Fatalf("filing calendar length = %d, want %d: %+v", len(calendar), len(want), calendar)
		}
		for i, wantFiling := range want {
			assertReportingFiling(t, calendar[i], wantFiling, reportingDay(2026, time.July, 5))
		}

		annualDue := reportingDay(2026, time.August, 14)
		for _, tt := range []struct {
			name       string
			now        time.Time
			wantStatus reports.FilingStatus
		}{
			{name: "upcoming", now: annualDue.AddDate(0, 0, -31), wantStatus: reports.FilingStatusUpcoming},
			{name: "due-soon", now: annualDue.AddDate(0, 0, -30), wantStatus: reports.FilingStatusDueSoon},
			{name: "overdue", now: annualDue.AddDate(0, 0, 1), wantStatus: reports.FilingStatusOverdue},
		} {
			t.Run(tt.name, func(t *testing.T) {
				h.Clock.Set(tt.now)
				calendar, err := reportService.FilingCalendarContext(ctx)
				if err != nil {
					t.Fatalf("FilingCalendarContext(%s) error = %v", tt.now.Format(time.DateOnly), err)
				}
				annual, ok := reportingFilingByKey(calendar, "annual_return")
				if !ok {
					t.Fatalf("annual_return missing from %+v", calendar)
				}
				if annual.Status != tt.wantStatus {
					t.Fatalf("annual_return status on %s = %q, want %q", tt.now.Format(time.DateOnly), annual.Status, tt.wantStatus)
				}
			})
		}

		for day := 0; day < 365; day++ {
			now := reportingDay(2026, time.January, 1).AddDate(0, 0, day)
			h.Clock.Set(now)
			calendar, err := reportService.FilingCalendarContext(ctx)
			if err != nil {
				t.Fatalf("FilingCalendarContext() on %s error = %v", now.Format(time.DateOnly), err)
			}
			if len(calendar) != 4 {
				t.Fatalf("FilingCalendarContext() on %s length = %d, want 4", now.Format(time.DateOnly), len(calendar))
			}
			for _, filing := range calendar {
				wantDays := reportingDaysBetween(now, filing.DueDate)
				if filing.DaysUntil != wantDays {
					t.Fatalf("%s on %s DaysUntil = %d, want %d", filing.Key, now.Format(time.DateOnly), filing.DaysUntil, wantDays)
				}
				wantStatus := reportingFilingStatus(wantDays, 30)
				if filing.Status != wantStatus {
					t.Fatalf("%s on %s Status = %q, want %q", filing.Key, now.Format(time.DateOnly), filing.Status, wantStatus)
				}
			}
		}
		it.AssertLedgerBalanced(t, h)
	})
}

func loadReportingJurisdictionPack(t testing.TB, selector string) {
	t.Helper()
	if err := jurisdiction.LoadActive(selector); err != nil {
		t.Fatalf("LoadActive(%s) error = %v", selector, err)
	}
	t.Cleanup(func() {
		if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
			t.Fatalf("restore default jurisdiction pack: %v", err)
		}
	})
}

func newReportingInvoiceService(t testing.TB, h *harness.Harness) *invoicing.Service {
	t.Helper()
	moneyFXService := moneyfx.NewService(moneyfx.NewStore(testdb.AsModule(t, moneyfx.ModuleName)), h.Clock)
	rateLocks := reportingRateLocker{service: moneyFXService}
	return invoicing.NewService(
		testdb.AsModule(t, invoicing.ModuleName),
		invoicing.Store{},
		invoicing.WithClock(h.Clock),
		invoicing.WithTodayRate(reportingTodayRate),
		invoicing.WithRateLocker(rateLocks),
		invoicing.WithRateLockReader(rateLocks),
		invoicing.WithLedger(ledger.New(h.LedgerPool, h.Bus)),
		invoicing.WithEventBus(h.Bus),
		invoicing.WithIdentity(reportingIdentity(t, h)),
	)
}

type reportingRateLocker struct {
	service *moneyfx.Service
}

func (l reportingRateLocker) LockRate(ctx context.Context, tx db.Tx, ref invoicing.RateLockRef, from string, to string, date time.Time) (invoicing.RateLock, error) {
	lock, err := l.service.Lock(ctx, tx, moneyfx.LockRef{Module: ref.Module, Ref: ref.Ref}, from, to, date)
	if err != nil {
		return invoicing.RateLock{}, err
	}
	return invoicing.RateLock{
		ID:       int64(lock.ID),
		From:     lock.From,
		To:       lock.To,
		Rate:     lock.Rate,
		RateDate: lock.RateDate,
		Source:   lock.Source,
	}, nil
}

func (l reportingRateLocker) RateLock(ctx context.Context, id int64) (invoicing.RateLock, error) {
	lock, err := l.service.GetLock(ctx, moneyfx.LockID(id))
	if err != nil {
		return invoicing.RateLock{}, err
	}
	return invoicing.RateLock{
		ID:       int64(lock.ID),
		From:     lock.From,
		To:       lock.To,
		Rate:     lock.Rate,
		RateDate: lock.RateDate,
		Source:   lock.Source,
	}, nil
}

func reportingTodayRate(_ context.Context, from string, to string) (invoicing.FXRate, time.Time, error) {
	value := "0.8500"
	if from == to {
		value = "1"
	}
	return invoicing.FXRate{
		From:   from,
		To:     to,
		Value:  value,
		Source: "reporting-test",
	}, reportingDay(2025, time.May, 1).Add(12 * time.Hour), nil
}

func newReportingBankingService(t testing.TB, h *harness.Harness, invoices *invoicing.Service) *banking.Service {
	t.Helper()
	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	moneyFXService := moneyfx.NewService(moneyfx.NewStore(testdb.AsModule(t, moneyfx.ModuleName)), h.Clock)
	return banking.NewService(
		h.BankingPool,
		ledgerService,
		banking.WithLedgerJournal(ledgerService),
		banking.WithMoneyFX(moneyFXService),
		banking.WithInvoicingSettler(invoices),
		banking.WithEventBus(h.Bus),
	)
}

func newReportingReportsService(t testing.TB, h *harness.Harness, invoices *invoicing.Service, archiveStore *reportingArchiveStore) *reports.Service {
	t.Helper()
	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	return reports.New(
		ledgerService,
		reportingIdentity(t, h),
		invoices,
		reports.WithClock(h.Clock),
		reports.WithDLA(dla.NewWithBusAndClock(h.DLAPool, h.Bus, h.Clock, ledgerService)),
		reports.WithExportArchiveStore(archiveStore),
		reports.WithPLPDFEngine(reportingPLPDFEngine{}),
		reports.WithAppVersion("test"),
	)
}

func reportingIdentity(t testing.TB, h *harness.Harness) identity.Identity {
	t.Helper()
	return identity.NewTransactionalProfileService(testdb.AsModule(t, "identity"), h.Bus, identity.WithDataDir(h.IdentityDataDir))
}

func sendReportingInvoice(t testing.TB, service *invoicing.Service, clientID string, description string, amount invoicing.Money) invoicing.Invoice {
	t.Helper()
	draft, err := service.CreateDraft(context.Background(), clientID)
	if err != nil {
		t.Fatalf("CreateDraft() error = %v", err)
	}
	lines := []invoicing.InvoiceLineInput{{
		ID:          "line-" + strings.ToLower(strings.ReplaceAll(description, " ", "-")),
		Description: description,
		Qty:         invoicing.MustQuantity("1"),
		UnitPrice:   amount,
	}}
	updated, err := service.UpdateDraft(context.Background(), draft.ID, invoicing.DraftPatch{Lines: &lines})
	if err != nil {
		t.Fatalf("UpdateDraft(lines) error = %v", err)
	}
	sent, err := service.Send(context.Background(), updated.ID)
	if err != nil {
		t.Fatalf("Send(%s) error = %v", description, err)
	}
	return sent
}

func settleReportingInvoiceViaBanking(
	t testing.TB,
	h *harness.Harness,
	service *banking.Service,
	invoice invoicing.Invoice,
	payee string,
	txnID string,
	settleDate time.Time,
	amount invoicing.Money,
) banking.ConfirmMatchResult {
	t.Helper()
	if invoice.Number == nil || strings.TrimSpace(*invoice.Number) == "" {
		t.Fatalf("invoice %s number is nil/blank", invoice.ID)
	}
	ctx := context.Background()
	account := createReportingBankAccount(t, ctx, service, "Revolut "+amount.Currency+" "+txnID, amount.Currency)
	summary, err := service.ImportCSV(ctx, account.ID, banking.ImportFile{
		Filename: txnID + ".csv",
		Reader: bytes.NewReader(fixtures.RevolutCSV(fixtures.RevolutTxn{
			ID:        txnID,
			Date:      settleDate.Add(12 * time.Hour),
			Payee:     payee,
			Reference: *invoice.Number,
			Amount:    money.Money{Amount: amount.Amount, Currency: amount.Currency},
		})),
	})
	if err != nil {
		t.Fatalf("ImportCSV(%s) error = %v", txnID, err)
	}
	if summary.NewRows != 1 || summary.DuplicateRows != 0 {
		t.Fatalf("ImportCSV(%s) summary = %+v, want one new row", txnID, summary)
	}
	match := reportingInvoiceMatch(t, ctx, service, invoice.ID)
	result, err := service.ConfirmMatch(ctx, match.Transaction.ID)
	if err != nil {
		t.Fatalf("ConfirmMatch(%s) error = %v", txnID, err)
	}
	if result.InvoiceID != invoice.ID || result.Transaction.State != banking.TransactionStateReconciled {
		t.Fatalf("ConfirmMatch(%s) = %+v, want reconciled invoice %s", txnID, result, invoice.ID)
	}
	return result
}

func createReportingBankAccount(t testing.TB, ctx context.Context, service *banking.Service, name string, currency string) banking.BankAccount {
	t.Helper()
	account, err := service.CreateAccount(ctx, banking.AccountInput{
		Name:     name,
		Provider: banking.ProviderRevolut,
		Currency: currency,
	})
	if err != nil {
		t.Fatalf("CreateAccount(%s/%s) error = %v", name, currency, err)
	}
	return account
}

func reportingInvoiceMatch(t testing.TB, ctx context.Context, service *banking.Service, invoiceID string) banking.ReviewQueueItem {
	t.Helper()
	queue, err := service.ReviewQueue(ctx)
	if err != nil {
		t.Fatalf("ReviewQueue() error = %v", err)
	}
	for _, item := range queue.InvoiceMatches {
		if item.Suggestion.Target == invoiceID {
			return item
		}
	}
	t.Fatalf("ReviewQueue invoice matches = %+v, want target invoice %s", queue.InvoiceMatches, invoiceID)
	return banking.ReviewQueueItem{}
}

func postReportingExpense(t testing.TB, h *harness.Harness, date time.Time, account ledger.AccountCode, amount int64) {
	t.Helper()
	ctx := context.Background()
	service := ledger.New(h.LedgerPool, h.Bus)
	tx, err := h.LedgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin expense tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	ensureReportingCashAccount(t, ctx, service, tx)
	if _, err := service.Post(ctx, tx, ledger.NewJournalEntry{
		Date:         date,
		Description:  "Reporting recoded expense " + string(account),
		SourceModule: "reporting-test",
		SourceRef:    "expense:" + string(account) + ":" + date.Format(time.DateOnly),
		Postings: []ledger.NewPosting{
			{AccountCode: account, Amount: money.Money{Amount: amount, Currency: "GBP"}, AmountGBP: money.Money{Amount: amount, Currency: "GBP"}},
			{AccountCode: "1000-cash-gbp", Amount: money.Money{Amount: -amount, Currency: "GBP"}, AmountGBP: money.Money{Amount: -amount, Currency: "GBP"}},
		},
	}); err != nil {
		t.Fatalf("post expense: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit expense tx: %v", err)
	}
	committed = true
}

func postReportingManualInputVATAdjustment(t testing.TB, h *harness.Harness, date time.Time, amount int64) {
	t.Helper()
	ctx := context.Background()
	service := ledger.New(h.LedgerPool, h.Bus)
	tx, err := h.LedgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin manual VAT adjustment tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	ensureReportingCashAccount(t, ctx, service, tx)
	if _, err := service.Post(ctx, tx, ledger.NewJournalEntry{
		Date:         date,
		Description:  "Manual quarterly input VAT adjustment",
		SourceModule: reports.ModuleName,
		SourceRef:    "manual-input-vat:" + date.Format(time.DateOnly),
		Postings: []ledger.NewPosting{
			{AccountCode: "2200-vat-control", Amount: money.Money{Amount: amount, Currency: "GBP"}, AmountGBP: money.Money{Amount: amount, Currency: "GBP"}},
			{AccountCode: "1000-cash-gbp", Amount: money.Money{Amount: -amount, Currency: "GBP"}, AmountGBP: money.Money{Amount: -amount, Currency: "GBP"}},
		},
	}); err != nil {
		t.Fatalf("post manual VAT adjustment: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit manual VAT adjustment tx: %v", err)
	}
	committed = true
}

func seedReportingDLAEntry(t testing.TB, h *harness.Harness) {
	t.Helper()
	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	ctx := context.Background()
	tx, err := h.LedgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin DLA cash account tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	ensureReportingCashAccount(t, ctx, ledgerService, tx)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit DLA cash account tx: %v", err)
	}
	committed = true

	service := dla.NewWithBusAndClock(h.DLAPool, h.Bus, h.Clock, ledgerService)
	if err := service.AddEntry(context.Background(), dla.NewEntry{
		Date:            reportingDay(2025, time.May, 15),
		Kind:            dla.EntryKindRepayment,
		Description:     "Director repaid reporting expenses",
		Amount:          money.Money{Amount: 5_000, Currency: "GBP"},
		Source:          "manual:reporting-repayment",
		CashAccountCode: "1000-cash-gbp",
	}); err != nil {
		t.Fatalf("seed DLA repayment: %v", err)
	}
}

func ensureReportingCashAccount(t testing.TB, ctx context.Context, service *ledger.Service, tx db.Tx) {
	t.Helper()
	currency := "GBP"
	if _, err := service.EnsureAccount(ctx, tx, ledger.AccountSpec{
		Code:     "1000-cash-gbp",
		Name:     "Reporting fixture cash GBP",
		Type:     ledger.AccountTypeAsset,
		Currency: &currency,
	}); err != nil {
		t.Fatalf("ensure cash account: %v", err)
	}
}

func attachReportingInvoicePDF(t testing.TB, h *harness.Harness, store *reportingArchiveStore, invoiceID string, assetURL string, pdfBytes []byte) {
	t.Helper()
	store.putAsset(assetURL, reports.StoredAsset{
		Filename:    strings.TrimPrefix(assetURL, "memory://invoice/"),
		ContentType: "application/pdf",
		Bytes:       pdfBytes,
	})
	if _, err := (invoicing.Store{}).SetInvoicePDFAsset(context.Background(), testdb.AsModule(t, invoicing.ModuleName), invoiceID, assetURL); err != nil {
		t.Fatalf("set invoice PDF asset: %v", err)
	}
}

func assertReportingPL(t testing.TB, ctx context.Context, h *harness.Harness, pl reports.PL) {
	t.Helper()
	gotIncome := map[string]int64{}
	for _, line := range pl.Income {
		gotIncome[line.ClientName+"/"+line.Currency] = line.Amount.Amount
	}
	wantIncome := map[string]int64{
		"Contoso GmbH/EUR": 382_500,
		"Fabrikam Ltd/GBP": 10_000,
	}
	if !reflect.DeepEqual(gotIncome, wantIncome) {
		t.Fatalf("P&L income = %#v, want %#v; lines=%#v", gotIncome, wantIncome, pl.Income)
	}
	assertReportingMoney(t, pl.IncomeTotal, 397_000, "GBP")
	assertReportingMoney(t, pl.RealisedFXGains.Amount, 4_500, "GBP")
	fxFromLedger := reportingRealisedFXFromLedger(t, ctx, h, pl.Period)
	if fxFromLedger != pl.RealisedFXGains.Amount {
		t.Fatalf("realised FX from ledger = %+v, P&L line = %+v", fxFromLedger, pl.RealisedFXGains.Amount)
	}

	gotExpenses := map[ledger.AccountCode]int64{}
	for _, line := range pl.Expenses {
		gotExpenses[line.AccountCode] = line.Amount.Amount
	}
	wantExpenses := map[ledger.AccountCode]int64{
		"5010-software": 25_000,
		"5020-travel":   10_000,
	}
	if !reflect.DeepEqual(gotExpenses, wantExpenses) {
		t.Fatalf("P&L expenses = %#v, want %#v; lines=%#v", gotExpenses, wantExpenses, pl.Expenses)
	}
	assertReportingMoney(t, pl.ExpenseTotal, 35_000, "GBP")
	assertReportingMoney(t, pl.ProfitBeforeTax, 362_000, "GBP")
	if pl.CorporateTax.Label != "IoM income tax at 0%" || pl.CorporateTax.Rate != "0.0" {
		t.Fatalf("CorporateTax = %#v, want IoM zero-rate line from pack", pl.CorporateTax)
	}
	assertReportingMoney(t, pl.CorporateTax.Amount, 0, "GBP")
	assertReportingMoney(t, pl.NetProfit, 362_000, "GBP")
}

func reportingRealisedFXFromLedger(t testing.TB, ctx context.Context, h *harness.Harness, period reports.Period) money.Money {
	t.Helper()
	filter := ledger.EntryFilter{
		From:  &period.From,
		To:    &period.To,
		Limit: ledger.MaxEntriesLimit,
	}
	var total int64
	service := ledger.New(h.LedgerPool, h.Bus)
	for {
		entries, err := service.Entries(ctx, filter)
		if err != nil {
			t.Fatalf("load ledger entries for realised FX: %v", err)
		}
		for _, entry := range entries {
			for _, posting := range entry.Postings {
				if posting.AccountCode == "4900-fx-gain-loss" {
					total += posting.AmountGBP.Amount
				}
			}
		}
		if len(entries) < filter.Limit {
			break
		}
		last := entries[len(entries)-1]
		filter.After = &ledger.EntryCursor{Date: last.Date, ID: last.ID}
	}
	return money.Money{Amount: -total, Currency: "GBP"}
}

type reportingPLSnapshot struct {
	Income          []reports.IncomeLine
	IncomeTotal     money.Money
	RealisedFXGains reports.LineItem
	Expenses        []reports.ExpenseLine
	ExpenseTotal    money.Money
	ProfitBeforeTax money.Money
	CorporateTax    reports.TaxLine
	NetProfit       money.Money
}

func reportingPLFrozenFields(pl reports.PL) reportingPLSnapshot {
	return reportingPLSnapshot{
		Income:          pl.Income,
		IncomeTotal:     pl.IncomeTotal,
		RealisedFXGains: pl.RealisedFXGains,
		Expenses:        pl.Expenses,
		ExpenseTotal:    pl.ExpenseTotal,
		ProfitBeforeTax: pl.ProfitBeforeTax,
		CorporateTax:    pl.CorporateTax,
		NetProfit:       pl.NetProfit,
	}
}

func assertReportingExportMembers(t testing.TB, files map[string][]byte) {
	t.Helper()
	for _, name := range []string{"pl.csv", "pl.pdf", "vat.csv", "journal.csv", "dla.csv", "manifest.json"} {
		if _, ok := files[name]; !ok {
			t.Fatalf("export archive missing %s; files=%v", name, reportingSortedKeys(files))
		}
	}
	hasInvoicePDF := false
	for name := range files {
		if strings.HasPrefix(name, "invoices/") && strings.HasSuffix(name, ".pdf") {
			hasInvoicePDF = true
			break
		}
	}
	if !hasInvoicePDF {
		t.Fatalf("export archive missing invoice PDFs; files=%v", reportingSortedKeys(files))
	}
}

func assertReportingJournalCSVBalanced(t testing.TB, data []byte) {
	t.Helper()
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatalf("read journal.csv: %v", err)
	}
	var debitTotal, creditTotal int64
	for _, row := range rows[1:] {
		debitTotal += reportingParseDecimalMinor(t, row[8])
		creditTotal += reportingParseDecimalMinor(t, row[9])
	}
	if debitTotal != creditTotal {
		t.Fatalf("journal.csv debits = %d, credits = %d", debitTotal, creditTotal)
	}
}

func assertReportingPLCSVTotals(t testing.TB, data []byte, pl reports.PL) {
	t.Helper()
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatalf("read pl.csv: %v", err)
	}
	got := map[string]int64{}
	for _, row := range rows[1:] {
		got[row[0]+"/"+row[1]] = reportingParseDecimalMinor(t, row[2])
	}
	want := map[string]int64{
		"total/Turnover":               pl.IncomeTotal.Amount,
		"total/Expenses":               pl.ExpenseTotal.Amount,
		"total/Profit before tax":      pl.ProfitBeforeTax.Amount,
		"tax/" + pl.CorporateTax.Label: pl.CorporateTax.Amount.Amount,
		"total/Net profit":             pl.NetProfit.Amount,
	}
	for key, wantAmount := range want {
		if got[key] != wantAmount {
			t.Fatalf("pl.csv %s = %d, want API payload %d; rows=%#v", key, got[key], wantAmount, rows)
		}
	}
}

func readReportingZipFiles(t testing.TB, data []byte) map[string][]byte {
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

func reportingParseDecimalMinor(t testing.TB, value string) int64 {
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

type reportingFilingWant struct {
	key       string
	label     string
	authority string
	dueDate   time.Time
	status    reports.FilingStatus
}

func assertReportingFiling(t testing.TB, got reports.Filing, want reportingFilingWant, now time.Time) {
	t.Helper()
	if got.Key != want.key || got.Label != want.label || got.Authority != want.authority || !got.DueDate.Equal(want.dueDate) || got.Status != want.status {
		t.Fatalf("filing = %+v, want key=%s label=%s authority=%s due=%s status=%s",
			got,
			want.key,
			want.label,
			want.authority,
			want.dueDate.Format(time.DateOnly),
			want.status,
		)
	}
	wantDays := reportingDaysBetween(now, want.dueDate)
	if got.DaysUntil != wantDays || got.WarnWindow != 30 {
		t.Fatalf("filing %s days/window = %d/%d, want %d/30", got.Key, got.DaysUntil, got.WarnWindow, wantDays)
	}
}

func reportingFilingByKey(calendar []reports.Filing, key string) (reports.Filing, bool) {
	for _, filing := range calendar {
		if filing.Key == key {
			return filing, true
		}
	}
	return reports.Filing{}, false
}

func reportingFilingStatus(daysUntil int, warnWindow int) reports.FilingStatus {
	if daysUntil < 0 {
		return reports.FilingStatusOverdue
	}
	if daysUntil <= warnWindow {
		return reports.FilingStatusDueSoon
	}
	return reports.FilingStatusUpcoming
}

func reportingDaysBetween(start time.Time, end time.Time) int {
	return int(reportingDateOnly(end).Sub(reportingDateOnly(start)) / (24 * time.Hour))
}

func reportingDateOnly(date time.Time) time.Time {
	if date.IsZero() {
		return time.Time{}
	}
	year, month, day := date.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func assertReportingMoney(t testing.TB, got money.Money, wantAmount int64, wantCurrency string) {
	t.Helper()
	if got.Amount != wantAmount || got.Currency != wantCurrency {
		t.Fatalf("money = %+v, want %d %s", got, wantAmount, wantCurrency)
	}
}

func reportingDay(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

type reportingPLPDFEngine struct{}

func (reportingPLPDFEngine) RenderPLPDF(context.Context, reports.PLPrintPayload) ([]byte, error) {
	return []byte("%PDF-1.4\n% reporting pl fixture\n%%EOF\n"), nil
}

type reportingArchiveStore struct {
	archives map[string]reports.ArchiveRef
	assets   map[string]reports.StoredAsset
	seq      int
}

func newReportingArchiveStore() *reportingArchiveStore {
	return &reportingArchiveStore{
		archives: map[string]reports.ArchiveRef{},
		assets:   map[string]reports.StoredAsset{},
	}
}

func (s *reportingArchiveStore) ExistingExportArchive(_ context.Context, key string) (reports.ArchiveRef, bool, error) {
	ref, ok := s.archives[key]
	return ref, ok, nil
}

func (s *reportingArchiveStore) StoreExportArchive(_ context.Context, key string, data []byte) (reports.ArchiveRef, error) {
	if ref, ok := s.archives[key]; ok {
		return ref, nil
	}
	s.seq++
	url := fmt.Sprintf("memory://exports/reporting-%d.zip", s.seq)
	ref := reports.ArchiveRef{
		URL:    url,
		SHA256: reportingSHA256Hex(data),
		Size:   int64(len(data)),
	}
	s.archives[key] = ref
	s.assets[url] = reports.StoredAsset{
		Filename:    fmt.Sprintf("reporting-%d.zip", s.seq),
		ContentType: "application/zip",
		Bytes:       append([]byte{}, data...),
	}
	return ref, nil
}

func (s *reportingArchiveStore) LoadAsset(_ context.Context, ref string) (reports.StoredAsset, error) {
	asset, ok := s.assets[strings.TrimSpace(ref)]
	if !ok {
		return reports.StoredAsset{}, fmt.Errorf("reporting archive asset %q not found", ref)
	}
	asset.Bytes = append([]byte{}, asset.Bytes...)
	return asset, nil
}

func (s *reportingArchiveStore) putAsset(ref string, asset reports.StoredAsset) {
	asset.Bytes = append([]byte{}, asset.Bytes...)
	s.assets[strings.TrimSpace(ref)] = asset
}

func (s *reportingArchiveStore) mustAssetBytes(t testing.TB, ref string) []byte {
	t.Helper()
	asset, err := s.LoadAsset(context.Background(), ref)
	if err != nil {
		t.Fatalf("load archive asset %s: %v", ref, err)
	}
	return asset.Bytes
}

func reportingSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func reportingSortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
