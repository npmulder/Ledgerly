//go:build integration

package harness_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/moneyfx"
	"github.com/npmulder/ledgerly/internal/platform/clock"
)

func TestMoneyFXRateLockStoresWeekendFallbackAndRoundTripsDecimal(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	friday := time.Date(2030, 1, 4, 0, 0, 0, 0, time.UTC)
	sunday := friday.AddDate(0, 0, 2)
	fx := newRateLockFixture(t, sunday.Add(11*time.Hour))
	fx.seedRates(t, ctx, moneyfx.ECBRate{
		Date:     friday,
		Currency: "GBP",
		Rate:     "0.81234567",
	})

	lock := fx.appendLock(t, ctx, moneyfx.LockRef{Module: "invoicing", Ref: "INV-2030-01"}, sunday)
	if lock.Rate != "0.81234567" {
		t.Fatalf("Lock() rate = %q, want exact Friday decimal", lock.Rate)
	}
	if !lock.RateDate.Equal(friday) {
		t.Fatalf("Lock() rate date = %s, want Friday %s", lock.RateDate.Format(time.DateOnly), friday.Format(time.DateOnly))
	}
	if lock.Source != "ECB" {
		t.Fatalf("Lock() source = %q, want ECB", lock.Source)
	}

	stored, err := fx.service.GetLock(ctx, lock.ID)
	if err != nil {
		t.Fatalf("GetLock() error = %v", err)
	}
	if stored.Rate != "0.81234567" {
		t.Fatalf("GetLock() rate = %q, want exact decimal round-trip", stored.Rate)
	}
	if stored.Ref != (moneyfx.LockRef{Module: "invoicing", Ref: "INV-2030-01"}) {
		t.Fatalf("GetLock() ref = %+v, want invoicing INV-2030-01", stored.Ref)
	}
}

func TestMoneyFXRateLockStoresSameCurrencyIdentity(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	issueDate := time.Date(2030, 1, 4, 0, 0, 0, 0, time.UTC)
	fx := newRateLockFixture(t, issueDate.Add(11*time.Hour))

	tx, err := fx.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	lock, err := fx.service.Lock(ctx, tx, moneyfx.LockRef{Module: "invoicing", Ref: "INV-GBP-IDENTITY"}, "GBP", "GBP", issueDate)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("Lock() error = %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}

	if lock.From != "GBP" || lock.To != "GBP" {
		t.Fatalf("Lock() pair = %s->%s, want GBP->GBP", lock.From, lock.To)
	}
	if lock.Rate != "1" {
		t.Fatalf("Lock() rate = %q, want identity decimal 1", lock.Rate)
	}
	if !lock.RateDate.Equal(issueDate) {
		t.Fatalf("Lock() rate date = %s, want issue date %s", lock.RateDate.Format(time.DateOnly), issueDate.Format(time.DateOnly))
	}
	if lock.Source != "ECB" {
		t.Fatalf("Lock() source = %q, want persisted source ECB", lock.Source)
	}

	stored, err := fx.service.GetLock(ctx, lock.ID)
	if err != nil {
		t.Fatalf("GetLock() error = %v", err)
	}
	if stored.Rate != "1" || stored.Source != "ECB" {
		t.Fatalf("GetLock() = rate %q source %q, want rate 1 source ECB", stored.Rate, stored.Source)
	}
}

func TestMoneyFXRateLocksAreAppendOnlyForModuleRole(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	friday := time.Date(2030, 1, 4, 0, 0, 0, 0, time.UTC)
	fx := newRateLockFixture(t, friday.Add(12*time.Hour))
	fx.seedRates(t, ctx, moneyfx.ECBRate{
		Date:     friday,
		Currency: "GBP",
		Rate:     "0.8",
	})

	lock := fx.appendLock(t, ctx, moneyfx.LockRef{Module: "invoicing", Ref: "INV-IMMUTABLE"}, friday)
	_, err := fx.pool.Exec(ctx, `UPDATE moneyfx.rate_locks SET rate = rate WHERE id = $1`, lock.ID)
	assertPermissionDenied(t, err)

	_, err = fx.pool.Exec(ctx, `DELETE FROM moneyfx.rate_locks WHERE id = $1`, lock.ID)
	assertPermissionDenied(t, err)
}

func TestMoneyFXRateLockRelockPreservesHistoryAndActiveIsNewest(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	friday := time.Date(2030, 1, 4, 0, 0, 0, 0, time.UTC)
	monday := friday.AddDate(0, 0, 3)
	fx := newRateLockFixture(t, monday.Add(12*time.Hour))
	ref := moneyfx.LockRef{Module: "invoicing", Ref: "INV-RELOCK"}
	fx.seedRates(t, ctx,
		moneyfx.ECBRate{Date: friday, Currency: "GBP", Rate: "0.8"},
		moneyfx.ECBRate{Date: monday, Currency: "GBP", Rate: "0.9"},
	)

	first := fx.appendLock(t, ctx, ref, friday)
	second := fx.appendLock(t, ctx, ref, monday)
	if first.ID == second.ID {
		t.Fatalf("re-lock reused id %d, want a new row", first.ID)
	}

	active, err := fx.service.ActiveLockFor(ctx, ref)
	if err != nil {
		t.Fatalf("ActiveLockFor() error = %v", err)
	}
	if active.ID != second.ID || active.Rate != "0.9" {
		t.Fatalf("ActiveLockFor() = id %d rate %q, want newest id %d rate 0.9", active.ID, active.Rate, second.ID)
	}

	var count int
	if err := fx.pool.QueryRow(ctx, `SELECT count(*) FROM moneyfx.rate_locks WHERE ref = $1`, ref.String()).Scan(&count); err != nil {
		t.Fatalf("count rate_locks error = %v", err)
	}
	if count != 2 {
		t.Fatalf("rate_locks count = %d, want 2 history rows", count)
	}
}

func TestMoneyFXRateLockRollbackRemovesLock(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	friday := time.Date(2030, 1, 4, 0, 0, 0, 0, time.UTC)
	fx := newRateLockFixture(t, friday.Add(12*time.Hour))
	fx.seedRates(t, ctx, moneyfx.ECBRate{
		Date:     friday,
		Currency: "GBP",
		Rate:     "0.8",
	})

	callerPool := testdb.AsModule(t, "invoicing")
	tx, err := callerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	lock, err := fx.service.Lock(ctx, tx, moneyfx.LockRef{Module: "invoicing", Ref: "INV-ROLLBACK"}, "EUR", "GBP", friday)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("Lock() error = %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback transaction: %v", err)
	}

	if _, err := fx.service.GetLock(ctx, lock.ID); !errors.Is(err, moneyfx.ErrLockNotFound) {
		t.Fatalf("GetLock() after rollback error = %v, want ErrLockNotFound", err)
	}
}

