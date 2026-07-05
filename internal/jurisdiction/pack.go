package jurisdiction

// DefaultSelector is the production jurisdiction pack selector. JUR-2 supplies
// the Isle of Man pack contents; JUR-1 owns the format, loader, and validation.
const DefaultSelector = "isle-of-man@1.0"

// PackMeta identifies a versioned jurisdiction rules pack.
type PackMeta struct {
	ID       string `yaml:"id"`
	Version  string `yaml:"version"`
	Name     string `yaml:"name"`
	Currency string `yaml:"currency"`
}

// Pack is the typed YAML schema for a jurisdiction rules pack.
type Pack struct {
	Meta          PackMeta          `yaml:"meta"`
	Tax           Tax               `yaml:"tax"`
	Filings       map[string]Filing `yaml:"filings"`
	DirectorLoans DLAPolicy         `yaml:"director_loans"`
	AdvisorRules  []AdvisorRule     `yaml:"advisor_rules"`
}

// Tax contains tax-year keyed rules. Rate and allowance values always live
// below a tax-year string such as "2025-26".
type Tax struct {
	CorporateIncome map[string]CorporateIncomeYear `yaml:"corporate_income"`
	PersonalIncome  map[string]PersonalIncomeYear  `yaml:"personal_income"`
	Dividends       map[string]DividendYear        `yaml:"dividends"`
	VAT             VAT                            `yaml:"vat"`
}

// Rate is a decimal string from the pack, for example "0.20".
// Keeping rates as strings avoids binary floating point in compliance data.
type Rate string

type CorporateIncomeYear struct {
	StandardRate Rate `yaml:"standard_rate"`
}

type PersonalIncomeYear struct {
	// PersonalAllowanceMinorUnits is stored as integer GBP minor units (pence).
	PersonalAllowanceMinorUnits int64     `yaml:"personal_allowance_minor_units"`
	Bands                       []TaxBand `yaml:"bands"`
}

type TaxBand struct {
	// UpToMinorUnits is stored as integer GBP minor units (pence). A nil value
	// marks the open-ended final band.
	UpToMinorUnits *int64 `yaml:"upto_minor_units,omitempty"`
	Rate           Rate   `yaml:"rate"`
}

type DividendYear struct {
	Withholding string `yaml:"withholding"`
}

type VAT struct {
	Regime        string             `yaml:"regime"`
	Authority     string             `yaml:"authority"`
	Years         map[string]VATYear `yaml:"-"`
	ReverseCharge map[string]Wording `yaml:"reverse_charge"`
}

type VATYear struct {
	StandardRate Rate `yaml:"standard_rate"`
}

type Wording struct {
	Article        string `yaml:"article"`
	InvoiceWording string `yaml:"invoice_wording"`
}

type Filing struct {
	Due                string `yaml:"due"`
	Authority          string `yaml:"authority"`
	Cadence            string `yaml:"cadence,omitempty"`
	RequiredAtZeroRate bool   `yaml:"required_at_zero_rate,omitempty"`
}

type DLAPolicy struct {
	S455Charge bool            `yaml:"s455_charge"`
	Overdrawn  OverdrawnPolicy `yaml:"overdrawn"`
}

type OverdrawnPolicy struct {
	Warn   string `yaml:"warn"`
	Remedy string `yaml:"remedy"`
}

type AdvisorRule struct {
	ID           string `yaml:"id"`
	Severity     string `yaml:"severity"`
	FactQuery    string `yaml:"fact_query"`
	Condition    string `yaml:"condition"`
	TextTemplate string `yaml:"text_template"`
	CTA          string `yaml:"cta"`
}
