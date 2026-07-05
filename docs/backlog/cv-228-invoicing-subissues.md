# CV-228 unpacking — invoicing sub-issues (INV-2…9)

**Status: IMPORTED ✅ (2026-07-05).** All sub-issues now live in Linear: INV-1 = CV-284, INV-2 = CV-285, INV-3 = CV-286, INV-4 = CV-287, INV-5 = CV-288, INV-6 = CV-289, INV-7 = CV-290, INV-8 = CV-291, INV-9 = CV-292. **Linear is the source of truth** — this file is kept only as the drafting record. Parent: CV-228 · Milestone: M3 — Invoicing · Label: `module`.

Execution order: INV-2 → INV-3 → INV-4 → INV-5 → {INV-6, INV-7, INV-8 in parallel} → INV-9.

---

## INV-2: Invoice model — drafts, lines, totals, numbering table
**blockedBy:** CV-284 (INV-1), CV-269 (FX-1)

**Agent brief.** Invoice domain + draft lifecycle. No send/posting yet (INV-3). Design: `docs/design/modules/invoicing.md`.

**Spec**
- `invoices`: id, number nullable (assigned at send), client_id, status enum (`draft`|`sent`|`paid`), issue_date, due_date (derived: issue + client terms, editable), currency (defaults from client), lock_id nullable, vat_treatment (defaults from client), settlement fields nullable (txn_ref, settled_date, settled_amount), pdf_asset nullable, timestamps. **No `overdue` in the enum — derived** (INV-4)
- `invoice_lines`: id, invoice_id, position, description, qty numeric, unit_price Money — line total = qty × unit_price via FX-1 `MulRat` (banker's rounding documented)
- Totals (computed, never stored): subtotal = Σ line totals; VAT = subtotal × pack rate for `domestic`, **zero for `reverse-charge-eu-b2b`** (rate via jurisdiction — no literals); total = subtotal + VAT; ≈GBP note via moneyfx `TodayRate` for drafts (locked rate after send)
- `invoice_numbering`: year, last_seq — `INV-YYYY-NN`; `nextNumber(tx, year)` with `FOR UPDATE` row lock, gap-free; assignment logic lands in INV-3
- API: `CreateDraft(clientID)` (defaults applied), `UpdateDraft(id, patch)` (only status=draft mutable), `Invoice(id)`, `Delete` drafts only
- Sent/paid invoices immutable except settlement fields (enforced at store level)

**Acceptance**
- Totals vs hand-computed (incl. fractional qty, 1p rounding at line level); reverse-charge → VAT exactly zero with rate read from pack; draft immutability matrix; nextNumber gap-free under 50 concurrent tx (race test); delete-sent rejected
- Boundary lint (invoicing imports: platform, money, moneyfx root, jurisdiction root, ledger root, identity root only)

**Do not:** send flow (INV-3), endpoints (INV-5), screens (INV-6/7).

---

## INV-3: Send & settle — number assignment, rate lock, ledger posting, events
**blockedBy:** INV-2, CV-271 (LED-2), CV-277 (FX-4), CV-278 (FX-5)

**Agent brief.** The financially significant transitions — everything in ONE transaction each. Design: `docs/design/modules/invoicing.md` (§Lifecycle & key flows).

**Spec**
- `Send(ctx, id)` — single tx: validate draft complete → assign `nextNumber` → `moneyfx.Lock(ref invoicing:<number>, cur→GBP, issueDate)` → store lock_id, status=`sent` → `ledger.Post` Dr trade-debtors-<cur> / Cr sales (native + GBP at **locked** rate, not TodayRate) → publish `invoicing.InvoiceSent{invoiceID, number, clientID, amount, dueDate}`. Any failure → whole tx rolls back; nextNumber row-lock is in the same tx so rollback preserves gap-free numbering
- `MarkSettled(ctx, tx, id, txnRef, date, amount)` — called by banking inside ITS tx: validate status=sent, exact-amount match (partial payments = typed error, out of scope v1), set settlement fields + status=`paid`, publish `invoicing.InvoiceSettled` — **replace the FX-5 contract stub with real publishing here**
- `RevertToDraft(id)` (unsend, v1 minimal): only sent ∧ unsettled ∧ same-day; ledger.Reverse the posting; **number stays consumed** (gap-free wins); resend creates a new lock (FX-4 re-lock semantics). Document in godoc
- GBP invoice path: lock is identity rate 1.0, posting GBP=native

**Acceptance**
- Harness tests (IT0-2): send happy path (sent + lock + balanced entry + event); mid-tx failure injection → still draft, no gap, no lock, no entry; settle → paid + FX-5 posts realised FX (flat and step rate fixtures); settle wrong amount → error; unsend → reversal nets zero, resend gets new number + new lock
- `AssertLedgerBalanced` in every teardown

**Do not:** overdue (INV-4), endpoints (INV-5), reminder (INV-9).

---

## INV-4: Overdue derivation, sweep, list queries & totals
**blockedBy:** INV-3, CV-273 (LED-4 cron)

**Agent brief.** Derived overdue status + the read queries the list screen and advisor need. Design doc: statuses DRAFT/SENT/PAID/OVERDUE (red, `OVERDUE 9D`).

**Spec**
- Overdue = `status=sent ∧ due_date < today` — computed in queries via injected clock date, never stored. `days_overdue` in query results
- Daily cron `invoicing.overdue-sweep`: finds newly overdue invoices (crossed the boundary since last run; sweep state table) → publish `invoicing.InvoiceOverdue{invoiceID, daysOverdue}` per invoice (advisor consumes). Idempotent per invoice per day
- `List(filter{status incl. virtual overdue, search over number+client, pagination})`; status filter counts for the pills (single grouped query); `Totals(filter)` for the footer (per-currency subtotals + GBP)
- `OverdueInvoices()` advisor fact API
- Search: ILIKE over number + client name, index accordingly

**Acceptance**
- FakeClock tests: invoice crosses due date → appears in overdue filter with correct day count; sweep fires event exactly once per crossing (advance clock 3 days → one event, day count 3 in fact API); counts match filters; totals hand-verified per currency
- Query performance: list at 5k invoices indexed (benchmark note)

**Do not:** reminder emails (INV-9), screens.

---

## INV-5: Invoicing HTTP endpoints + OpenAPI fragment
**blockedBy:** INV-3, INV-4

**Agent brief.** Full REST surface for invoices; the fragment the SPA + CLI generate against.

**Spec**
- `GET /api/invoicing/invoices` (filters/search/pagination/counts/totals) · `POST /api/invoicing/invoices` (create draft) · `GET /api/invoicing/invoices/{id}` · `PATCH /api/invoicing/invoices/{id}` (draft autosave — partial, idempotent, returns computed totals for the editor) · `POST …/{id}/send` · `POST …/{id}/revert` (same-day unsend) · `GET …/{id}/pdf` (302 to stored asset once INV-8 lands; 404 before) — no settle endpoint (banking calls the Go API)
- Autosave semantics: PATCH accepts partial line arrays with client-generated line ids; last-write-wins, `updated_at` echo for the autosave chip
- Problem-details for all domain errors (draft-only edits, incomplete send, wrong-amount) with field pointers
- OpenAPI fragment complete; auth-guarded

**Acceptance**
- httptest matrix per endpoint incl. error cases; PATCH autosave round-trip returns recomputed totals; send via API → 200 with number + locked rate in response; `npm run api:generate` compiles; 401s unauthenticated

**Do not:** screens; PDF rendering (INV-8).

---

## INV-6: Invoices list screen (02)
**blockedBy:** INV-5, CV-256 (FE-4), CV-257 (FE-5)

**Agent brief.** **Fidelity source:** handoff screen 02 — recreate pixel-faithfully with FE-3 primitives.

**Spec**
- Status filter pills with live counts (ALL/DRAFT/SENT/PAID/OVERDUE); search box; table columns: number (mono), client, issued, amount (native, mono), locked rate (mono, — for drafts), ≈GBP (mono), status badge
- Overdue rows tinted `--danger-row-tint` (#fdf5f6) with `OVERDUE 9D`-style badge (day count from API)
- Footer totals row (per-currency + GBP from `Totals`)
- Advisor strip slot below header (renders CV-233 insights for surface `invoices` when available; empty until then — component takes insights as props)
- "New invoice" → creates draft via POST, navigates to editor route
- Empty state: "All caught up" pattern when no invoices match
- TanStack Query keys per convention; loading/error states via FE-5 patterns

**Acceptance**
- Component tests: pill counts, overdue tint + badge, footer totals, empty state
- Playwright smoke: fixture invoices render; filter + search round-trip; new-invoice navigation
- Visual check vs handoff screen 02

**Do not:** editor (INV-7); reminder CTA action (INV-9 wires it).

---

## INV-7: Invoice editor screen (03)
**blockedBy:** INV-5, CV-256, CV-257

**Agent brief.** **Fidelity source:** handoff screen 03.

**Spec**
- Draft editor: client select (unarchived clients), issue/due dates, currency (defaults from client, editable while draft), VAT treatment select, line items table (add/remove/reorder, qty × unit price, line totals), totals card: subtotal / VAT (shows "Reverse charge — €0.00" for RC) / total + ≈GBP note
- **Autosave chip**: debounced PATCH (~800ms), states saving/saved/error with `updated_at` echo; navigating away flushes
- **Locked FX rate field**: hidden for drafts (shows indicative TodayRate as "≈ rate, locks at send"); after send read-only with 🔒 + "Source: ECB <rate_date>"
- Right rail: document preview thumbnail (placeholder until INV-8, then live) linking to PDF; advisor notes slot (auto reverse-charge wording note from jurisdiction; FX explanation)
- Send button: validation summary if incomplete; success → sent state (fields readonly, number shown); same-day revert action
- Sent/paid view mode: fully read-only rendering of the same layout

**Acceptance**
- Component tests: autosave debounce + error state; RC vs domestic totals; locked-rate field states (draft/sent); read-only enforcement in sent view
- Playwright: create → edit lines → autosave → send → locked rate visible → revert same-day
- Visual check vs handoff screen 03

**Do not:** PDF generation (INV-8).

---

## INV-8: Invoice PDF — print route, chromedp rendering, immutable storage
**blockedBy:** INV-5, CV-251 (SKEL-7 Chromium), CV-282 (IT0-4 goldens), CV-260 (ID-2 assets), CV-265 (JUR-2 wording)

**Agent brief.** **Fidelity source:** handoff screen 04 (A4, 794px). The document that leaves the building — highest fidelity bar in the app.

**Spec**
- React print route `/print/invoice/{id}` (no shell per FE-4 convention): logo (identity asset) + From (trading name, IoM address, company number) / Bill to (client + VAT no.) / Terms columns; line table with **navy double rule**; totals with highlighted "Total due"; reverse-charge legal note (exact wording via jurisdiction API, VAT €0.00) for RC invoices; SEPA bank details footer (identity); "Generated by Ledgerly"
- Render service `internal/invoicing/pdf.go`: chromedp against the embedded SPA print route (localhost), A4, print CSS, waits for fonts/logo; store via content-addressed asset store (reuse ID-2 pattern) + set `invoices.pdf_asset`
- Render happens at **send time** (same flow, after commit — async with retry; invoice send must not fail on render hiccup; re-render action for recovery) — stored PDF never regenerated after success (immutability; identity changes don't touch it — IT-6 asserts)
- `GET /api/invoicing/invoices/{id}/pdf` → 302 to asset (INV-5 stub goes live)
- Draft preview: same route rendered on-demand, watermarked DRAFT, never stored

**Acceptance**
- Golden tests (IT0-4): RC invoice (Contoso) — text layer asserts wording incl. "Article 196", VAT €0.00, locked rate, SEPA footer; domestic (Fabrikam) — 20% VAT line from pack; raster layer pins layout
- Identity change after send → stored PDF byte-identical (IT-6 precursor); render failure → invoice still sent, retry succeeds, alert logged
- Visual check vs handoff screen 04

**Do not:** voucher/minutes (dividends epic); email (INV-9).

---

## INV-9: Overdue reminder email — SMTP platform helper + CTA endpoint
**blockedBy:** INV-4, INV-8

**Agent brief.** Manual-trigger reminder per design (v1: no automatic sending). Closes the epic.

**Spec**
- `internal/platform/mail`: minimal SMTP sender (config: host/port/user/pass/from via env), `Send(msg{to, subject, textBody, attachments})`; harness substitution point (IT0-2 module-substitution) with in-memory fake capturing sends
- `SendReminder(ctx, invoiceID)`: only sent ∧ overdue; composes from template (invoice number, days overdue, amount, due date; PDF attached from stored asset); records `reminders(invoice_id, sent_at)` — visible in editor right rail; rate-limit one reminder per invoice per day
- `POST /api/invoicing/invoices/{id}/remind` endpoint; list-screen advisor strip + editor button wire the CTA (declarative advisor CTA `invoicing.sendReminder(id)` resolves here)
- Template: plain text v1, professional tone, no marketing fluff; company signature from identity profile

**Acceptance**
- Harness tests with fake mailer: remind overdue → one send with PDF attachment + record row; remind non-overdue → typed error; second remind same day → rate-limit error; template snapshot test
- E2E: overdue fixture → CTA visible on list strip → click → success toast + reminder logged

**Definition of done for CV-228 overall:** INV-9 green = invoicing complete; IT-2 (CV-236) suite work unblocked on the invoicing side.

---

## Parent (CV-228) description to apply on import

Parent tracker — work happens in INV-1…9. INV-1 = CV-284 (created). Order: INV-2 → INV-3 → INV-4 → INV-5 → {INV-6, INV-7, INV-8 parallel} → INV-9. Key contracts: overdue is derived, never stored; send/settle are single transactions with gap-free numbering surviving rollback; sent documents are immutable; VAT logic reads the jurisdiction pack exclusively; INV-3 replaces FX-5's InvoiceSettled contract stub with real publishing.
