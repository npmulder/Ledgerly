//go:build integration

package harness_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
)

func TestInvoicingInvoicesDraftLifecycleAndTotals(t *testing.T) {
	h := harness.New(t, harness.Options{})
	h.Clock.Set(time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC))
	service := newInvoiceService(t, h)

	fabrikam := fixtures.Fabrikam(t, h)
	draft, err := service.CreateDraft(context.Background(), fabrikam.ID)
	if err != nil {
		t.Fatalf("CreateDraft() error = %v", err)
	}
	if draft.Number != nil {
		t.Fatalf("draft Number = %v, want nil", *draft.Number)
	}
	if draft.Status != invoicing.InvoiceStatusDraft {
		t.Fatalf("draft Status = %q, want draft", draft.Status)
	}
	if draft.Currency != invoicing.CurrencyGBP {
		t.Fatalf("draft Currency = %q, want GBP", draft.Currency)
	}
	if draft.VATTreatment != invoicing.VATTreatmentDomestic {
		t.Fatalf("draft VATTreatment = %q, want domestic", draft.VATTreatment)
	}
	assertDate(t, draft.IssueDate, "2025-05-01")
	assertDate(t, draft.DueDate, "2025-05-31")

	lines := []invoicing.InvoiceLineInput{
		{
			Description: "Fractional day",
			Qty:         invoicing.MustQuantity("1.5"),
			UnitPrice:   invoicing.Money{Amount: 1005, Currency: string(invoicing.CurrencyGBP)},
		},
		{
			Description: "Half-penny tie",
			Qty:         invoicing.MustQuantity("0.5"),
			UnitPrice:   invoicing.Money{Amount: 5, Currency: string(invoicing.CurrencyGBP)},
		},
	}
	updated, err := service.UpdateDraft(context.Background(), draft.ID, invoicing.DraftPatch{Lines: &lines})
	if err != nil {
		t.Fatalf("UpdateDraft(lines) error = %v", err)
	}
	assertMoney(t, updated.Lines[0].LineTotal, 1508, "GBP")
	assertMoney(t, updated.Lines[1].LineTotal, 2, "GBP")
	assertMoney(t, updated.Totals.Subtotal, 1510, "GBP")
	assertMoney(t, updated.Totals.VAT, 302, "GBP")
	assertMoney(t, updated.Totals.Total, 1812, "GBP")
	if updated.Totals.ApproxGBP == nil {
		t.Fatal("ApproxGBP = nil, want draft approximation")
	}
	assertMoney(t, updated.Totals.ApproxGBP.Amount, 1812, "GBP")

	fetched, err := service.Invoice(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("Invoice() error = %v", err)
	}
	assertMoney(t, fetched.Totals.Total, 1812, "GBP")

	contoso := fixtures.Contoso(t, h)
	reverseDraft, err := service.CreateDraft(context.Background(), contoso.ID)
	if err != nil {
		t.Fatalf("CreateDraft(reverse) error = %v", err)
	}
	reverseLines := []invoicing.InvoiceLineInput{
		{
			Description: "Tie down",
			Qty:         invoicing.MustQuantity("0.5"),
			UnitPrice:   invoicing.Money{Amount: 5, Currency: string(invoicing.CurrencyEUR)},
		},
		{
			Description: "Tie up",
			Qty:         invoicing.MustQuantity("0.5"),
			UnitPrice:   invoicing.Money{Amount: 7, Currency: string(invoicing.CurrencyEUR)},
		},
	}
	reverseUpdated, err := service.UpdateDraft(context.Background(), reverseDraft.ID, invoicing.DraftPatch{Lines: &reverseLines})
	if err != nil {
		t.Fatalf("UpdateDraft(reverse lines) error = %v", err)
	}
	assertMoney(t, reverseUpdated.Lines[0].LineTotal, 2, "EUR")
	assertMoney(t, reverseUpdated.Lines[1].LineTotal, 4, "EUR")
	assertMoney(t, reverseUpdated.Totals.Subtotal, 6, "EUR")
	assertMoney(t, reverseUpdated.Totals.VAT, 0, "EUR")
	assertMoney(t, reverseUpdated.Totals.Total, 6, "EUR")
	if reverseUpdated.Totals.ApproxGBP == nil {
		t.Fatal("reverse ApproxGBP = nil, want fake EUR->GBP approximation")
	}
	assertMoney(t, reverseUpdated.Totals.ApproxGBP.Amount, 5, "GBP")

	if _, err := h.DB.Exec(context.Background(), `UPDATE invoicing.invoices SET status = 'sent' WHERE id = $1`, draft.ID); err != nil {
		t.Fatalf("mark draft sent: %v", err)
	}
	newDueDate := time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC)
	_, err = service.UpdateDraft(context.Background(), draft.ID, invoicing.DraftPatch{DueDate: &newDueDate})
	if !errors.Is(err, invoicing.ErrInvoiceImmutable) {
		t.Fatalf("UpdateDraft(sent) error = %v, want ErrInvoiceImmutable", err)
	}
	if err := service.DeleteDraft(context.Background(), draft.ID); !errors.Is(err, invoicing.ErrInvoiceImmutable) {
		t.Fatalf("DeleteDraft(sent) error = %v, want ErrInvoiceImmutable", err)
	}

	modulePool := testdb.AsModule(t, invoicing.ModuleName)
	store := invoicing.Store{}
	tx, err := modulePool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin settlement tx: %v", err)
	}
	txnRef := "txn_123"
	settledDate := time.Date(2025, 5, 20, 0, 0, 0, 0, time.UTC)
	settledAmount := invoicing.Money{Amount: 1812, Currency: string(invoicing.CurrencyGBP)}
	settled, err := store.SetInvoiceSettlement(context.Background(), tx, draft.ID, invoicing.InvoiceSettlement{
		TxnRef:        &txnRef,
		SettledDate:   &settledDate,
		SettledAmount: &settledAmount,
	})
	if err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("SetInvoiceSettlement(sent) error = %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit settlement tx: %v", err)
	}
	if settled.Status != invoicing.InvoiceStatusPaid {
		t.Fatalf("settled Status = %q, want paid", settled.Status)
	}
	if settled.SettlementTxnRef == nil || *settled.SettlementTxnRef != txnRef {
		t.Fatalf("SettlementTxnRef = %v, want %q", settled.SettlementTxnRef, txnRef)
	}
	if settled.SettledAmount == nil {
		t.Fatal("SettledAmount = nil, want amount")
	}
	assertMoney(t, *settled.SettledAmount, 1812, "GBP")
}

