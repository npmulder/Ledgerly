package jurisdiction

import (
	"errors"
	"testing"
	"testing/quick"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

const personalTaxTestYear = "2025-26"

type personalTaxFixture struct {
	currency     string
	year         PersonalIncomeYear
	allowance    Money
	firstBandCap Money
	boundary     Money
}

func TestPersonalTaxEstimateHandComputedTable(t *testing.T) {
	fixture := loadPersonalTaxFixture(t)
	zero := money.Zero(fixture.currency)
	onePenny := Money{Amount: 1, Currency: fixture.currency}

	delta250 := mustParseMoney(t, "250.00", fixture.currency)
	gross15000 := mustAddMoney(t, fixture.allowance, delta250)

	gross50000 := mustParseMoney(t, "50,000.00", fixture.currency)
	taxable50000 := mustSubMoney(t, gross50000, fixture.allowance)

	boundaryMinus := mustSubMoney(t, fixture.boundary, onePenny)
	boundaryMinusTaxable := mustSubMoney(t, fixture.firstBandCap, onePenny)
	boundaryPlus := mustAddMoney(t, fixture.boundary, onePenny)
	boundaryPlusTaxable := mustAddMoney(t, fixture.firstBandCap, onePenny)

	firstTax25 := mustParseMoney(t, "25.00", fixture.currency)
	firstTax650 := mustParseMoney(t, "650.00", fixture.currency)
	secondTax603750 := mustParseMoney(t, "6,037.50", fixture.currency)

	tests := []struct {
		name            string
		gross           Money
		taxable         Money
		wantBandAmounts []Money
	}{
		{
			name:            "GBP 0.00",
			gross:           zero,
			taxable:         zero,
			wantBandAmounts: twoBandAmounts(t, fixture, zero, zero),
		},
		{
			name:            "GBP 14,750.00 allowance boundary",
			gross:           fixture.allowance,
			taxable:         zero,
			wantBandAmounts: twoBandAmounts(t, fixture, zero, zero),
		},
		{
			// Pack allowance plus GBP 250.00 leaves GBP 250.00 taxable;
			// GBP 250.00 at the first pack rate is GBP 25.00.
			name:            "GBP 15,000.00",
			gross:           gross15000,
			taxable:         delta250,
			wantBandAmounts: twoBandAmounts(t, fixture, firstTax25, zero),
		},
		{
			// Pack allowance plus the first band cap leaves the full first
			// band taxable; the first pack rate produces GBP 650.00.
			name:            "GBP 21,250.00 first band boundary",
			gross:           fixture.boundary,
			taxable:         fixture.firstBandCap,
			wantBandAmounts: twoBandAmounts(t, fixture, firstTax650, zero),
		},
		{
			// GBP 50,000.00 leaves GBP 35,250.00 taxable after the pack
			// allowance. The first capped band produces GBP 650.00; the
			// remaining GBP 28,750.00 at the final pack rate produces
			// GBP 6,037.50, for a total of GBP 6,687.50.
			name:            "GBP 50,000.00",
			gross:           gross50000,
			taxable:         taxable50000,
			wantBandAmounts: twoBandAmounts(t, fixture, firstTax650, secondTax603750),
		},
		{
			// One penny below the first band boundary: the taxable amount is
			// one penny below the first cap; applying the first pack rate gives
			// 64,999.9 pence, which rounds half-even to 65,000 pence.
			name:            "first band boundary minus 1p",
			gross:           boundaryMinus,
			taxable:         boundaryMinusTaxable,
			wantBandAmounts: twoBandAmounts(t, fixture, firstTax650, zero),
		},
		{
			// One penny above the first band boundary: the capped first band
			// remains 65,000 pence, while one penny in the final band rounds
			// to zero tax at the final pack rate.
			name:            "first band boundary plus 1p",
			gross:           boundaryPlus,
			taxable:         boundaryPlusTaxable,
			wantBandAmounts: twoBandAmounts(t, fixture, firstTax650, zero),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PersonalTaxEstimate(personalTaxTestYear, tt.gross)
			if err != nil {
				t.Fatalf("PersonalTaxEstimate() error = %v", err)
			}
			assertPersonalTaxEstimate(t, fixture, got, tt.gross, tt.taxable, tt.wantBandAmounts)
		})
	}
}

