# Development Guide

This guide is the implementation companion for Ledgerly's walking skeleton. Use
`internal/demo` as the reference module until real business modules replace it.

## Anatomy of a Module

Each module lives under `internal/<module>/` and keeps the same file shape:

- `api.go` exposes the public module surface: constructor, config, public
  request/response types, HTTP mount, OpenAPI fragment, and event subscription
  hook.
- `events.go` defines domain events published by the module and subscribers
  owned by the module.
- `service.go` owns command/query orchestration. It starts transactions,
  coordinates stores, publishes events, and commits or rolls back.
- `store.go` owns private SQL. It accepts `db.Tx` so callers and subscribers can
  share one transaction.
- `http.go` maps REST JSON to service commands and maps service errors to
  problem-details responses.

The demo module shows the whole path with one table, `demo.notes`.

## Database Shape

Every database module gets:

- one PostgreSQL schema matching the module name;
- one role named `ledgerly_<module>`;
- one migration directory under `db/migrations/<module>`;
- one entry in `internal/platform/db` so migrations, module roles, and bus event
  validation all agree on the canonical module list.

Demo's bootstrap migration creates the `demo` schema, the `ledgerly_demo` role,
and `demo.notes`. The table uses `kind = 'note'` for user-facing rows and
`kind = 'audit'` for subscriber audit rows. This keeps the walking skeleton to
one table while still proving publisher and subscriber writes share a
transaction.

## Transactional Events

Publish domain events only from inside the service transaction:

1. Start a transaction from the module-scoped pool.
2. Write the command's primary row through the store.
3. Publish the domain event through `bus.Publish(ctx, tx, event)`.
4. Commit only after every subscriber succeeds.

`internal/demo/service.go` is the reference. `CreateNote` inserts a note, then
publishes `demo.NoteCreated` with the same `tx`. `internal/demo/events.go`
registers an audit subscriber that receives that same `tx` and inserts the
audit row. If the subscriber returns an error, the service returns the error and
the deferred rollback removes both rows.

Subscribers must be synchronous and deterministic. Do not start goroutines,
retry in the handler, or write outside the supplied `db.Tx`.

## HTTP Shape

Module routes are mounted by the platform under `/api/<module>`. Demo registers:

- `POST /api/demo/notes` to create a note;
- `GET /api/demo/notes` to list note rows.

Handlers should stay thin: decode JSON, call the service, encode JSON, and map
domain errors to RFC 7807 problem responses through `internal/platform/http`.

## Application Wiring

`cmd/ledgerly` is the reference for real module wiring:

1. Load runtime config and logger.
2. Open a database handle for platform health checks.
3. Open a module-scoped pgx pool with `db.WithModule("<module>")`.
4. Create one platform event bus.
5. Construct the module with its pool and the event bus.
6. Register module subscribers before serving requests.
7. Pass `httpserver.Module` and `httpserver.OpenAPIFragment` values into the
   platform router.

New modules should follow the demo pattern instead of reaching into another
module's store or mounting routes directly in `cmd/ledgerly`.

## Integration Proof

The IT-0 walking-skeleton test lives in `cmd/ledgerly` with the `integration`
build tag. It runs against the compose PostgreSQL database:

```sh
LEDGERLY_TEST_DB=postgres://postgres:postgres@localhost:5432/ledgerly_dev?sslmode=disable \
  go test -tags=integration ./cmd/ledgerly -run TestDemoWalkingSkeletonE2E -count=1
```

The proof:

- migrates the compose database;
- boots the same router assembly used by `ledgerly serve`;
- posts a demo note through HTTP;
- lists notes through HTTP;
- verifies an audit row exists in `demo.notes`;
- injects a subscriber error and verifies the note and audit rows both rolled
  back.

CI runs this as the first step in the integration job before the broader
`task test:integration` command.

## Guardrails

When adding a module, update these checks in the same PR:

- `internal/platform/db` module registry and tests;
- `tools/archcheck` dependency map;
- `db/migrations/<module>`;
- `cmd/ledgerly` module wiring;
- focused unit or integration coverage for the module's transaction/event path.

Run:

```sh
go test ./...
go run ./tools/archcheck arch
go run ./tools/archcheck rates
```
