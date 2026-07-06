# Testing

Ledgerly keeps unit tests fast by default and runs cross-module integration
flows explicitly. The normal unit command is:

```sh
go test ./...
```

Integration suites use the Go build tag convention:

```go
//go:build integration
```

Run the full integration harness with:

```sh
go test -tags=integration ./internal/it/...
```

CI has a separate required `integration` job for this command. The job uses a
fresh PostgreSQL service, writes Go test output to
`.task/integration-artifacts/harness.log`, writes golden raster artifacts under
`.task/integration-artifacts/golden`, and uploads that directory on failure. A
nightly scheduled workflow run exercises the same full suite against a fresh
checkout and build. CI never retries integration tests: a flaky suite is a bug.

## Suite Placement

Every cross-module flow gets an integration suite under `internal/it` named for
the flow, for example `internal/it/invoice_settlement_test.go`. Harness,
testdb, fixture, and golden self-tests stay in their existing helper packages.

Use package `it_test` for business-flow suites. Keep module-level unit tests in
the module package unless the test needs real app wiring, real PostgreSQL
schemas, or transactional cross-module behavior.

## Suite Skeleton

```go
//go:build integration

package it_test

import (
	"context"
	"net/http"
	"os"
	"testing"

	it "github.com/npmulder/ledgerly/internal/it"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
)

func TestMain(m *testing.M) {
	os.Exit(testdb.Main(m))
}

func TestCrossModuleFlow(t *testing.T) {
	h := harness.New(t, harness.Options{})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	it.AssertLedgerBalanced(t, h) // Optional early check; harness.New also checks in cleanup.
}
```

`harness.New` registers `it.AssertLedgerBalanced(t, h)` in cleanup by default.
The assertion wraps the ledger `TrialBalance` invariant and checks the entire
suite database as of the maximum supported ledger date, so future-dated fixture
entries are covered too.

Opting out is rare and must be explicit:

```go
h := harness.New(t, harness.Options{
	// This suite intentionally leaves a corrupt historical fixture so it can
	// assert the health-check degraded state without repairing the row.
	BalanceCheck: harness.WithoutBalanceCheck("intentional corrupt ledger fixture for health-check assertion"),
})
```

The comment explains the local test intent; the non-empty justification string
makes the opt-out visible in test logs.

## Fixtures

Use `internal/it/fixtures` for stable business objects shared across suites.
Fixture helpers should:

- create records through public HTTP or service APIs when that is the behavior
  under test;
- keep deterministic names, currencies, dates, and amounts;
- return domain types, not raw database rows;
- leave no wall-clock dependency; set or advance `h.Clock` instead.

Database access is available for assertions and targeted failure injection
through `h.Tx`. Module-scoped stores can use `testdb.AsModule(t, moduleName)`
when they need the same role and search path production code uses.

## Failure Injection

Drive failures through the harness instead of sleeps or background races. For
event rollback paths, inject a subscriber failure and then assert both the
request failure and the database rollback:

```go
h.FailNextBusSubscriber(module.EventCreatedName, errors.New("forced failure"))
callModuleCommandExpectingFailure(t, h, "rollback probe")
h.Tx(func(ctx context.Context, tx db.Tx) error {
	var count int
	if err := tx.QueryRow(ctx, `
SELECT count(*)
FROM module_schema.rows
WHERE body = $1`, "rollback probe").Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("rolled-back note count = %d, want 0", count)
	}
	return nil
})
```

Do not use wall-clock sleeps in integration suites. Advance `h.Clock` for
time-dependent behavior and run deterministic jobs with `h.RunJob`.

## Golden Workflow

Rendered document tests use `internal/it/golden`:

```go
golden.PDF(t, "invoice-paid", pdfBytes,
	golden.WithMasks(`20[0-9]{2}-[0-9]{2}-[0-9]{2}T[0-9:]+Z`),
)
```

Goldens live under the calling package's `testdata/golden` directory:

- `<name>.txt` protects extracted wording, amounts, company names, and other
  business content.
- `<name>.hash` protects the fixed-DPI raster hash.
- `<name>.png` is the baseline image used to write `got.png` and `want.png`
  artifacts on raster mismatch.

Use masks only for volatile fields such as generated timestamps or IDs. Never
mask locked rates, document amounts, legal wording, or company names that the
golden is meant to protect.

To update a golden:

1. Render a deterministic fixture.
2. Run the focused package test with `-update`, or use `task golden:update` for
   the helper self-tests.
3. Review the `.txt`, `.hash`, and `.png` changes.
4. Run `task golden:docker` when local Chrome or fonts differ from CI.

`-update` is rejected when `CI=true`. Raster mismatches write artifacts under
`GOLDEN_ARTIFACT_DIR` when it is set; the integration CI job sets that variable
to `.task/integration-artifacts/golden`.

## Module Author Checklist

- [ ] Add focused unit coverage for pure module rules and edge cases.
- [ ] Add or extend `internal/it/<flow>_test.go` for every cross-module flow.
- [ ] Use `//go:build integration` on integration suites.
- [ ] Boot the app with `harness.New(t, harness.Options{})`.
- [ ] Keep the default ledger balance cleanup enabled, or add an explicit
      `WithoutBalanceCheck` opt-out with a justification comment.
- [ ] Use fixtures for shared business objects and keep dates, amounts, and
      currencies deterministic.
- [ ] Use golden snapshots for rendered documents and commit reviewed
      `.txt`, `.hash`, and `.png` files.
- [ ] Run `go test ./...` and `go test -tags=integration ./internal/it/...`
      before opening review.
