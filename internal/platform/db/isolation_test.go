package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestModuleRoleIsolation(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("LEDGERLY_TEST_DB"))
	if databaseURL == "" {
		t.Skip("set LEDGERLY_TEST_DB to run Postgres isolation test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	adminPool, err := OpenURL(ctx, databaseURL)
	if err != nil {
		t.Fatalf("OpenURL() admin error = %v", err)
	}
	defer adminPool.Close()
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		dropIsolationTables(t, cleanupCtx, adminPool)
	}()

	root := findRepoRoot(t)
	if _, err := MigrateDir(ctx, adminPool, filepath.Join(root, "db", "migrations")); err != nil {
		t.Fatalf("MigrateDir() error = %v", err)
	}

	assertSchemaAndRoleCounts(t, ctx, adminPool)
	dropIsolationTables(t, ctx, adminPool)

	if _, err := adminPool.Exec(ctx, "CREATE TABLE ledger.boundary_probe (id integer)"); err != nil {
		t.Fatalf("create ledger boundary table as admin: %v", err)
	}

	invoicingPool := openModuleRolePool(t, ctx, databaseURL, "invoicing")
	defer invoicingPool.Close()

	var searchPath string
	if err := invoicingPool.QueryRow(ctx, "SHOW search_path").Scan(&searchPath); err != nil {
		t.Fatalf("SHOW search_path as ledgerly_invoicing: %v", err)
	}
	if searchPath != "invoicing" {
		t.Fatalf("search_path = %q, want invoicing", searchPath)
	}

	if _, err := invoicingPool.Exec(ctx, "CREATE TABLE invoicing.x (id integer)"); err != nil {
		t.Fatalf("ledgerly_invoicing CREATE TABLE invoicing.x error = %v", err)
	}

	_, err = invoicingPool.Exec(ctx, "SELECT id FROM ledger.boundary_probe")
	if err == nil {
		t.Fatal("ledgerly_invoicing SELECT from ledger.boundary_probe succeeded, want permission denied")
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("SELECT from ledger.boundary_probe error = %v, want PostgreSQL insufficient_privilege 42501", err)
	}
}

func openModuleRolePool(t *testing.T, ctx context.Context, databaseURL string, module string) *pgxpool.Pool {
	t.Helper()

	role, err := RoleForModule(module)
	if err != nil {
		t.Fatalf("RoleForModule(%q) error = %v", module, err)
	}

	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	cfg.ConnConfig.User = role
	cfg.ConnConfig.Password = role
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = make(map[string]string)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = module

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open pool as %s: %v", role, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping as %s: %v", role, err)
	}

	return pool
}

func assertSchemaAndRoleCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	var schemaCount int
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM information_schema.schemata WHERE schema_name = ANY($1::text[])",
		Modules(),
	).Scan(&schemaCount); err != nil {
		t.Fatalf("count module schemas: %v", err)
	}
	if schemaCount != 10 {
		t.Fatalf("module schema count = %d, want 10", schemaCount)
	}

	var roleCount int
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM pg_roles WHERE rolname LIKE 'ledgerly\\_%' ESCAPE '\\'",
	).Scan(&roleCount); err != nil {
		t.Fatalf("count ledgerly roles: %v", err)
	}
	if roleCount != 10 {
		t.Fatalf("ledgerly role count = %d, want 10", roleCount)
	}
}

func dropIsolationTables(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS invoicing.x"); err != nil {
		t.Fatalf("drop invoicing.x: %v", err)
	}
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS ledger.boundary_probe"); err != nil {
		t.Fatalf("drop ledger.boundary_probe: %v", err)
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repository root containing go.mod")
		}
		dir = parent
	}
}
