// Package db owns PostgreSQL pool construction, transaction handles, and
// schema-per-module migrations.
package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/npmulder/ledgerly/internal/platform/config"
)

// DefaultDevDatabaseURL matches the local Docker Compose Postgres service.
const DefaultDevDatabaseURL = "postgres://postgres:postgres@localhost:5432/ledgerly_dev?sslmode=disable"

var moduleNames = []string{
	"ledger",
	"moneyfx",
	"invoicing",
	"banking",
	"dla",
	"dividends",
	"reports",
	"jurisdiction",
	"advisor",
	"identity",
}

var moduleSet = map[string]struct{}{
	"ledger":       {},
	"moneyfx":      {},
	"invoicing":    {},
	"banking":      {},
	"dla":          {},
	"dividends":    {},
	"reports":      {},
	"jurisdiction": {},
	"advisor":      {},
	"identity":     {},
}

// Tx is the database handle accepted by module APIs. A pgx.Tx satisfies this
// interface, allowing multiple module operations to share one transaction.
type Tx interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ScopeTransactionToModule switches tx to module's role and search_path until
// the returned restore function is called.
//
// Use this in same-transaction cross-module event subscribers before writing
// through the subscriber module's store. The transaction remains the publisher's
// original transaction; only the transaction-local role and search_path change.
// The database user or current module role must be allowed to SET ROLE to the
// target module role.
func ScopeTransactionToModule(ctx context.Context, tx Tx, module string) (func(context.Context) error, error) {
	if tx == nil {
		return nil, errors.New("db: nil transaction")
	}
	role, err := RoleForModule(module)
	if err != nil {
		return nil, err
	}

	var previousRole string
	var previousSearchPath string
	if err := tx.QueryRow(ctx, "SELECT current_role, current_setting('search_path')").Scan(&previousRole, &previousSearchPath); err != nil {
		return nil, fmt.Errorf("db: read transaction module scope: %w", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
		return nil, fmt.Errorf("db: set transaction role %s: %w", role, err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('search_path', $1, true)", module); err != nil {
		return nil, fmt.Errorf("db: set transaction search_path %s: %w", module, err)
	}

	return func(ctx context.Context) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{previousRole}.Sanitize()); err != nil {
			return fmt.Errorf("db: restore transaction role %s: %w", previousRole, err)
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('search_path', $1, true)", previousSearchPath); err != nil {
			return fmt.Errorf("db: restore transaction search_path %s: %w", previousSearchPath, err)
		}
		return nil
	}, nil
}

// PoolOption customizes a pgx pool before it is opened.
type PoolOption func(*pgxpool.Config) error

// Open creates a pgx v5 pool from Ledgerly runtime config.
func Open(ctx context.Context, cfg config.Config, opts ...PoolOption) (*pgxpool.Pool, error) {
	return OpenURL(ctx, cfg.DatabaseURL, opts...)
}

// OpenURL creates and pings a pgx v5 pool for databaseURL.
func OpenURL(ctx context.Context, databaseURL string, opts ...PoolOption) (*pgxpool.Pool, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("database URL is required")
	}

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}

	for _, opt := range opts {
		if err := opt(poolConfig); err != nil {
			return nil, err
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return pool, nil
}

// WithModule pins a pool to the module's database role and schema.
func WithModule(module string) PoolOption {
	return func(cfg *pgxpool.Config) error {
		role, err := RoleForModule(module)
		if err != nil {
			return err
		}

		if cfg.ConnConfig.RuntimeParams == nil {
			cfg.ConnConfig.RuntimeParams = make(map[string]string)
		}
		cfg.ConnConfig.RuntimeParams["search_path"] = module

		previousAfterConnect := cfg.AfterConnect
		cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			if previousAfterConnect != nil {
				if err := previousAfterConnect(ctx, conn); err != nil {
					return err
				}
			}

			if _, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
				return fmt.Errorf("set module role %s: %w", role, err)
			}
			if _, err := conn.Exec(ctx, "SET search_path = "+pgx.Identifier{module}.Sanitize()); err != nil {
				return fmt.Errorf("set module search_path %s: %w", module, err)
			}
			return nil
		}
		return nil
	}
}

// Modules returns the canonical Ledgerly modules that own database schemas.
func Modules() []string {
	modules := make([]string, len(moduleNames))
	copy(modules, moduleNames)
	return modules
}

// RoleForModule returns the database role name for module.
func RoleForModule(module string) (string, error) {
	if err := ValidateModule(module); err != nil {
		return "", err
	}
	return "ledgerly_" + module, nil
}

// ValidateModule rejects unknown schema names before they reach SQL or
// connection runtime parameters.
func ValidateModule(module string) error {
	if _, ok := moduleSet[module]; ok {
		return nil
	}
	return fmt.Errorf("unknown Ledgerly module %q", module)
}
