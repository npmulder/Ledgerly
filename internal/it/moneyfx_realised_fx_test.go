//go:build integration

package it_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/invoicing"
	it "github.com/npmulder/ledgerly/internal/it"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

func TestMain(m *testing.M) {
	os.Exit(testdb.Main(m))
}

func TestRealisedFXSettlementPostsGainAndIsIdempotent(t *testing.T) {
	f := newRealisedFXFixture(t, true)
	issueDate := day(2030, 1, 2)
	settlementDate := day(2030, 1, 3)
	invoiceID := "invoice-realised-gain"
	sourceRef := "invoicing:invoice-realised-gain:settlement"

	f.seedRates(t,
		moneyfx.ECBRate{Date: issueDate, Currency: "GBP", Rate: "0.8500"},
		moneyfx.ECBRate{Date: settlementDate, Currency: "GBP", Rate: "0.8600"},
	)
	lock := f.lockInvoice(t, invoiceID, issueDate)

	evt := invoicing.InvoiceSettled{
		InvoiceID:      invoiceID,
		LockID:         int64(lock.ID),
		NativeAmount:   eur(450_000),
		SettlementDate: settlementDate,
		SourceRef:      sourceRef,
	}
	if err := f.publishInvoiceSettled(evt); err != nil {
		t.Fatalf("publish gain settlement: %v", err)
	}

	assertRealisedFXEvents(t, f.events(), []moneyfx.RealisedFX{{
		InvoiceID: invoiceID,
		AmountGBP: gbp(4_500),
	}})
	assertMoneyFXLedgerPostings(t, f.ctx, f.harness.DB, sourceRef, []wantPosting{
		{account: "1101-debtors-gbp", amount: 4_500},
		{account: "4900-fx-gain-loss", amount: -4_500},
	})
	assertRealisedFXRows(t, f.ctx, f.harness.DB, invoiceID, lock.ID, 1, 4_500)
	it.AssertLedgerBalanced(t, f.harness)

	if err := f.publishInvoiceSettled(evt); err != nil {
		t.Fatalf("replay gain settlement: %v", err)
	}
	assertRealisedFXEvents(t, f.events(), []moneyfx.RealisedFX{{
		InvoiceID: invoiceID,
		AmountGBP: gbp(4_500),
	}})
	assertCountWhere(t, f.ctx, f.harness.DB, "ledger.journal_entries", "source_module = 'moneyfx' AND source_ref = $1", 1, sourceRef)
	assertRealisedFXRows(t, f.ctx, f.harness.DB, invoiceID, lock.ID, 1, 4_500)
	it.AssertLedgerBalanced(t, f.harness)
}

func TestRealisedFXSettlementPostsLoss(t *testing.T) {
	f := newRealisedFXFixture(t, true)
	issueDate := day(2030, 2, 2)
	settlementDate := day(2030, 2, 3)
	invoiceID := "invoice-realised-loss"
	sourceRef := "invoicing:invoice-realised-loss:settlement"

	f.seedRates(t,
		moneyfx.ECBRate{Date: issueDate, Currency: "GBP", Rate: "0.8600"},
		moneyfx.ECBRate{Date: settlementDate, Currency: "GBP", Rate: "0.8500"},
	)
	lock := f.lockInvoice(t, invoiceID, issueDate)

	if err := f.publishInvoiceSettled(invoicing.InvoiceSettled{
		InvoiceID:      invoiceID,
		LockID:         int64(lock.ID),
		NativeAmount:   eur(450_000),
		SettlementDate: settlementDate,
		SourceRef:      sourceRef,
	}); err != nil {
		t.Fatalf("publish loss settlement: %v", err)
	}

	assertRealisedFXEvents(t, f.events(), []moneyfx.RealisedFX{{
		InvoiceID: invoiceID,
		AmountGBP: gbp(-4_500),
	}})
	assertMoneyFXLedgerPostings(t, f.ctx, f.harness.DB, sourceRef, []wantPosting{
		{account: "1101-debtors-gbp", amount: -4_500},
		{account: "4900-fx-gain-loss", amount: 4_500},
	})
	assertRealisedFXRows(t, f.ctx, f.harness.DB, invoiceID, lock.ID, 1, -4_500)
	it.AssertLedgerBalanced(t, f.harness)
}

