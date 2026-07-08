//go:build integration

package it_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	pdfreader "github.com/ledongthuc/pdf"

	"github.com/npmulder/ledgerly/internal/banking"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/invoicing"
	it "github.com/npmulder/ledgerly/internal/it"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/golden"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
	"github.com/npmulder/ledgerly/internal/reports"
	"github.com/npmulder/ledgerly/web"
)

func TestInvoiceLifecycleE2E(t *testing.T) {
	t.Run("draft send settle overdue pdf and reports", func(t *testing.T) {
		ctx := context.Background()
		issueDate := day(2025, time.May, 1)
		settleDate := day(2025, time.May, 2)
		h := harness.New(t, harness.Options{ClockStart: issueDate.Add(9 * time.Hour)})
		fixtures.Company(t, h)
		fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{
			issueDate:  "0.8500",
			settleDate: "0.8600",
		}))
		contoso := fixtures.Contoso(t, h)
		invoices := newLifecycleInvoiceService(t, h, lifecycleIdentity(t, h))

		var sentEvents []invoicing.InvoiceSent
		h.Bus.Subscribe(invoicing.InvoiceSentName, func(_ context.Context, _ db.Tx, evt bus.Event) error {
			sent, ok := evt.(invoicing.InvoiceSent)
			if !ok {
				t.Fatalf("InvoiceSent event = %T, want invoicing.InvoiceSent", evt)
			}
			sentEvents = append(sentEvents, sent)
			return nil
		})

		draft := createLifecycleDraftViaHTTP(t, h, contoso.ID)
		// EUR 4,500.00 at the issue-date locked EUR->GBP rate 0.8500 is GBP 3,825.00.
		patched := patchLifecycleDraftLinesViaHTTP(t, h, draft.ID, "retainer-main", "Monthly retainer", 450_000, "2025-05-15")
		send := sendLifecycleInvoiceViaHTTP(t, h, patched.ID)
		sent := send.Invoice
		if sent.Number == nil || *sent.Number != "INV-2025-01" || send.Number != "INV-2025-01" {
			t.Fatalf("sent number response=%q invoice=%v, want INV-2025-01", send.Number, sent.Number)
		}
		if sent.Status != invoicing.InvoiceStatusSent {
			t.Fatalf("sent status = %q, want sent", sent.Status)
		}
		assertLifecycleMoney(t, sent.Totals.Total, 450_000, "EUR")
		assertLifecycleRateLock(t, h, *sent.LockID, "invoicing:INV-2025-01", "EUR", "GBP", "0.850000000000000000", "2025-05-01")
		assertLifecyclePostings(t, h, invoicing.ModuleName, invoiceLifecycleSendSourceRef("INV-2025-01"), []wantLifecyclePosting{
			{account: "1100-debtors-eur", amount: 450_000, currency: "EUR", amountGBP: 382_500},
			{account: "4000-sales", amount: -450_000, currency: "EUR", amountGBP: -382_500},
		})
		wantSentEvents := []invoicing.InvoiceSent{{
			InvoiceID: sent.ID,
			Number:    "INV-2025-01",
			ClientID:  contoso.ID,
			Amount:    invoicing.Money{Amount: 450_000, Currency: "EUR"},
			DueDate:   sent.DueDate,
		}}
		if !reflect.DeepEqual(sentEvents, wantSentEvents) {
			t.Fatalf("InvoiceSent events = %#v, want %#v", sentEvents, wantSentEvents)
		}

		assertLifecycleInvoicePDFGolden(t, ctx, invoices, sent.ID)

		bankingService := newLifecycleBankingService(t, h, invoices)
		account := createLifecycleBankAccount(t, ctx, bankingService, "Revolut EUR", "EUR")
		summary, err := bankingService.ImportCSV(ctx, account.ID, banking.ImportFile{
			Filename: "revolut-contoso.csv",
			Reader: bytes.NewReader(fixtures.RevolutCSV(fixtures.RevolutTxn{
				ID:        "contoso-paid-4500",
				Date:      settleDate.Add(12 * time.Hour),
				Payee:     "Contoso GmbH",
				Reference: "INV-2025-01",
				Amount:    money.Money{Amount: 450_000, Currency: "EUR"},
			})),
		})
		if err != nil {
			t.Fatalf("ImportCSV() error = %v", err)
		}
		if summary.NewRows != 1 || summary.DuplicateRows != 0 {
			t.Fatalf("ImportCSV() summary = %+v, want one new row", summary)
		}
		match := lifecycleInvoiceMatch(t, ctx, bankingService, sent.ID)
		if match.Suggestion.Confidence < 0.95 {
			t.Fatalf("match confidence = %.2f, want >= 0.95", match.Suggestion.Confidence)
		}

		result, err := bankingService.ConfirmMatch(ctx, match.Transaction.ID)
		if err != nil {
			t.Fatalf("ConfirmMatch() error = %v", err)
		}
		if result.InvoiceID != sent.ID || result.Transaction.State != banking.TransactionStateReconciled {
			t.Fatalf("ConfirmMatch() = %+v, want reconciled invoice %s", result, sent.ID)
		}
		assertLifecycleMoney(t, result.RealisedFXGBP, 4_500, "GBP")
		settled, err := invoices.Invoice(ctx, sent.ID)
		if err != nil {
			t.Fatalf("Invoice(%s) after settlement: %v", sent.ID, err)
		}
		if settled.Status != invoicing.InvoiceStatusPaid {
			t.Fatalf("settled status = %q, want paid", settled.Status)
		}
		lockID := mustLifecycleLockID(t, settled)
		// EUR 4,500.00 at settlement-date rate 0.8600 is GBP 3,870.00.
		assertLifecyclePostings(t, h, banking.ModuleName, invoiceLifecycleBankingSourceRef(match.Transaction.ID), []wantLifecyclePosting{
			{account: string(account.LedgerAccountCode), amount: 450_000, currency: "EUR", amountGBP: 387_000},
			{account: "1100-debtors-eur", amount: -450_000, currency: "EUR", amountGBP: -387_000},
		})
		// Settlement GBP 3,870.00 less issue GBP 3,825.00 is a realised FX gain of GBP 45.00.
		assertRealisedFXRows(t, ctx, h.DB, sent.ID, moneyfx.LockID(lockID), 1, 4_500)
		assertLifecyclePostings(t, h, moneyfx.ModuleName, invoiceLifecycleSettlementSourceRef(sent.ID), []wantLifecyclePosting{
			{account: "1101-debtors-gbp", amount: 4_500, currency: "GBP", amountGBP: 4_500},
			{account: "4900-fx-gain-loss", amount: -4_500, currency: "GBP", amountGBP: -4_500},
		})
		assertLifecycleTrialBalanceZero(t, h)
		assertLifecycleInvoiceDebtorsNetZero(t, h, []string{
			invoiceLifecycleSendSourceRef("INV-2025-01"),
			invoiceLifecycleBankingSourceRef(match.Transaction.ID),
			invoiceLifecycleSettlementSourceRef(sent.ID),
		})
		assertLifecycleRealisedFXPL(t, ctx, h, invoices, 4_500)

		overdueDraft := createLifecycleDraftViaHTTP(t, h, contoso.ID)
		patchLifecycleDraftLinesViaHTTP(t, h, overdueDraft.ID, "retainer-overdue", "Overdue retainer", 125_000, "2025-05-04")
		overdueSend := sendLifecycleInvoiceViaHTTP(t, h, overdueDraft.ID)
		if overdueSend.Invoice.Number == nil || *overdueSend.Invoice.Number != "INV-2025-02" {
			t.Fatalf("overdue invoice number = %v, want INV-2025-02", overdueSend.Invoice.Number)
		}
		h.Clock.Set(day(2025, time.May, 7).Add(9 * time.Hour))
		overdueList := listLifecycleInvoicesViaHTTP(t, h, "status=overdue&search=contoso&limit=10&offset=0")
		if overdueList.TotalCount != 1 || len(overdueList.Invoices) != 1 {
			t.Fatalf("overdue list = total %d rows %+v, want one row", overdueList.TotalCount, overdueList.Invoices)
		}
		overdueRow := overdueList.Invoices[0]
		if overdueRow.ID != overdueSend.Invoice.ID || overdueRow.Status != invoicing.InvoiceStatusOverdue || overdueRow.DaysOverdue != 3 {
			t.Fatalf("overdue row = id %s status %q days %d, want invoice %s overdue 3",
				overdueRow.ID, overdueRow.Status, overdueRow.DaysOverdue, overdueSend.Invoice.ID)
		}
		var overdueEvents []invoicing.InvoiceOverdue
		h.Bus.Subscribe(invoicing.InvoiceOverdueName, func(_ context.Context, _ db.Tx, evt bus.Event) error {
			overdue, ok := evt.(invoicing.InvoiceOverdue)
			if !ok {
				t.Fatalf("InvoiceOverdue event = %T, want invoicing.InvoiceOverdue", evt)
			}
			overdueEvents = append(overdueEvents, overdue)
			return nil
		})
		if err := h.RunJob(invoicing.OverdueSweepJobName); err != nil {
			t.Fatalf("RunJob(%s) error = %v", invoicing.OverdueSweepJobName, err)
		}
		wantOverdue := []invoicing.InvoiceOverdue{{InvoiceID: overdueSend.Invoice.ID, DaysOverdue: 3}}
		if !reflect.DeepEqual(overdueEvents, wantOverdue) {
			t.Fatalf("InvoiceOverdue events = %#v, want %#v", overdueEvents, wantOverdue)
		}
		if err := h.RunJob(invoicing.OverdueSweepJobName); err != nil {
			t.Fatalf("RunJob(%s second run) error = %v", invoicing.OverdueSweepJobName, err)
		}
		if !reflect.DeepEqual(overdueEvents, wantOverdue) {
			t.Fatalf("InvoiceOverdue events after second sweep = %#v, want unchanged", overdueEvents)
		}
		it.AssertLedgerBalanced(t, h)
	})

	t.Run("draft invoice import confirms by sending and settling", func(t *testing.T) {
		ctx := context.Background()
		issueDate := day(2025, time.May, 1)
		settleDate := day(2025, time.May, 2)
		h := harness.New(t, harness.Options{ClockStart: issueDate.Add(9 * time.Hour)})
		fixtures.Company(t, h)
		fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{
			issueDate:  "0.8500",
			settleDate: "0.8600",
		}))
		contoso := fixtures.Contoso(t, h)
		invoices := newLifecycleInvoiceService(t, h, lifecycleIdentity(t, h))

		var sentEvents []invoicing.InvoiceSent
		h.Bus.Subscribe(invoicing.InvoiceSentName, func(_ context.Context, _ db.Tx, evt bus.Event) error {
			sent, ok := evt.(invoicing.InvoiceSent)
			if !ok {
				t.Fatalf("InvoiceSent event = %T, want invoicing.InvoiceSent", evt)
			}
			sentEvents = append(sentEvents, sent)
			return nil
		})

		draft := patchLifecycleDraftLinesViaHTTP(
			t,
			h,
			createLifecycleDraftViaHTTP(t, h, contoso.ID).ID,
			"retainer-draft-match",
			"Draft retainer",
			450_000,
			"2025-05-15",
		)
		if draft.Status != invoicing.InvoiceStatusDraft || draft.Number != nil {
			t.Fatalf("draft invoice before import = status %q number %v, want unnumbered draft", draft.Status, draft.Number)
		}

		bankingService := newLifecycleBankingService(t, h, invoices)
		account := createLifecycleBankAccount(t, ctx, bankingService, "Revolut EUR", "EUR")
		if _, err := bankingService.ImportCSV(ctx, account.ID, banking.ImportFile{
			Filename: "revolut-draft-contoso.csv",
			Reader: bytes.NewReader(fixtures.RevolutCSV(fixtures.RevolutTxn{
				ID:        "contoso-draft-paid-4500",
				Date:      settleDate.Add(12 * time.Hour),
				Payee:     "Contoso GmbH",
				Reference: "bank transfer",
				Amount:    money.Money{Amount: 450_000, Currency: "EUR"},
			})),
		}); err != nil {
			t.Fatalf("ImportCSV(draft) error = %v", err)
		}

		match := lifecycleInvoiceMatch(t, ctx, bankingService, draft.ID)
		if match.Suggestion.Confidence < 0.80 {
			t.Fatalf("draft match confidence = %.2f, want >= 0.80", match.Suggestion.Confidence)
		}
		if !strings.Contains(match.Suggestion.Explanation, "draft invoice match") ||
			!strings.Contains(match.Suggestion.Explanation, "will send the invoice before allocating payment") {
			t.Fatalf("draft match explanation = %q, want draft send-and-allocate copy", match.Suggestion.Explanation)
		}

		result, err := bankingService.ConfirmMatch(ctx, match.Transaction.ID)
		if err != nil {
			t.Fatalf("ConfirmMatch(draft) error = %v", err)
		}
		if result.InvoiceID != draft.ID || result.Transaction.State != banking.TransactionStateReconciled {
			t.Fatalf("ConfirmMatch(draft) = %+v, want reconciled invoice %s", result, draft.ID)
		}
		assertLifecycleMoney(t, result.RealisedFXGBP, 4_500, "GBP")

		settled, err := invoices.Invoice(ctx, draft.ID)
		if err != nil {
			t.Fatalf("Invoice(%s) after draft confirm: %v", draft.ID, err)
		}
		if settled.Status != invoicing.InvoiceStatusPaid {
			t.Fatalf("draft-confirmed invoice status = %q, want paid", settled.Status)
		}
		if settled.Number == nil || *settled.Number != "INV-2025-01" {
			t.Fatalf("draft-confirmed invoice number = %v, want INV-2025-01", settled.Number)
		}
		lockID := mustLifecycleLockID(t, settled)
		assertLifecycleRateLock(t, h, *settled.LockID, "invoicing:INV-2025-01", "EUR", "GBP", "0.850000000000000000", "2025-05-01")
		assertLifecyclePostings(t, h, invoicing.ModuleName, invoiceLifecycleSendSourceRef("INV-2025-01"), []wantLifecyclePosting{
			{account: "1100-debtors-eur", amount: 450_000, currency: "EUR", amountGBP: 382_500},
			{account: "4000-sales", amount: -450_000, currency: "EUR", amountGBP: -382_500},
		})
		assertLifecyclePostings(t, h, banking.ModuleName, invoiceLifecycleBankingSourceRef(match.Transaction.ID), []wantLifecyclePosting{
			{account: string(account.LedgerAccountCode), amount: 450_000, currency: "EUR", amountGBP: 387_000},
			{account: "1100-debtors-eur", amount: -450_000, currency: "EUR", amountGBP: -387_000},
		})
		assertRealisedFXRows(t, ctx, h.DB, draft.ID, moneyfx.LockID(lockID), 1, 4_500)
		assertLifecyclePostings(t, h, moneyfx.ModuleName, invoiceLifecycleSettlementSourceRef(draft.ID), []wantLifecyclePosting{
			{account: "1101-debtors-gbp", amount: 4_500, currency: "GBP", amountGBP: 4_500},
			{account: "4900-fx-gain-loss", amount: -4_500, currency: "GBP", amountGBP: -4_500},
		})
		assertLifecycleActiveSuggestionCount(t, h, match.Transaction.ID, 0)
		wantSentEvents := []invoicing.InvoiceSent{{
			InvoiceID: draft.ID,
			Number:    "INV-2025-01",
			ClientID:  contoso.ID,
			Amount:    invoicing.Money{Amount: 450_000, Currency: "EUR"},
			DueDate:   settled.DueDate,
		}}
		if !reflect.DeepEqual(sentEvents, wantSentEvents) {
			t.Fatalf("draft confirm InvoiceSent events = %#v, want %#v", sentEvents, wantSentEvents)
		}
		assertLifecycleInvoiceDebtorsNetZero(t, h, []string{
			invoiceLifecycleSendSourceRef("INV-2025-01"),
			invoiceLifecycleBankingSourceRef(match.Transaction.ID),
			invoiceLifecycleSettlementSourceRef(draft.ID),
		})
		it.AssertLedgerBalanced(t, h)
	})

	t.Run("settlement rollback retry leaves no postings or numbering gap", func(t *testing.T) {
		ctx := context.Background()
		issueDate := day(2025, time.May, 1)
		settleDate := day(2025, time.May, 2)
		h := harness.New(t, harness.Options{ClockStart: issueDate.Add(9 * time.Hour)})
		fixtures.Company(t, h)
		fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{
			issueDate:  "0.8500",
			settleDate: "0.8600",
		}))
		contoso := fixtures.Contoso(t, h)
		invoices := newLifecycleInvoiceService(t, h, lifecycleIdentity(t, h))
		sent := sendLifecycleInvoiceViaHTTP(t, h, patchLifecycleDraftLinesViaHTTP(
			t,
			h,
			createLifecycleDraftViaHTTP(t, h, contoso.ID).ID,
			"retainer-crash",
			"Crash consistency retainer",
			450_000,
			"2025-05-15",
		).ID).Invoice
		lockID := mustLifecycleLockID(t, sent)
		bankingService := newLifecycleBankingService(t, h, invoices)
		account := createLifecycleBankAccount(t, ctx, bankingService, "Revolut EUR", "EUR")
		if _, err := bankingService.ImportCSV(ctx, account.ID, banking.ImportFile{
			Filename: "revolut-crash.csv",
			Reader: bytes.NewReader(fixtures.RevolutCSV(fixtures.RevolutTxn{
				ID:        "contoso-crash-4500",
				Date:      settleDate.Add(12 * time.Hour),
				Payee:     "Contoso GmbH",
				Reference: "INV-2025-01",
				Amount:    money.Money{Amount: 450_000, Currency: "EUR"},
			})),
		}); err != nil {
			t.Fatalf("ImportCSV(crash) error = %v", err)
		}
		match := lifecycleInvoiceMatch(t, ctx, bankingService, sent.ID)

		forced := errors.New("forced realised FX crash")
		h.Bus.Subscribe(moneyfx.RealisedFXName, func(context.Context, db.Tx, bus.Event) error {
			return nil
		})
		h.FailNextBusSubscriber(moneyfx.RealisedFXName, forced)
		_, err := bankingService.ConfirmMatch(ctx, match.Transaction.ID)
		if !errors.Is(err, forced) {
			t.Fatalf("ConfirmMatch() error = %v, want forced realised FX failure", err)
		}
		afterFailure, err := invoices.Invoice(ctx, sent.ID)
		if err != nil {
			t.Fatalf("Invoice(%s) after failed settlement: %v", sent.ID, err)
		}
		if afterFailure.Status != invoicing.InvoiceStatusSent {
			t.Fatalf("invoice status after failed settlement = %q, want sent", afterFailure.Status)
		}
		assertLifecycleTransactionState(t, h, match.Transaction.ID, banking.TransactionStateSuggested)
		assertLifecycleActiveSuggestionCount(t, h, match.Transaction.ID, 1)
		assertLifecycleLedgerEntryCount(t, h, banking.ModuleName, invoiceLifecycleBankingSourceRef(match.Transaction.ID), 0)
		assertLifecycleLedgerEntryCount(t, h, moneyfx.ModuleName, invoiceLifecycleSettlementSourceRef(sent.ID), 0)
		assertRealisedFXRows(t, ctx, h.DB, sent.ID, moneyfx.LockID(lockID), 0, 0)

		retry, err := bankingService.ConfirmMatch(ctx, match.Transaction.ID)
		if err != nil {
			t.Fatalf("ConfirmMatch() retry error = %v", err)
		}
		assertLifecycleMoney(t, retry.RealisedFXGBP, 4_500, "GBP")
		assertLifecycleTransactionState(t, h, match.Transaction.ID, banking.TransactionStateReconciled)
		assertRealisedFXRows(t, ctx, h.DB, sent.ID, moneyfx.LockID(lockID), 1, 4_500)

		next := sendLifecycleInvoiceViaHTTP(t, h, patchLifecycleDraftLinesViaHTTP(
			t,
			h,
			createLifecycleDraftViaHTTP(t, h, contoso.ID).ID,
			"retainer-after-crash",
			"Post-crash retainer",
			10_000,
			"2025-05-15",
		).ID).Invoice
		if next.Number == nil || *next.Number != "INV-2025-02" {
			t.Fatalf("post-retry invoice number = %v, want no gap at INV-2025-02", next.Number)
		}
		it.AssertLedgerBalanced(t, h)
	})

	t.Run("flat rate settlement records no FX entry", func(t *testing.T) {
		ctx := context.Background()
		issueDate := day(2025, time.May, 1)
		settleDate := day(2025, time.May, 2)
		h := harness.New(t, harness.Options{ClockStart: issueDate.Add(9 * time.Hour)})
		fixtures.Company(t, h)
		fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{
			issueDate:  "0.8500",
			settleDate: "0.8500",
		}))
		contoso := fixtures.Contoso(t, h)
		invoices := newLifecycleInvoiceService(t, h, lifecycleIdentity(t, h))
		sent := sendLifecycleInvoiceViaHTTP(t, h, patchLifecycleDraftLinesViaHTTP(
			t,
			h,
			createLifecycleDraftViaHTTP(t, h, contoso.ID).ID,
			"retainer-zero",
			"Flat-rate retainer",
			450_000,
			"2025-05-15",
		).ID).Invoice
		lockID := mustLifecycleLockID(t, sent)
		bankingService := newLifecycleBankingService(t, h, invoices)
		account := createLifecycleBankAccount(t, ctx, bankingService, "Revolut EUR", "EUR")
		if _, err := bankingService.ImportCSV(ctx, account.ID, banking.ImportFile{
			Filename: "revolut-zero.csv",
			Reader: bytes.NewReader(fixtures.RevolutCSV(fixtures.RevolutTxn{
				ID:        "contoso-zero-4500",
				Date:      settleDate.Add(12 * time.Hour),
				Payee:     "Contoso GmbH",
				Reference: "INV-2025-01",
				Amount:    money.Money{Amount: 450_000, Currency: "EUR"},
			})),
		}); err != nil {
			t.Fatalf("ImportCSV(zero) error = %v", err)
		}
		match := lifecycleInvoiceMatch(t, ctx, bankingService, sent.ID)
		result, err := bankingService.ConfirmMatch(ctx, match.Transaction.ID)
		if err != nil {
			t.Fatalf("ConfirmMatch(zero) error = %v", err)
		}
		assertLifecycleMoney(t, result.RealisedFXGBP, 0, "GBP")
		assertRealisedFXRows(t, ctx, h.DB, sent.ID, moneyfx.LockID(lockID), 1, 0)
		assertLifecycleLedgerEntryCount(t, h, moneyfx.ModuleName, invoiceLifecycleSettlementSourceRef(sent.ID), 0)
		it.AssertLedgerBalanced(t, h)
	})
}