func TestMoneyFXRateLocksOnDistinctRefsAreConcurrentClean(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	friday := time.Date(2030, 1, 4, 0, 0, 0, 0, time.UTC)
	fx := newRateLockFixture(t, friday.Add(12*time.Hour))
	fx.seedRates(t, ctx, moneyfx.ECBRate{
		Date:     friday,
		Currency: "GBP",
		Rate:     "0.8",
	})

	const workers = 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tx, err := fx.pool.Begin(ctx)
			if err != nil {
				errs <- fmt.Errorf("begin %d: %w", i, err)
				return
			}
			ref := moneyfx.LockRef{Module: "invoicing", Ref: fmt.Sprintf("INV-CONCURRENT-%02d", i)}
			if _, err := fx.service.Lock(ctx, tx, ref, "EUR", "GBP", friday); err != nil {
				_ = tx.Rollback(ctx)
				errs <- fmt.Errorf("lock %d: %w", i, err)
				return
			}
			if err := tx.Commit(ctx); err != nil {
				errs <- fmt.Errorf("commit %d: %w", i, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	var count int
	if err := fx.pool.QueryRow(ctx, `SELECT count(*) FROM moneyfx.rate_locks WHERE ref LIKE 'invoicing:INV-CONCURRENT-%'`).Scan(&count); err != nil {
		t.Fatalf("count concurrent locks error = %v", err)
	}
	if count != workers {
		t.Fatalf("concurrent lock count = %d, want %d", count, workers)
	}
}

type rateLockFixture struct {
	pool    *pgxpool.Pool
	store   moneyfx.Store
	service *moneyfx.Service
}

func newRateLockFixture(t testing.TB, now time.Time) rateLockFixture {
	t.Helper()

	pool := testdb.AsModule(t, moneyfx.ModuleName)
	store := moneyfx.NewStore(pool)
	return rateLockFixture{
		pool:    pool,
		store:   store,
		service: moneyfx.NewService(store, clock.NewFake(now)),
	}
}

func (f rateLockFixture) seedRates(t testing.TB, ctx context.Context, rates ...moneyfx.ECBRate) {
	t.Helper()

	if err := f.store.StoreECBRates(ctx, rates); err != nil {
		t.Fatalf("StoreECBRates() error = %v", err)
	}
}

func (f rateLockFixture) appendLock(t testing.TB, ctx context.Context, ref moneyfx.LockRef, date time.Time) moneyfx.RateLock {
	t.Helper()

	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	lock, err := f.service.Lock(ctx, tx, ref, "EUR", "GBP", date)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("Lock() error = %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
	return lock
}
