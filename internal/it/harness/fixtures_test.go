//go:build integration

package harness_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/moneyfx"
)

func TestFixturesCompanyFlatRatesAndFrozenClock(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: fixtures.TaxYear2526.Start})
	company := fixtures.Company(t, h)
	table := fixtures.Rates(t, h)

	if !h.Clock.Now().Equal(fixtures.TaxYear2526.Start) {
		t.Fatalf("clock = %s, want %s", h.Clock.Now(), fixtures.TaxYear2526.Start)
	}
	if company.TradingName != "NPM Limited" || company.CompanyNumber != "137792C" {
		t.Fatalf("company = %+v, want NPM Limited 137792C", company)
	}
	if company.RegisteredOffice.Line1 != "18 Athol St" || company.RegisteredOffice.Locality != "Douglas" {
		t.Fatalf("registered office = %+v, want 18 Athol St Douglas", company.RegisteredOffice)
	}
	if len(company.Shareholders) != 1 || company.Shareholders[0].Name != "N. Meyer" || company.Shareholders[0].Shares != 100 {
		t.Fatalf("shareholders = %+v, want N. Meyer 100 shares", company.Shareholders)
	}
	if company.BankDetails.IBAN == "" || company.BankDetails.BIC != "REVOGB21" {
		t.Fatalf("bank details = %+v, want Revolut SEPA details", company.BankDetails)
	}
	if table.Name != "RatesFlat085" {
		t.Fatalf("rate table = %q, want RatesFlat085", table.Name)
	}
	assertStoredRate(t, fixtures.TaxYear2526.Start, "0.8500")
	assertStoredRate(t, fixtures.TaxYear2526.EndInclusive, "0.8500")
}

func TestFixturesCompanyOverridesYearEndAndIncorporationDate(t *testing.T) {
	h := harness.New(t, harness.Options{})
	company := fixtures.Company(t, h).With(
		fixtures.CompanyYearEnd(time.December, 31),
		fixtures.CompanyIncorporationDate(time.Date(2021, time.January, 4, 0, 0, 0, 0, time.UTC)),
	)

	if company.YearEnd.Month != time.December || company.YearEnd.Day != 31 {
		t.Fatalf("YearEnd = %+v, want 31 Dec", company.YearEnd)
	}
	if company.IncorporationDate.Format(time.DateOnly) != "2021-01-04" {
		t.Fatalf("IncorporationDate = %s, want 2021-01-04", company.IncorporationDate.Format(time.DateOnly))
	}
}

func TestFixturesRatesStepUsesMoneyFXLookup(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, time.June, 1, 9, 0, 0, 0, time.UTC)})
	table := fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{
		time.Date(2025, time.May, 1, 12, 0, 0, 0, time.UTC):  "0.8580",
		time.Date(2025, time.June, 1, 12, 0, 0, 0, time.UTC): "0.8473",
	}))

	if table.Name != "RatesStep" {
		t.Fatalf("rate table = %q, want RatesStep", table.Name)
	}
	assertStoredRate(t, time.Date(2025, time.May, 1, 0, 0, 0, 0, time.UTC), "0.8580")
	assertStoredRate(t, time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC), "0.8473")
}

func TestFixturesRejectDuplicateSeeds(t *testing.T) {
	h := harness.New(t, harness.Options{})
	fixtures.Company(t, h)
	if _, err := fixtures.TryCompany(t, h); !errors.Is(err, fixtures.ErrAlreadySeeded) {
		t.Fatalf("TryCompany duplicate error = %v, want ErrAlreadySeeded", err)
	}

	fixtures.Rates(t, h)
	if _, err := fixtures.TryRates(t, h); !errors.Is(err, fixtures.ErrAlreadySeeded) {
		t.Fatalf("TryRates duplicate error = %v, want ErrAlreadySeeded", err)
	}
}

func assertStoredRate(t *testing.T, date time.Time, want string) {
	t.Helper()

	store := moneyfx.NewStore(testdb.AsModule(t, moneyfx.ModuleName))
	stored, err := store.ECBRate(context.Background(), date, "GBP")
	if err != nil {
		t.Fatalf("ECBRate(%s, GBP) error = %v", date.Format(time.DateOnly), err)
	}
	rat, err := stored.Rat()
	if err != nil {
		t.Fatalf("stored.Rat() error = %v", err)
	}
	if got := rat.FloatString(4); got != want {
		t.Fatalf("ECBRate(%s, GBP) = %s, want %s", date.Format(time.DateOnly), got, want)
	}
}