func TestPersonalTaxEstimateRejectsNonPackCurrency(t *testing.T) {
	fixture := loadPersonalTaxFixture(t)

	_, err := PersonalTaxEstimate(personalTaxTestYear, Money{Amount: 1, Currency: "EUR"})
	if err == nil {
		t.Fatal("PersonalTaxEstimate() error = nil, want UnsupportedCurrencyError")
	}

	var currencyErr UnsupportedCurrencyError
	if !errors.As(err, &currencyErr) {
		t.Fatalf("PersonalTaxEstimate() error type = %T, want UnsupportedCurrencyError", err)
	}
	if currencyErr.Got != "EUR" {
		t.Fatalf("UnsupportedCurrencyError.Got = %q, want EUR", currencyErr.Got)
	}
	if currencyErr.Want != fixture.currency {
		t.Fatalf("UnsupportedCurrencyError.Want = %q, want %q", currencyErr.Want, fixture.currency)
	}
}

func TestPersonalTaxEstimateUsesAllPackBands(t *testing.T) {
	if err := LoadActiveFromFS(testFixtureFS(t), "testland@0.1"); err != nil {
		t.Fatalf("LoadActiveFromFS() error = %v", err)
	}

	meta := ActivePack()
	year, err := PersonalIncomeTax(personalTaxTestYear)
	if err != nil {
		t.Fatalf("PersonalIncomeTax() error = %v", err)
	}
	if len(year.Bands) < 3 {
		t.Fatalf("test fixture bands length = %d, want at least 3", len(year.Bands))
	}
	if year.Bands[1].UpToMinorUnits == nil {
		t.Fatal("test fixture second band cap is nil")
	}

	allowance := Money{Amount: year.PersonalAllowanceMinorUnits, Currency: meta.Currency}
	secondBandCap := Money{Amount: *year.Bands[1].UpToMinorUnits, Currency: meta.Currency}
	finalBandTaxable := Money{Amount: 1000, Currency: meta.Currency}
	taxable := mustAddMoney(t, secondBandCap, finalBandTaxable)
	gross := mustAddMoney(t, allowance, taxable)

	got, err := PersonalTaxEstimate(personalTaxTestYear, gross)
	if err != nil {
		t.Fatalf("PersonalTaxEstimate() error = %v", err)
	}
	if len(got.PerBand) != len(year.Bands) {
		t.Fatalf("PerBand length = %d, want %d", len(got.PerBand), len(year.Bands))
	}
	for i, gotBand := range got.PerBand {
		if gotBand.Rate != year.Bands[i].Rate {
			t.Fatalf("PerBand[%d].Rate = %q, want %q", i, gotBand.Rate, year.Bands[i].Rate)
		}
		assertCapMatchesPack(t, meta.Currency, i, gotBand.Cap, year.Bands[i])
	}
	if got.PerBand[len(got.PerBand)-1].Amount.IsZero() {
		t.Fatal("final open-ended band amount is zero; want taxable amount assigned beyond capped bands")
	}
	assertTotalEqualsBandSum(t, got)
}

func TestPersonalTaxEstimateHandlesMaxInt64Gross(t *testing.T) {
	fixture := loadPersonalTaxFixture(t)
	const maxInt64 = int64(^uint64(0) >> 1)

	got, err := PersonalTaxEstimate(personalTaxTestYear, Money{Amount: maxInt64, Currency: fixture.currency})
	if err != nil {
		t.Fatalf("PersonalTaxEstimate() error = %v", err)
	}
	if got.Gross.Amount != maxInt64 {
		t.Fatalf("Gross.Amount = %d, want %d", got.Gross.Amount, maxInt64)
	}
	if got.Taxable.Amount <= 0 {
		t.Fatalf("Taxable.Amount = %d, want positive", got.Taxable.Amount)
	}
	assertTotalEqualsBandSum(t, got)
	cmp, err := got.Total.Cmp(got.Gross)
	if err != nil {
		t.Fatalf("Total.Cmp(Gross) error = %v", err)
	}
	if cmp > 0 {
		t.Fatalf("Total = %+v exceeds Gross = %+v", got.Total, got.Gross)
	}
}

