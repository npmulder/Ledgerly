package jurisdiction

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

func validatePack(file, id, version string, pack *Pack) error {
	if err := validateMeta(file, id, version, pack.Meta); err != nil {
		return err
	}

	if err := validateYearMap(file, "tax.corporate_income", "corporate_income", pack.Tax.CorporateIncome, validateCorporateIncomeYear); err != nil {
		return err
	}
	if err := validateYearMap(file, "tax.personal_income", "personal_income", pack.Tax.PersonalIncome, validatePersonalIncomeYear); err != nil {
		return err
	}
	if err := validateYearMap(file, "tax.dividends", "dividends", pack.Tax.Dividends, validateDividendYear); err != nil {
		return err
	}
	if err := validateVAT(file, pack.Tax.VAT); err != nil {
		return err
	}
	if err := validateFilings(file, pack.Filings); err != nil {
		return err
	}
	if err := validateYearMap(file, "director_loans", "director_loans", pack.DirectorLoans, validateDirectorLoanYear); err != nil {
		return err
	}
	if err := validateAdvisorRules(file, pack.AdvisorRules); err != nil {
		return err
	}

	return nil
}

func validateMeta(file, id, version string, meta PackMeta) error {
	if strings.TrimSpace(meta.ID) == "" {
		return fieldError(file, "meta.id", "id", "must not be empty")
	}
	if meta.ID != id {
		return fieldError(file, "meta.id", "id", fmt.Sprintf("must match pack path id %q", id))
	}
	if strings.TrimSpace(meta.Version) == "" {
		return fieldError(file, "meta.version", "version", "must not be empty")
	}
	if meta.Version != version {
		return fieldError(file, "meta.version", "version", fmt.Sprintf("must match pack path version %q", version))
	}
	if strings.TrimSpace(meta.Name) == "" {
		return fieldError(file, "meta.name", "name", "must not be empty")
	}
	if strings.TrimSpace(meta.Currency) == "" {
		return fieldError(file, "meta.currency", "currency", "must not be empty")
	}
	return nil
}

func validateYearMap[T any](file, path, field string, years map[string]T, validate func(string, string, T) error) error {
	if len(years) == 0 {
		return fieldError(file, path, field, "must contain at least one tax year")
	}

	keys := make([]string, 0, len(years))
	for year := range years {
		keys = append(keys, year)
	}
	sort.Strings(keys)

	for _, year := range keys {
		if !taxYearPattern.MatchString(year) {
			return fieldError(file, path+"."+year, "tax_year", "must use format YYYY-YY")
		}
		if err := validate(file, year, years[year]); err != nil {
			return err
		}
	}

	return nil
}

func validateCorporateIncomeYear(file, year string, value CorporateIncomeYear) error {
	return validateRate(file, "tax.corporate_income."+year+".standard_rate", "standard_rate", value.StandardRate)
}

func validatePersonalIncomeYear(file, year string, value PersonalIncomeYear) error {
	path := "tax.personal_income." + year
	if value.PersonalAllowance < 0 {
		return fieldError(file, path+".personal_allowance", "personal_allowance", "must be greater than or equal to 0")
	}
	if len(value.Bands) == 0 {
		return fieldError(file, path+".bands", "bands", "must contain at least one band")
	}

	var previousUpTo *int64
	for index, band := range value.Bands {
		bandPath := fmt.Sprintf("%s.bands[%d]", path, index)
		if err := validateRate(file, bandPath+".rate", "rate", band.Rate); err != nil {
			return err
		}
		if band.UpTo == nil {
			if index != len(value.Bands)-1 {
				return fieldError(file, bandPath+".upto", "upto", "open-ended band must be last")
			}
			continue
		}
		if *band.UpTo < 0 {
			return fieldError(file, bandPath+".upto", "upto", "must be greater than or equal to 0")
		}
		if previousUpTo != nil && *band.UpTo <= *previousUpTo {
			return fieldError(file, bandPath+".upto", "upto", "bands must be ordered by increasing upto")
		}
		upTo := *band.UpTo
		previousUpTo = &upTo
	}

	return nil
}

