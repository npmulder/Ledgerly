# Module: cli — headless client & agent interface

**Package:** `internal/cli` (+ `internal/mcp`) · **Schema:** none (pure API client) · **Depends on:** the HTTP API only — never module internals or the DB

## Responsibility

A first-class terminal interface to Ledgerly for use without the web UI, plus an **MCP server mode** so AI agents (Claude Code, Codex CLI, Cowork, or any MCP client) can read the books and drive workflows. Both are thin clients over the same REST/OpenAPI API the SPA uses — one API surface, three consumers (web, CLI, agents).

```
React SPA ──┐
ledgerly CLI ──┼──► REST / OpenAPI ──► Go monolith
MCP clients ──┘      (single API)
(Claude Code, Codex CLI, …)
```

## Design decisions

- **Same binary.** `ledgerly serve` runs the server (existing); everything else is a client subcommand talking HTTP. No second deployable, no direct DB access from client code paths — module boundaries stay intact because the CLI can only do what the API allows.
- **Generated client.** Go client generated from the same OpenAPI spec as the TS client. API drift breaks the CLI build, not the user.
- **Auth:** personal access token, created under Settings → Users, stored in `~/.config/ledgerly/config.toml` (0600). `ledgerly auth login --url https://… --token …`.
- **Output:** human tables by default (design-token-ish: aligned mono numerals); `--json` on every command for scripting — this same flag is what makes the CLI agent-friendly.
- **Writes are explicit.** Mutating commands print what will happen and require `--yes` in non-interactive mode. Money-moving actions (send invoice, declare dividend, confirm match) are never auto-confirmed.

## Command tree (v1)

```
ledgerly auth      login | status
ledgerly invoice   list | show | create | send | pdf | remind
ledgerly client    list | add
ledgerly bank      import <file.csv> --account | review | confirm <txn> | file-dla <txn> | recode <txn> --account | exclude <txn>
ledgerly dla       ledger | balance | add
ledgerly dividend  headroom | declare <amount> | history
ledgerly report    pl --period | vat --period | calendar | export --period
ledgerly advisor   insights [--surface] | dismiss <id>
ledgerly rates     today | history
ledgerly settings  profile | set
```

Every command maps 1:1 to an existing module API endpoint — the CLI adds no business logic.

## MCP server mode — how AI CLIs interact with Ledgerly

`ledgerly mcp` runs a stdio MCP server (register it in Claude Code / Codex CLI config). This is the intended integration point for AI tools.

**Important scoping note: the advisor module does not call any LLM, and no AI CLI is a dependency of Ledgerly.** The advisor stays a deterministic rule engine over the jurisdiction pack — auditable, testable, correct-by-construction for a compliance product. The relationship is inverted: AI agents *consume* Ledgerly through MCP, with the advisor's outputs exposed as tools. Claude can then explain an insight, draft the reminder email, or walk through "should I declare a dividend this month?" using real figures — while every number and rule evaluation stays deterministic inside Ledgerly.

**Tools (v1):**

| Tool | Maps to | Notes |
|---|---|---|
| `list_invoices`, `get_invoice` | invoicing read API | filters as params |
| `advisor_insights` | advisor.InsightsFor | severity, fact bindings, CTA included |
| `dividend_headroom` | dividends.Headroom | full breakdown lines |
| `dla_balance`, `dla_ledger` | dla read API | |
| `profit_and_loss`, `vat_position`, `filing_calendar` | reports API | |
| `bank_review_queue` | banking.ReviewQueue | suggestions + confidence |
| `create_draft_invoice` | invoicing.CreateDraft | **write — draft only** |
| `send_invoice_reminder` | invoicing.SendReminder | **write** |

**Write-tool policy:** v1 write tools are limited to reversible/low-stakes actions (drafts, reminders). Money-posting actions (send, settle, declare) are deliberately **not** exposed as MCP tools — an agent can prepare, a human confirms in the CLI or web UI. Revisit once there's operational trust.

Auth: MCP mode reuses the same PAT; a `read-only` token scope makes an agent physically unable to write.

## Events / Data

None — stateless client. Server-side additions: PAT issuance/scopes in identity, and OpenAPI spec completeness (every screen-facing endpoint documented, since CLI coverage = spec coverage).

## Work items

1. Cobra skeleton, config, `auth login/status`, generated OpenAPI Go client
2. PAT issuance + scopes (read-only vs full) in identity module (small server-side change)
3. Read commands: invoice/client/dla/dividend/report/advisor/rates groups, table + `--json` renderers
4. Write commands with confirm semantics: invoice create/send/remind, bank confirm/file-dla/recode, dividend declare
5. `ledgerly mcp` stdio server: read tools + draft/reminder write tools, read-only scope enforcement
6. Docs: install, quickstart, agent-registration snippets for Claude Code (`claude mcp add ledgerly -- ledgerly mcp`) and Codex CLI
