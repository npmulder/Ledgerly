package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq"

	"github.com/npmulder/ledgerly/internal/platform/config"
	"github.com/npmulder/ledgerly/internal/platform/db"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
	platformlog "github.com/npmulder/ledgerly/internal/platform/log"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		if _, writeErr := fmt.Fprintln(os.Stderr, err); writeErr != nil {
			os.Exit(1)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return printVersion(stdout)
	}

	switch args[0] {
	case "migrate":
		if len(args) != 1 {
			return fmt.Errorf("usage: ledgerly migrate")
		}
		return runMigrate(ctx, stdout)
	case "serve":
		if len(args) > 1 {
			return fmt.Errorf("serve accepts no arguments")
		}
		return runServe(ctx)
	case "version", "--version", "-v":
		return printVersion(stdout)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printVersion(stdout io.Writer) error {
	_, err := fmt.Fprintf(stdout, "ledgerly %s\n", version)
	return err
}

func runMigrate(ctx context.Context, stdout io.Writer) error {
	databaseURL := strings.TrimSpace(os.Getenv("LEDGERLY_DATABASE_URL"))
	if databaseURL == "" {
		databaseURL = db.DefaultDevDatabaseURL
	}

	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
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

func runServe(ctx context.Context) (err error) {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := platformlog.Configure(platformlog.Config{
		Env:   string(cfg.Env),
		Level: cfg.LogLevel,
	})

	sqlDB, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database handle: %w", err)
	}
	defer func() {
		closeErr := sqlDB.Close()
		if err == nil && closeErr != nil {
			err = fmt.Errorf("close database handle: %w", closeErr)
		}
	}()

	router := httpserver.NewRouter(httpserver.Config{
		Version: version,
		Logger:  logger,
		DB:      sqlDB,
	})
	server := httpserver.Server(cfg.HTTPAddr, router)

	errc := make(chan error, 1)
	go func() {
		logger.InfoContext(ctx, "http server listening", "addr", cfg.HTTPAddr)
		errc <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		return nil
	case err := <-errc:
		if errors.Is(err, nethttp.ErrServerClosed) {
			return nil
		}
		return err
	}
}