func TestPersonalTaxEstimateProperties(t *testing.T) {
	fixture := loadPersonalTaxFixture(t)
	config := &quick.Config{MaxCount: 1000}

	if err := quick.Check(func(raw uint64) bool {
		gross := Money{Amount: nonnegativeMinorUnits(raw), Currency: fixture.currency}
		got, err := PersonalTaxEstimate(personalTaxTestYear, gross)
		if err != nil {
			t.Logf("PersonalTaxEstimate(%+v) error = %v", gross, err)
			return false
		}
		sum, err := bandAmountSum(got)
		if err != nil {
			t.Logf("bandAmountSum() error = %v", err)
			return false
		}
		if got.Total != sum {
			t.Logf("Total = %+v, want band sum %+v", got.Total, sum)
			return false
		}
		return true
	}, config); err != nil {
		t.Fatalf("total equals sum property failed: %v", err)
	}

	if err := quick.Check(func(rawLeft, rawRight uint64) bool {
		leftAmount := nonnegativeMinorUnits(rawLeft)
		rightAmount := nonnegativeMinorUnits(rawRight)
		if leftAmount > rightAmount {
			leftAmount, rightAmount = rightAmount, leftAmount
		}

		left, err := PersonalTaxEstimate(personalTaxTestYear, Money{Amount: leftAmount, Currency: fixture.currency})
		if err != nil {
			t.Logf("left PersonalTaxEstimate(%d) error = %v", leftAmount, err)
			return false
		}
		right, err := PersonalTaxEstimate(personalTaxTestYear, Money{Amount: rightAmount, Currency: fixture.currency})
		if err != nil {
			t.Logf("right PersonalTaxEstimate(%d) error = %v", rightAmount, err)
			return false
		}
		cmp, err := left.Total.Cmp(right.Total)
		if err != nil {
			t.Logf("Total.Cmp() error = %v", err)
			return false
		}
		if cmp > 0 {
			t.Logf("tax not monotonic: gross %d total %+v > gross %d total %+v", leftAmount, left.Total, rightAmount, right.Total)
			return false
		}
		return true
	}, config); err != nil {
		t.Fatalf("monotonic property failed: %v", err)
	}
}

func loadPersonalTaxFixture(t *testing.T) personalTaxFixture {
	t.Helper()
	loadIsleOfManForAccessors(t)

	meta := ActivePack()
	year, err := PersonalIncomeTax(personalTaxTestYear)
	if err != nil {
		t.Fatalf("PersonalIncomeTax() error = %v", err)
	}
	if len(year.Bands) != 2 {
		t.Fatalf("production personal tax bands length = %d, want 2 for required hand table", len(year.Bands))
	}
	if year.Bands[0].UpToMinorUnits == nil {
		t.Fatal("first production personal tax band cap is nil")
	}

	allowance := Money{Amount: year.PersonalAllowanceMinorUnits, Currency: meta.Currency}
	firstBandCap := Money{Amount: *year.Bands[0].UpToMinorUnits, Currency: meta.Currency}
	return personalTaxFixture{
		currency:     meta.Currency,
		year:         year,
		allowance:    allowance,
		firstBandCap: firstBandCap,
		boundary:     mustAddMoney(t, allowance, firstBandCap),
	}
}

