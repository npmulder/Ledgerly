package jurisdiction

import (
	"embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

//go:embed testdata/packs/*/*/pack.yaml
var embeddedTestFixtures embed.FS

func TestLoadFromFSEmbeddedFixture(t *testing.T) {
	pack, err := LoadFromFS(testFixtureFS(t), "testland@0.1")
	if err != nil {
		t.Fatalf("LoadFromFS() error = %v", err)
	}

	if pack.Meta.ID != "testland" {
		t.Fatalf("Meta.ID = %q, want testland", pack.Meta.ID)
	}
	if pack.Meta.Version != "0.1" {
		t.Fatalf("Meta.Version = %q, want 0.1", pack.Meta.Version)
	}
	if pack.Tax.CorporateIncome["2025-26"].StandardRate != "0.19" {
		t.Fatalf("corporate standard rate not loaded: %#v", pack.Tax.CorporateIncome)
	}
	if pack.Tax.YearEnd.Month != 6 || pack.Tax.YearEnd.Day != 30 {
		t.Fatalf("tax year end = %#v, want 30 June", pack.Tax.YearEnd)
	}
	if pack.Tax.PersonalIncome["2025-26"].PersonalAllowanceMinorUnits != 1234 {
		t.Fatalf("personal allowance not loaded: %#v", pack.Tax.PersonalIncome)
	}
	if len(pack.Tax.PersonalIncome["2025-26"].Bands) != 3 {
		t.Fatalf("personal bands length = %d, want 3", len(pack.Tax.PersonalIncome["2025-26"].Bands))
	}
	if pack.Tax.Dividends["2025-26"].Withholding != "test-withholding" {
		t.Fatalf("dividend withholding not loaded: %#v", pack.Tax.Dividends)
	}
	if pack.Tax.Dividends["2025-26"].PersonalTaxSetAsideTemplate != "test set aside {{ estimate }}" {
		t.Fatalf("dividend set-aside template not loaded: %#v", pack.Tax.Dividends)
	}
	if pack.Tax.VAT.Regime != "test-shared" {
		t.Fatalf("VAT regime = %q, want test-shared", pack.Tax.VAT.Regime)
	}
	if pack.Tax.VAT.Authority != "Testland Customs" {
		t.Fatalf("VAT authority = %q, want Testland Customs", pack.Tax.VAT.Authority)
	}
	if pack.Tax.VAT.Years["2025-26"].StandardRate != "0.17" {
		t.Fatalf("VAT standard rate not loaded: %#v", pack.Tax.VAT.Years)
	}
	if pack.Tax.VAT.ReverseCharge["b2b_services_eu"].InvoiceWording == "" {
		t.Fatal("reverse charge wording was empty")
	}
	if len(pack.Filings) != 3 {
		t.Fatalf("filings length = %d, want 3", len(pack.Filings))
	}
	if !pack.Filings["vat_return"].RequiresVATRegistration {
		t.Fatalf("vat_return RequiresVATRegistration = false, want true")
	}
	if !pack.DirectorLoans.S455Charge {
		t.Fatalf("director loan policy not loaded: %#v", pack.DirectorLoans)
	}
	if len(pack.AdvisorRules) != 1 {
		t.Fatalf("advisor rules length = %d, want 1", len(pack.AdvisorRules))
	}
}

func TestValidateAdvisorRulesRejectsDuplicateIDs(t *testing.T) {
	rule := AdvisorRule{
		ID:           "duplicate",
		Severity:     "amber",
		Surfaces:     []string{"dashboard"},
		FactQuery:    []string{"balance"},
		Condition:    "balance > 0",
		TextTemplate: "Review balance",
		CTA:          AdvisorCTA{Label: "Open", Action: "test.open"},
	}

	err := validateAdvisorRules("test-pack", []AdvisorRule{rule, rule})
	if err == nil {
		t.Fatal("validateAdvisorRules() error = nil, want duplicate rule id error")
	}

	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("validateAdvisorRules() error type = %T, want ValidationError", err)
	}
	if validationErr.Path != "advisor_rules[1].id" || !strings.Contains(validationErr.Message, `duplicate advisor rule id "duplicate"`) {
		t.Fatalf("ValidationError = %#v, want duplicate id at advisor_rules[1].id", validationErr)
	}
}

