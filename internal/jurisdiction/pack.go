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
	Meta          PackMeta                    `yaml:"meta"`
	Tax           Tax                         `yaml:"tax"`
	Filings       map[string]Filing           `yaml:"filings"`
	DirectorLoans map[string]DirectorLoanYear `yaml:"director_loans"`
	AdvisorRules  []AdvisorRule               `yaml:"advisor_rules"`
}

// Tax contains tax-year keyed rules. Rate and allowance values always live
// below a tax-year string such as "2025-26".
type Tax struct {
	CorporateIncome map[string]CorporateIncomeYear `yaml:"corporate_income"`
	PersonalIncome  map[string]PersonalIncomeYear  `yaml:"personal_income"`
	Dividends       map[string]DividendYear        `yaml:"dividends"`
	VAT             VAT                            `yaml:"vat"`
}

type CorporateIncomeYear struct {
	StandardRate float64 `yaml:"standard_rate"`
}

type PersonalIncomeYear struct {
	PersonalAllowance int64     `yaml:"personal_allowance"`
	Bands             []TaxBand `yaml:"bands"`
}

type TaxBand struct {
	UpTo *int64  `yaml:"upto,omitempty"`
	Rate float64 `yaml:"rate"`
}

type DividendYear struct {
	WithholdingRate float64 `yaml:"withholding_rate"`
}

type VAT struct {
	Regime        string                          `yaml:"regime"`
	Years         map[string]VATYear              `yaml:"-"`
	ReverseCharge map[string]ReverseChargeWording `yaml:"reverse_charge"`
}

type VATYear struct {
	StandardRate float64 `yaml:"standard_rate"`
}

type ReverseChargeWording struct {
	Article        string `yaml:"article"`
	InvoiceWording string `yaml:"invoice_wording"`
}

type Filing struct {
	Due                string `yaml:"due"`
	Authority          string `yaml:"authority"`
	Cadence            string `yaml:"cadence,omitempty"`
	RequiredAtZeroRate bool   `yaml:"required_at_zero_rate,omitempty"`

	dueExpression *deadlineExpression `yaml:"-"`
}

type DirectorLoanYear struct {
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