func TestInvoicingNumberingConcurrentGapFree(t *testing.T) {
	h := harness.New(t, harness.Options{})
	_ = h
	modulePool := testdb.AsModule(t, invoicing.ModuleName)
	store := invoicing.Store{}

	const workers = 50
	start := make(chan struct{})
	results := make(chan string, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			tx, err := modulePool.Begin(ctx)
			if err != nil {
				errs <- fmt.Errorf("begin tx: %w", err)
				return
			}
			committed := false
			defer func() {
				if !committed {
					_ = tx.Rollback(context.Background())
				}
			}()
			number, err := store.NextNumber(ctx, tx, 2025)
			if err != nil {
				errs <- err
				return
			}
			if err := tx.Commit(ctx); err != nil {
				errs <- fmt.Errorf("commit tx: %w", err)
				return
			}
			committed = true
			results <- number
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent NextNumber error = %v", err)
		}
	}

	got := make([]string, 0, workers)
	for number := range results {
		got = append(got, number)
	}
	sort.Strings(got)
	if len(got) != workers {
		t.Fatalf("len(numbers) = %d, want %d; numbers=%v", len(got), workers, got)
	}
	for i, number := range got {
		want := fmt.Sprintf("INV-2025-%02d", i+1)
		if number != want {
			t.Fatalf("number[%d] = %q, want %q; all=%v", i, number, want, got)
		}
	}
}

func newInvoiceService(t testing.TB, h *harness.Harness) *invoicing.Service {
	t.Helper()
	modulePool := testdb.AsModule(t, invoicing.ModuleName)
	return invoicing.NewService(
		modulePool,
		invoicing.Store{},
		invoicing.WithClock(h.Clock),
		invoicing.WithTodayRate(fakeTodayRate),
	)
}

func fakeTodayRate(_ context.Context, from string, to string) (invoicing.FXRate, time.Time, error) {
	value := "4/5"
	if from == to {
		value = "1"
	}
	return invoicing.FXRate{
		From:   from,
		To:     to,
		Value:  value,
		Source: "test",
	}, time.Date(2025, 5, 1, 12, 0, 0, 0, time.UTC), nil
}

func assertMoney(t testing.TB, got invoicing.Money, wantAmount int64, wantCurrency string) {
	t.Helper()
	if got.Amount != wantAmount || got.Currency != wantCurrency {
		t.Fatalf("money = %+v, want %d %s", got, wantAmount, wantCurrency)
	}
}

func assertDate(t testing.TB, got time.Time, want string) {
	t.Helper()
	if got.Format(time.DateOnly) != want {
		t.Fatalf("date = %s, want %s", got.Format(time.DateOnly), want)
	}
}