func TestCloneAdvisorRulesDeepCopiesNestedCTAParams(t *testing.T) {
	rules := []AdvisorRule{
		{
			ID:           "nested-cta",
			Severity:     "teal",
			Surfaces:     []string{"dashboard"},
			FactQuery:    []string{"balance"},
			Condition:    "balance > 0",
			TextTemplate: "Review balance",
			CTA: AdvisorCTA{
				Label:  "Open",
				Action: "test.open",
				Params: map[string]any{
					"nested": map[string]any{"value": "original"},
					"items":  []any{map[string]any{"value": "item-original"}},
				},
			},
		},
	}

	cloned := cloneAdvisorRules(rules)
	cloned[0].CTA.Params["nested"].(map[string]any)["value"] = "changed"
	cloned[0].CTA.Params["items"].([]any)[0].(map[string]any)["value"] = "item-changed"

	nested := rules[0].CTA.Params["nested"].(map[string]any)
	if nested["value"] != "original" {
		t.Fatalf("original nested CTA param = %#v, want original", nested["value"])
	}
	items := rules[0].CTA.Params["items"].([]any)
	if items[0].(map[string]any)["value"] != "item-original" {
		t.Fatalf("original nested CTA slice item = %#v, want item-original", items[0])
	}
}

func TestLoadActiveInstallsActivePackMeta(t *testing.T) {
	if err := LoadActiveFromFS(testFixtureFS(t), "testland@0.1"); err != nil {
		t.Fatalf("LoadActiveFromFS() error = %v", err)
	}

	meta := ActivePack()
	if meta.ID != "testland" || meta.Version != "0.1" || meta.Name != "Testland" || meta.Currency != "TST" {
		t.Fatalf("ActivePack() = %#v, want testland metadata", meta)
	}
}

func TestLoadFromFSEmbeddedIsleOfManPack(t *testing.T) {
	pack, err := LoadFromFS(embeddedPacks, DefaultSelector)
	if err != nil {
		t.Fatalf("LoadFromFS(%q) error = %v", DefaultSelector, err)
	}

	if pack.Meta.ID != "isle-of-man" || pack.Meta.Version != "1.0" {
		t.Fatalf("Meta = %#v, want isle-of-man@1.0", pack.Meta)
	}
	if pack.Tax.VAT.Authority != "Isle of Man Customs & Excise" {
		t.Fatalf("VAT authority = %q, want Isle of Man Customs & Excise", pack.Tax.VAT.Authority)
	}
	if !pack.Filings["vat_return"].RequiresVATRegistration {
		t.Fatalf("vat_return RequiresVATRegistration = false, want true")
	}
}

func TestEmbeddedPacksExcludeTestlandFixture(t *testing.T) {
	_, err := LoadFromFS(embeddedPacks, "testland@0.1")
	if err == nil {
		t.Fatal("LoadFromFS() error = nil, want missing production pack error")
	}

	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("LoadFromFS() error type = %T, want ValidationError", err)
	}
	if validationErr.File != PackPath("testland", "0.1") {
		t.Fatalf("ValidationError.File = %q, want %q", validationErr.File, PackPath("testland", "0.1"))
	}
	if !strings.Contains(validationErr.Message, "read embedded pack") {
		t.Fatalf("ValidationError.Message = %q, want missing pack read error", validationErr.Message)
	}
}

