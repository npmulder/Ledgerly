package jurisdiction

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

// Money is the shared money value type used by jurisdiction estimates.
type Money = money.Money

// Estimate is a line-by-line personal income tax estimate for one annual gross
// amount. Per-band tax is rounded to minor units with money.Money.MulRat's
// round-half-even rule; Total is the exact sum of those rounded band amounts.
//
// Inputs and pack allowance/band caps are int64 minor units. For any in-range
// grossAnnual.Amount and validated pack rates between 0 and 1, taxable amounts,
// per-band amounts, and totals remain inside int64 pence bounds; additions and
// subtractions still go through money.Money so overflow is reported if a future
// pack breaks that invariant.
type Estimate struct {
	Gross     Money
	Allowance Money
	Taxable   Money
	PerBand   []EstimateBand
	Total     Money
}

// EstimateBand is the tax amount calculated for one pack band. Cap is the
// taxable-income upper bound for capped bands; nil means the open-ended band.
type EstimateBand struct {
	Cap    *Money
	Rate   Rate
	Amount Money
}

// UnsupportedCurrencyError reports a personal tax estimate request in a
// currency other than the active pack currency.
type UnsupportedCurrencyError struct {
	Got  string
	Want string
}

func (e UnsupportedCurrencyError) Error() string {
	return fmt.Sprintf("jurisdiction: personal tax estimate currency %q does not match pack currency %q", e.Got, e.Want)
}

// PersonalTaxEstimate calculates personal income tax for grossAnnual using the
// active jurisdiction pack's personal allowance and band list for taxYear.
func PersonalTaxEstimate(taxYear string, grossAnnual Money) (Estimate, error) {
	pack, err := activePackSnapshot()
	if err != nil {
		return Estimate{}, err
	}
	if grossAnnual.Currency != pack.Meta.Currency {
		return Estimate{}, UnsupportedCurrencyError{Got: grossAnnual.Currency, Want: pack.Meta.Currency}
	}

	year, ok := pack.Tax.PersonalIncome[taxYear]
	if !ok {
		return Estimate{}, UnknownTaxYearError{TaxYear: taxYear, Path: "tax.personal_income"}
	}

	allowance := Money{Amount: year.PersonalAllowanceMinorUnits, Currency: pack.Meta.Currency}
	taxable, err := taxableIncome(grossAnnual, allowance)
	if err != nil {
		return Estimate{}, err
	}

	estimate := Estimate{
		Gross:     grossAnnual,
		Allowance: allowance,
		Taxable:   taxable,
		PerBand:   make([]EstimateBand, 0, len(year.Bands)),
		Total:     money.Zero(pack.Meta.Currency),
	}

	previousCap := money.Zero(pack.Meta.Currency)
	for _, band := range year.Bands {
		rate, err := parseRateRat(band.Rate)
		if err != nil {
			return Estimate{}, err
		}

		var cap *Money
		if band.UpToMinorUnits != nil {
			capValue := Money{Amount: *band.UpToMinorUnits, Currency: pack.Meta.Currency}
			cap = &capValue
		}

		bandTaxable, err := bandTaxableAmount(taxable, previousCap, cap)
		if err != nil {
			return Estimate{}, err
		}
		amount := bandTaxable.MulRat(rate)
		total, err := estimate.Total.Add(amount)
		if err != nil {
			return Estimate{}, err
		}
		estimate.Total = total
		estimate.PerBand = append(estimate.PerBand, EstimateBand{
			Cap:    cloneMoneyPointer(cap),
			Rate:   band.Rate,
			Amount: amount,
		})

		if cap != nil {
			previousCap = *cap
		}
	}

	return estimate, nil
}

func taxableIncome(gross, allowance Money) (Money, error) {
	cmp, err := gross.Cmp(allowance)
	if err != nil {
		return Money{}, err
	}
	if cmp <= 0 {
		return money.Zero(gross.Currency), nil
	}
	return gross.Sub(allowance)
}

func bandTaxableAmount(taxable, previousCap Money, cap *Money) (Money, error) {
	cmp, err := taxable.Cmp(previousCap)
	if err != nil {
		return Money{}, err
	}
	if cmp <= 0 {
		return money.Zero(taxable.Currency), nil
	}

	abovePrevious, err := taxable.Sub(previousCap)
	if err != nil {
		return Money{}, err
	}
	if cap == nil {
		return abovePrevious, nil
	}

	width, err := cap.Sub(previousCap)
	if err != nil {
		return Money{}, err
	}
	cmp, err = abovePrevious.Cmp(width)
	if err != nil {
		return Money{}, err
	}
	if cmp < 0 {
		return abovePrevious, nil
	}
	return width, nil
}

func parseRateRat(rate Rate) (*big.Rat, error) {
	parsed, ok := new(big.Rat).SetString(strings.TrimSpace(string(rate)))
	if !ok {
		return nil, fmt.Errorf("jurisdiction: parse personal income tax rate %q", rate)
	}
	return parsed, nil
}

func cloneMoneyPointer(in *Money) *Money {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
