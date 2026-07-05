package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "ledgerly: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		_, err := fmt.Fprintf(stdout, "ledgerly %s\n", version)
		return err
	}

	switch args[0] {
	case "migrate":
		if len(args) != 1 {
			return fmt.Errorf("usage: ledgerly migrate")
		}
		return runMigrate(stdout)
	case "version", "--version", "-v":
		_, err := fmt.Fprintf(stdout, "ledgerly %s\n", version)
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runMigrate(stdout io.Writer) error {
	databaseURL := strings.TrimSpace(os.Getenv("LEDGERLY_DATABASE_URL"))
	if databaseURL == "" {
		databaseURL = db.DefaultDevDatabaseURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	pool, err := openPoolWithRetry(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	applied, err := db.MigrateDir(ctx, pool, "db/migrations")
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(stdout, "applied %d migrations\n", len(applied))
	return err
}

func openPoolWithRetry(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	var lastErr error
	for {
		pool, err := db.OpenURL(ctx, databaseURL)
		if err == nil {
			return pool, nil
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("connect to postgres: %w", errors.Join(lastErr, ctx.Err()))
		case <-time.After(500 * time.Millisecond):
		}
	}
}