func TestRealisedFXSettlementZeroDeltaNoEntryOrEvent(t *testing.T) {
	f := newRealisedFXFixture(t, true)
	issueDate := day(2030, 3, 2)
	settlementDate := day(2030, 3, 3)
	invoiceID := "invoice-realised-zero"
	sourceRef := "invoicing:invoice-realised-zero:settlement"

	f.seedRates(t,
		moneyfx.ECBRate{Date: issueDate, Currency: "GBP", Rate: "0.8500"},
		moneyfx.ECBRate{Date: settlementDate, Currency: "GBP", Rate: "0.8500"},
	)
	lock := f.lockInvoice(t, invoiceID, issueDate)

	if err := f.publishInvoiceSettled(invoicing.InvoiceSettled{
		InvoiceID:      invoiceID,
		LockID:         int64(lock.ID),
		NativeAmount:   eur(450_000),
		SettlementDate: settlementDate,
		SourceRef:      sourceRef,
	}); err != nil {
		t.Fatalf("publish zero-delta settlement: %v", err)
	}

	assertRealisedFXEvents(t, f.events(), nil)
	assertCountWhere(t, f.ctx, f.harness.DB, "ledger.journal_entries", "source_module = 'moneyfx' AND source_ref = $1", 0, sourceRef)
	assertRealisedFXRows(t, f.ctx, f.harness.DB, invoiceID, lock.ID, 1, 0)
	it.AssertLedgerBalanced(t, f.harness)
}

func TestRealisedFXSettlementRejectsLockForDifferentInvoice(t *testing.T) {
	f := newRealisedFXFixture(t, true)
	issueDate := day(2030, 4, 2)
	settlementDate := day(2030, 4, 3)
	invoiceID := "invoice-realised-owner"
	otherInvoiceID := "invoice-realised-other"
	sourceRef := "invoicing:invoice-realised-owner:settlement"

	f.seedRates(t,
		moneyfx.ECBRate{Date: issueDate, Currency: "GBP", Rate: "0.8500"},
		moneyfx.ECBRate{Date: settlementDate, Currency: "GBP", Rate: "0.8600"},
	)
	otherLock := f.lockInvoice(t, otherInvoiceID, issueDate)

	err := f.publishInvoiceSettled(invoicing.InvoiceSettled{
		InvoiceID:      invoiceID,
		LockID:         int64(otherLock.ID),
		NativeAmount:   eur(450_000),
		SettlementDate: settlementDate,
		SourceRef:      sourceRef,
	})
	if err == nil || !strings.Contains(err.Error(), "belongs to invoicing:"+otherInvoiceID) {
		t.Fatalf("publish settlement with stale lock error = %v, want lock owner mismatch", err)
	}
	assertRealisedFXEvents(t, f.events(), nil)
	assertCountWhere(t, f.ctx, f.harness.DB, "ledger.journal_entries", "source_module = 'moneyfx' AND source_ref = $1", 0, sourceRef)
	assertRealisedFXRows(t, f.ctx, f.harness.DB, invoiceID, otherLock.ID, 0, 0)
	it.AssertLedgerBalanced(t, f.harness)

	lock := f.lockInvoice(t, invoiceID, issueDate)
	if err := f.publishInvoiceSettled(invoicing.InvoiceSettled{
		InvoiceID:      invoiceID,
		LockID:         int64(lock.ID),
		NativeAmount:   eur(450_000),
		SettlementDate: settlementDate,
		SourceRef:      sourceRef,
	}); err != nil {
		t.Fatalf("publish settlement with correct lock: %v", err)
	}
	assertRealisedFXEvents(t, f.events(), []moneyfx.RealisedFX{{
		InvoiceID: invoiceID,
		AmountGBP: gbp(4_500),
	}})
	assertMoneyFXLedgerPostings(t, f.ctx, f.harness.DB, sourceRef, []wantPosting{
		{account: "1101-debtors-gbp", amount: 4_500},
		{account: "4900-fx-gain-loss", amount: -4_500},
	})
	assertRealisedFXRows(t, f.ctx, f.harness.DB, invoiceID, lock.ID, 1, 4_500)
	it.AssertLedgerBalanced(t, f.harness)
}

