# Ledgerly CLI

## Install

Ledgerly ships as a single `ledgerly` binary. Build it locally with:

```sh
go build -trimpath -o ledgerly ./cmd/ledgerly
```

Put the binary on `PATH`, then run `ledgerly version` to verify it.

## Quickstart

```sh
ledgerly auth login --url https://ledgerly.example --token "$LEDGERLY_PAT"
ledgerly bank import statement.csv --account 1 --yes
ledgerly bank confirm 42 --yes
ledgerly report pl --from 2026-04-01 --to 2026-06-30
```

Money-moving commands require an interactive y/N prompt or `--yes` in scripts.

## Scripting

Use `--json` for stable machine output:

```sh
ledgerly --json invoice list --status sent | jq '.invoices[].number'
ledgerly --json bank feed --state suggested | jq '.transactions[].id'
```

Non-interactive money-moving commands fail with exit 2 unless `--yes` is present.
409/already-done responses return exit 1 and print the server explanation.

## PAT Scopes

Use read-only personal access tokens for reporting, dashboards, and review
commands. Use full-scope tokens only for write commands such as invoice sending,
bank reconciliation, DLA entries, and dividend declarations. Store tokens in the
CLI config via `ledgerly auth login`; the config file is written with 0600
permissions.

## Command Reference

### `ledgerly`

Ledgerly API client

Usage:

```text
ledgerly
```

Global Flags:

- `--config`: path to config.toml
- `--json`: emit JSON output
- `--yes`: confirm mutating actions

### `ledgerly advisor`

Read advisor insights

Usage:

```text
ledgerly advisor
```

### `ledgerly advisor insights`

List advisor insights

Usage:

```text
ledgerly advisor insights [flags]
```

Flags:

- `--surface`: advisor surface

### `ledgerly auth`

Manage CLI authentication

Usage:

```text
ledgerly auth
```

### `ledgerly auth login`

Store a personal access token

Usage:

```text
ledgerly auth login --url <url> --token <token> [flags]
```

Flags:

- `--token`: personal access token
- `--url`: Ledgerly API URL

### `ledgerly auth status`

Show authentication status

Usage:

```text
ledgerly auth status
```

### `ledgerly bank`

Read banking

Usage:

```text
ledgerly bank
```

### `ledgerly bank accounts`

List bank accounts

Usage:

```text
ledgerly bank accounts
```

### `ledgerly bank confirm`

Confirm a suggested bank match

Usage:

```text
ledgerly bank confirm <txn>
```

### `ledgerly bank exclude`

Exclude a bank transaction from reconciliation

Usage:

```text
ledgerly bank exclude <txn> --reason <reason> [flags]
```

Flags:

- `--reason`: exclusion reason

### `ledgerly bank feed`

List banking feed

Usage:

```text
ledgerly bank feed [flags]
```

Flags:

- `--account`: bank account id (default 0)
- `--cursor`: next cursor
- `--state`: transaction state

### `ledgerly bank file-dla`

File a bank transaction to the director loan account

Usage:

```text
ledgerly bank file-dla <txn>
```

### `ledgerly bank import`

Import a bank statement CSV

Usage:

```text
ledgerly bank import <file.csv> --account <id> [flags]
```

Flags:

- `--account`: bank account id (default 0)

### `ledgerly bank recode`

Recode a bank transaction to a ledger account

Usage:

```text
ledgerly bank recode <txn> --account <code> [flags]
```

Flags:

- `--account`: target ledger account code

### `ledgerly bank review`

List banking review queue

Usage:

```text
ledgerly bank review
```

### `ledgerly client`

Read clients

Usage:

```text
ledgerly client
```

### `ledgerly client add`

Add a client

Usage:

```text
ledgerly client add [flags]
```

Flags:

- `--address-line1`: address line 1
- `--address-line2`: address line 2
- `--country`: address country (default IM)
- `--currency`: default currency (default GBP)
- `--day-rate`: day rate amount
- `--email`: client email
- `--from-json`: read client JSON from path or -
- `--locality`: address locality
- `--name`: client name
- `--postal-code`: address postal code
- `--region`: address region
- `--retainer`: retainer amount
- `--terms`: payment terms in days (default 14)
- `--vat-number`: VAT number
- `--vat-treatment`: VAT treatment (default domestic)

