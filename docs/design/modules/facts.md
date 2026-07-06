# Advisor Fact Keys

This table is the contract between advisor fact providers and jurisdiction rule
packs. Provider rows name facts emitted by `internal/advisor` adapters. Current
flat rule-binding rows document compatibility facts emitted for the active Isle
of Man pack fact queries that pre-date collection expansion.

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
| `filings` | `[]advisor.FilingFact` (`key`, `label`, `authority`, `dueDate`, `daysUntil`, `status`, `warnWindow`) | reports | Source collection for `filing_deadline_window` |
| `rates.lastDate` | `date?` | moneyfx | Source fact for `rates_stale` |
| `rates.stale` | `bool` | moneyfx | Source fact for `rates_stale` |
| `company.incorporationDate` | `date` | identity | Filing deadline and company templates |
| `company.yearEnd` | `advisor.CompanyYearEndFact` | identity | Filing deadline and company templates |
| `company.yearEnd.month` | `int` | identity | Filing deadline and company templates |
| `company.yearEnd.day` | `int` | identity | Filing deadline and company templates |
| `client_name` | `string` | invoicing compatibility binding from `invoices.overdue[].client` | `overdue_invoice` |
| `count` | `int` | invoicing compatibility binding from `invoices.overdue` length | `overdue_invoice` |
| `days_overdue` | `int` | invoicing compatibility binding from `invoices.overdue[].daysOverdue` | `overdue_invoice` |
| `invoice_id` | `string` | invoicing compatibility binding from `invoices.overdue[].id` | `overdue_invoice` |
| `invoice_number` | `string` | invoicing compatibility binding from `invoices.overdue[].number` | `overdue_invoice` |
| `balance` | `money.Money` | dla compatibility binding from `dla.balance` | `dla_overdrawn_bik` |
| `status` | `string` | dla compatibility binding from `dla.status` | `dla_overdrawn_bik` |
| `clearance_amount` | `money.Money` | dla compatibility binding from `dla.suggestedClearance` | `dla_overdrawn_bik` |
| `clearance_amount_minor_units` | `int64` | dla compatibility binding from `dla.suggestedClearance` | `dla_overdrawn_bik` |
| `authority` | `string` | reports compatibility binding from `filings[].authority` | `filing_deadline_window` |
| `due_date` | `date` | reports compatibility binding from `filings[].dueDate` | `filing_deadline_window` |
| `filing_name` | `string` | reports compatibility binding from `filings[].label` | `filing_deadline_window` |
| `dividend_headroom` | `money.Money` | dividends compatibility binding from `dividends.headroom` | `dividend_set_aside` |
| `headroom_minor_units` | `int64` | dividends compatibility binding from `dividends.headroom` | `dividend_set_aside` |
| `dividends_ytd` | `money.Money` | dividends compatibility binding from declared dividends YTD | `dividend_set_aside` |
| `estimate` | `money.Money` | dividends compatibility binding from personal-tax estimate | `dividend_set_aside` |
| `estimate_minor_units` | `int64` | dividends compatibility binding from personal-tax estimate | `dividend_set_aside` |
| `stale_days` | `int` | moneyfx compatibility binding from rate staleness | `rates_stale` |