type invoiceLifecycleSendResponse struct {
	Invoice    invoicing.Invoice `json:"invoice"`
	Number     string            `json:"number"`
	LockedRate struct {
		ID   int64  `json:"id"`
		Rate string `json:"rate"`
	} `json:"locked_rate"`
}

type invoiceLifecycleListResponse struct {
	Invoices   []invoicing.InvoiceListItem    `json:"invoices"`
	Counts     []invoicing.InvoiceStatusCount `json:"counts"`
	TotalCount int                            `json:"total_count"`
	Totals     invoicing.InvoiceTotalsSummary `json:"totals"`
}

type invoiceLifecycleHTTPResponse struct {
	StatusCode int
	Header     nethttp.Header
	Body       []byte
}

type wantLifecyclePosting struct {
	account   string
	amount    int64
	currency  string
	amountGBP int64
}

type lifecycleRateLocker struct {
	service *moneyfx.Service
}

func (l lifecycleRateLocker) LockRate(ctx context.Context, tx db.Tx, ref invoicing.RateLockRef, from string, to string, date time.Time) (invoicing.RateLock, error) {
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

func (l lifecycleRateLocker) RateLock(ctx context.Context, id int64) (invoicing.RateLock, error) {
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

func newLifecycleInvoiceService(t testing.TB, h *harness.Harness, identityAPI identity.Identity) *invoicing.Service {
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
	)
}

func newLifecycleBankingService(t testing.TB, h *harness.Harness, invoices *invoicing.Service) *banking.Service {
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

func lifecycleIdentity(t testing.TB, h *harness.Harness) identity.Identity {
	t.Helper()
	return identity.NewTransactionalProfileService(testdb.AsModule(t, "identity"), h.Bus, identity.WithDataDir(h.IdentityDataDir))
}

func lifecycleTodayRate(_ context.Context, from string, to string) (invoicing.FXRate, time.Time, error) {
	value := "0.8500"
	if from == to {
		value = "1"
	}
	return invoicing.FXRate{
		From:   from,
		To:     to,
		Value:  value,
		Source: "test",
	}, day(2025, time.May, 1).Add(12 * time.Hour), nil
}

func createLifecycleDraftViaHTTP(t testing.TB, h *harness.Harness, clientID string) invoicing.Invoice {
	t.Helper()
	resp := lifecycleInvoiceRequest(t, h, nethttp.MethodPost, "/api/invoicing/invoices", lifecycleJSON(t, map[string]any{
		"client_id": clientID,
	}))
	if resp.StatusCode != nethttp.StatusCreated {
		t.Fatalf("create draft status = %d, want %d; body=%s", resp.StatusCode, nethttp.StatusCreated, string(resp.Body))
	}
	return decodeLifecycleInvoice(t, resp)
}

func patchLifecycleDraftLinesViaHTTP(t testing.TB, h *harness.Harness, id string, lineID string, description string, amount int64, dueDate string) invoicing.Invoice {
	t.Helper()
	resp := lifecycleInvoiceRequest(t, h, nethttp.MethodPatch, "/api/invoicing/invoices/"+id, lifecycleJSON(t, map[string]any{
		"due_date": dueDate,
		"lines": []map[string]any{
			{
				"id":          lineID,
				"description": description,
				"qty":         "1",
				"unit_price": map[string]any{
					"amount":   amount,
					"currency": string(invoicing.CurrencyEUR),
				},
			},
		},
	}))
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("patch draft status = %d, want %d; body=%s", resp.StatusCode, nethttp.StatusOK, string(resp.Body))
	}
	return decodeLifecycleInvoice(t, resp)
}

func sendLifecycleInvoiceViaHTTP(t testing.TB, h *harness.Harness, id string) invoiceLifecycleSendResponse {
	t.Helper()
	resp := lifecycleInvoiceRequest(t, h, nethttp.MethodPost, "/api/invoicing/invoices/"+id+"/send", nil)
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("send invoice status = %d, want %d; body=%s", resp.StatusCode, nethttp.StatusOK, string(resp.Body))
	}
	var body invoiceLifecycleSendResponse
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("decode send response: %v; body=%s", err, string(resp.Body))
	}
	return body
}