func TestRealisedFXSettlementRollsBackWhenHandlerErrors(t *testing.T) {
	f := newRealisedFXFixture(t, false)
	issueDate := day(2030, 5, 2)
	settlementDate := day(2030, 5, 3)
	invoiceID := "invoice-realised-rollback"
	sourceRef := "invoicing:invoice-realised-rollback:settlement"
	forced := errors.New("forced realised FX subscriber failure")

	f.seedRates(t,
		moneyfx.ECBRate{Date: issueDate, Currency: "GBP", Rate: "0.8500"},
		moneyfx.ECBRate{Date: settlementDate, Currency: "GBP", Rate: "0.8600"},
	)
	lock := f.lockInvoice(t, invoiceID, issueDate)
	f.harness.Bus.Subscribe(moneyfx.RealisedFXName, func(context.Context, db.Tx, bus.Event) error {
		return nil
	})
	f.harness.FailNextBusSubscriber(moneyfx.RealisedFXName, forced)

	err := f.publishInvoiceSettled(invoicing.InvoiceSettled{
		InvoiceID:      invoiceID,
		LockID:         int64(lock.ID),
		NativeAmount:   eur(450_000),
		SettlementDate: settlementDate,
		SourceRef:      sourceRef,
	})
	if !errors.Is(err, forced) {
		t.Fatalf("publish settlement error = %v, want forced subscriber failure", err)
	}
	assertCountWhere(t, f.ctx, f.harness.DB, "ledger.journal_entries", "source_module = 'moneyfx' AND source_ref = $1", 0, sourceRef)
	assertRealisedFXRows(t, f.ctx, f.harness.DB, invoiceID, lock.ID, 0, 0)
	it.AssertLedgerBalanced(t, f.harness)
}

type realisedFXFixture struct {
	ctx           context.Context
	harness       *harness.Harness
	invoicingPool *pgxpool.Pool
	moneyFXPool   *pgxpool.Pool

	mu             sync.Mutex
	realisedEvents []moneyfx.RealisedFX
}

func newRealisedFXFixture(t *testing.T, captureEvents bool) *realisedFXFixture {
	t.Helper()

	h := harness.New(t, harness.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	f := &realisedFXFixture{
		ctx:           ctx,
		harness:       h,
		invoicingPool: testdb.AsModule(t, invoicing.ModuleName),
		moneyFXPool:   testdb.AsModule(t, moneyfx.ModuleName),
	}
	if captureEvents {
		h.Bus.Subscribe(moneyfx.RealisedFXName, func(_ context.Context, _ db.Tx, evt bus.Event) error {
			realised, err := realisedFXEvent(evt)
			if err != nil {
				return err
			}
			f.mu.Lock()
			f.realisedEvents = append(f.realisedEvents, realised)
			f.mu.Unlock()
			return nil
		})
	}
	return f
}

func (f *realisedFXFixture) seedRates(t *testing.T, rates ...moneyfx.ECBRate) {
	t.Helper()
	if err := moneyfx.NewStore(f.moneyFXPool).StoreECBRates(f.ctx, rates); err != nil {
		t.Fatalf("seed ECB rates: %v", err)
	}
}

func (f *realisedFXFixture) lockInvoice(t *testing.T, invoiceID string, issueDate time.Time) moneyfx.RateLock {
	t.Helper()

	tx, err := f.moneyFXPool.Begin(f.ctx)
	if err != nil {
		t.Fatalf("begin rate lock transaction: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	service := moneyfx.NewService(moneyfx.NewStore(f.moneyFXPool), f.harness.Clock)
	lock, err := service.Lock(f.ctx, tx, moneyfx.LockRef{Module: invoicing.ModuleName, Ref: invoiceID}, "EUR", "GBP", issueDate)
	if err != nil {
		t.Fatalf("lock invoice rate: %v", err)
	}
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatalf("commit rate lock: %v", err)
	}
	committed = true
	return lock
}

func (f *realisedFXFixture) publishInvoiceSettled(evt invoicing.InvoiceSettled) (err error) {
	tx, err := f.invoicingPool.Begin(f.ctx)
	if err != nil {
		return fmt.Errorf("begin invoicing transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = errors.Join(err, tx.Rollback(context.Background()))
		}
	}()

	if err = f.harness.Bus.Publish(f.ctx, tx, evt); err != nil {
		return err
	}
	if err = tx.Commit(f.ctx); err != nil {
		return fmt.Errorf("commit invoicing transaction: %w", err)
	}
	committed = true
	return nil
}

func (f *realisedFXFixture) events() []moneyfx.RealisedFX {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]moneyfx.RealisedFX(nil), f.realisedEvents...)
}

