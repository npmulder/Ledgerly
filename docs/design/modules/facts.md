# Advisor Fact Keys

This table is the contract between advisor fact providers and jurisdiction rule
packs. Provider rows name facts emitted by `internal/advisor` adapters. Current
flat rule-binding rows document the active Isle of Man pack fact queries that
pre-date collection expansion.

| Key | Type | Provider | Consuming rules |
| --- | --- | --- | --- |
| `invoices.overdue` | `[]advisor.OverdueInvoiceFact` | invoicing | Source collection for `overdue_invoice` |
| `dla.balance` | `money.Money` | dla | Source fact for `dla_overdrawn_bik` |
| `dla.status` | `string` (`credit`, `overdrawn`) | dla | Source fact for `dla_overdrawn_bik` |
| `dla.suggestedClearance` | `money.Money` | dla | DLA clearance templates and CTAs |
| `dividends.headroom` | `money.Money` | dividends | Source fact for `dividend_set_aside` |
| `dividends.distributable` | `bool` | dividends | Dividend availability rules |
| `vat.position` | `reports.VATPosition` | reports | VAT return advisor rules |
| `vat.dueDate` | `date` | reports | VAT filing deadline templates |
| `filings` | `[]advisor.FilingFact` | reports | Source collection for `filing_deadline_window` |
| `rates.lastDate` | `date?` | moneyfx | Source fact for `rates_stale` |
| `rates.stale` | `bool` | moneyfx | Source fact for `rates_stale` |
| `company.incorporationDate` | `date` | identity | Filing deadline and company templates |
| `company.yearEnd` | `advisor.CompanyYearEndFact` | identity | Filing deadline and company templates |
| `company.yearEnd.month` | `int` | identity | Filing deadline and company templates |
| `company.yearEnd.day` | `int` | identity | Filing deadline and company templates |
| `client_name` | `string` | current flat binding from `invoices.overdue[].client` | `overdue_invoice` |
| `count` | `int` | current flat binding from `invoices.overdue` length | `overdue_invoice` |
| `days_overdue` | `int` | current flat binding from `invoices.overdue[].daysOverdue` | `overdue_invoice` |
| `invoice_id` | `string` | current flat binding from `invoices.overdue[].id` | `overdue_invoice` |
| `invoice_number` | `string` | current flat binding from `invoices.overdue[].number` | `overdue_invoice` |
| `balance` | `money.Money` | current flat binding from `dla.balance` | `dla_overdrawn_bik` |
| `status` | `string` | current flat binding from `dla.status` | `dla_overdrawn_bik` |
| `authority` | `string` | current flat binding from `filings[].authority` | `filing_deadline_window` |
| `due_date` | `date` | current flat binding from `filings[].dueDate` | `filing_deadline_window` |
| `filing_name` | `string` | current flat binding from `filings[].label` | `filing_deadline_window` |
| `dividends_ytd` | `money.Money` | current flat binding from dividend personal-tax estimate | `dividend_set_aside` |
| `estimate` | `money.Money` | current flat binding from dividend personal-tax estimate | `dividend_set_aside` |
| `estimate_minor_units` | `int64` | current flat binding from dividend personal-tax estimate | `dividend_set_aside` |
| `stale_days` | `int` | current flat binding from `rates.lastDate` | `rates_stale` |
