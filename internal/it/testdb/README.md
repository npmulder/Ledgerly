# Test Database Harness

`internal/it/testdb` provisions real PostgreSQL databases for integration
tests. Each test binary shares one Postgres runtime, and each suite gets its own
database cloned from a migrated template.

Use it from an integration package like this:

```go
func TestMain(m *testing.M) {
	os.Exit(testdb.Main(m))
}

func TestSomething(t *testing.T) {
	pool, cleanup := testdb.New(t)
	defer cleanup()

	invoicingPool := testdb.AsModule(t, "invoicing")
	rawPool := testdb.Raw(t)
	_, _, _ = pool, invoicingPool, rawPool
}
```

When `LEDGERLY_TEST_DB` is set, the harness uses that PostgreSQL server as the
admin connection. This is the CI/service-container path. When it is unset, the
harness starts `postgres:16-alpine` through testcontainers-go. If neither path
is available, tests skip with a message that names the missing runtime.

## Template Budget

Migrations run once into a template database keyed by a hash of
`db/migrations`. Suite provisioning then uses:

```sql
CREATE DATABASE <suite_db> TEMPLATE <template_db>
```

The per-suite clone budget is less than 500 ms. Tests log the clone duration as
`testdb suite database ... provisioned ... in ...`, and
`TestSuiteProvisioningTimeUnderBudget` enforces the budget for the clone step.

## Migration Rules

Keep migrations template-compatible:

- Do not leave open sessions connected to the database being migrated.
- Do not depend on the database name inside migration SQL.
- Do not use non-transactional external side effects from migrations.
- Keep module schemas and roles idempotent. Template rebuilds happen whenever
  the migration path/content hash changes.