func validateDividendYear(file, year string, value DividendYear) error {
	return validateRate(file, "tax.dividends."+year+".withholding_rate", "withholding_rate", value.WithholdingRate)
}

func validateVAT(file string, vat VAT) error {
	if strings.TrimSpace(vat.Regime) == "" {
		return fieldError(file, "tax.vat.regime", "regime", "must not be empty")
	}
	if err := validateYearMap(file, "tax.vat", "vat", vat.Years, validateVATYear); err != nil {
		return err
	}
	if len(vat.ReverseCharge) == 0 {
		return fieldError(file, "tax.vat.reverse_charge", "reverse_charge", "must contain at least one wording")
	}

	keys := make([]string, 0, len(vat.ReverseCharge))
	for key := range vat.ReverseCharge {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		wording := vat.ReverseCharge[key]
		path := "tax.vat.reverse_charge." + key
		if strings.TrimSpace(wording.Article) == "" {
			return fieldError(file, path+".article", "article", "must not be empty")
		}
		if strings.TrimSpace(wording.InvoiceWording) == "" {
			return fieldError(file, path+".invoice_wording", "invoice_wording", "must not be empty")
		}
	}

	return nil
}

func validateVATYear(file, year string, value VATYear) error {
	return validateRate(file, "tax.vat."+year+".standard_rate", "standard_rate", value.StandardRate)
}

func validateFilings(file string, filings map[string]Filing) error {
	if len(filings) == 0 {
		return fieldError(file, "filings", "filings", "must contain at least one filing")
	}

	keys := make([]string, 0, len(filings))
	for key := range filings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		filing := filings[key]
		path := "filings." + key
		dueExpression, err := parseDeadlineExpression(filing.Due)
		if err != nil {
			return fieldError(file, path+".due", "due", err.Error())
		}
		filing.dueExpression = dueExpression
		filings[key] = filing
		if strings.TrimSpace(filing.Authority) == "" {
			return fieldError(file, path+".authority", "authority", "must not be empty")
		}
	}
	return nil
}

func validateDirectorLoanYear(file, year string, value DirectorLoanYear) error {
	path := "director_loans." + year + ".overdrawn"
	if strings.TrimSpace(value.Overdrawn.Warn) == "" {
		return fieldError(file, path+".warn", "warn", "must not be empty")
	}
	if strings.TrimSpace(value.Overdrawn.Remedy) == "" {
		return fieldError(file, path+".remedy", "remedy", "must not be empty")
	}
	return nil
}

func validateAdvisorRules(file string, rules []AdvisorRule) error {
	if len(rules) == 0 {
		return fieldError(file, "advisor_rules", "advisor_rules", "must contain at least one rule")
	}
	for index, rule := range rules {
		path := fmt.Sprintf("advisor_rules[%d]", index)
		if strings.TrimSpace(rule.ID) == "" {
			return fieldError(file, path+".id", "id", "must not be empty")
		}
		if strings.TrimSpace(rule.Severity) == "" {
			return fieldError(file, path+".severity", "severity", "must not be empty")
		}
		if strings.TrimSpace(rule.FactQuery) == "" {
			return fieldError(file, path+".fact_query", "fact_query", "must not be empty")
		}
		if strings.TrimSpace(rule.Condition) == "" {
			return fieldError(file, path+".condition", "condition", "must not be empty")
		}
		if strings.TrimSpace(rule.TextTemplate) == "" {
			return fieldError(file, path+".text_template", "text_template", "must not be empty")
		}
		if strings.TrimSpace(rule.CTA) == "" {
			return fieldError(file, path+".cta", "cta", "must not be empty")
		}
	}
	return nil
}

func validateRate(file, path, field string, rate float64) error {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 1 {
		return fieldError(file, path, field, "rate must be between 0 and 1")
	}
	return nil
}
