# Development Guide

This guide is the implementation companion for Ledgerly's module architecture.
Use `internal/ledger` as the reference module for production module shape.

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

`internal/ledger` shows the production path for module-owned schema, service
orchestration, store queries, HTTP mounting, and OpenAPI contribution.

## Database Shape

Every database module gets:

- one PostgreSQL schema matching the module name;
- one role named `ledgerly_<module>`;
- one migration directory under `db/migrations/<module>`;
- one entry in `internal/platform/db` so migrations, module roles, and bus event
  validation all agree on the canonical module list.

The ledger bootstrap migrations create the `ledger` schema, the `ledgerly_ledger`
role, chart-of-accounts tables, journal entries, and postings. Other modules
use public Go APIs to post ledger facts in their own transactions instead of
owning ledger tables directly.

## Transactional Events

Publish domain events only from inside the service transaction:

1. Start a transaction from the module-scoped pool.
2. Write the command's primary row through the store.
3. Publish the domain event through `bus.Publish(ctx, tx, event)`.
4. Commit only after every subscriber succeeds.

`internal/ledger/service.go` is the reference for validating inputs, writing
through store methods, publishing domain events inside the supplied transaction,
and returning typed domain errors. If a subscriber returns an error, the service
returns the error and the caller rolls back the shared transaction.

Subscribers must be synchronous and deterministic. Do not start goroutines,
retry in the handler, or write outside the supplied `db.Tx`.

## HTTP Shape

Module routes are mounted by the platform under `/api/<module>`. The ledger read
surface registers:

- `GET /api/ledger/entries` to browse journal entries and postings;
- `GET /api/ledger/accounts` to list the chart of accounts;
- `GET /api/ledger/trial-balance` to read the current invariant status.

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

New modules should follow the module pattern instead of reaching into another
module's store or mounting routes directly in `cmd/ledgerly`.

## Integration Proof

See [Testing](testing.md) for the full integration-suite conventions, CI
artifact behavior, flake policy, and module-author checklist.

The integration harness lives under `internal/it/harness` with the `integration`
build tag. It boots the full monolith in-process on an isolated database:

```sh
go test -tags=integration ./internal/it/harness -run TestLedgerReadSurfaceE2E -count=1
```

The proof:

- clones a migrated IT0-1 template database for the suite;
- boots the same `internal/app.Build` wiring used by `ledgerly serve`;
- logs in a first-run owner and returns an authenticated HTTP client;
- exercises an authenticated module HTTP route;
- verifies the route is backed by the same `internal/app.Build` wiring used by
  `ledgerly serve`;
- provides helpers for shared database transactions, deterministic clocks, and
  driven background jobs.

### Writing an Integration Suite

Use `internal/it/harness` for app-level tests:

```go
//go:build integration

package mymodule_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/app"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

func TestMain(m *testing.M) {
	os.Exit(testdb.Main(m))
}

func TestWorkflow(t *testing.T) {
	t.Parallel()

	h := harness.New(t, harness.Options{
		Jobs: map[string]app.Job{
			"job-name": func(context.Context) error { return nil },
		},
	})
	h.Clock.Advance(time.Hour)
	if err := h.RunJob("job-name"); err != nil {
		t.Fatalf("run job: %v", err)
	}
	h.Tx(func(ctx context.Context, tx db.Tx) error {
		_, err := tx.Exec(ctx, "SELECT 1")
		return err
	})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/api/ledger/accounts", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("GET accounts: %v", err)
	}
	defer resp.Body.Close()
}
```

Do not use wall-clock sleeps in integration suites. Advance `h.Clock` for time
dependent behavior and drive background work through `h.RunJob` or module-level
`RunNow` helpers.

## Document Golden Snapshots

Rendered document tests use `internal/it/golden` to compare both content and
layout. A document test renders the document PDF bytes, then calls:

```go
golden.PDF(t, "invoice-paid", pdfBytes,
	golden.WithMasks(`20[0-9]{2}-[0-9]{2}-[0-9]{2}T[0-9:]+Z`),
)
```

Goldens live under the calling package's `testdata/golden` directory:

- `<name>.txt` is extracted PDF text for wording, amounts, company names, and
  other content assertions.
- `<name>.hash` is the fixed-DPI raster hash for layout drift detection.
- `<name>.png` is the baseline raster image kept only so failures can write both
  `got.png` and `want.png` artifacts.

Use masks only for volatile fields such as generation timestamps or generated
IDs. Do not mask locked-rate values, document amounts, company names, legal
wording, or other business facts that the golden is meant to protect.

To add a golden for a new document type:

1. Render a deterministic fixture PDF with stable business inputs.
2. Call `golden.PDF(t, "<document-name>", pdfBytes)` from the document package's
   test, adding `golden.WithMasks(...)` only for volatile text.
3. Run `task golden:update` to regenerate the self-test snapshots, or run the
   package-specific command with `-update` for document module goldens.
4. Review the committed `.txt`, `.hash`, and `.png` files. The text file should
   read like the business assertion; the PNG is for visual diagnostics.
5. Run `task golden:docker` when local Chrome/fonts differ from CI. The Docker
   wrapper uses the same headless-shell binary and font environment expected by
   the golden suite.

`-update` is rejected when `CI=true`, so CI can never rewrite snapshots. Raster
mismatches write artifacts under `GOLDEN_ARTIFACT_DIR` when it is set; otherwise
they fall back to a temporary artifact directory and include the exact paths in
the test failure.

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