func TestTopLevelFixtureMatchesEmbeddedFixture(t *testing.T) {
	embedded := readEmbeddedFixture(t)
	topLevel, err := os.ReadFile(filepath.Join("..", "..", "packs", "testland", "0.1", "pack.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	if string(topLevel) != embedded {
		t.Fatal("top-level packs/testland/0.1/pack.yaml differs from embedded fixture")
	}
}

func TestTopLevelIsleOfManPackMatchesEmbeddedPack(t *testing.T) {
	embedded, err := fs.ReadFile(embeddedPacks, PackPath("isle-of-man", "1.0"))
	if err != nil {
		t.Fatal(err)
	}
	topLevel, err := os.ReadFile(filepath.Join("..", "..", "packs", "isle-of-man", "1.0", "pack.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	if string(topLevel) != string(embedded) {
		t.Fatal("top-level packs/isle-of-man/1.0/pack.yaml differs from embedded pack")
	}
}

func TestValidationFailuresNameFilePathAndField(t *testing.T) {
	valid := readEmbeddedFixture(t)

	tests := []struct {
		name      string
		pack      string
		wantPath  string
		wantField string
		wantText  string
	}{
		{
			name: "missing year",
			pack: strings.Replace(
				valid,
				"  corporate_income:\n    \"2025-26\":\n      standard_rate: \"0.19\"",
				"  corporate_income: {}",
				1,
			),
			wantPath:  "tax.corporate_income",
			wantField: "corporate_income",
			wantText:  "at least one tax year",
		},
		{
			name:      "rate greater than one",
			pack:      strings.Replace(valid, "standard_rate: \"0.19\"", "standard_rate: \"1.2\"", 1),
			wantPath:  "tax.corporate_income.2025-26.standard_rate",
			wantField: "standard_rate",
			wantText:  "between 0 and 1",
		},
		{
			name:      "missing dividend set-aside template",
			pack:      strings.Replace(valid, "      personal_tax_set_aside_template: 'test set aside {{ estimate }}'\n", "", 1),
			wantPath:  "tax.dividends.2025-26.personal_tax_set_aside_template",
			wantField: "personal_tax_set_aside_template",
			wantText:  "must not be empty",
		},
		{
			name:      "dividend set-aside template missing estimate placeholder",
			pack:      strings.Replace(valid, "{{ estimate }}", "{{ amount }}", 1),
			wantPath:  "tax.dividends.2025-26.personal_tax_set_aside_template",
			wantField: "personal_tax_set_aside_template",
			wantText:  "must contain {{ estimate }}",
		},
		{
			name: "unordered bands",
			pack: strings.Replace(
				valid,
				"        - upto_minor_units: 1000\n          rate: \"0.05\"\n        - upto_minor_units: 2000",
				"        - upto_minor_units: 2000\n          rate: \"0.05\"\n        - upto_minor_units: 1000",
				1,
			),
			wantPath:  "tax.personal_income.2025-26.bands[1].upto_minor_units",
			wantField: "upto_minor_units",
			wantText:  "ordered",
		},
		{
			name:      "unknown deadline anchor",
			pack:      strings.Replace(valid, "incorporation_anniversary + 1 month", "mystery_date + 1 month", 1),
			wantPath:  "filings.annual_return.due",
			wantField: "due",
			wantText:  "unknown deadline anchor",
		},
		{
			name:      "old vat period anchor",
			pack:      strings.Replace(valid, "quarter_end + 1 month", "vat_period_end + 1 month", 1),
			wantPath:  "filings.vat_return.due",
			wantField: "due",
			wantText:  "unknown deadline anchor",
		},
		{
			name:      "unsupported year offset",
			pack:      strings.Replace(valid, "accounting_year_end + 12 months + 1 day", "accounting_year_end + 1 year", 1),
			wantPath:  "filings.company_tax_return.due",
			wantField: "due",
			wantText:  "unknown deadline offset unit",
		},
		{
			name:      "malformed advisor condition",
			pack:      strings.Replace(valid, "condition: balance > 0", "condition: balance >", 1),
			wantPath:  "advisor_rules[0].condition",
			wantField: "condition",
			wantText:  "expected expression",
		},
		{
			name: "missing annual return filing",
			pack: strings.Replace(
				valid,
				"  annual_return:\n    due: incorporation_anniversary + 1 month\n    authority: Testland Companies Office\n",
				"",
				1,
			),
			wantPath:  "filings.annual_return",
			wantField: "annual_return",
			wantText:  "required for pack summaries",
		},
		{
			name: "missing company tax return filing",
			pack: strings.Replace(
				valid,
				"  company_tax_return:\n    due: accounting_year_end + 12 months + 1 day\n    authority: Testland Revenue\n    required_at_zero_rate: true\n",
				"",
				1,
			),
			wantPath:  "filings.company_tax_return",
			wantField: "company_tax_return",
			wantText:  "required for pack summaries",
		},
		{
			name:      "empty wording",
			pack:      strings.Replace(valid, "invoice_wording: Testland reverse charge applies", "invoice_wording: \"\"", 1),
			wantPath:  "tax.vat.reverse_charge.b2b_services_eu.invoice_wording",
			wantField: "invoice_wording",
			wantText:  "must not be empty",
		},
		{
			name: "missing domestic VAT treatment semantics",
			pack: strings.Replace(
				valid,
				"      domestic:\n        output_vat: true\n        vat_return_net_sales: true\n",
				"",
				1,
			),
			wantPath:  "tax.vat.treatments.domestic",
			wantField: "domestic",
			wantText:  "required",
		},
		{
			name: "missing reverse-charge VAT treatment semantics",
			pack: strings.Replace(
				valid,
				"      reverse-charge-eu-b2b:\n        output_vat: false\n        vat_return_net_sales: true\n        reverse_charge_kind: b2b_services_eu\n",
				"",
				1,
			),
			wantPath:  "tax.vat.treatments.reverse-charge-eu-b2b",
			wantField: "reverse-charge-eu-b2b",
			wantText:  "required",
		},
		{
			name: "domestic VAT treatment must report output VAT",
			pack: strings.Replace(
				valid,
				"      domestic:\n        output_vat: true\n        vat_return_net_sales: true\n",
				"      domestic: {}\n",
				1,
			),
			wantPath:  "tax.vat.treatments.domestic.output_vat",
			wantField: "output_vat",
			wantText:  "must be true",
		},
		{
			name: "reverse-charge VAT treatment must report net sales",
			pack: strings.Replace(
				valid,
				"      reverse-charge-eu-b2b:\n        output_vat: false\n        vat_return_net_sales: true\n        reverse_charge_kind: b2b_services_eu\n",
				"      reverse-charge-eu-b2b:\n        output_vat: false\n        reverse_charge_kind: b2b_services_eu\n",
				1,
			),
			wantPath:  "tax.vat.treatments.reverse-charge-eu-b2b.vat_return_net_sales",
			wantField: "vat_return_net_sales",
			wantText:  "must be true",
		},
		{
			name: "reverse-charge VAT treatment must reference wording",
			pack: strings.Replace(
				valid,
				"      reverse-charge-eu-b2b:\n        output_vat: false\n        vat_return_net_sales: true\n        reverse_charge_kind: b2b_services_eu\n",
				"      reverse-charge-eu-b2b:\n        output_vat: false\n        vat_return_net_sales: true\n",
				1,
			),
			wantPath:  "tax.vat.treatments.reverse-charge-eu-b2b.reverse_charge_kind",
			wantField: "reverse_charge_kind",
			wantText:  "must not be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFromFS(mapPack(tt.pack), "testland@0.1")
			if err == nil {
				t.Fatal("LoadFromFS() error = nil, want validation error")
			}

			var validationErr ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("LoadFromFS() error type = %T, want ValidationError", err)
			}
			if validationErr.File != PackPath("testland", "0.1") {
				t.Fatalf("ValidationError.File = %q, want %q", validationErr.File, PackPath("testland", "0.1"))
			}
			if validationErr.Path != tt.wantPath {
				t.Fatalf("ValidationError.Path = %q, want %q", validationErr.Path, tt.wantPath)
			}
			if validationErr.Field != tt.wantField {
				t.Fatalf("ValidationError.Field = %q, want %q", validationErr.Field, tt.wantField)
			}
			if !strings.Contains(validationErr.Message, tt.wantText) {
				t.Fatalf("ValidationError.Message = %q, want text %q", validationErr.Message, tt.wantText)
			}
		})
	}
}

