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
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	migrationAdvisoryLockKey        int64 = 240247
	clusterMigrationAdvisoryLockKey int64 = 0x6c65646765726c79
	devSeedDataSetting                    = "ledgerly.seed_dev_data"
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

type migrationKey struct {
	module   string
	filename string
}

type migrationConfig struct {
	seedDevData bool
}

var historicalMigrationChecksums = map[migrationKey]map[string]struct{}{
	// CV-338 rewrites dev-only seed blocks to use explicit migration runner
	// state. Databases that already applied the old files can safely keep their
	// recorded checksums; fresh installs apply the new SQL.
	{module: "identity", filename: "003_company_profile.sql"}: {
		"8feff29291685754dc96a2955b89167fe25d73b198a091fbbcb5a11a0fa3af6a": {},
	},
	{module: "identity", filename: "004_assets.sql"}: {
		"b23a5221da9deea627b01e30433a6fdf19d3ae4af05088b262df7829eea74c87": {},
	},
}

// MigrationOption customizes migration execution.
type MigrationOption func(*migrationConfig)

// WithDevSeedData enables development/test-only seed data in migrations.
func WithDevSeedData() MigrationOption {
	return func(cfg *migrationConfig) {
		cfg.seedDevData = true
	}
}

// MigrateDir applies migrations from a filesystem directory.
func MigrateDir(ctx context.Context, pool *pgxpool.Pool, dir string, opts ...MigrationOption) ([]AppliedMigration, error) {
	return MigrateFS(ctx, pool, os.DirFS(dir), opts...)
}

// MigrateFS applies module migrations from fsys. The root must contain one
// directory per Ledgerly database module.
func MigrateFS(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, opts ...MigrationOption) (applied []AppliedMigration, err error) {
	cfg := migrationConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	clusterLockConn, err := acquireClusterMigrationLock(ctx, pool)
	if err != nil {
		return nil, err
	}
	defer func() {
		if unlockErr := releaseClusterMigrationLock(clusterLockConn); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()

	locked := false
	defer func() {
		if !locked {
			return
		}

		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if unlockErr := unlockMigrations(unlockCtx, conn); unlockErr != nil && err == nil {
			err = unlockErr
		}
	}()

	if err := lockMigrations(ctx, conn); err != nil {
		return nil, err
	}
	locked = true

	if err := ensureMigrationTable(ctx, conn); err != nil {
		return nil, err
	}

	plan, err := readMigrationPlan(fsys)
	if err != nil {
		return nil, err
	}

	for _, migration := range plan {
		didApply, err := applyMigration(ctx, conn, migration, cfg)
		if err != nil {
			return nil, err
		}
		if didApply {
			applied = append(applied, migration.AppliedMigration)
		}
	}

	return applied, nil
}

type advisoryLockConn interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func acquireClusterMigrationLock(ctx context.Context, pool *pgxpool.Pool) (*pgx.Conn, error) {
	cfg := pool.Config().ConnConfig.Copy()
	var errs []error
	for _, databaseName := range clusterLockDatabases(cfg.Database) {
		lockCfg := cfg.Copy()
		lockCfg.Database = databaseName

		conn, err := pgx.ConnectConfig(ctx, lockCfg)
		if err != nil {
			errs = append(errs, fmt.Errorf("connect migration lock database %s: %w", databaseName, err))
			continue
		}
		if err := lockAdvisory(ctx, conn, clusterMigrationAdvisoryLockKey, "lock cluster migrations"); err != nil {
			_ = conn.Close(context.Background())
			return nil, err
		}
		return conn, nil
	}

	return nil, fmt.Errorf("connect migration lock database: %w", errors.Join(errs...))
}

func releaseClusterMigrationLock(conn *pgx.Conn) error {
	unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 5*time.Second)
	unlockErr := unlockAdvisory(unlockCtx, conn, clusterMigrationAdvisoryLockKey, "unlock cluster migrations")
	unlockCancel()

	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	closeErr := conn.Close(closeCtx)
	closeCancel()

	return errors.Join(unlockErr, closeErr)
}

func clusterLockDatabases(current string) []string {
	candidates := []string{"postgres", "template1"}
	current = strings.TrimSpace(current)
	if current != "" && !slices.Contains(candidates, current) {
		candidates = append(candidates, current)
	}
	return candidates
}

func lockMigrations(ctx context.Context, conn *pgxpool.Conn) error {
	return lockAdvisory(ctx, conn, migrationAdvisoryLockKey, "lock migrations")
}

func unlockMigrations(ctx context.Context, conn *pgxpool.Conn) error {
	return unlockAdvisory(ctx, conn, migrationAdvisoryLockKey, "unlock migrations")
}

func lockAdvisory(ctx context.Context, conn advisoryLockConn, key int64, label string) error {
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

func unlockAdvisory(ctx context.Context, conn advisoryLockConn, key int64, label string) error {
	var unlocked bool
	if err := conn.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", key).Scan(&unlocked); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	if !unlocked {
		return fmt.Errorf("%s: advisory lock was not held", label)
	}
	return nil
}

func ensureMigrationTable(ctx context.Context, conn *pgxpool.Conn) error {
	if _, err := conn.Exec(ctx, "REVOKE ALL ON SCHEMA public FROM PUBLIC"); err != nil {
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
	if _, err := conn.Exec(ctx, sql); err != nil {
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

func applyMigration(ctx context.Context, conn *pgxpool.Conn, migration migrationFile, cfg migrationConfig) (bool, error) {
	var existingChecksum string
	err := conn.QueryRow(
		ctx,
		"SELECT checksum FROM public.ledgerly_migrations WHERE module = $1 AND filename = $2",
		migration.Module,
		migration.Filename,
	).Scan(&existingChecksum)
	if err == nil {
		if existingChecksum != migration.Checksum && !isHistoricalMigrationChecksum(migration, existingChecksum) {
			return false, fmt.Errorf("migration checksum changed for %s/%s", migration.Module, migration.Filename)
		}
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("read migration state for %s/%s: %w", migration.Module, migration.Filename, err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin migration %s/%s: %w", migration.Module, migration.Filename, err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", devSeedDataSetting, migrationBool(cfg.seedDevData)); err != nil {
		return false, fmt.Errorf("configure migration %s/%s dev seed setting: %w", migration.Module, migration.Filename, err)
	}
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

func isHistoricalMigrationChecksum(migration migrationFile, checksum string) bool {
	accepted := historicalMigrationChecksums[migrationKey{
		module:   migration.Module,
		filename: migration.Filename,
	}]
	if accepted == nil {
		return false
	}
	_, ok := accepted[checksum]
	return ok
}

func migrationBool(value bool) string {
	if value {
		return "on"
	}
	return "off"
}
