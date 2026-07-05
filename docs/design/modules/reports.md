# Module: reports

**Package:** `internal/reports` · **Schema:** `reports` (none/minimal — mostly derived reads) · **Depends on:** ledger (read API), jurisdiction (VAT rate, CIT 0%, FilingDeadlines), identity (company facts), invoicing (per-client income attribution)

## Responsibility

P&L (presentational currency GBP), VAT return figures, filing calendar, export pack. Screen: 08 Reports.

## P&L

Period-selectable (handoff shows Apr–Jun). Built from `ledger.BalancesByType` GBP amounts (frozen at posting-date rates — no retranslation):

income per client/currency (attribution via invoicing read API joined on ledger source refs) · expenses by category · **realised FX gains** line (FX gain/loss account) · **"IoM income tax at 0%"** line — rate from jurisdiction, always displayed, always £0 in v1.

## VAT return

UK-regime boxes relevant to the mock: Box 1 (output VAT), Box 4 (input VAT), Box 6 (net sales); net position (reclaim in mock), due badge from filing calendar. Reverse-charge sales contribute £0 to Box 1 but appear in Box 6. Figures derive from VAT control account postings + invoice VAT treatments. v1 computes figures for manual filing with IoM Customs & Excise — no e-filing integration.

## Filing calendar

`jurisdiction.FilingDeadlines(companyFacts)` resolved against identity data (incorporation date, year end 31 Mar): VAT return (quarterly) · annual return (incorporation anniversary + 1 month, IoM Companies Registry) · company income tax return (year end + 12 months + 1 day — required even at 0%) · personal tax return. Each with due-date badge; amber advisor insights as deadlines approach.

## Export pack

"Export pack" + "Share with accountant": zip of P&L (CSV + PDF), VAT figures, journal export (ledger.Entries), invoice PDFs, DLA ledger CSV, dividend documents. Share = email with attachment v1.

## Public API (Go)

```go
type Reports interface {
    ProfitAndLoss(period) (PL, error)
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
