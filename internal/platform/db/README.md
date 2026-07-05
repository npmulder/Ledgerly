# Platform DB

Ledgerly uses one PostgreSQL database with one schema and one login role per
module:

```text
<module> schema, ledgerly_<module> role
```

The initial module set is:

```text
ledger, moneyfx, invoicing, banking, dla, dividends, reports, jurisdiction, advisor, identity
```

## Local Database

`docker compose up -d` starts PostgreSQL 16 on `localhost:5432` with database
`ledgerly_dev` and admin credentials `postgres` / `postgres`.

`ledgerly migrate` uses `LEDGERLY_DATABASE_URL` when it is set. Otherwise it
defaults to the local compose database:

```text
postgres://postgres:postgres@localhost:5432/ledgerly_dev?sslmode=disable
```

## Migrations

Migrations live under `db/migrations/<module>/`. The runner validates that all
known module directories exist, then applies module directories and SQL files in
lexical order. Applied file checksums are recorded in
`public.ledgerly_migrations`.

Bootstrap migrations create only module schemas and module roles. Business
tables belong to module implementation tickets.

## Module Pools

Use `db.Open` for a normal pool from runtime config, and pin a module pool's
`search_path` with `db.WithModule`:

```go
cfg, err := config.Load()
if err != nil {
	return err
}

pool, err := db.Open(ctx, cfg, db.WithModule("invoicing"))
if err != nil {
	return err
}
defer pool.Close()
```

Module roles also have a default `search_path` set by the bootstrap migrations,
but application pools should still pin it explicitly through pgx runtime
parameters.

## Shared Transactions

Module APIs should accept `db.Tx`, not a concrete pool. `pgx.Tx` satisfies this
interface, so cross-module operations can share one transaction while each
module still owns its schema and SQL.
