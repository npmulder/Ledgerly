package jurisdiction

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

type requiredVATTreatment struct {
	key               string
	outputVAT         bool
	vatReturnNetSales bool
	reverseChargeKind bool
}

var requiredVATTreatments = []requiredVATTreatment{
	{
		key:               "domestic",
		outputVAT:         true,
		vatReturnNetSales: true,
	},
	{
		key:               "reverse-charge-eu-b2b",
		outputVAT:         false,
		vatReturnNetSales: true,
		reverseChargeKind: true,
	},
}

func validatePack(file, id, version string, pack *Pack) error {
	if err := validateMeta(file, id, version, pack.Meta); err != nil {
		return err
	}

	if err := validateCompanyActs(file, pack.CompanyActs); err != nil {
		return err
	}
	if err := validateYearEnd(pack.Tax.YearEnd); err != nil {
		return fieldError(file, "tax.year_end", "year_end", err.Error())
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

func validateCompanyActs(file string, acts map[string]CompanyAct) error {
	if len(acts) == 0 {
		return fieldError(file, "company_acts", "company_acts", "must contain at least one company act")
	}

	keys := make([]string, 0, len(acts))
	for key := range acts {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	seenSuffixes := map[string]string{}
	for _, key := range keys {
		act := acts[key]
		path := "company_acts." + key
		if strings.TrimSpace(key) == "" {
			return fieldError(file, path, "company_act", "must not be empty")
		}
		if strings.TrimSpace(act.Label) == "" {
			return fieldError(file, path+".label", "label", "must not be empty")
		}
		if act.MinimumDirectors < 1 {
			return fieldError(file, path+".minimum_directors", "minimum_directors", "must be greater than or equal to 1")
		}
		if len(act.CompanyNumberSuffixes) == 0 {
			return fieldError(file, path+".company_number_suffixes", "company_number_suffixes", "must contain at least one company number suffix")
		}
		seenActSuffixes := map[string]struct{}{}
		for index, suffix := range act.CompanyNumberSuffixes {
			suffix = strings.TrimSpace(strings.ToUpper(suffix))
			suffixPath := fmt.Sprintf("%s.company_number_suffixes[%d]", path, index)
			if suffix == "" {
				return fieldError(file, suffixPath, "company_number_suffix", "must not be empty")
			}
			if _, ok := seenActSuffixes[suffix]; ok {
				return fieldError(file, suffixPath, "company_number_suffix", fmt.Sprintf("duplicate suffix %q for company act %q", suffix, key))
			}
			if previous, ok := seenSuffixes[suffix]; ok {
				return fieldError(file, suffixPath, "company_number_suffix", fmt.Sprintf("suffix %q is already assigned to company act %q", suffix, previous))
			}
			seenActSuffixes[suffix] = struct{}{}
			seenSuffixes[suffix] = key
		}
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
	path := "tax.dividends." + year
	if strings.TrimSpace(value.Withholding) == "" {
		return fieldError(file, path+".withholding", "withholding", "must not be empty")
	}
	template := strings.TrimSpace(value.PersonalTaxSetAsideTemplate)
	if template == "" {
		return fieldError(file, path+".personal_tax_set_aside_template", "personal_tax_set_aside_template", "must not be empty")
	}
	if !strings.Contains(template, "{{ estimate }}") {
		return fieldError(file, path+".personal_tax_set_aside_template", "personal_tax_set_aside_template", "must contain {{ estimate }}")
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
	if err := validateVATTreatments(file, vat); err != nil {
		return err
	}

	return nil
}

func validateVATTreatments(file string, vat VAT) error {
	if len(vat.Treatments) == 0 {
		return fieldError(file, "tax.vat.treatments", "treatments", "must contain at least one treatment")
	}
	for _, required := range requiredVATTreatments {
		treatment, ok := vat.Treatments[required.key]
		if !ok {
			return fieldError(file, "tax.vat.treatments."+required.key, required.key, "is required for supported invoicing VAT treatments")
		}
		path := "tax.vat.treatments." + required.key
		if treatment.OutputVAT != required.outputVAT {
			return fieldError(file, path+".output_vat", "output_vat", fmt.Sprintf("must be %t for supported invoicing VAT treatment %q", required.outputVAT, required.key))
		}
		if treatment.VATReturnNetSales != required.vatReturnNetSales {
			return fieldError(file, path+".vat_return_net_sales", "vat_return_net_sales", fmt.Sprintf("must be %t for supported invoicing VAT treatment %q", required.vatReturnNetSales, required.key))
		}
		if required.reverseChargeKind && strings.TrimSpace(treatment.ReverseChargeKind) == "" {
			return fieldError(file, path+".reverse_charge_kind", "reverse_charge_kind", fmt.Sprintf("must not be empty for supported invoicing VAT treatment %q", required.key))
		}
		if !required.reverseChargeKind && strings.TrimSpace(treatment.ReverseChargeKind) != "" {
			return fieldError(file, path+".reverse_charge_kind", "reverse_charge_kind", fmt.Sprintf("must be empty for supported invoicing VAT treatment %q", required.key))
		}
	}

	keys := make([]string, 0, len(vat.Treatments))
	for key := range vat.Treatments {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		path := "tax.vat.treatments." + key
		if strings.TrimSpace(key) == "" {
			return fieldError(file, path, "treatment", "must not be empty")
		}
		treatment := vat.Treatments[key]
		reverseChargeKind := strings.TrimSpace(treatment.ReverseChargeKind)
		if reverseChargeKind == "" {
			continue
		}
		if _, ok := vat.ReverseCharge[reverseChargeKind]; !ok {
			return fieldError(file, path+".reverse_charge_kind", "reverse_charge_kind", fmt.Sprintf("must reference configured reverse charge wording %q", reverseChargeKind))
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
	for _, key := range []string{ruleSummaryAnnualReturn, ruleSummaryCompanyTax} {
		if _, ok := filings[key]; !ok {
			return fieldError(file, "filings."+key, key, "is required for pack summaries")
		}
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
			dueExpression, err := parseDeadlineExpression(filing.Due)
			if err != nil {
				return fieldError(file, path+".due", "due", err.Error())
			}
			filing.dueExpression = dueExpression
		}
		if filing.Authority != "" && strings.TrimSpace(filing.Authority) == "" {
			return fieldError(file, path+".authority", "authority", "must not be blank")
		}
		filings[key] = filing
	}
	return nil
}

func validateDLAPolicy(file string, value DLAPolicy) error {
	creditPath := "director_loans.credit"
	if strings.TrimSpace(value.Credit.StatusText) == "" {
		return fieldError(file, creditPath+".status_text", "status_text", "must not be empty")
	}
	if strings.TrimSpace(value.Credit.ExplainerTemplate) == "" {
		return fieldError(file, creditPath+".explainer_template", "explainer_template", "must not be empty")
	}

	path := "director_loans.overdrawn"
	if strings.TrimSpace(value.Overdrawn.Warn) == "" {
		return fieldError(file, path+".warn", "warn", "must not be empty")
	}
	if strings.TrimSpace(value.Overdrawn.WarningTemplate) == "" {
		return fieldError(file, path+".warning_template", "warning_template", "must not be empty")
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
	seenIDs := make(map[string]struct{}, len(rules))
	for index, rule := range rules {
		path := fmt.Sprintf("advisor_rules[%d]", index)
		ruleID := strings.TrimSpace(rule.ID)
		if ruleID == "" {
			return fieldError(file, path+".id", "id", "must not be empty")
		}
		if _, ok := seenIDs[ruleID]; ok {
			return fieldError(file, path+".id", "id", fmt.Sprintf("duplicate advisor rule id %q", ruleID))
		}
		seenIDs[ruleID] = struct{}{}
		switch strings.TrimSpace(rule.Severity) {
		case "teal", "amber":
		default:
			return fieldError(file, path+".severity", "severity", "must be teal or amber")
		}
		if len(rule.Surfaces) == 0 {
			return fieldError(file, path+".surfaces", "surfaces", "must contain at least one surface")
		}
		for surfaceIndex, surface := range rule.Surfaces {
			if !validAdvisorSurface(strings.TrimSpace(surface)) {
				return fieldError(file, fmt.Sprintf("%s.surfaces[%d]", path, surfaceIndex), "surface", "must be dashboard, settings, invoices, banking, dla, dividends, or reports")
			}
		}
		if len(rule.FactQuery) == 0 {
			return fieldError(file, path+".fact_query", "fact_query", "must contain at least one fact key")
		}
		for factIndex, fact := range rule.FactQuery {
			if strings.TrimSpace(fact) == "" {
				return fieldError(file, fmt.Sprintf("%s.fact_query[%d]", path, factIndex), "fact_key", "must not be empty")
			}
		}
		if strings.TrimSpace(rule.Condition) == "" {
			return fieldError(file, path+".condition", "condition", "must not be empty")
		}
		if err := validateAdvisorConditionSyntax(rule.Condition); err != nil {
			return fieldError(file, path+".condition", "condition", err.Error())
		}
		if strings.TrimSpace(rule.TextTemplate) == "" {
			return fieldError(file, path+".text_template", "text_template", "must not be empty")
		}
		if strings.TrimSpace(rule.CTA.Label) == "" {
			return fieldError(file, path+".cta.label", "label", "must not be empty")
		}
		if strings.TrimSpace(rule.CTA.Action) == "" {
			return fieldError(file, path+".cta.action", "action", "must not be empty")
		}
	}
	return nil
}

func validAdvisorSurface(value string) bool {
	switch value {
	case "dashboard", "settings", "invoices", "banking", "dla", "dividends", "reports":
		return true
	default:
		return false
	}
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
