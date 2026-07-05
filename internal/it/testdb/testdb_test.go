package testdb

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestNewCreatesIsolatedSuiteDatabases(t *testing.T) {
	var firstDB string
	t.Run("suite_one", func(t *testing.T) {
		pool, _ := New(t)
		ctx := context.Background()

		if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&firstDB); err != nil {
			t.Fatalf("current_database suite one: %v", err)
		}
		if _, err := pool.Exec(ctx, "CREATE TABLE demo.suite_isolation_probe (id integer PRIMARY KEY)"); err != nil {
			t.Fatalf("create suite isolation probe: %v", err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO demo.suite_isolation_probe (id) VALUES (1)"); err != nil {
			t.Fatalf("insert suite isolation probe: %v", err)
		}
	})

	t.Run("suite_two", func(t *testing.T) {
		pool, _ := New(t)
		ctx := context.Background()

		var secondDB string
		if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&secondDB); err != nil {
			t.Fatalf("current_database suite two: %v", err)
		}
		if secondDB == firstDB {
			t.Fatalf("suite database reused: %s", secondDB)
		}

		var relation string
		if err := pool.QueryRow(ctx, "SELECT COALESCE(to_regclass('demo.suite_isolation_probe')::text, '')").Scan(&relation); err != nil {
			t.Fatalf("check suite isolation probe: %v", err)
		}
		if relation != "" {
			t.Fatalf("suite two can see suite one table %q", relation)
		}
	})
}

func TestAsModuleDeniedOnLedgerSchema(t *testing.T) {
	ctx := context.Background()
	raw := Raw(t)
	if _, err := raw.Exec(ctx, "CREATE TABLE ledger.boundary_probe (id integer PRIMARY KEY)"); err != nil {
		t.Fatalf("create ledger boundary probe: %v", err)
	}

	invoicing := AsModule(t, "invoicing")
	var currentRole string
	if err := invoicing.QueryRow(ctx, "SELECT current_role").Scan(&currentRole); err != nil {
		t.Fatalf("current_role: %v", err)
	}
	if currentRole != "ledgerly_invoicing" {
		t.Fatalf("current_role = %q, want ledgerly_invoicing", currentRole)
	}

	_, err := invoicing.Exec(ctx, "SELECT id FROM ledger.boundary_probe")
	if err == nil {
		t.Fatal("ledgerly_invoicing SELECT from ledger.boundary_probe succeeded, want permission denied")
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("SELECT from ledger.boundary_probe error = %v, want PostgreSQL insufficient_privilege 42501", err)
	}
}

func TestTemplateRebuildsWhenMigrationsChange(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("find repo root: %v", err)
	}
	migrationsDir := copyMigrations(t, filepath.Join(root, "db", "migrations"))

	var firstHash string
	var firstBuilds int
	t.Run("before_change", func(t *testing.T) {
		defaultManager.suiteFor(t, suiteConfig{
			key:           t.Name(),
			migrationsDir: migrationsDir,
		})
		stats := defaultManager.stats()
		firstHash = stats.TemplateHash
		firstBuilds = stats.TemplateBuilds
		if firstHash == "" {
			t.Fatal("template hash is empty after first build")
		}
	})

	migration := filepath.Join(migrationsDir, "invoicing", "001_bootstrap.sql")
	file, err := os.OpenFile(migration, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open migration for mutation: %v", err)
	}
	if _, err := file.WriteString("\n-- testdb hash probe\n"); err != nil {
		_ = file.Close()
		t.Fatalf("append migration hash probe: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close migration hash probe: %v", err)
	}

	t.Run("after_change", func(t *testing.T) {
		defaultManager.suiteFor(t, suiteConfig{
			key:           t.Name(),
			migrationsDir: migrationsDir,
		})
		stats := defaultManager.stats()
		if stats.TemplateHash == firstHash {
			t.Fatalf("template hash did not change: %s", stats.TemplateHash)
		}
		if stats.TemplateBuilds <= firstBuilds {
			t.Fatalf("template build count = %d, want > %d", stats.TemplateBuilds, firstBuilds)
		}
	})
}

func TestSuiteProvisioningTimeUnderBudget(t *testing.T) {
	s := defaultManager.suiteFor(t, defaultSuiteConfig(t))
	t.Logf("testdb suite clone duration: %s", s.cloneDuration)
	if s.cloneDuration >= cloneBudget {
		t.Fatalf("suite clone duration = %s, want < %s", s.cloneDuration, cloneBudget)
	}
}

func BenchmarkProvisionSuite(b *testing.B) {
	for i := 0; i < b.N; i++ {
		s := defaultManager.suiteFor(b, suiteConfig{
			key:           fmt.Sprintf("%s/%d", b.Name(), i),
			migrationsDir: defaultSuiteConfig(b).migrationsDir,
		})
		b.ReportMetric(float64(s.cloneDuration.Microseconds())/1000, "clone_ms")
		if err := s.cleanup(); err != nil {
			b.Fatalf("cleanup benchmark suite: %v", err)
		}
	}
}

func copyMigrations(t *testing.T, src string) string {
	t.Helper()

	dst := t.TempDir()
	if err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		bytes, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, bytes, 0o644)
	}); err != nil {
		t.Fatalf("copy migrations: %v", err)
	}
	return dst
}
