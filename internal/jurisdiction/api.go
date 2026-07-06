package jurisdiction

import (
	"errors"
	"fmt"
	"time"
)

// ErrNoActivePack means accessors were called before startup installed a pack.
var ErrNoActivePack = errors.New("jurisdiction: no active pack loaded")

// UnknownTaxYearError reports a missing tax year for a specific pack path.
type UnknownTaxYearError struct {
	TaxYear string
	Path    string
}

func (e UnknownTaxYearError) Error() string {
	return fmt.Sprintf("jurisdiction: %s has no tax year %q", e.Path, e.TaxYear)
}

// UnknownReverseChargeKindError reports a missing reverse-charge wording kind.
type UnknownReverseChargeKindError struct {
	Kind string
}

func (e UnknownReverseChargeKindError) Error() string {
	return fmt.Sprintf("jurisdiction: reverse charge wording kind %q not found", e.Kind)
}

// UnknownVATTreatmentError reports a missing VAT treatment semantics entry.
type UnknownVATTreatmentError struct {
	Treatment string
}

func (e UnknownVATTreatmentError) Error() string {
	return fmt.Sprintf("jurisdiction: VAT treatment %q not found", e.Treatment)
}

func (r Rate) String() string {
	return string(r)
}

// CorporateRate returns the corporate income tax standard rate for a tax year.
func CorporateRate(taxYear string) (Rate, error) {
	pack, err := activePackSnapshot()
	if err != nil {
		return "", err
	}
	year, ok := pack.Tax.CorporateIncome[taxYear]
	if !ok {
		return "", UnknownTaxYearError{TaxYear: taxYear, Path: "tax.corporate_income"}
	}
	return year.StandardRate, nil
}

// PersonalIncomeTax returns the personal income tax bands used by later
// dividend set-aside estimation. It does not perform estimator math.
func PersonalIncomeTax(taxYear string) (PersonalIncomeYear, error) {
	pack, err := activePackSnapshot()
	if err != nil {
		return PersonalIncomeYear{}, err
	}
	year, ok := pack.Tax.PersonalIncome[taxYear]
	if !ok {
		return PersonalIncomeYear{}, UnknownTaxYearError{TaxYear: taxYear, Path: "tax.personal_income"}
	}
	return clonePersonalIncomeYear(year), nil
}

// DividendWithholding returns the dividend withholding rule for a tax year.
func DividendWithholding(taxYear string) (string, error) {
	pack, err := activePackSnapshot()
	if err != nil {
		return "", err
	}
	year, ok := pack.Tax.Dividends[taxYear]
	if !ok {
		return "", UnknownTaxYearError{TaxYear: taxYear, Path: "tax.dividends"}
	}
	return year.Withholding, nil
}

// VATStandardRate returns the VAT standard rate for a tax year.
func VATStandardRate(taxYear string) (Rate, error) {
	pack, err := activePackSnapshot()
	if err != nil {
		return "", err
	}
	year, ok := pack.Tax.VAT.Years[taxYear]
	if !ok {
		return "", UnknownTaxYearError{TaxYear: taxYear, Path: "tax.vat"}
	}
	return year.StandardRate, nil
}

// VATStandardRateForDate returns the VAT standard rate for the active pack tax
// year containing date.
func VATStandardRateForDate(date time.Time) (Rate, string, error) {
	taxYear, err := TaxYearForDate(date)
	if err != nil {
		return "", "", err
	}
	rate, err := VATStandardRate(taxYear)
	if err != nil {
		return "", taxYear, err
	}
	return rate, taxYear, nil
}

// TaxYearForDate resolves a date into the active pack's tax-year key, for
// example 2025-26.
func TaxYearForDate(date time.Time) (string, error) {
	pack, err := activePackSnapshot()
	if err != nil {
		return "", err
	}
	return taxYearForDate(date, pack.Tax.YearEnd)
}

// ReverseChargeWording returns invoice wording for a reverse-charge kind.
func ReverseChargeWording(kind string) (Wording, error) {
	pack, err := activePackSnapshot()
	if err != nil {
		return Wording{}, err
	}
	wording, ok := pack.Tax.VAT.ReverseCharge[kind]
	if !ok {
		return Wording{}, UnknownReverseChargeKindError{Kind: kind}
	}
	return wording, nil
}

// VATSemanticsForTreatment returns pack-backed VAT reporting semantics for a
// treatment value stored by modules such as invoicing.
func VATSemanticsForTreatment(treatment string) (VATTreatmentSemantics, error) {
	pack, err := activePackSnapshot()
	if err != nil {
		return VATTreatmentSemantics{}, err
	}
	semantics, ok := pack.Tax.VAT.Treatments[treatment]
	if !ok {
		return VATTreatmentSemantics{}, UnknownVATTreatmentError{Treatment: treatment}
	}
	return semantics, nil
}

func taxYearForDate(date time.Time, yearEnd YearEnd) (string, error) {
	if date.IsZero() {
		return "", fmt.Errorf("jurisdiction: date is required")
	}
	if err := validateYearEnd(yearEnd); err != nil {
		return "", err
	}

	year, _, _ := date.UTC().Date()
	yearEndDate := time.Date(year, yearEnd.Month, yearEnd.Day, 0, 0, 0, 0, time.UTC)
	dateOnly := time.Date(year, date.UTC().Month(), date.UTC().Day(), 0, 0, 0, 0, time.UTC)

	startYear := year - 1
	endYear := year
	if dateOnly.After(yearEndDate) {
		startYear = year
		endYear = year + 1
	}
	return fmt.Sprintf("%04d-%02d", startYear, endYear%100), nil
}

// FilingRules returns declarative filing rules. Use FilingDeadlines to resolve
// concrete next due dates from caller-supplied company facts.
func FilingRules() map[string]Filing {
	pack := activePackSnapshotOrNil()
	if pack == nil {
		return nil
	}
	return cloneMap(pack.Filings)
}

// DirectorLoanPolicy returns the director loan policy for the active pack.
func DirectorLoanPolicy() DLAPolicy {
	pack := activePackSnapshotOrNil()
	if pack == nil {
		return DLAPolicy{}
	}
	return pack.DirectorLoans
}

// AdvisorRules returns advisor rule definitions for the active pack. Rule
// evaluation belongs to the advisor engine.
func AdvisorRules() []AdvisorRule {
	pack := activePackSnapshotOrNil()
	if pack == nil {
		return nil
	}
	return cloneAdvisorRules(pack.AdvisorRules)
}

func activePackSnapshot() (*Pack, error) {
	activePack.RLock()
	defer activePack.RUnlock()

	if activePack.pack == nil {
		return nil, ErrNoActivePack
	}
	return clonePack(activePack.pack), nil
}

func activePackSnapshotOrNil() *Pack {
	pack, err := activePackSnapshot()
	if err != nil {
		return nil
	}
	return pack
}
