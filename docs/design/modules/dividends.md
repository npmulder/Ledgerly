# Module: dividends

**Package:** `internal/dividends` · **Schema:** `dividends` · **Depends on:** ledger (retained earnings + postings), reports (YTD profit), jurisdiction (no-WHT, PersonalTaxEstimate, CIT 0%), identity (company/shareholder facts for docs)

## Responsibility

Dividend headroom calculation, declaration, and document generation (voucher + board minutes). Screen: 07 Dividends.

## Headroom calculation (live, screen top)

```
retained earnings b/fwd            (ledger: retained earnings account)
+ YTD profit                       (reports.ProfitYTD — GBP presentational)
− corporation tax provision        (jurisdiction.CorporateRate = 0% → £0, line still shown)
− dividends declared this year     (this module)
= available headroom               (navy total rule)
```

Computed on demand — no stored headroom figure to go stale.

## Declaration flow

1. User enters amount → validation strip renders: within headroom ✓ · no withholding tax (jurisdiction) ✓ · personal tax estimate ("set aside personally £X", from `PersonalTaxEstimate` over YTD dividends + this one).
2. "Generate voucher + minutes" → create **Declaration** `{id, date, amountPerShare, total, shareholder}` → post Dr Retained earnings / Cr DLA (credits the director's loan — how "clear with dividend" works) → render documents → publish `dividends.Declared`.
3. Over-headroom amounts are rejected (illegal distribution), with the distributable-reserves figure shown.

## Documents (right rail, screen 07)

**Dividend voucher** and **board minutes** — React print routes → chromedp PDF, stored immutably like invoice PDFs. Content: company name + number, shareholder (N. Meyer, 100 ordinary £1 shares — from identity), per-share amount, **distributable-reserves recital**, signature lines. Company name/logo propagate from identity.

## Public API (Go)

```go
type Dividends interface {
    Headroom() (HeadroomBreakdown, error)        // advisor fact + screen calc
    Declare(amount Money) (Declaration, error)
    History() ([]Declaration, error)
    DeclaredInYear(taxYear) (Money, error)
}
```

## Events

Publishes: `dividends.Declared` (advisor: refresh headroom insight; dla shows the credit). Consumes: none.

## Data (schema `dividends`)

`declarations`, `rendered_docs`.

## Work items

1. Headroom query composed from ledger/reports/jurisdiction + unit tests per line
2. Declare command (validation, posting, event — transactional)
3. Voucher + minutes print routes + rendering + storage
4. Screen: calc panel, amount input, validation strip, history table, document previews
5. "Clear with dividend" entry point (prefilled amount from DLA overdrawn CTA)
