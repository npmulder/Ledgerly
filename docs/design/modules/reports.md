# Module: reports

**Package:** `internal/reports` · **Schema:** `reports` (none/minimal — mostly derived reads) · **Depends on:** ledger (read API), jurisdiction (VAT rate, CIT 0%, FilingDeadlines), identity (company facts), invoicing (per-client income attribution), banking (expense payee/reference attribution)

## Responsibility

P&L (presentational currency GBP), expenses-by-category drill-down, VAT return figures, filing calendar, export pack. Screen: 08 Reports.

## P&L

Period-selectable (handoff shows Apr–Jun). Built from `ledger.BalancesByType` GBP amounts (frozen at posting-date rates — no retranslation):

income per client/currency (attribution via invoicing read API joined on ledger source refs) · expenses by category · **realised FX gains** line (FX gain/loss account) · **"IoM income tax at 0%"** line — rate from jurisdiction, always displayed, always £0 in v1.

## VAT return

UK-regime boxes relevant to the mock: Box 1 (output VAT), Box 4 (input VAT), Box 6 (net sales); net position (reclaim in mock), due badge from filing calendar. Reverse-charge sales contribute £0 to Box 1 but appear in Box 6. Figures derive from VAT control account postings + invoice VAT treatments. v1 computes figures for manual filing with IoM Customs & Excise — no e-filing integration.

**v1 input-VAT limitation:** Box 4 is not derived from expense recodes. Until receipts capture VAT portions, input VAT is entered as a manual quarterly adjustment entry posted to the ledger VAT control account with `source_module='reports'` and a `source_ref` prefixed `manual-input-vat:`; this entry is the only v1 source for reclaimable input VAT in the VAT return figures.

## Expense drill-down

Period-selectable with the same inclusive posting-date range as P&L. Category totals derive from ledger postings against expense accounts, grouped by chart account and presented in GBP. The transaction drill-down keeps the ledger posting as the source of truth for amount/category, then resolves payee and reference through the banking read API when the ledger source ref points at a bank recode (`banking:<txnID>:recode`). Non-banking expense postings remain visible with ledger-entry fallback attribution rather than being dropped.

The Reports screen shows category totals, top payees, and per-category transaction rows. The accountant CSV export contains `date,payee,reference,amount,currency,category` for the selected period.

## Filing calendar

`jurisdiction.FilingDeadlines(companyFacts)` resolved against identity data (incorporation date, year end 31 Mar): VAT return (quarterly) · annual return (incorporation anniversary + 1 month, IoM Companies Registry) · company income tax return (year end + 12 months + 1 day — required even at 0%) · personal tax return. Each with due-date badge; amber advisor insights as deadlines approach. v1 is informational only: no filed/completed tracking is stored by reports.

## Export pack

"Export pack" + "Share with accountant": zip of P&L (CSV + PDF), expense transactions CSV, VAT figures, journal export (ledger.Entries), invoice PDFs, DLA ledger CSV, dividend documents. Share = email with attachment v1.

## Public API (Go)

```go
type Reports interface {
    ProfitAndLoss(period) (PL, error)
    ExpensesByCategory(period) (ExpensesReport, error)
    ProfitYTD(taxYear) (Money, error)      // dividends headroom input
    VATReturn(period) (VATFigures, error)  // advisor fact
    FilingCalendar() ([]Filing, error)     // advisor fact
    ExportPack(period) (ArchiveRef, error)
}
```

## Events

Consumes `ledger.EntryPosted` (invalidate cached aggregates, if caching is ever needed — start uncached). Publishes: none.

## Work items

1. P&L query + per-client income attribution
2. VAT figures derivation + tests incl. reverse-charge cases
3. Filing calendar (jurisdiction + identity composition)
4. Screen: P&L card, VAT card with due badge, calendar
5. Export pack assembly + share-by-email