func listLifecycleInvoicesViaHTTP(t testing.TB, h *harness.Harness, query string) invoiceLifecycleListResponse {
	t.Helper()
	resp := lifecycleInvoiceRequest(t, h, nethttp.MethodGet, "/api/invoicing/invoices?"+query, nil)
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("list invoices status = %d, want %d; body=%s", resp.StatusCode, nethttp.StatusOK, string(resp.Body))
	}
	var body invoiceLifecycleListResponse
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("decode list response: %v; body=%s", err, string(resp.Body))
	}
	return body
}

func lifecycleInvoiceRequest(t testing.TB, h *harness.Harness, method string, path string, body io.Reader) invoiceLifecycleHTTPResponse {
	t.Helper()
	req, err := nethttp.NewRequestWithContext(context.Background(), method, path, body)
	if err != nil {
		t.Fatalf("create %s %s request: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return invoiceLifecycleHTTPResponse{StatusCode: resp.StatusCode, Header: resp.Header, Body: respBody}
}

func lifecycleJSON(t testing.TB, body map[string]any) io.Reader {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal lifecycle request: %v", err)
	}
	return bytes.NewReader(encoded)
}

func decodeLifecycleInvoice(t testing.TB, resp invoiceLifecycleHTTPResponse) invoicing.Invoice {
	t.Helper()
	var invoice invoicing.Invoice
	if err := json.Unmarshal(resp.Body, &invoice); err != nil {
		t.Fatalf("decode invoice response: %v; body=%s", err, string(resp.Body))
	}
	return invoice
}

func createLifecycleBankAccount(t testing.TB, ctx context.Context, service *banking.Service, name string, currency string) banking.BankAccount {
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

func lifecycleInvoiceMatch(t testing.TB, ctx context.Context, service *banking.Service, invoiceID string) banking.ReviewQueueItem {
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

func assertLifecycleInvoicePDFGolden(t testing.TB, ctx context.Context, invoices *invoicing.Service, invoiceID string) {
	t.Helper()
	if !requireLifecycleChrome(t) {
		return
	}

	payload, err := invoices.InvoicePrintPayload(ctx, invoiceID, false)
	if err != nil {
		t.Fatalf("InvoicePrintPayload(%s) error = %v", invoiceID, err)
	}
	server := startLifecycleInvoicePrintServer(t)
	pdfBytes, err := invoicing.NewChromePDFEngine(server.URL).RenderInvoicePDF(ctx, payload)
	if err != nil {
		t.Fatalf("RenderInvoicePDF(%s) error = %v", invoiceID, err)
	}
	text, err := extractLifecyclePDFText(pdfBytes)
	if err != nil {
		t.Fatalf("extract invoice PDF text: %v", err)
	}
	for _, want := range []string{"Article 196", "\\u20ac0.00", "0.8500", "SEPA BANK DETAILS"} {
		want = strings.ReplaceAll(want, "\\u20ac", "\u20ac")
		if !strings.Contains(text, want) {
			t.Fatalf("invoice PDF text missing %q:\n%s", want, text)
		}
	}
	if !lifecycleDeterministicChromeEnabled() {
		t.Log("invoice lifecycle PDF raster golden skipped; set GOLDEN_REQUIRE_CHROME=1 with deterministic CHROME_BIN to enforce")
		return
	}
	golden.PDF(t, "invoice-lifecycle-contoso", pdfBytes, golden.WithCanonicalText())
}

func startLifecycleInvoicePrintServer(t testing.TB) *httptest.Server {
	t.Helper()
	assets, err := web.Dist()
	if err != nil {
		t.Fatalf("load embedded web assets: %v", err)
	}
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("invoice lifecycle PDF golden requires built SPA assets: %v; run task web:build", err)
	}
	fileServer := nethttp.FileServer(nethttp.FS(assets))
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		cleanPath := strings.TrimPrefix(r.URL.Path, "/")
		if cleanPath != "" && !strings.HasPrefix(r.URL.Path, "/print/") {
			if _, err := fs.Stat(assets, cleanPath); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	}))
	t.Cleanup(server.Close)
	return server
}