func realisedFXEvent(evt bus.Event) (moneyfx.RealisedFX, error) {
	switch e := evt.(type) {
	case moneyfx.RealisedFX:
		return e, nil
	case *moneyfx.RealisedFX:
		if e == nil {
			return moneyfx.RealisedFX{}, errors.New("nil RealisedFX event")
		}
		return *e, nil
	default:
		return moneyfx.RealisedFX{}, fmt.Errorf("got %T, want moneyfx.RealisedFX", evt)
	}
}

func assertRealisedFXEvents(t *testing.T, got []moneyfx.RealisedFX, want []moneyfx.RealisedFX) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RealisedFX events = %#v, want %#v", got, want)
	}
}

func assertMoneyFXLedgerPostings(t *testing.T, ctx context.Context, tx db.Tx, sourceRef string, want []wantPosting) {
	t.Helper()

	rows, err := tx.Query(ctx, `
SELECT p.account_code, p.amount, p.currency, p.amount_gbp
FROM ledger.journal_entries AS je
JOIN ledger.postings AS p ON p.entry_id = je.id
WHERE je.source_module = 'moneyfx'
	AND je.source_ref = $1
ORDER BY p.id`, sourceRef)
	if err != nil {
		t.Fatalf("query moneyfx ledger postings for %s: %v", sourceRef, err)
	}
	defer rows.Close()

	got := []wantPosting{}
	for rows.Next() {
		var posting wantPosting
		var currency string
		var amountGBP int64
		if err := rows.Scan(&posting.account, &posting.amount, &currency, &amountGBP); err != nil {
			t.Fatalf("scan moneyfx ledger posting for %s: %v", sourceRef, err)
		}
		if currency != "GBP" || amountGBP != posting.amount {
			t.Fatalf("posting for %s has currency=%s amount_gbp=%d amount=%d, want GBP and matching GBP amount",
				sourceRef,
				currency,
				amountGBP,
				posting.amount,
			)
		}
		got = append(got, posting)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("collect moneyfx ledger postings for %s: %v", sourceRef, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("moneyfx ledger postings for %s = %#v, want %#v", sourceRef, got, want)
	}
}

func assertRealisedFXRows(t *testing.T, ctx context.Context, tx db.Tx, invoiceID string, lockID moneyfx.LockID, wantCount int, wantAmountGBP int64) {
	t.Helper()

	rows, err := tx.Query(ctx, `
SELECT amount_gbp
FROM moneyfx.realised_fx
WHERE invoice_id = $1
	AND lock_id = $2`, invoiceID, int64(lockID))
	if err != nil {
		t.Fatalf("query realised_fx rows: %v", err)
	}
	defer rows.Close()

	var amounts []int64
	for rows.Next() {
		var amount int64
		if err := rows.Scan(&amount); err != nil {
			t.Fatalf("scan realised_fx row: %v", err)
		}
		amounts = append(amounts, amount)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("collect realised_fx rows: %v", err)
	}
	if len(amounts) != wantCount {
		t.Fatalf("realised_fx row count = %d (%v), want %d", len(amounts), amounts, wantCount)
	}
	if wantCount > 0 && amounts[0] != wantAmountGBP {
		t.Fatalf("realised_fx amount_gbp = %d, want %d", amounts[0], wantAmountGBP)
	}
}

func assertCountWhere(t *testing.T, ctx context.Context, tx db.Tx, table string, predicate string, want int, args ...any) {
	t.Helper()

	query := "SELECT count(*) FROM " + table + " WHERE " + predicate
	var got int
	if err := tx.QueryRow(ctx, query, args...).Scan(&got); err != nil {
		t.Fatalf("count %s where %s: %v", table, predicate, err)
	}
	if got != want {
		t.Fatalf("count %s where %s = %d, want %d", table, predicate, got, want)
	}
}

type wantPosting struct {
	account ledger.AccountCode
	amount  int64
}

func eur(amount int64) money.Money {
	return money.Money{Amount: amount, Currency: "EUR"}
}

func gbp(amount int64) money.Money {
	return money.Money{Amount: amount, Currency: "GBP"}
}

func day(year int, month time.Month, date int) time.Time {
	return time.Date(year, month, date, 0, 0, 0, 0, time.UTC)
}
