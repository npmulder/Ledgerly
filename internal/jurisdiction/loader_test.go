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
	if !pack.DirectorLoans.S455Charge {
		t.Fatalf("director loan policy not loaded: %#v", pack.DirectorLoans)
	}
	if len(pack.AdvisorRules) != 1 {
		t.Fatalf("advisor rules length = %d, want 1", len(pack.AdvisorRules))
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
			name:      "empty wording",
			pack:      strings.Replace(valid, "invoice_wording: Testland reverse charge applies", "invoice_wording: \"\"", 1),
			wantPath:  "tax.vat.reverse_charge.b2b_services_eu.invoice_wording",
			wantField: "invoice_wording",
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
