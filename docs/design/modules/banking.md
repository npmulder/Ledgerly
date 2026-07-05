# Module: banking

**Package:** `internal/banking` · **Schema:** `banking` · **Depends on:** ledger (postings), moneyfx (ToGBP), invoicing (match candidates + MarkSettled), dla (FileDrawing)

## Responsibility

Statement CSV import (Revolut GBP + EUR to start), transaction feed, match-suggestion engine (invoice matching, recurring-payee rules, DLA detection), reconciliation state. Screen: 05 Banking.

## Domain model

- **BankAccount** — `{id, name, provider: revolut, currency, ledgerAccountCode}` (asks ledger to `EnsureAccount` a cash account on creation).
- **Transaction** — `{id, accountID, date, amount Money, payee, reference, importBatch, state: unreconciled | suggested | reconciled | excluded, dedupeHash}`.
- **Suggestion** — `{txnID, kind: invoice-match | dla | payee-rule, confidence, target, explanation}`.
- **PayeeRule** — `{matcher (payee pattern), category/account, timesApplied}` — learned from user recodes ("applied 11 times" card).

## CSV import

Per-provider parser interface (`RevolutCSV` first; provider column mapping isolated so new banks = new parser). Dedupe via hash(account, date, amount, reference) — re-importing an overlapping export is safe. Import batch recorded for undo.

## Match engine

On import (and on new invoice events), for each unreconciled txn produce best suggestion:

1. **Invoice match** — score on amount (exact native-currency match strongest), currency, payee ≈ client name, date ≥ issue date. Shown as "98% match" card; confidence = weighted score.
2. **DLA detection** — payee ≈ director name / known personal patterns → suggest drawing ("File to DLA" + "Recode ▾").
3. **Payee rule** — matcher hit → auto-categorize; rules with high `timesApplied` can auto-post (threshold configurable).

## Reconciliation flows (one transaction each)

- **Confirm invoice match**: post Dr Cash / Cr Trade debtors → `invoicing.MarkSettled(...)` → (moneyfx auto-posts realised FX, surfaced back on the match card: "auto-posted FX gain") → txn state `reconciled`.
- **File to DLA**: `dla.FileDrawing(txn)` → dla posts its ledger entry → state `reconciled`.
- **Accept payee rule / recode**: post to chosen expense account; recode updates/creates the rule (learning).

## Public API (Go)

```go
type Banking interface {
    ImportCSV(accountID, file) (BatchSummary, error)
    Feed(filter) · ReviewQueue() · RecentlyReconciled()
    ConfirmMatch(txnID) · FileToDLA(txnID) · Recode(txnID, accountCode) · Exclude(txnID)
    Accounts() · UnreconciledCount(accountID) // account-card badges, advisor fact
}
```

## Events

Publishes: `banking.TransactionsImported`, `banking.TransactionReconciled`. Consumes: `invoicing.InvoiceSent` (refresh match candidates).

## Data (schema `banking`)

`bank_accounts`, `transactions`, `suggestions`, `payee_rules`, `import_batches`.

## UI notes (screen 05)

Account cards (GBP selected, EUR shows review count), CSV import CTA, review queue of three card kinds (match / suggestion / rule), right rail recently-reconciled + empty state "All caught up…" (pattern reused by invoices/DLA).

## Work items

1. Accounts + Revolut CSV parser + dedupe + import batches
2. Transaction feed + states + endpoints
3. Match engine: invoice scorer, DLA detector, payee rules
4. Reconciliation commands (transactional, incl. settle handoff to invoicing)
5. Review-queue screen + cards + empty state
6. Rule learning from recodes
