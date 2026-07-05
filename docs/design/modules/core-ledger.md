# Module: core/ledger

**Package:** `internal/ledger` · **Schema:** `ledger` · **Depends on:** nothing (leaf)

## Responsibility

Double-entry bookkeeping core: chart of accounts, journal, postings. Every other module records financial facts here through this module's API; **nothing else writes ledger rows**. Provides balance/aggregate read APIs for reports, dashboard, and dividends headroom.

## Domain model

- **Account** — `{id, code, name, type: asset|liability|equity|income|expense, currency?}`. Seeded chart of accounts (cash per bank account, trade debtors per currency, sales, VAT control, DLA, retained earnings, FX gain/loss, expense categories). Feature modules request account creation via API (e.g. banking creates a cash account per imported bank account).
- **JournalEntry** — `{id, date, description, sourceModule, sourceRef, postings[]}`. Immutable once written; corrections are reversing entries, never updates/deletes.
- **Posting** — `{account, amount Money (native), amountGBP Money}`. GBP amount frozen at posting-date ECB rate (caller supplies both — moneyfx converts; ledger stores, never computes FX).

**Invariants:** per entry, Σ amountGBP = 0 (double entry balances in presentational currency); native amounts balance per-currency within an entry. Enforced at API level and re-checked by trial-balance job.

## Public API (Go)

```go
type Ledger interface {
    Post(ctx, tx, entry NewJournalEntry) (EntryID, error)   // validates balance, appends
    Reverse(ctx, tx, id EntryID, reason string) (EntryID, error)
    AccountBalance(ctx, code string, asOf date) (Money, MoneyGBP, error)
    BalancesByType(ctx, from, to date) ([]AccountBalance, error) // P&L / BS aggregates
    Entries(ctx, filter EntryFilter) ([]JournalEntry, error)     // journal browse/export
    EnsureAccount(ctx, tx, spec AccountSpec) (AccountCode, error)
}
```

`tx` is the platform transaction handle — callers post in the same transaction as their own state change (invoice settled + FX entry + reconciliation commit atomically).

## Events

Publishes `ledger.EntryPosted{entryID, sourceModule, accounts, date}` (advisor and reports listen for cache invalidation). Consumes nothing.

## Data (schema `ledger`)

`accounts`, `journal_entries`, `postings`. Append-only on entries/postings (no UPDATE/DELETE grants for the app role beyond status-free inserts).

## Key flows

- **Invoice issued** → invoicing posts: Dr Trade debtors (EUR native + GBP) / Cr Sales.
- **Settlement** → banking posts: Dr Cash / Cr Trade debtors; moneyfx posts FX gain/loss delta.
- **Trial balance check**: after every `Post` (cheap, per-entry) and nightly full sweep; failure logs at critical.

## Work items

1. Schema + migrations + seeded chart of accounts
2. `Post`/`Reverse` with balance validation + property-based tests (random entry sets always balance)
3. Balance/aggregate queries with date ranges
4. Trial-balance job + critical alerting
5. Journal browse endpoint (feeds export pack later)
