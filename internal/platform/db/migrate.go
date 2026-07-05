package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AppliedMigration describes a migration applied by this run.
type AppliedMigration struct {
	Module   string
	Filename string
	Checksum string
}

type migrationFile struct {
	AppliedMigration
	SQL string
}

// MigrateDir applies migrations from a filesystem directory.
func MigrateDir(ctx context.Context, pool *pgxpool.Pool, dir string) ([]AppliedMigration, error) {
	return MigrateFS(ctx, pool, os.DirFS(dir))
}

// MigrateFS applies module migrations from fsys. The root must contain one
// directory per Ledgerly database module.
func MigrateFS(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) ([]AppliedMigration, error) {
	if err := ensureMigrationTable(ctx, pool); err != nil {
		return nil, err
	}

	plan, err := readMigrationPlan(fsys)
	if err != nil {
		return nil, err
	}

	var applied []AppliedMigration
	for _, migration := range plan {
		didApply, err := applyMigration(ctx, pool, migration)
		if err != nil {
			return nil, err
		}
		if didApply {
			applied = append(applied, migration.AppliedMigration)
		}
	}

	return applied, nil
}

func ensureMigrationTable(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, "REVOKE ALL ON SCHEMA public FROM PUBLIC"); err != nil {
		return fmt.Errorf("revoke public schema privileges: %w", err)
	}

	const sql = `
CREATE TABLE IF NOT EXISTS public.ledgerly_migrations (
	module text NOT NULL,
	filename text NOT NULL,
	checksum text NOT NULL,
	applied_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (module, filename)
)`
	if _, err := pool.Exec(ctx, sql); err != nil {
		return fmt.Errorf("ensure migration table: %w", err)
	}
	return nil
}

func readMigrationPlan(fsys fs.FS) ([]migrationFile, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations root: %w", err)
	}

	seen := make(map[string]bool)
	var modules []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !entry.IsDir() {
			return nil, fmt.Errorf("migration root contains non-directory %q", name)
		}
		if err := ValidateModule(name); err != nil {
			return nil, err
		}
		seen[name] = true
		modules = append(modules, name)
	}
	slices.Sort(modules)

	var missing []string
	for _, module := range moduleNames {
		if !seen[module] {
			missing = append(missing, module)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing migration directories for modules: %s", strings.Join(missing, ", "))
	}

	var plan []migrationFile
	for _, module := range modules {
		files, err := fs.ReadDir(fsys, module)
		if err != nil {
			return nil, fmt.Errorf("read migrations for %s: %w", module, err)
		}

		for _, file := range files {
			name := file.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if file.IsDir() {
				return nil, fmt.Errorf("migration directory %s contains subdirectory %q", module, name)
			}
			if !strings.HasSuffix(name, ".sql") {
				return nil, fmt.Errorf("migration file %s/%s must end in .sql", module, name)
			}

			sqlBytes, err := fs.ReadFile(fsys, path.Join(module, name))
			if err != nil {
				return nil, fmt.Errorf("read migration %s/%s: %w", module, name, err)
			}
			sql := strings.TrimSpace(string(sqlBytes))
			if sql == "" {
				return nil, fmt.Errorf("migration %s/%s is empty", module, name)
			}

			hash := sha256.Sum256(sqlBytes)
			plan = append(plan, migrationFile{
				AppliedMigration: AppliedMigration{
					Module:   module,
					Filename: name,
					Checksum: hex.EncodeToString(hash[:]),
				},
				SQL: sql,
			})
		}
	}

	return plan, nil
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, migration migrationFile) (bool, error) {
	var existingChecksum string
	err := pool.QueryRow(
		ctx,
		"SELECT checksum FROM public.ledgerly_migrations WHERE module = $1 AND filename = $2",
		migration.Module,
		migration.Filename,
	).Scan(&existingChecksum)
	if err == nil {
		if existingChecksum != migration.Checksum {
			return false, fmt.Errorf("migration checksum changed for %s/%s", migration.Module, migration.Filename)
		}
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("read migration state for %s/%s: %w", migration.Module, migration.Filename, err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin migration %s/%s: %w", migration.Module, migration.Filename, err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, migration.SQL); err != nil {
		return false, fmt.Errorf("apply migration %s/%s: %w", migration.Module, migration.Filename, err)
	}
	if _, err := tx.Exec(
		ctx,
		"INSERT INTO public.ledgerly_migrations (module, filename, checksum) VALUES ($1, $2, $3)",
		migration.Module,
		migration.Filename,
		migration.Checksum,
	); err != nil {
		return false, fmt.Errorf("record migration %s/%s: %w", migration.Module, migration.Filename, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit migration %s/%s: %w", migration.Module, migration.Filename, err)
	}

	return true, nil
}
