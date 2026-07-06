package fixtures

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/moneyfx"
)

const (
	ratesSeedPrefix = "rates:"
	defaultCurrency = "GBP"
)

// RateTable is a named set of EUR-base ECB rates for a fixture scenario.
type RateTable struct {
	Name  string
	Rates []moneyfx.ECBRate
}

// RatesFlat085 is a constant EUR->GBP 0.8500 table for TaxYear2526.
var RatesFlat085 = RateTable{
	Name:  "RatesFlat085",
	Rates: flatRates(TaxYear2526.Start, TaxYear2526.EndInclusive, defaultCurrency, "0.8500"),
}

// Rates seeds a named ECB rate table through moneyfx storage.
func Rates(t testing.TB, h *harness.Harness, tables ...RateTable) RateTable {
	t.Helper()

	table, err := TryRates(t, h, tables...)
	if err != nil {
		t.Fatalf("seed rates fixture: %v", err)
	}
	return table
}

// TryRates is the error-returning form of Rates for duplicate-seed tests.
func TryRates(t testing.TB, h *harness.Harness, tables ...RateTable) (RateTable, error) {
	t.Helper()

	table, err := selectedRateTable(tables...)
	if err != nil {
		return RateTable{}, err
	}
	release, err := claimSeed(t, h, ratesSeedPrefix+table.Name)
	if err != nil {
		return RateTable{}, err
	}
	success := false
	defer func() {
		release(success)
	}()

	store := moneyfx.NewStore(testdb.AsModule(t, moneyfx.ModuleName))
	if err := store.StoreECBRates(context.Background(), table.Rates); err != nil {
		return RateTable{}, fmt.Errorf("store %s ECB rates: %w", table.Name, err)
	}
	success = true
	return table, nil
}

// RatesStep creates a deterministic stepped EUR->GBP ECB table from date->rate.
func RatesStep(steps map[time.Time]string) RateTable {
	dates := make([]time.Time, 0, len(steps))
	for date := range steps {
		dates = append(dates, date)
	}
	sort.Slice(dates, func(i int, j int) bool {
		return dates[i].Before(dates[j])
	})

	rates := make([]moneyfx.ECBRate, 0, len(dates))
	for _, date := range dates {
		rates = append(rates, moneyfx.ECBRate{
			Date:     dateOnly(date),
			Currency: defaultCurrency,
			Rate:     steps[date],
		})
	}
	return RateTable{Name: "RatesStep", Rates: rates}
}

func selectedRateTable(tables ...RateTable) (RateTable, error) {
	switch len(tables) {
	case 0:
		return cloneRateTable(RatesFlat085), nil
	case 1:
		table := tables[0]
		if table.Name == "" {
			return RateTable{}, fmt.Errorf("fixtures: rate table name is required")
		}
		return cloneRateTable(table), nil
	default:
		return RateTable{}, fmt.Errorf("fixtures: seed one rate table at a time")
	}
}

func cloneRateTable(table RateTable) RateTable {
	table.Rates = append([]moneyfx.ECBRate(nil), table.Rates...)
	return table
}

func flatRates(start time.Time, endInclusive time.Time, currency string, rate string) []moneyfx.ECBRate {
	var rates []moneyfx.ECBRate
	for date := dateOnly(start); !date.After(dateOnly(endInclusive)); date = date.AddDate(0, 0, 1) {
		rates = append(rates, moneyfx.ECBRate{
			Date:     date,
			Currency: currency,
			Rate:     rate,
		})
	}
	return rates
}

func dateOnly(date time.Time) time.Time {
	if date.IsZero() {
		return time.Time{}
	}
	year, month, day := date.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
