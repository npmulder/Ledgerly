# Module: dla

**Package:** `internal/dla` · **Schema:** `dla` · **Depends on:** ledger (postings), jurisdiction (DirectorLoanPolicy), moneyfx (ToGBP)

## Responsibility

Director's loan account: running ledger of drawings, repayments, and personally-paid expenses; credit/overdrawn status. Screen: 06 DLA.

## Domain model

**DLAEntry** — `{id, date, kind: drawing | repayment | expense-owed, description, amount Money, source (banking txn ref | manual), runningBalance}`. Balance convention: **CR (in credit) = company owes director** (tax-free to withdraw); **DR (overdrawn) = director owes company**. Table shows owed-to-you / drawn / balance columns in mono numerals; banner shows current balance (teal, e.g. `£2,150.00 CR`).

Running balance is derived (window over entries) — stored balance would be a second source of truth; the ledger DLA account is authoritative and the nightly check compares them.

## Key flows

- **Drawing filed from banking** (`FileDrawing`): append entry, post Dr DLA / Cr Cash.
- **Personally-paid expense** (manual entry): append `expense-owed`, post Dr Expense / Cr DLA.
- **Repayment**: append, post Dr Cash / Cr DLA.
- **Overdrawn transition**: when balance crosses into DR, publish `dla.WentOverdrawn`. Policy from jurisdiction (`s455_charge: false` — no UK s455): advisor shows amber **benefit-in-kind warning** on an interest-free loan + **"Clear with dividend"** CTA (routes to dividends screen with amount prefilled). Right-rail edge state per handoff.

## Public API (Go)

```go
type DLA interface {
    FileDrawing(ctx, tx, src BankTxnRef) error      // called by banking
    AddEntry(ctx, e NewEntry) error                  // manual expense/repayment
    Ledger(filter) ([]DLAEntry, error)               // running-balance table
    CurrentBalance() (Money, Status, error)          // Status: credit | overdrawn — advisor fact
}
```

## Events

Publishes: `dla.WentOverdrawn`, `dla.BackInCredit`. Consumes: none (banking calls API in-transaction).

## Data (schema `dla`)

`dla_entries`.

## Work items

1. Entries + running-balance query + ledger postings per kind
2. Banking integration (`FileDrawing`)
3. Status + transition events
4. Screen: table, balance banner, status card, overdrawn edge state with CTA
5. Nightly consistency check vs ledger DLA account
