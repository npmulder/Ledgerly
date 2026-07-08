package jurisdiction

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

const (
	ruleSummaryCorporateTax    = "corporate_income_tax"
	ruleSummaryPersonalTax     = "personal_tax_dividends"
	ruleSummaryVAT             = "vat"
	ruleSummaryAnnualReturn    = "annual_return"
	ruleSummaryCompanyTax      = "company_tax_return"
	ruleSummaryDirectorLoan    = "director_loan"
	defaultReverseChargeKind   = "b2b_services_eu"
	noDividendWithholdingValue = "none"
)

// PackOverview is the UI-safe read model for the active jurisdiction pack.
type PackOverview struct {
	Meta          PackMeta
	CompanyActs   []CompanyActOverview
	RuleSummaries []RuleSummary
}

// CompanyActOverview is the UI-safe company act metadata from the active pack.
type CompanyActOverview struct {
	ActType               string
	Label                 string
	MinimumDirectors      int
	CorporateDirectors    *bool
	CompanyNumberSuffixes []string
}

// RuleSummary is one human-readable summary generated from rules-pack data.
type RuleSummary struct {
	ID      string
	Label   string
	Summary string
}

// ActivePackOverview returns metadata and generated human summaries for the
// active pack.
func ActivePackOverview() (PackOverview, error) {
	pack, err := activePackSnapshot()
	if err != nil {
		return PackOverview{}, err
	}
	return packOverview(pack)
}

func packOverview(pack *Pack) (PackOverview, error) {
	if pack == nil {
		return PackOverview{}, fmt.Errorf("jurisdiction: pack is nil")
	}

	corporateYear, corporate, err := latestYear(pack.Tax.CorporateIncome)
	if err != nil {
		return PackOverview{}, fmt.Errorf("corporate income tax summary: %w", err)
	}
	personalYear, personal, err := latestYear(pack.Tax.PersonalIncome)
	if err != nil {
		return PackOverview{}, fmt.Errorf("personal income tax summary: %w", err)
	}
	dividendYear, dividends, err := latestYear(pack.Tax.Dividends)
	if err != nil {
		return PackOverview{}, fmt.Errorf("dividend summary: %w", err)
	}
	vatYear, vat, err := latestYear(pack.Tax.VAT.Years)
	if err != nil {
		return PackOverview{}, fmt.Errorf("VAT summary: %w", err)
	}
	annualReturn, err := requiredFiling(pack.Filings, ruleSummaryAnnualReturn)
	if err != nil {
		return PackOverview{}, err
	}
	companyTaxReturn, err := requiredFiling(pack.Filings, ruleSummaryCompanyTax)
	if err != nil {
		return PackOverview{}, err
	}

	return PackOverview{
		Meta:        pack.Meta,
		CompanyActs: companyActOverviews(pack.CompanyActs),
		RuleSummaries: []RuleSummary{
			{
				ID:      ruleSummaryCorporateTax,
				Label:   "Corporate income tax",
				Summary: fmt.Sprintf("%s CIT (%s)", formatRatePercent(corporate.StandardRate), corporateYear),
			},
			{
				ID:    ruleSummaryPersonalTax,
				Label: "Personal tax and dividends",
				Summary: fmt.Sprintf(
					"%s; personal allowance %s; bands %s (%s/%s)",
					formatDividendWithholding(dividends.Withholding),
					formatMinorUnits(personal.PersonalAllowanceMinorUnits, pack.Meta.Currency),
					formatTaxBands(personal.Bands, pack.Meta.Currency),
					personalYear,
					dividendYear,
				),
			},
			{
				ID:      ruleSummaryVAT,
				Label:   "VAT",
				Summary: fmt.Sprintf("VAT %s via %s; %s (%s)", formatRatePercent(vat.StandardRate), pack.Tax.VAT.Authority, formatReverseCharge(pack.Tax.VAT.ReverseCharge), vatYear),
			},
			filingSummary(ruleSummaryAnnualReturn, "Annual return", annualReturn),
			filingSummary(ruleSummaryCompanyTax, "Company tax return", companyTaxReturn),
			{
				ID:      ruleSummaryDirectorLoan,
				Label:   "Director loan account",
				Summary: formatDirectorLoanPolicy(pack.DirectorLoans),
			},
		},
	}, nil
}

func companyActOverviews(acts map[string]CompanyAct) []CompanyActOverview {
	keys := make([]string, 0, len(acts))
	for key := range acts {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]CompanyActOverview, 0, len(keys))
	for _, key := range keys {
		act := acts[key]
		overview := CompanyActOverview{
			ActType:               key,
			Label:                 act.Label,
			MinimumDirectors:      act.MinimumDirectors,
			CompanyNumberSuffixes: append([]string(nil), act.CompanyNumberSuffixes...),
		}
		if act.CorporateDirectors != nil {
			corporateDirectors := *act.CorporateDirectors
			overview.CorporateDirectors = &corporateDirectors
		}
		out = append(out, overview)
	}
	return out
}

