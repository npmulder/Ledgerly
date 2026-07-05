# Ledgerly — High-Level Design

**Status:** Draft v1 · 2026-07-03
**Source:** `docs/design_handoff_keel/README.md` (design handoff, 9 screens)
**Stack decision:** Go modular monolith backend + React/TypeScript frontend + PostgreSQL

---

## 1. What we're building

Ledgerly is a bookkeeping/invoicing system for a single-director limited company that invoices in multiple currencies (EUR + GBP) and needs jurisdiction-aware compliance help. First (only) jurisdiction: **Isle of Man** — not UK; rules differ materially and live in a versioned rules pack, never hard-coded.

### Functional requirements (from handoff)

Double-entry ledger underneath everything; multi-currency invoicing with FX rate locked at issue (ECB daily) and realised gain/loss on settlement; bank CSV import (Revolut GBP + EUR) with match suggestions and reconciliation; director's loan account running ledger with overdrawn detection; dividend headroom calculation + voucher/board-minutes generation; P&L (GBP presentational), VAT return figures, filing calendar, export pack; rule-engine "advisor" insights; configurable company identity (name/logo propagate to header, PDFs, vouchers, minutes).

### Non-functional requirements

- **Correctness over scale.** One company, one or two users. Tens of invoices/month, hundreds of bank transactions/month. No horizontal scaling concerns for years.
- **Auditability.** Ledger is append-only; every balance is derivable from postings.
- **Single deployable** (mandated modular monolith), simple ops.
- **Jurisdiction extensibility.** Adding a country = adding a rules pack, no code changes in other modules.

### Constraints

Solo/small-team development; no existing codebase; high-fidelity designs are final intent (recreate faithfully); architecture MUST be a modular monolith with strict boundaries — no shared table access across modules.

---

## 2. Architecture overview

```
┌────────────────────────────────────────────────────────────────┐
│  React SPA (Vite + TS)                                         │
│  Screens 01–09 · design tokens · TanStack Query                │
└───────────────▲────────────────────────────────────────────────┘
                │ REST/JSON  (OpenAPI-generated TS client)
┌───────────────┴────────────────────────────────────────────────┐
│  Go binary (single deployable)                                 │
│                                                                │
│  HTTP layer (chi router) → thin handlers per module            │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐           │
│  │invoicing │ │ banking  │ │   dla    │ │dividends │  feature  │
│  └────┬─────┘ └────┬─────┘ └────┬─────┘ └────┬─────┘  modules  │
│       │            │            │            │                 │
│  ┌────▼────────────▼────────────▼────────────▼─────┐           │
│  │ core/ledger    core/moneyfx                     │  core     │
│  └──────────────────────────────────────────────────┘          │
│  ┌──────────┐ ┌────────────────────┐ ┌──────────┐              │
│  │ reports  │ │compliance/jurisdictn│ │ advisor  │  derived    │
│  └──────────┘ └────────────────────┘ └──────────┘              │
│  ┌──────────────────┐  ┌─────────────────────────┐             │
│  │ settings/identity│  │ in-process event bus    │             │
│  └──────────────────┘  └─────────────────────────┘             │
│                                                                │
│  PDF/doc rendering (chromedp against React print routes)       │
└───────────────┬────────────────────────────────────────────────┘
                │
        ┌───────▼────────┐      ┌──────────────────┐
        │  PostgreSQL    │      │ ECB rates (daily │
        │  schema-per-   │      │ XML fetch, cron) │
        │  module        │      └──────────────────┘
        └────────────────┘
```

### Repository layout

```
ledgerly/
├── cmd/ledgerly/             # main: wiring, DI, HTTP server, migrations, cron
├── internal/
│   ├── platform/             # db pool, event bus, http middleware, config, clock
│   ├── ledger/               # core/ledger
│   ├── moneyfx/              # core/money-fx
│   ├── invoicing/
│   ├── banking/
│   ├── dla/
│   ├── dividends/
│   ├── reports/
│   ├── jurisdiction/         # compliance/jurisdiction
│   ├── advisor/
│   └── identity/             # settings/identity
├── web/                      # React SPA (Vite, TS)
├── packs/isle-of-man/1.0/    # rules pack data (embedded via go:embed)
├── db/migrations/
└── docs/design/              # this document set
```

Each module directory follows the same shape: `api.go` (public interface — the ONLY thing other modules may import), `events.go` (published event types), `service.go`, `store.go` (private SQL), `http.go` (its REST handlers). Boundary rule enforced in CI with `go-arch-lint` (or a small custom `go vet` check): `internal/<mod>/...` may import only `internal/<other-mod>` root package (the `api.go` surface) and `internal/platform`.

---