func TestLoadFromFSRejectsUnknownVATYearField(t *testing.T) {
	valid := readEmbeddedFixture(t)
	pack := strings.Replace(valid, "standard_rate: \"0.17\"", "standard_ratee: \"0.17\"", 1)

	_, err := LoadFromFS(mapPack(pack), "testland@0.1")
	if err == nil {
		t.Fatal("LoadFromFS() error = nil, want unknown VAT year field error")
	}

	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("LoadFromFS() error type = %T, want ValidationError", err)
	}
	if validationErr.File != PackPath("testland", "0.1") {
		t.Fatalf("ValidationError.File = %q, want %q", validationErr.File, PackPath("testland", "0.1"))
	}
	if validationErr.Path != "pack" || validationErr.Field != "yaml" {
		t.Fatalf("ValidationError path/field = %q/%q, want pack/yaml", validationErr.Path, validationErr.Field)
	}
	if !strings.Contains(validationErr.Message, `unknown vat year field "standard_ratee"`) {
		t.Fatalf("ValidationError.Message = %q, want unknown VAT field", validationErr.Message)
	}
}

func testFixtureFS(t testing.TB) fs.FS {
	t.Helper()

	files, err := fs.Sub(embeddedTestFixtures, "testdata")
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func readEmbeddedFixture(t *testing.T) string {
	t.Helper()

	data, err := fs.ReadFile(testFixtureFS(t), PackPath("testland", "0.1"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mapPack(pack string) fstest.MapFS {
	return fstest.MapFS{
		PackPath("testland", "0.1"): {
			Data: []byte(pack),
		},
	}
}
