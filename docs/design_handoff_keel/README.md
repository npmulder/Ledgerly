# Handoff: Keel — multi-currency books for owner-directors (Isle of Man)

## Overview
Keel is a bookkeeping/invoicing system for a single-director limited company that invoices in multiple currencies (EUR + GBP) and needs jurisdiction-aware compliance help. First jurisdiction: **Isle of Man** (NOT UK — rules differ materially, see Jurisdiction Rules Pack below). The product replaces FreeAgent-style tools for the owner.

## About the Design Files
The files in this bundle are **design references created in HTML** (`Keel App.dc.html` + `support.js` runtime + logo asset). They are prototypes showing intended look and behavior — **not production code to copy directly**. Your task is to recreate these designs in the target codebase. No codebase exists yet: choose an appropriate stack, but the architecture MUST be a **modular monolith** (see Architecture section).

Open `Keel App.dc.html` in a browser to see all 9 screens stacked vertically, each labeled 01–09.

## Fidelity
**High-fidelity.** Colors, typography, spacing, radii and copy are final intent. Recreate pixel-faithfully using your chosen component library or hand-rolled CSS.

## Architecture requirement: modular monolith
Single deployable, strict module boundaries, communication via explicit interfaces/events — no shared table access across modules. Suggested modules:

- **core/ledger** — double-entry postings, chart of accounts, journal. Every other module posts here; nothing else writes ledger rows.
- **core/money-fx** — Money value type (amount + currency), ECB daily rate ingestion, rate-locking (rate frozen at invoice issue), realised FX gain/loss computation on settlement.
- **invoicing** — clients, invoice lifecycle (draft → sent → paid/overdue), numbering (`INV-YYYY-NN`), PDF rendering, reminders.
- **banking** — statement CSV import (Revolut GBP + EUR to start), transaction feed, match-suggestion engine (invoice matching, recurring-payee rules, DLA detection), reconciliation state.
- **dla** — director's loan account running ledger (drawings, repayments, personally-paid expenses), credit/overdrawn status.
- **dividends** — headroom calculation (retained earnings + YTD profit − declared), voucher + board-minutes document generation.
- **reports** — P&L (presentational currency GBP), VAT return figures, export pack.
- **compliance/jurisdiction** — pluggable, versioned **rules pack** per jurisdiction. `isle-of-man@1.0` is the only pack. All tax rates, filing deadlines, and advisor rule text live HERE, never hard-coded in other modules. Adding a jurisdiction later = adding a pack.
- **advisor** — rule engine that evaluates facts from other modules against the active rules pack and emits insights (see Advisor section).
- **settings/identity** — company profile (name, logo, number, registered office, year end), users.

## Jurisdiction Rules Pack — Isle of Man v1.0 (business rules to encode)
- Corporate income tax: **0% standard rate** → no corporation-tax provision anywhere in the app.
- Dividends: **no withholding tax**. Instead, estimate the director's **personal** IoM income tax (10% band ~£6,500 above personal allowance ~£14,750, then 21% top rate) and surface it as a "set aside personally" advisor insight.
- VAT: IoM shares the UK VAT regime (20%), filed with **Isle of Man Customs & Excise**. B2B services to EU clients = reverse charge (Article 196, Directive 2006/112/EC) — invoice carries the wording, VAT €0.00.
- Annual return: due **1 month after incorporation anniversary**, filed with IoM Companies Registry.
- Company income tax return: due **12 months + 1 day after accounting year end** (required even at 0%).
- Director loans: **no UK s455 charge**. If DLA goes overdrawn, warn about benefit-in-kind on an interest-free loan; offer "clear with dividend".
- Rates/allowances are year-versioned data in the pack, not constants.

## Design Tokens
- **Fonts**: `Instrument Sans` (UI, 400/500/600/700), `IBM Plex Mono` (numbers, invoice IDs, rates, 400/500/600). Google Fonts.
- **Colors**:
  - Navy primary `#16337f` (buttons, active nav, advisor panel bg, PAID badge text)
  - Teal accent `#2bb8b8` (active-nav underline, count badges); teal-light `#7fe0dd` (emphasis on navy)
  - Success/teal-dark `#0e7c7b` on `#d9f3f2`; tint `#f2fbfb`
  - Navy tint `#e8edf9`, panel tint `#f2f5fa`
  - Warning `#b07408` on `#fdf1dc` / callout bg `#fff8ef` border `#f2e3c8`
  - Danger `#b0263a` on `#f7dee2`; row tint `#fdf5f6`
  - Text `#1c2433`; secondary `#6b7484`; muted `#9aa2b1`
  - Page bg `#f7f8fb`; card `#ffffff`; borders `#e7eaf1` (hairline) / `#d6dcea` (inputs); canvas desk `#e9ebf0`
- **Radii**: cards 12px, inputs/buttons 8px, inner rows 8px, badges 999px (pills)
- **Shadow**: screens `0 8px 40px rgba(20,30,60,0.12)`; cards rely on 1px borders, no shadow
- **Type scale**: page title 24–26px/700; card title 14–15px/700; body 13.5px; secondary 12–13px; stat 26px/700; uppercase labels 11–12px/600–700, letter-spacing 0.05em; mono numerals 12–13px
- **Layout**: screens 1280px wide; header 18px 32px padding; content padding 28px 32px; grid gaps 16–20px; main split 1.6fr/1fr (dashboard, banking, DLA)

