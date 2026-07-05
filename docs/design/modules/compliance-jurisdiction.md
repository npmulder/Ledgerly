# Module: compliance/jurisdiction

**Package:** `internal/jurisdiction` · **Schema:** `jurisdiction` (dismissals/active-pack state only) · **Depends on:** nothing (leaf)

## Responsibility

Pluggable, versioned **rules pack** per jurisdiction. All tax rates, filing deadlines, and advisor rule text live here — **never hard-coded in other modules**. v1 ships exactly one pack: `isle-of-man@1.0`. Adding a jurisdiction later = adding a pack directory.

## Pack format

Versioned YAML under `packs/<jurisdiction>/<version>/`, embedded via `go:embed`, validated at startup (fail fast on malformed pack). Structure:

```yaml
meta: { id: isle-of-man, version: "1.0", name: Isle of Man, currency: GBP }
tax:
  year_end: { month: 4, day: 5 }                 # pack-owned tax_year_end anchor
  corporate_income:
    "2025-26": { standard_rate: "0.0" }        # 0% — no CT provision anywhere
  personal_income:                              # for dividend set-aside estimate
    "2025-26": { personal_allowance_minor_units: 1475000,
                 bands: [{upto_minor_units: 650000, rate: "0.10"}, {rate: "0.21"}] }
  dividends:
    "2025-26": { withholding: none }
  vat:
    regime: uk-shared                           # 20%, filed with IoM Customs & Excise
    authority: Isle of Man Customs & Excise
    "2025-26": { standard_rate: "0.20" }
    reverse_charge:
      b2b_services_eu: { article: "Article 196, Directive 2006/112/EC",
                         invoice_wording: "VAT reverse charge applies: VAT to be accounted for by the recipient under Article 196, Council Directive 2006/112/EC. Supplier is established in the Isle of Man." }
filings:
  annual_return:      { due: incorporation_anniversary + 1 month, authority: IoM Companies Registry }
  company_tax_return: { due: accounting_year_end + 12 months + 1 day, required_at_zero_rate: true }
  vat_return:         { due: quarter_end + 1 month, cadence: quarterly, authority: IoM Customs & Excise }
director_loans:
  s455_charge: false                            # no UK s455
  overdrawn:  { warn: benefit_in_kind_interest_free, remedy: clear_with_dividend }
advisor_rules: [ ... ]                          # rule defs consumed by advisor module
```

Rates/allowances are **year-versioned data** keyed by tax year, not constants. Money amounts use integer GBP minor units (pence); rates use quoted decimal strings so pack data is not decoded through binary floating point. Deadline expressions are a tiny declarative grammar (`anchor + offset`) evaluated against company facts (incorporation date, year end) supplied by the caller. The `tax_year_end` anchor is resolved from the pack's `tax.year_end` data.

Deadline month arithmetic is deliberately calendar-based and applied left-to-right. Month offsets clamp overflow days to the destination month end (`31 Jan + 1 month` becomes `28 Feb` or `29 Feb` in a leap year). Leap-day anniversaries clamp to `28 Feb` in non-leap years and remain `29 Feb` in leap years. Compound offsets apply in order, so `accounting_year_end + 12 months + 1 day` first resolves the 12-month calendar shift, then adds one day.

## Public API (Go)

```go
type Jurisdiction interface {
    ActivePack() PackMeta                                   // isle-of-man@1.0
    CorporateRate(taxYear) (Rate, error)                    // 0.0
    PersonalIncomeTax(taxYear) (PersonalIncomeYear, error)   // JUR-4 estimator input; no math here
    DividendWithholding(taxYear) (string, error)
    VATStandardRate(taxYear) (Rate, error)
    ReverseChargeWording(kind) (Wording, error)             // article ref + invoice text
    FilingRules() map[string]Filing                         // JUR-3 resolves concrete dates
    DirectorLoanPolicy() DLAPolicy                          // s455=false, BIK warning, remedy
    AdvisorRules() []RuleDef                                // for advisor engine
}
```

Pure functions over pack data + caller-supplied facts; this module never reaches into other modules.

## Consumers

invoicing (reverse-charge wording, VAT rate), dividends (no-WHT + personal tax estimate), dla (loan policy), reports (0% CIT line, VAT rate, filing calendar), advisor (rule defs), settings screen (pack card: version + 6 rule summaries).

## Guard rails

CI check greps feature modules for literal rates/allowances (`0.20`, `6500`, `14750`, `0.10`, `0.21`) — build fails if compliance data leaks out of the pack.

## Work items

1. Pack schema + loader + startup validation
2. `isle-of-man@1.0` pack content (encode all 7 handoff rules)
3. Deadline expression grammar + resolver + tests (anniversary edge cases, leap years)
4. Personal tax banded estimator + tests against hand-computed cases
5. CI literal-rate guard
