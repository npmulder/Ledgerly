package jurisdiction

import (
	"errors"
	"strings"
	"testing"
)

func TestIsleOfManAccessorsReturnHandoffValues2025_26(t *testing.T) {
	loadIsleOfManForAccessors(t)

	tests := []struct {
		name  string
		check func(*testing.T)
	}{
		{
			name: "corporate income tax standard rate",
			check: func(t *testing.T) {
				got, err := CorporateRate("2025-26")
				if err != nil {
					t.Fatalf("CorporateRate() error = %v", err)
				}
				if got != "0.0" {
					t.Fatalf("CorporateRate() = %q, want 0.0", got)
				}
			},
		},
		{
			name: "personal income tax data",
			check: func(t *testing.T) {
				got, err := PersonalIncomeTax("2025-26")
				if err != nil {
					t.Fatalf("PersonalIncomeTax() error = %v", err)
				}
				if got.PersonalAllowanceMinorUnits != 1475000 {
					t.Fatalf("PersonalAllowanceMinorUnits = %d, want 1475000", got.PersonalAllowanceMinorUnits)
				}
				if len(got.Bands) != 2 {
					t.Fatalf("Bands length = %d, want 2", len(got.Bands))
				}
				if got.Bands[0].UpToMinorUnits == nil || *got.Bands[0].UpToMinorUnits != 650000 {
					t.Fatalf("Bands[0].UpToMinorUnits = %v, want 650000", got.Bands[0].UpToMinorUnits)
				}
				if got.Bands[0].Rate != "0.10" {
					t.Fatalf("Bands[0].Rate = %q, want 0.10", got.Bands[0].Rate)
				}
				if got.Bands[1].UpToMinorUnits != nil {
					t.Fatalf("Bands[1].UpToMinorUnits = %v, want nil", got.Bands[1].UpToMinorUnits)
				}
				if got.Bands[1].Rate != "0.21" {
					t.Fatalf("Bands[1].Rate = %q, want 0.21", got.Bands[1].Rate)
				}
			},
		},
		{
			name: "dividend withholding",
			check: func(t *testing.T) {
				got, err := DividendWithholding("2025-26")
				if err != nil {
					t.Fatalf("DividendWithholding() error = %v", err)
				}
				if got != "none" {
					t.Fatalf("DividendWithholding() = %q, want none", got)
				}
			},
		},
		{
			name: "VAT standard rate",
			check: func(t *testing.T) {
				got, err := VATStandardRate("2025-26")
				if err != nil {
					t.Fatalf("VATStandardRate() error = %v", err)
				}
				if got != "0.20" {
					t.Fatalf("VATStandardRate() = %q, want 0.20", got)
				}
			},
		},
		{
			name: "reverse charge wording",
			check: func(t *testing.T) {
				got, err := ReverseChargeWording("b2b_services_eu")
				if err != nil {
					t.Fatalf("ReverseChargeWording() error = %v", err)
				}
				if got.Article != "Article 196, Directive 2006/112/EC" {
					t.Fatalf("Article = %q, want Article 196, Directive 2006/112/EC", got.Article)
				}
				want := "VAT reverse charge applies: VAT to be accounted for by the recipient under Article 196, Council Directive 2006/112/EC. Supplier is established in the Isle of Man."
				if got.InvoiceWording != want {
					t.Fatalf("InvoiceWording = %q, want %q", got.InvoiceWording, want)
				}
			},
		},
		{
			name: "filing rules",
			check: func(t *testing.T) {
				got := FilingRules()
				if len(got) != 3 {
					t.Fatalf("FilingRules length = %d, want 3", len(got))
				}
				if got["annual_return"].Due != "incorporation_anniversary + 1 month" {
					t.Fatalf("annual_return due = %q", got["annual_return"].Due)
				}
				if got["annual_return"].Authority != "IoM Companies Registry" {
					t.Fatalf("annual_return authority = %q", got["annual_return"].Authority)
				}
				if got["company_tax_return"].Due != "accounting_year_end + 12 months + 1 day" {
					t.Fatalf("company_tax_return due = %q", got["company_tax_return"].Due)
				}
				if !got["company_tax_return"].RequiredAtZeroRate {
					t.Fatal("company_tax_return RequiredAtZeroRate = false, want true")
				}
				if got["vat_return"].Cadence != "quarterly" {
					t.Fatalf("vat_return cadence = %q, want quarterly", got["vat_return"].Cadence)
				}
				if got["vat_return"].Due != "quarter_end + 1 month" {
					t.Fatalf("vat_return due = %q", got["vat_return"].Due)
				}
				if got["vat_return"].Authority != "Isle of Man Customs & Excise" {
					t.Fatalf("vat_return authority = %q, want Isle of Man Customs & Excise", got["vat_return"].Authority)
				}
			},
		},
		{
			name: "director loan policy",
			check: func(t *testing.T) {
				got := DirectorLoanPolicy()
				if got.S455Charge {
					t.Fatal("S455Charge = true, want false")
				}
				if got.Overdrawn.Warn != "benefit_in_kind_interest_free" {
					t.Fatalf("Overdrawn.Warn = %q, want benefit_in_kind_interest_free", got.Overdrawn.Warn)
				}
				if got.Overdrawn.Remedy != "clear_with_dividend" {
					t.Fatalf("Overdrawn.Remedy = %q, want clear_with_dividend", got.Overdrawn.Remedy)
				}
			},
		},
		{
			name: "advisor rules",
			check: func(t *testing.T) {
				got := AdvisorRules()
				if len(got) != 5 {
					t.Fatalf("AdvisorRules length = %d, want 5", len(got))
				}
				want := map[string]struct {
					severity     string
					textTemplate string
				}{
					"overdue_invoice": {
						severity:     "amber",
						textTemplate: "Invoice {{ invoice_number }} is {{ days_overdue }} days overdue. Send a reminder to {{ client_name }}.",
					},
					"dla_overdrawn_bik": {
						severity:     "amber",
						textTemplate: "Your loan account is {{ balance }} overdrawn. The Isle of Man has no UK-style s455 charge, but an interest-free loan can create a taxable benefit in kind - charge interest at the official rate or clear it with a dividend.",
					},
					"filing_deadline_window": {
						severity:     "amber",
						textTemplate: "{{ filing_name }} due {{ due_date }} - file with {{ authority }}.",
					},
					"dividend_set_aside": {
						severity:     "teal",
						textTemplate: "Set aside {{ estimate }} personally for IoM income tax on {{ dividends_ytd }} dividends YTD (10% band, then 21%).",
					},
					"rates_stale": {
						severity:     "amber",
						textTemplate: "ECB rates are stale. Refresh rates before issuing or settling foreign-currency transactions.",
					},
				}
				for _, rule := range got {
					wantRule, ok := want[rule.ID]
					if !ok {
						t.Fatalf("unexpected advisor rule ID %q", rule.ID)
					}
					if rule.Severity != wantRule.severity {
						t.Fatalf("%s severity = %q, want %q", rule.ID, rule.Severity, wantRule.severity)
					}
					if rule.TextTemplate != wantRule.textTemplate {
						t.Fatalf("%s text_template = %q, want %q", rule.ID, rule.TextTemplate, wantRule.textTemplate)
					}
					delete(want, rule.ID)
				}
				if len(want) != 0 {
					t.Fatalf("missing advisor rules: %#v", want)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

func TestTaxYearAccessorsReturnTypedErrorForUnknownTaxYear(t *testing.T) {
	loadIsleOfManForAccessors(t)

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "corporate rate",
			call: func() error {
				_, err := CorporateRate("2099-00")
				return err
			},
		},
		{
			name: "personal income tax",
			call: func() error {
				_, err := PersonalIncomeTax("2099-00")
				return err
			},
		},
		{
			name: "dividend withholding",
			call: func() error {
				_, err := DividendWithholding("2099-00")
				return err
			},
		},
		{
			name: "VAT standard rate",
			call: func() error {
				_, err := VATStandardRate("2099-00")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil {
				t.Fatal("accessor error = nil, want UnknownTaxYearError")
			}
			var unknownYear UnknownTaxYearError
			if !errors.As(err, &unknownYear) {
				t.Fatalf("accessor error type = %T, want UnknownTaxYearError", err)
			}
			if unknownYear.TaxYear != "2099-00" {
				t.Fatalf("UnknownTaxYearError.TaxYear = %q, want 2099-00", unknownYear.TaxYear)
			}
		})
	}
}

func TestActivePackOverviewReturnsIsleOfManSummaries(t *testing.T) {
	loadIsleOfManForAccessors(t)

	overview, err := ActivePackOverview()
	if err != nil {
		t.Fatalf("ActivePackOverview() error = %v", err)
	}
	if overview.Meta.ID != "isle-of-man" || overview.Meta.Version != "1.0" || overview.Meta.Name != "Isle of Man" {
		t.Fatalf("Meta = %+v, want isle-of-man@1.0 Isle of Man", overview.Meta)
	}
	if len(overview.RuleSummaries) != 6 {
		t.Fatalf("RuleSummaries length = %d, want 6", len(overview.RuleSummaries))
	}

	want := map[string]string{
		ruleSummaryCorporateTax: "0% CIT (2025-26)",
		ruleSummaryPersonalTax:  "no dividend WHT; personal allowance GBP 14,750; bands 10% to GBP 6,500, then 21% (2025-26/2025-26)",
		ruleSummaryVAT:          "VAT 20% via Isle of Man Customs & Excise; reverse charge via Article 196, Directive 2006/112/EC (2025-26)",
		ruleSummaryAnnualReturn: "due incorporation anniversary + 1 month with IoM Companies Registry",
		ruleSummaryCompanyTax:   "due accounting year end + 12 months + 1 day; required at zero rate",
		ruleSummaryDirectorLoan: "no s455 charge; overdrawn warning: benefit in kind interest free; remedy: clear with dividend",
	}
	for _, summary := range overview.RuleSummaries {
		wantSummary, ok := want[summary.ID]
		if !ok {
			t.Fatalf("unexpected summary ID %q", summary.ID)
		}
		if summary.Summary != wantSummary {
			t.Fatalf("%s summary = %q, want %q", summary.ID, summary.Summary, wantSummary)
		}
		delete(want, summary.ID)
	}
	if len(want) > 0 {
		t.Fatalf("missing summaries: %#v", want)
	}
}

func TestPackOverviewRequiresSummaryFilingKeys(t *testing.T) {
	for _, key := range []string{ruleSummaryAnnualReturn, ruleSummaryCompanyTax} {
		t.Run(key, func(t *testing.T) {
			pack, err := LoadFromFS(testFixtureFS(t), "testland@0.1")
			if err != nil {
				t.Fatalf("LoadFromFS() error = %v", err)
			}
			delete(pack.Filings, key)

			_, err = packOverview(pack)
			if err == nil {
				t.Fatal("packOverview() error = nil, want missing filing error")
			}
			if !strings.Contains(err.Error(), `required filing "`+key+`" is not configured`) {
				t.Fatalf("packOverview() error = %q, want missing %s filing", err, key)
			}
		})
	}
}

func TestReverseChargeWordingReturnsTypedErrorForUnknownKind(t *testing.T) {
	loadIsleOfManForAccessors(t)

	_, err := ReverseChargeWording("unknown")
	if err == nil {
		t.Fatal("ReverseChargeWording() error = nil, want UnknownReverseChargeKindError")
	}
	var unknownKind UnknownReverseChargeKindError
	if !errors.As(err, &unknownKind) {
		t.Fatalf("ReverseChargeWording() error type = %T, want UnknownReverseChargeKindError", err)
	}
	if unknownKind.Kind != "unknown" {
		t.Fatalf("UnknownReverseChargeKindError.Kind = %q, want unknown", unknownKind.Kind)
	}
}

func loadIsleOfManForAccessors(t *testing.T) {
	t.Helper()

	if err := LoadActive(DefaultSelector); err != nil {
		t.Fatalf("LoadActive(%q) error = %v", DefaultSelector, err)
	}
}
