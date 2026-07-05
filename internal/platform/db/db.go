// Package db owns PostgreSQL pool construction, transaction handles, and
// schema-per-module migrations.
package db

import (
	"context"
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
	"demo",
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
	"demo":         {},
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