## Screens (01–09, matching labels in the HTML)
1. **Dashboard** — greeting + company name, primary CTA "Raise July invoice", 4 stat cards (Cash GBP-equiv with per-currency breakdown; Outstanding with ≈GBP + due date; Director's loan; Dividend headroom), recent invoices, to-reconcile preview, navy Advisor panel (4 insights), EUR/GBP rate card ("frozen onto today's postings").
2. **Invoices list** — status filter pills with counts, search, table (number/client/issued/amount/locked rate/GBP/status). Overdue row tinted `#fdf5f6` with `OVERDUE 9D` badge. Footer totals. Advisor strip: overdue reminder with "Send reminder" CTA.
3. **Invoice editor** — draft with autosave chip; details (client, dates, currency defaulting from client, **locked FX rate field — read-only, shows source ECB + 🔒**, VAT treatment select); line items with subtotal/VAT(reverse charge €0)/total + ≈GBP note; right rail: doc preview thumbnail linking to PDF, advisor notes (auto reverse-charge wording, FX gain/loss explanation).
4. **Invoice PDF** — A4 (794px), logo, From (IoM address + company number) / Bill to (client + VAT no.) / Terms columns, line table with navy double rule, totals with highlighted "Total due", reverse-charge legal note, SEPA bank details footer, "Generated by Keel".
5. **Banking** — account cards (Revolut GBP selected, EUR with review count), CSV import CTA, review queue: match card (98% match to invoice, auto-posted FX gain), suggestion card (DLA drawing → "File to DLA" + "Recode ▾"), rule card (recurring payee → category, "applied 11 times"). Right: recently reconciled + **empty state** ("All caught up…").
6. **DLA** — running ledger table (owed-to-you / drawn / balance columns, mono numerals), current balance banner (teal, `£2,150.00 CR`). Right: status card ("In credit — tax-free to withdraw") + **overdrawn edge state** (amber, BIK warning, "Clear with dividend" CTA).
7. **Dividends** — live headroom calc (retained b/fwd + YTD profit − 0% CIT − declared = available, navy total rule), amount input + "Generate voucher + minutes", validation strip (within headroom · no WHT · personal tax estimate), history table. Right: rendered **dividend voucher** and **board minutes** documents (company no., shareholder, per-share amount, distributable-reserves recital, signature lines).
8. **Reports** — P&L Apr–Jun (GBP presentational currency; income per client/currency; realised FX gains line; "IoM income tax at 0%" line), VAT return card (boxes 1/4/6, net reclaim, due badge), filing calendar (VAT, annual return, company tax return, personal tax return with due-date badges), "Export pack" + "Share with accountant".
9. **Settings** — left nav (Company / Jurisdiction / Clients / Invoicing defaults / Bank connections / Users). Company identity: **replaceable logo** (dashed drop area + "Replace logo…") and **editable trading name** — both must propagate everywhere (header, PDF, voucher, minutes). Jurisdiction rules-pack card (versioned, 6 rule summaries + note that packs are installable modules). Clients list (currency badge, retainer/day-rate, terms + VAT treatment).

## Interactions & Behavior
- Header nav persists on all screens; active item navy + teal underline (in the prototype nav links jump between screen sections).
- Statuses: DRAFT (gray) → SENT (teal) → PAID (navy) / OVERDUE (red, shows days).
- FX: rate locked at invoice issue (ECB daily); on settlement, realised gain/loss auto-posted and shown on the match card and P&L.
- Reconciliation: confirm-match one click; suggestions offer accept or recode; payee rules learn from history.
- Advisor: insights are rule-engine outputs (id, severity, fact bindings, CTA) — navy panel on dashboard, single contextual strips on other screens. Severity colors: teal = opportunity, amber = deadline/warning.
- Overdue: reminder email CTA from the advisor strip.
- Empty states: banking "All caught up"; apply the same pattern to invoices/DLA when no data.

## State Management (minimum)
Company profile; clients (currency, terms, VAT treatment, retainer); invoices (status, currency, locked rate, settlement); bank accounts + transactions (reconciliation state, match suggestions); DLA entries (running balance); dividends (declarations + generated docs); rules-pack facts (deadlines, rates); advisor insights (derived, dismissible).

## Sample data used in the mocks
Company "NPM Limited", Co. No. 137792C, 18 Athol St, Douglas, IoM, year end 31 Mar. Director/sole shareholder N. Meyer (100 ordinary £1 shares). Client Contoso GmbH (Munich, VAT DE 129 273 398), €4,500/month retainer, Net 14, reverse charge. Client Fabrikam Ltd (Leeds, GBP, day rate £600, Net 30). Banks: Revolut Business GBP + EUR. These are placeholders — company name and logo are user-configurable (in the prototype, name/director/client are tweakable props on the design component).

## Assets
- `uploads/invoice_brand-1783009881094.png` — the user's current company logo (placeholder; logo is user-replaceable in Settings).

## Files
- `Keel App.dc.html` — all 9 screens (open in a browser; requires `support.js` alongside)
- `support.js` — design-component runtime (prototype only, do not port)
- `uploads/invoice_brand-1783009881094.png` — logo