func assertPersonalTaxEstimate(t *testing.T, fixture personalTaxFixture, got Estimate, wantGross, wantTaxable Money, wantBandAmounts []Money) {
	t.Helper()

	if got.Gross != wantGross {
		t.Fatalf("Gross = %+v, want %+v", got.Gross, wantGross)
	}
	if got.Allowance != fixture.allowance {
		t.Fatalf("Allowance = %+v, want %+v", got.Allowance, fixture.allowance)
	}
	if got.Taxable != wantTaxable {
		t.Fatalf("Taxable = %+v, want %+v", got.Taxable, wantTaxable)
	}
	if len(got.PerBand) != len(fixture.year.Bands) {
		t.Fatalf("PerBand length = %d, want %d", len(got.PerBand), len(fixture.year.Bands))
	}
	if len(wantBandAmounts) != len(fixture.year.Bands) {
		t.Fatalf("test wantBandAmounts length = %d, want %d", len(wantBandAmounts), len(fixture.year.Bands))
	}
	for i, gotBand := range got.PerBand {
		wantBand := fixture.year.Bands[i]
		if gotBand.Rate != wantBand.Rate {
			t.Fatalf("PerBand[%d].Rate = %q, want %q", i, gotBand.Rate, wantBand.Rate)
		}
		assertCapMatchesPack(t, fixture.currency, i, gotBand.Cap, wantBand)
		if gotBand.Amount != wantBandAmounts[i] {
			t.Fatalf("PerBand[%d].Amount = %+v, want %+v", i, gotBand.Amount, wantBandAmounts[i])
		}
	}

	wantTotal := money.Zero(fixture.currency)
	for _, amount := range wantBandAmounts {
		wantTotal = mustAddMoney(t, wantTotal, amount)
	}
	if got.Total != wantTotal {
		t.Fatalf("Total = %+v, want %+v", got.Total, wantTotal)
	}
	assertTotalEqualsBandSum(t, got)
}

func assertCapMatchesPack(t *testing.T, currency string, index int, got *Money, want TaxBand) {
	t.Helper()

	if want.UpToMinorUnits == nil {
		if got != nil {
			t.Fatalf("PerBand[%d].Cap = %+v, want nil", index, *got)
		}
		return
	}
	if got == nil {
		t.Fatalf("PerBand[%d].Cap = nil, want %d %s minor units", index, *want.UpToMinorUnits, currency)
	}
	wantCap := Money{Amount: *want.UpToMinorUnits, Currency: currency}
	if *got != wantCap {
		t.Fatalf("PerBand[%d].Cap = %+v, want %+v", index, *got, wantCap)
	}
}

func assertTotalEqualsBandSum(t *testing.T, estimate Estimate) {
	t.Helper()

	sum, err := bandAmountSum(estimate)
	if err != nil {
		t.Fatalf("bandAmountSum() error = %v", err)
	}
	if estimate.Total != sum {
		t.Fatalf("Total = %+v, want band sum %+v", estimate.Total, sum)
	}
}

func bandAmountSum(estimate Estimate) (Money, error) {
	sum := money.Zero(estimate.Total.Currency)
	for _, band := range estimate.PerBand {
		var err error
		sum, err = sum.Add(band.Amount)
		if err != nil {
			return Money{}, err
		}
	}
	return sum, nil
}

func twoBandAmounts(t *testing.T, fixture personalTaxFixture, first, second Money) []Money {
	t.Helper()

	if len(fixture.year.Bands) != 2 {
		t.Fatalf("fixture bands length = %d, want 2", len(fixture.year.Bands))
	}
	return []Money{first, second}
}

func mustParseMoney(t *testing.T, input, currency string) Money {
	t.Helper()

	amount, err := money.ParseAmount(input, currency)
	if err != nil {
		t.Fatalf("money.ParseAmount(%q, %q) error = %v", input, currency, err)
	}
	return amount
}

func mustAddMoney(t *testing.T, left, right Money) Money {
	t.Helper()

	sum, err := left.Add(right)
	if err != nil {
		t.Fatalf("Money.Add(%+v, %+v) error = %v", left, right, err)
	}
	return sum
}

func mustSubMoney(t *testing.T, left, right Money) Money {
	t.Helper()

	diff, err := left.Sub(right)
	if err != nil {
		t.Fatalf("Money.Sub(%+v, %+v) error = %v", left, right, err)
	}
	return diff
}

func nonnegativeMinorUnits(raw uint64) int64 {
	return int64(raw >> 1)
}
