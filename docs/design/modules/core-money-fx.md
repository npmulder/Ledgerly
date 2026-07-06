# Module: core/money-fx

**Package:** `internal/moneyfx` · **Schema:** `moneyfx` · **Depends on:** ledger (posts realised FX)

## Responsibility

The `Money` value type and all FX concerns: ECB daily rate ingestion, rate-locking (rate frozen at invoice issue), conversion for presentational-GBP postings, and realised FX gain/loss computation on settlement. If code multiplies money by a rate, it lives here.

## Money type (shared value type)

```go
type Money struct { Amount int64; Currency string } // minor units; no floats ever
```

Exposed from `moneyfx/money` as a leaf sub-package importable by all modules (pure value type, no I/O — doesn't violate boundary rules). Operations: Add/Sub (same-currency enforced), MulRat (big.Rat internally, banker's rounding documented), Allocate (largest-remainder — pennies never leak), Format (per-currency symbols/decimals for UI + PDF).

## Rates

- **Ingestion**: daily cron fetches ECB reference rates (XML feed), stores `{date, base EUR, currency, rate}` rows. Retry with backoff; if today's fetch fails, latest available rate is used and a `moneyfx.RatesStale` event fires (advisor turns it into an amber insight).
- **Lookup**: `RateOn(date, from, to)` — cross-rates via EUR base. GBP/EUR pair is all v1 needs but the API is general. If the requested date has no ECB rate, lookup walks back to the most recent prior ECB rate date for up to seven calendar days; beyond that it returns `ErrNoRate` so callers do not silently use stale FX.
- Dashboard rate card ("frozen onto today's postings") reads `TodayRate("EUR","GBP")` + fetch timestamp.
- **Lookup performance**: rates are queried directly by indexed `{date, currency}` lookups. Per-request in-process caching is unnecessary for the small read surface and would add invalidation complexity without measurable benefit.

## Rate locking

```go
Lock(ctx, tx, ref LockRef, from, to string, date) (RateLock, error)
// RateLock{id, rate decimal-string, source: "ECB", lockedAt, ref}
```

Invoicing calls `Lock` at issue; the lock row is immutable and the invoice stores `lock_id`. Editor shows the rate read-only with source ECB + 🔒. Re-issuing (draft→sent again after unsend) creates a new lock.

## Realised FX on settlement

On `invoicing.InvoiceSettled{invoiceID, lockID, settledAmount, settlementDate}`:
gain/loss (GBP) = native amount × (rate(settlementDate) − lockedRate). Posts Dr/Cr FX gain/loss vs trade debtors in the same transaction, and publishes `moneyfx.RealisedFX{invoiceID, amountGBP}` — banking shows it on the match card ("auto-posted FX gain"), reports includes it as the P&L "Realised FX gains" line.

## Public API (Go)

```go
type MoneyFX interface {
    RateOn(ctx, date, from, to) (Rate, error)
    TodayRate(ctx, from, to) (Rate, time.Time, error)
    Lock(ctx, tx, ref, from, to, date) (RateLock, error)
    GetLock(ctx, id LockID) (RateLock, error)
    ToGBP(ctx, m Money, date) (Money, error)   // for callers building ledger postings
}
```

## Events

Publishes: `moneyfx.RealisedFX`, `moneyfx.RatesStale`. Consumes: `invoicing.InvoiceSettled`.

## Data (schema `moneyfx`)

`ecb_rates` (date, currency, rate — decimal as numeric), `rate_locks` (immutable).

## Work items

1. Money type + arithmetic + allocation, exhaustive unit/property tests (this is the most-tested code in the repo)
2. ECB fetch cron + storage + staleness event
3. Rate lookup incl. cross-rates
4. Rate locking API
5. Realised FX handler (subscribe settle event → compute → post → publish)