## 3. Module boundaries and communication

Two communication styles, both explicit:

1. **Synchronous calls** through a module's public Go interface — for queries and commands where the caller needs the result now (e.g. invoicing asks moneyfx for today's locked rate; dividends asks reports for YTD profit).
2. **Domain events** on an in-process bus — for facts other modules react to (e.g. `invoicing.InvoiceSettled` → moneyfx computes realised FX → posts to ledger; `dla.WentOverdrawn` → advisor emits the BIK warning insight). Events are dispatched **in the same DB transaction** where possible (transactional outbox table + synchronous handlers) so ledger postings never drift from their source facts.

**Golden rule (from handoff):** every module posts financial facts to core/ledger through its API; nothing else writes ledger rows. No module reads another module's tables — PostgreSQL **schema-per-module** with per-module DB roles makes this mechanical, not just convention.

### Module map (details in per-module docs)

| Module | Owns | Doc |
|---|---|---|
| core/ledger | Chart of accounts, journal, postings | [modules/core-ledger.md](modules/core-ledger.md) |
| core/money-fx | Money type, ECB rates, rate locks, realised FX | [modules/core-money-fx.md](modules/core-money-fx.md) |
| invoicing | Clients, invoice lifecycle, numbering, PDF, reminders | [modules/invoicing.md](modules/invoicing.md) |
| banking | CSV import, transaction feed, matching, reconciliation | [modules/banking.md](modules/banking.md) |
| dla | Director's loan running ledger, credit/overdrawn status | [modules/dla.md](modules/dla.md) |
| dividends | Headroom calc, voucher + minutes generation | [modules/dividends.md](modules/dividends.md) |
| reports | P&L, VAT figures, filing calendar, export pack | [modules/reports.md](modules/reports.md) |
| compliance/jurisdiction | Versioned rules packs (isle-of-man@1.0) | [modules/compliance-jurisdiction.md](modules/compliance-jurisdiction.md) |
| advisor | Rule engine evaluating facts → insights | [modules/advisor.md](modules/advisor.md) |
| settings/identity | Company profile, logo, users | [modules/settings-identity.md](modules/settings-identity.md) |
| cli (interface) | Terminal client + MCP server for AI agents | [modules/cli.md](modules/cli.md) |

### Dependency direction (compile-time)

```
invoicing ─┬─► moneyfx ─► ledger
banking  ──┤
dla      ──┤        reports ─► ledger (read API)
dividends ─┘        advisor ─► jurisdiction + read APIs of all feature modules
all ─► identity (company profile read)
jurisdiction ─► (nothing)      ledger ─► (nothing)
```

`ledger` and `jurisdiction` are leaves. `advisor` is the only module allowed to fan-in reads across modules (via their public read APIs, never their tables).

---

## 4. Money and FX — the correctness core

- **Money value type**: `{Amount int64 (minor units), Currency string}` — no floats anywhere in money paths. Arithmetic via the moneyfx package only; division/allocation uses largest-remainder so pennies never leak.
- **Rate locking**: on invoice issue, moneyfx snapshots the ECB daily rate into a `rate_locks` row referenced by the invoice. The locked rate is immutable and displayed with source + 🔒 in the editor.
- **Realised FX**: on settlement, gain/loss = amount × (settlement-date rate − locked rate), auto-posted to ledger and surfaced on the banking match card and P&L.
- **Presentational currency**: GBP. Ledger postings store both native amount and GBP amount at posting-date rate ("frozen onto today's postings", per dashboard rate card).

## 5. Data storage

PostgreSQL 16. One database, **one schema per module** (`ledger`, `moneyfx`, `invoicing`, …), migrations per module under `db/migrations/<module>/`. Cross-module references are by opaque ID + module name (e.g. ledger postings carry `source_module`, `source_ref`), never FK across schemas. Rules packs are versioned YAML embedded in the binary (`go:embed`) and loaded into `jurisdiction` at startup; rates/allowances are year-versioned data inside the pack.

Backups: nightly `pg_dump`; the export pack (reports module) doubles as a human-readable escape hatch.

## 6. API and frontend