func requireLifecycleChrome(t testing.TB) bool {
	t.Helper()
	if findLifecycleChromePath() != "" {
		return true
	}
	message := "Chrome/headless-shell not found; set CHROME_BIN for invoice lifecycle PDF golden"
	if os.Getenv("GOLDEN_REQUIRE_CHROME") == "1" {
		t.Fatal(message)
	}
	t.Log(message)
	return false
}

func lifecycleDeterministicChromeEnabled() bool {
	return os.Getenv("GOLDEN_REQUIRE_CHROME") == "1" || os.Getenv("GOLDEN_RUN_CHROME") == "1"
}

func findLifecycleChromePath() string {
	if chromePath := os.Getenv("CHROME_BIN"); chromePath != "" {
		if _, err := os.Stat(chromePath); err == nil {
			return chromePath
		}
		return ""
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "headless-shell"} {
		path, err := exec.LookPath(name)
		if err == nil {
			return path
		}
	}
	if runtime.GOOS == "darwin" {
		for _, path := range []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		} {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}
	return ""
}

func extractLifecyclePDFText(pdfBytes []byte) (string, error) {
	reader, err := pdfreader.NewReader(bytes.NewReader(pdfBytes), int64(len(pdfBytes)))
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for pageNum := 1; pageNum <= reader.NumPage(); pageNum++ {
		if pageNum > 1 {
			text.WriteByte('\n')
		}
		rows, err := reader.Page(pageNum).GetTextByRow()
		if err != nil {
			return "", fmt.Errorf("extract page %d rows: %w", pageNum, err)
		}
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].Position < rows[j].Position
		})
		for _, row := range rows {
			sort.Sort(row.Content)
			line := lifecyclePDFTextLine(row.Content)
			if line == "" {
				continue
			}
			text.WriteString(line)
			text.WriteByte('\n')
		}
	}
	return text.String(), nil
}

