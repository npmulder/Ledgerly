package jurisdiction

import (
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

var (
	knownDeadlineAnchors = map[string]struct{}{
		"accounting_year_end":       {},
		"incorporation_anniversary": {},
		"tax_year_end":              {},
		"vat_period_end":            {},
	}
	knownDeadlineUnits = map[string]struct{}{
		"day":    {},
		"days":   {},
		"month":  {},
		"months": {},
		"year":   {},
		"years":  {},
	}
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
	if err := validateDLAPolicy(file, pack.DirectorLoans); err != nil {
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
	if value.PersonalAllowanceMinorUnits < 0 {
		return fieldError(file, path+".personal_allowance_minor_units", "personal_allowance_minor_units", "must be greater than or equal to 0")
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
		if band.UpToMinorUnits == nil {
			if index != len(value.Bands)-1 {
				return fieldError(file, bandPath+".upto_minor_units", "upto_minor_units", "open-ended band must be last")
			}
			continue
		}
		if *band.UpToMinorUnits < 0 {
			return fieldError(file, bandPath+".upto_minor_units", "upto_minor_units", "must be greater than or equal to 0")
		}
		if previousUpTo != nil && *band.UpToMinorUnits <= *previousUpTo {
			return fieldError(file, bandPath+".upto_minor_units", "upto_minor_units", "bands must be ordered by increasing upto_minor_units")
		}
		upTo := *band.UpToMinorUnits
		previousUpTo = &upTo
	}

	return nil
}

func validateDividendYear(file, year string, value DividendYear) error {
	if strings.TrimSpace(value.Withholding) == "" {
		return fieldError(file, "tax.dividends."+year+".withholding", "withholding", "must not be empty")
	}
	return nil
}

func validateVAT(file string, vat VAT) error {
	if strings.TrimSpace(vat.Regime) == "" {
		return fieldError(file, "tax.vat.regime", "regime", "must not be empty")
	}
	if strings.TrimSpace(vat.Authority) == "" {
		return fieldError(file, "tax.vat.authority", "authority", "must not be empty")
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
		if strings.TrimSpace(filing.Due) == "" {
			if strings.TrimSpace(filing.Cadence) == "" {
				return fieldError(file, path+".due", "due", "must have a due expression or cadence")
			}
		} else {
			if err := validateDeadlineExpression(file, path+".due", "due", filing.Due); err != nil {
				return err
			}
		}
		if filing.Authority != "" && strings.TrimSpace(filing.Authority) == "" {
			return fieldError(file, path+".authority", "authority", "must not be blank")
		}
	}
	return nil
}

func validateDLAPolicy(file string, value DLAPolicy) error {
	path := "director_loans.overdrawn"
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

func validateRate(file, path, field string, rate Rate) error {
	parsed, ok := new(big.Rat).SetString(strings.TrimSpace(string(rate)))
	if !ok {
		return fieldError(file, path, field, "rate must be a decimal string")
	}
	if parsed.Sign() < 0 || parsed.Cmp(big.NewRat(1, 1)) > 0 {
		return fieldError(file, path, field, "rate must be between 0 and 1")
	}
	return nil
}

func validateDeadlineExpression(file, path, field, expression string) error {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return fieldError(file, path, field, "must not be empty")
	}

	parts := strings.Split(expression, "+")
	anchor := strings.TrimSpace(parts[0])
	if len(strings.Fields(anchor)) != 1 {
		return fieldError(file, path, field, "deadline anchor must be a single token")
	}
	if _, ok := knownDeadlineAnchors[anchor]; !ok {
		return fieldError(file, path, field, fmt.Sprintf("unknown deadline anchor %q", anchor))
	}

	for _, offset := range parts[1:] {
		fields := strings.Fields(strings.TrimSpace(offset))
		if len(fields) != 2 {
			return fieldError(file, path, field, "deadline offsets must use '<number> <unit>'")
		}
		amount, err := strconv.Atoi(fields[0])
		if err != nil || amount <= 0 {
			return fieldError(file, path, field, "deadline offset amount must be a positive integer")
		}
		if _, ok := knownDeadlineUnits[fields[1]]; !ok {
			return fieldError(file, path, field, fmt.Sprintf("unknown deadline offset unit %q", fields[1]))
		}
	}

	return nil
}