- **REST/JSON**, OpenAPI 3 spec assembled from per-module fragments; TS client generated for the SPA. Routes namespaced per module: `/api/invoicing/...`, `/api/banking/...`.
- **React SPA** (Vite + TS + TanStack Query + React Router). Design tokens from the handoff (§Design Tokens) as CSS variables; Instrument Sans + IBM Plex Mono via Google Fonts. Screens 01–09 map 1:1 to routes.
- **PDF/doc rendering**: invoice PDF, dividend voucher, board minutes are React print routes (A4, 794px) rendered to PDF server-side with `chromedp`. Same components serve the on-screen previews — one source of truth for layout, and the handoff's HTML designs port directly. Trade-off: ships headless Chromium in the deploy image (~300 MB); acceptable for a single-instance product, and the alternative (hand-building PDFs in Go) makes pixel-fidelity to the handoff much harder.
- **Auth**: single-tenant, email + password session auth (users table in identity), plus personal access tokens (scoped read-only/full) for the CLI and MCP clients. Keep boring.
- **CLI & agents**: `ledgerly` client subcommands and `ledgerly mcp` (stdio MCP server for Claude Code/Codex CLI etc.) are thin clients over the same REST API — no separate surface, no business logic outside modules. See [modules/cli.md](modules/cli.md).

## 7. Advisor and rules pack (extensibility spine)

- `jurisdiction` exposes typed rule data: rates, bands, deadlines, boolean flags (e.g. `s455Applies: false`), and advisor rule definitions with template text. Pack = data, versioned (`isle-of-man@1.0`).
- `advisor` evaluates rule definitions against **facts** gathered from module read APIs (overdue invoices, DLA balance, VAT period position, filing anniversaries, dividend headroom) and emits insights `{id, severity, factBindings, cta}`. Severity: teal = opportunity, amber = deadline/warning. Insights are derived + dismissible (dismissals persisted).
- **Nothing outside `jurisdiction` hard-codes a tax rate, deadline, or advisor wording.** CI grep-check for literal rates (e.g. `0.20`, `6500`, `14750`) in feature modules as a cheap guard.

## 8. Background work

Single in-process cron (robfig/cron): daily ECB rate fetch (~16:00 CET, with retry + "rates stale" advisor insight on failure); daily advisor re-evaluation; invoice overdue status sweep; reminder emails (SMTP, manual-trigger first — see invoicing doc).

## 9. Error handling & observability

Typed domain errors per module mapped to problem-details JSON at the HTTP layer. `slog` structured logging with module attribute. Ledger invariant checks (trial balance = 0) run after every posting batch and nightly; violation = page-the-human log level. Health endpoint checks DB + last ECB fetch age.

## 10. Key trade-offs

| Decision | Chose | Over | Why / cost |
|---|---|---|---|
| Backend language | Go | TS/Node, C# | User preference; strong boundary discipline, single static binary. Cost: two languages (Go + TS), PDF needs headless Chromium. |
| Money representation | int64 minor units | decimal lib, floats | Exact, fast, boring. Cost: careful allocation logic (largest remainder). |
| Module isolation | Schema-per-module + DB roles | Convention only | Boundary violations become runtime errors, not review comments. Cost: slightly more migration ceremony. |
| Events | In-process bus, same-transaction handlers | Message broker | One deployable, no infra; exactly-once by construction. Cost: refactor needed if modules ever split into services (accepted — they won't soon). |
| PDF | chromedp + React print routes | Go PDF libs (gofpdf etc.) | Pixel fidelity to handoff, single layout source. Cost: Chromium in image. |
| API style | REST + OpenAPI | GraphQL, gRPC-web | Screen-shaped needs are simple; codegen keeps TS client honest. |
| Rules pack format | Embedded versioned YAML | DB-stored packs, plugin .so | Reviewable in git, versioned, no dynamic-loading complexity. Cost: pack update = redeploy (fine at this scale). |

## 11. What we'd revisit as it grows

Multi-company/multi-tenant (currently single-tenant assumptions in identity + ledger); pack update without redeploy (move packs to DB with signature verification); bank feeds via Open Banking API instead of CSV; event bus → outbox + broker if any module needs independent scaling; accountant-facing read-only access (currently just "export pack").

## 12. Suggested build order (breakdown seed)

1. **Walking skeleton**: repo scaffold, platform (db, bus, http), CI boundary lint, one deploy.
2. **core/money-fx + core/ledger** — Money type, ECB ingestion, chart of accounts, postings, trial balance. (Everything depends on these.)
3. **settings/identity** — company profile feeds every screen and document.
4. **compliance/jurisdiction** — isle-of-man@1.0 pack loading + typed access.
5. **invoicing** — clients, lifecycle, rate locking, editor + list screens, PDF.
6. **banking** — CSV import, matching, reconciliation screen; realised FX flow end-to-end.
7. **dla** — ledger + screen + overdrawn edge state.
8. **dividends** — headroom, voucher + minutes docs.
9. **reports** — P&L, VAT figures, filing calendar, export pack.
10. **advisor** — rule engine + insights across all screens; dashboard last (it aggregates everything).
11. **cli + MCP** — terminal client and agent interface over the finished API (read commands can start as soon as invoicing ships).

Each module doc ends with its own work-item breakdown.