### `ledgerly client list`

List clients

Usage:

```text
ledgerly client list
```

### `ledgerly dividend`

Read dividends

Usage:

```text
ledgerly dividend
```

### `ledgerly dividend declare`

Declare a dividend

Usage:

```text
ledgerly dividend declare <amount>
```

### `ledgerly dividend headroom`

Show dividend headroom

Usage:

```text
ledgerly dividend headroom
```

### `ledgerly dividend history`

List dividend history

Usage:

```text
ledgerly dividend history
```

### `ledgerly dla`

Read director loan account

Usage:

```text
ledgerly dla
```

### `ledgerly dla add`

Add a manual DLA entry

Usage:

```text
ledgerly dla add --kind repayment|expense-owed --date YYYY-MM-DD --amount <amount> --description <text> [flags]
```

Flags:

- `--amount`: entry amount
- `--cash-account`: cash/bank account code for repayments
- `--date`: entry date YYYY-MM-DD
- `--description`: entry description
- `--expense-category`: expense account code for expense-owed entries
- `--kind`: entry kind: repayment or expense-owed
- `--source-ref`: manual source reference

### `ledgerly dla balance`

Show DLA balance

Usage:

```text
ledgerly dla balance
```

### `ledgerly dla ledger`

List DLA ledger

Usage:

```text
ledgerly dla ledger [flags]
```

Flags:

- `--cursor`: next cursor
- `--from`: inclusive entry date lower bound
- `--to`: inclusive entry date upper bound

### `ledgerly invoice`

Read invoices

Usage:

```text
ledgerly invoice
```

### `ledgerly invoice create`

Create a draft invoice

Usage:

```text
ledgerly invoice create --client <id> [--line "desc:qty:price"]... [flags]
```

Flags:

- `--client`: client id
- `--line`: invoice line in "desc:qty:price" form (default [])

### `ledgerly invoice list`

List invoices

Usage:

```text
ledgerly invoice list [flags]
```

Flags:

- `--cursor`: next cursor
- `--limit`: page size (default 0)
- `--search`: search invoice number or client
- `--status`: filter by invoice status

### `ledgerly invoice pdf`

Download an invoice PDF

Usage:

```text
ledgerly invoice pdf <id> [flags]
```

Flags:

- `--output`: output PDF path

### `ledgerly invoice remind`

Send an invoice reminder

Usage:

```text
ledgerly invoice remind <id>
```

### `ledgerly invoice revert`

Revert a sent invoice to draft

Usage:

```text
ledgerly invoice revert <id>
```

### `ledgerly invoice send`

Send an invoice

Usage:

```text
ledgerly invoice send <id>
```

### `ledgerly invoice show`

Show invoice details

Usage:

```text
ledgerly invoice show <number|id>
```

### `ledgerly mcp`

Run the Ledgerly stdio MCP server

Usage:

```text
ledgerly mcp
```

### `ledgerly rates`

Read FX rates

Usage:

```text
ledgerly rates
```

### `ledgerly rates today`

Show today's FX rate

Usage:

```text
ledgerly rates today [flags]
```

Flags:

- `--from`: source currency (default EUR)
- `--to`: target currency (default GBP)

### `ledgerly report`

Read reports

Usage:

```text
ledgerly report
```

### `ledgerly report calendar`

Show filing calendar

Usage:

```text
ledgerly report calendar
```

### `ledgerly report pl`

Show profit and loss

Usage:

```text
ledgerly report pl [flags]
```

Flags:

- `--from`: inclusive posting date lower bound
- `--to`: inclusive posting date upper bound

### `ledgerly report profit-ytd`

Show profit year to date

Usage:

```text
ledgerly report profit-ytd [flags]
```

Flags:

- `--tax-year`: tax year in YYYY-YY form

### `ledgerly report vat`

Show VAT return

Usage:

```text
ledgerly report vat [flags]
```

Flags:

- `--period`: VAT quarter, for example 2026-Q2