func lifecyclePDFTextLine(texts pdfreader.TextHorizontal) string {
	var line strings.Builder
	var prevRight float64
	for i, text := range texts {
		rawChunk := strings.ReplaceAll(text.S, "\t", " ")
		chunk := strings.TrimSpace(rawChunk)
		if chunk == "" {
			continue
		}
		if line.Len() > 0 && i > 0 && (strings.HasPrefix(rawChunk, " ") || text.X-prevRight > math.Max(1, text.FontSize*0.2)) {
			line.WriteByte(' ')
		}
		line.WriteString(chunk)
		prevRight = text.X + text.W
	}
	return line.String()
}

func assertLifecycleRateLock(t testing.TB, h *harness.Harness, lockID string, ref string, from string, to string, rate string, rateDate string) {
	t.Helper()
	var gotRef, gotFrom, gotTo, gotRate, gotDate string
	if err := h.DB.QueryRow(context.Background(), `
SELECT ref, from_currency, to_currency, rate::text, rate_date::text
FROM moneyfx.rate_locks
WHERE id = $1`, lockID).Scan(&gotRef, &gotFrom, &gotTo, &gotRate, &gotDate); err != nil {
		t.Fatalf("query rate lock %s: %v", lockID, err)
	}
	if gotRef != ref || gotFrom != from || gotTo != to || gotRate != rate || gotDate != rateDate {
		t.Fatalf("rate lock %s = ref=%s from=%s to=%s rate=%s date=%s, want ref=%s from=%s to=%s rate=%s date=%s",
			lockID, gotRef, gotFrom, gotTo, gotRate, gotDate, ref, from, to, rate, rateDate)
	}
}

