# Ledgerly Agent Registration

Ledgerly exposes a stdio MCP server through the CLI:

```sh
ledgerly mcp
```

The server uses the same Ledgerly API URL and personal access token as the CLI.
Amounts are integer minor units with explicit currency codes, for example
`125000 GBP` means `GBP 1,250.00`.

## Token Scope

Use a `read-only` personal access token for exploratory agent sessions. It can
read invoices, advisor insights, dividend headroom, DLA, reports, VAT, filing
calendar, and bank review data, but it cannot create drafts or send reminders.

Use a `full` personal access token only when the agent must call the two write
tools:

- `create_draft_invoice`
- `send_invoice_reminder`

Money-moving actions are deliberately not MCP tools. Agents can prepare drafts
and reminders; a human confirms sending invoices, settling payments, confirming
bank matches, and declaring dividends in the CLI or web UI.

## Claude Code

Claude Code registers stdio MCP servers with `claude mcp add <name> --
<command> [args...]`; this matches `claude mcp add --help`.

If the Ledgerly CLI is already authenticated with `ledgerly auth login`:

```sh
claude mcp add ledgerly -- ledgerly mcp
```

To keep the token in environment variables instead of the Ledgerly CLI config:

```sh
claude mcp add ledgerly \
  -e LEDGERLY_URL=https://ledgerly.example \
  -e LEDGERLY_TOKEN="$LEDGERLY_PAT" \
  -- ledgerly mcp
```

## Codex CLI

Codex CLI stores MCP servers in `~/.codex/config.toml` or a trusted project's
`.codex/config.toml` under `[mcp_servers.<name>]`. Codex also reads the MCP
`instructions` field returned by Ledgerly during initialization.

Add Ledgerly with the CLI:

```sh
codex mcp add ledgerly -- ledgerly mcp
```

Environment-token variant:

```sh
codex mcp add ledgerly \
  --env LEDGERLY_URL=https://ledgerly.example \
  --env LEDGERLY_TOKEN="$LEDGERLY_PAT" \
  -- ledgerly mcp
```

Equivalent `config.toml`:

```toml
[mcp_servers.ledgerly]
command = "ledgerly"
args = ["mcp"]

[mcp_servers.ledgerly.env]
LEDGERLY_URL = "https://ledgerly.example"
LEDGERLY_TOKEN = "lgy_..."
```

## Worked Examples

### Explain my advisor insights

Prompt:

```text
Explain my Ledgerly advisor insights for the invoices screen. Show which facts
drive each insight and what action, if any, I should consider.
```

Expected agent path:

- Call `advisor_insights` with `{"surface":"invoices"}`.
- Explain `severity`, `rendered_text`, `bindings`, and `cta` as deterministic
  rule output from Ledgerly.
- If the CTA suggests a reminder, read the invoice first with `get_invoice`;
  use `send_invoice_reminder` only with a full-scope token and only when asked.

### Draft next month's retainer invoice

Prompt:

```text
Draft next month's retainer invoice for Fabrikam Ltd for GBP 1,250 plus VAT.
Do not send it.
```

Expected agent path:

- Use a full-scope token.
- Call `create_draft_invoice`:

```json
{
  "clientName": "Fabrikam Ltd",
  "lines": [
    {
      "description": "Next month retainer",
      "qty": "1",
      "unitPriceMinor": 125000,
      "currency": "GBP"
    }
  ]
}
```

- Return the draft id and editor URL.
- Do not call any send, settle, confirm, or declare action. A human reviews the
  draft and sends it in the web UI or with `ledgerly invoice send <id>`.

### Should I declare a dividend this quarter?

Prompt:

```text
Should I declare a dividend this quarter? Use Ledgerly figures and explain the
headroom, trading result, VAT position, and DLA context.
```

Expected agent path:

- Call `dividend_headroom`.
- Call `profit_and_loss` for the quarter.
- Call `vat_position` for the VAT quarter if VAT cash obligations matter.
- Call `dla_balance` to check whether an overdrawn director loan changes the
  recommendation.
- Explain the deterministic numbers and any limits or warnings.
- Do not declare the dividend through MCP. If the human decides to proceed, they
  declare it in the web UI or with the CLI after reviewing the amount.
