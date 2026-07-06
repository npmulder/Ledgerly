//go:build integration

package harness_test

import (
	"context"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/moneyfx"
)

func TestMoneyFXStoreRoundTripsExactDecimal(t *testing.T) {
	t.Parallel()

	pool := testdb.AsModule(t, moneyfx.ModuleName)
	store := moneyfx.NewStore(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	date := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	rates := []moneyfx.ECBRate{{
		Date:     date,
		Currency: "GBP",
		Rate:     "0.85423",
	}}
	if err := store.StoreECBRates(ctx, rates); err != nil {
		t.Fatalf("StoreECBRates() error = %v", err)
	}
	if err := store.StoreECBRates(ctx, rates); err != nil {
		t.Fatalf("second StoreECBRates() error = %v", err)
	}

	stored, err := store.ECBRate(ctx, date, "GBP")
	if err != nil {
		t.Fatalf("ECBRate() error = %v", err)
	}
	if stored.Rate != "0.85423" {
		t.Fatalf("stored rate = %q, want 0.85423", stored.Rate)
	}
	if _, err := stored.Rat(); err != nil {
		t.Fatalf("stored.Rat() error = %v", err)
	}
	actualDate, ok, err := store.ECBRateDateOnOrBefore(ctx, date.AddDate(0, 0, 3), date.AddDate(0, 0, -4))
	if err != nil {
		t.Fatalf("ECBRateDateOnOrBefore() error = %v", err)
	}
	if !ok {
		t.Fatal("ECBRateDateOnOrBefore() ok = false, want true")
	}
	if !actualDate.Equal(date) {
		t.Fatalf("ECBRateDateOnOrBefore() = %s, want %s", actualDate.Format(time.DateOnly), date.Format(time.DateOnly))
	}
	count, err := store.CountECBRates(ctx)
	if err != nil {
		t.Fatalf("CountECBRates() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("stored rows = %d, want 1 after idempotent upsert", count)
	}
}