func assertLifecyclePostings(t testing.TB, h *harness.Harness, module string, sourceRef string, want []wantLifecyclePosting) {
	t.Helper()
	rows, err := h.DB.Query(context.Background(), `
SELECT p.account_code, p.amount, p.currency, p.amount_gbp
FROM ledger.journal_entries AS je
JOIN ledger.postings AS p ON p.entry_id = je.id
WHERE je.source_module = $1
	AND je.source_ref = $2
ORDER BY p.id`, module, sourceRef)
	if err != nil {
		t.Fatalf("query ledger postings for %s/%s: %v", module, sourceRef, err)
	}
	defer rows.Close()
	var got []wantLifecyclePosting
	for rows.Next() {
		var posting wantLifecyclePosting
		if err := rows.Scan(&posting.account, &posting.amount, &posting.currency, &posting.amountGBP); err != nil {
			t.Fatalf("scan ledger posting for %s/%s: %v", module, sourceRef, err)
		}
		got = append(got, posting)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("collect ledger postings for %s/%s: %v", module, sourceRef, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ledger postings for %s/%s = %#v, want %#v", module, sourceRef, got, want)
	}
}

func assertLifecycleLedgerEntryCount(t testing.TB, h *harness.Harness, module string, sourceRef string, want int) {
	t.Helper()
	var got int
	if err := h.DB.QueryRow(context.Background(), `
SELECT count(*)
FROM ledger.journal_entries
WHERE source_module = $1
	AND source_ref = $2`, module, sourceRef).Scan(&got); err != nil {
		t.Fatalf("count ledger entries for %s/%s: %v", module, sourceRef, err)
	}
	if got != want {
		t.Fatalf("ledger entries for %s/%s = %d, want %d", module, sourceRef, got, want)
	}
}

func assertLifecycleTrialBalanceZero(t testing.TB, h *harness.Harness) {
	t.Helper()
	report, err := ledger.New(h.LedgerPool).TrialBalance(context.Background(), time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("TrialBalance() error = %v; report=%+v", err, report)
	}
	if !report.Balanced || report.GBPTotal != 0 {
		t.Fatalf("TrialBalance() = %+v, want balanced zero", report)
	}
}

func assertLifecycleInvoiceDebtorsNetZero(t testing.TB, h *harness.Harness, sourceRefs []string) {
	t.Helper()
	rows, err := h.DB.Query(context.Background(), `
SELECT COALESCE(sum(p.amount_gbp), 0)::bigint
FROM ledger.journal_entries AS je
JOIN ledger.postings AS p ON p.entry_id = je.id
WHERE p.account_code IN ('1100-debtors-eur', '1101-debtors-gbp')
	AND je.source_ref = ANY($1::text[])`, sourceRefs)
	if err != nil {
		t.Fatalf("query invoice debtors net: %v", err)
	}
	defer rows.Close()
	var got int64
	if rows.Next() {
		if err := rows.Scan(&got); err != nil {
			t.Fatalf("scan invoice debtors net: %v", err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("collect invoice debtors net: %v", err)
	}
	if got != 0 {
		t.Fatalf("invoice debtors amount_gbp net = %d, want zero", got)
	}
}

func assertLifecycleRealisedFXPL(t testing.TB, ctx context.Context, h *harness.Harness, invoices *invoicing.Service, want int64) {
	t.Helper()
	service := reports.New(
		ledger.New(h.LedgerPool),
		lifecycleIdentity(t, h),
		invoices,
	)
	pl, err := service.ProfitAndLoss(ctx, reports.Period{
		From: day(2025, time.May, 1),
		To:   day(2025, time.May, 31),
	})
	if err != nil {
		t.Fatalf("ProfitAndLoss() error = %v", err)
	}
	if pl.RealisedFXGains.Label != "Realised FX gains" || pl.RealisedFXGains.Amount != (money.Money{Amount: want, Currency: "GBP"}) {
		t.Fatalf("RealisedFXGains = %#v, want GBP %d", pl.RealisedFXGains, want)
	}
}

func assertLifecycleTransactionState(t testing.TB, h *harness.Harness, txnID banking.TransactionID, want banking.TransactionState) {
	t.Helper()
	var got string
	if err := h.BankingPool.QueryRow(context.Background(), `
SELECT state::text
FROM transactions
WHERE id = $1`, int64(txnID)).Scan(&got); err != nil {
		t.Fatalf("load transaction %d state: %v", txnID, err)
	}
	if banking.TransactionState(got) != want {
		t.Fatalf("transaction %d state = %q, want %q", txnID, got, want)
	}
}

func assertLifecycleActiveSuggestionCount(t testing.TB, h *harness.Harness, txnID banking.TransactionID, want int) {
	t.Helper()
	var got int
	if err := h.BankingPool.QueryRow(context.Background(), `
SELECT count(*)::integer
FROM suggestions
WHERE txn_id = $1
	AND superseded_at IS NULL`, int64(txnID)).Scan(&got); err != nil {
		t.Fatalf("count active suggestions for %d: %v", txnID, err)
	}
	if got != want {
		t.Fatalf("active suggestions for %d = %d, want %d", txnID, got, want)
	}
}

func assertLifecycleMoney(t testing.TB, got money.Money, wantAmount int64, wantCurrency string) {
	t.Helper()
	if got.Amount != wantAmount || got.Currency != wantCurrency {
		t.Fatalf("money = %+v, want %d %s", got, wantAmount, wantCurrency)
	}
}

func mustLifecycleLockID(t testing.TB, invoice invoicing.Invoice) int64 {
	t.Helper()
	if invoice.LockID == nil {
		t.Fatal("invoice LockID = nil")
	}
	id, err := strconv.ParseInt(*invoice.LockID, 10, 64)
	if err != nil {
		t.Fatalf("parse invoice LockID %q: %v", *invoice.LockID, err)
	}
	return id
}

func invoiceLifecycleSendSourceRef(number string) string {
	return "invoice:" + number + ":send"
}

func invoiceLifecycleSettlementSourceRef(invoiceID string) string {
	return "invoice:" + invoiceID + ":settlement"
}

func invoiceLifecycleBankingSourceRef(txnID banking.TransactionID) string {
	return fmt.Sprintf("banking:%d:confirm-match", txnID)
}
