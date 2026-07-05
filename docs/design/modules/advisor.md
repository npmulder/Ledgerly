# Module: advisor

**Package:** `internal/advisor` · **Schema:** `advisor` · **Depends on:** jurisdiction + public **read** APIs of invoicing, banking, dla, dividends, reports, moneyfx, identity

## Responsibility

Rule engine that evaluates facts from other modules against the active rules pack and emits **insights**: `{id, severity, factBindings, cta}`. Rendered as the navy panel on the dashboard (4 insights) and single contextual strips on other screens. The only module allowed to fan-in reads across all modules — via their APIs, never their tables.

## Model

- **RuleDef** (from jurisdiction pack): `{id, severity, factQuery, condition, textTemplate, cta}`. Severity: `teal` = opportunity, `amber` = deadline/warning.
- **Fact providers**: each feature module exposes a narrow read API the advisor maps into named facts — e.g. `invoicing.OverdueInvoices()`, `dla.CurrentBalance()`, `dividends.Headroom()`, `reports.VATPosition()`, `identity.CompanyFacts()`, `moneyfx` staleness.
- **Insight**: derived, persisted with a deterministic key (`ruleID + factHash`) so re-evaluation is idempotent; **dismissible** — dismissals stored, insight stays suppressed until its facts change (new factHash).

Example v1 rules (from handoff): overdue invoice → amber + "Send reminder" CTA (invoices screen strip); DLA overdrawn → amber BIK warning + "Clear with dividend" CTA; filing deadline approaching (annual return / company tax return / VAT / personal return) → amber with due badge; dividend headroom available → teal "set aside personally £X for IoM income tax" (uses `PersonalTaxEstimate`); ECB rates stale → amber.

## Evaluation

Triggered by: daily cron; relevant domain events (`invoicing.InvoiceOverdue`, `dla.WentOverdrawn`, `ledger.EntryPosted`, `moneyfx.RatesStale`); manual refresh. Evaluation is read-only + upsert of insights — cheap enough to run whole-set every time (few dozen rules, one company).

## Public API (Go)

```go
type Advisor interface {
    InsightsFor(ctx, surface Surface) ([]Insight, error) // dashboard | invoices | dla | ...
    Dismiss(ctx, id InsightID) error
}
```

CTAs are declarative `{label, action}` where action is a frontend route/command (e.g. `invoicing.sendReminder(invoiceID)`), keeping advisor free of side effects — it recommends, other modules act.

## Events

Consumes: any (see triggers). Publishes: none.

## Data (schema `advisor`)

`insights` (rule id, severity, bindings jsonb, fact hash, surface), `dismissals`.

## Work items

1. RuleDef evaluation engine + condition grammar (small: comparisons over named facts)
2. Fact-provider adapters per module (thin, typed)
3. Idempotent upsert + dismissal semantics
4. Surface-scoped query API + HTTP endpoints
5. Wire the 5+ v1 rules end-to-end; snapshot tests of rendered insight text against pack templates