func requiredFiling(filings map[string]Filing, key string) (Filing, error) {
	filing, ok := filings[key]
	if !ok {
		return Filing{}, fmt.Errorf("jurisdiction: required filing %q is not configured", key)
	}
	return filing, nil
}

type orderedYear interface {
	CorporateIncomeYear | PersonalIncomeYear | DividendYear | VATYear
}

func latestYear[T orderedYear](years map[string]T) (string, T, error) {
	var zero T
	if len(years) == 0 {
		return "", zero, fmt.Errorf("no tax years configured")
	}
	keys := make([]string, 0, len(years))
	for key := range years {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	key := keys[len(keys)-1]
	return key, years[key], nil
}

func filingSummary(id, label string, filing Filing) RuleSummary {
	summary := fmt.Sprintf("due %s", formatExpression(filing.Due))
	if strings.TrimSpace(filing.Authority) != "" {
		summary = fmt.Sprintf("%s with %s", summary, filing.Authority)
	}
	if filing.RequiredAtZeroRate {
		summary += "; required at zero rate"
	}
	return RuleSummary{
		ID:      id,
		Label:   label,
		Summary: summary,
	}
}

func formatDividendWithholding(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, noDividendWithholdingValue) {
		return "no dividend WHT"
	}
	if value == "" {
		return "dividend WHT not configured"
	}
	return "dividend WHT: " + humanizeToken(value)
}

func formatDirectorLoanPolicy(policy DLAPolicy) string {
	charge := "no s455 charge"
	if policy.S455Charge {
		charge = "s455 charge applies"
	}
	return fmt.Sprintf(
		"%s; overdrawn warning: %s; remedy: %s",
		charge,
		humanizeToken(policy.Overdrawn.Warn),
		humanizeToken(policy.Overdrawn.Remedy),
	)
}

func formatReverseCharge(wording map[string]Wording) string {
	if len(wording) == 0 {
		return "no reverse charge wording configured"
	}
	if value, ok := wording[defaultReverseChargeKind]; ok {
		return "reverse charge " + formatReverseChargeArticle(value.Article)
	}
	keys := make([]string, 0, len(wording))
	for key := range wording {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return "reverse charge " + formatReverseChargeArticle(wording[keys[0]].Article)
}

func formatReverseChargeArticle(article string) string {
	article = strings.TrimSpace(article)
	if article == "" {
		return "configured"
	}
	return "via " + article
}

func formatTaxBands(bands []TaxBand, currency string) string {
	if len(bands) == 0 {
		return "not configured"
	}

	parts := make([]string, 0, len(bands))
	for _, band := range bands {
		rate := formatRatePercent(band.Rate)
		if band.UpToMinorUnits == nil {
			parts = append(parts, "then "+rate)
			continue
		}
		parts = append(parts, fmt.Sprintf("%s to %s", rate, formatMinorUnits(*band.UpToMinorUnits, currency)))
	}
	return strings.Join(parts, ", ")
}

func formatRatePercent(rate Rate) string {
	rat, ok := new(big.Rat).SetString(strings.TrimSpace(string(rate)))
	if !ok {
		return strings.TrimSpace(string(rate))
	}
	rat.Mul(rat, big.NewRat(100, 1))
	if rat.IsInt() {
		return rat.Num().String() + "%"
	}
	return strings.TrimRight(strings.TrimRight(rat.FloatString(2), "0"), ".") + "%"
}

func formatMinorUnits(amount int64, currency string) string {
	sign := ""
	if amount < 0 {
		sign = "-"
		amount = -amount
	}

	units := amount / 100
	cents := amount % 100
	currency = strings.TrimSpace(currency)
	if currency == "" {
		currency = "GBP"
	}
	if cents == 0 {
		return fmt.Sprintf("%s%s %s", sign, currency, formatThousands(units))
	}
	return fmt.Sprintf("%s%s %s.%02d", sign, currency, formatThousands(units), cents)
}

func formatThousands(value int64) string {
	raw := fmt.Sprintf("%d", value)
	if len(raw) <= 3 {
		return raw
	}

	var builder strings.Builder
	remainder := len(raw) % 3
	if remainder == 0 {
		remainder = 3
	}
	builder.WriteString(raw[:remainder])
	for i := remainder; i < len(raw); i += 3 {
		builder.WriteByte(',')
		builder.WriteString(raw[i : i+3])
	}
	return builder.String()
}

func formatExpression(value string) string {
	return humanizeToken(value)
}

func humanizeToken(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "_", " ")
	return value
}
