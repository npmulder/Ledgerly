package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/npmulder/ledgerly/internal/app"
	"github.com/npmulder/ledgerly/internal/platform/chrome"
	"github.com/npmulder/ledgerly/internal/platform/config"
	"github.com/npmulder/ledgerly/internal/platform/db"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
	platformlog "github.com/npmulder/ledgerly/internal/platform/log"
)

var version = "dev"

const migrationsDirEnv = app.MigrationsDirEnv

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
	case "chrome-smoke":
		if len(args) > 2 {
			return fmt.Errorf("chrome-smoke accepts at most one output path")
		}
		outputPath := "/tmp/ledgerly-about-blank.pdf"
		if len(args) == 2 {
			outputPath = args[1]
		}
		return runChromeSmoke(ctx, stdout, outputPath)
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
	migrationsDir, err := resolveMigrationsDir()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	pool, err := app.OpenPoolWithRetry(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	applied, err := db.MigrateDir(ctx, pool, migrationsDir)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(stdout, "applied %d migrations\n", len(applied))
	return err
}

func resolveMigrationsDir() (string, error) {
	return app.ResolveMigrationsDir()
}

func runChromeSmoke(ctx context.Context, stdout io.Writer, outputPath string) error {
	if err := chrome.RenderAboutBlankPDF(ctx, outputPath); err != nil {
		return err
	}

	_, err := fmt.Fprintf(stdout, "rendered about:blank PDF to %s\n", outputPath)
	return err
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

	startupCtx, startupCancel := context.WithTimeout(ctx, 45*time.Second)
	defer startupCancel()

	builtApp, err := app.Build(startupCtx, app.Config{
		Runtime: cfg,
		Version: version,
	}, app.Dependencies{
		Logger:        logger,
		CronAutostart: true,
	})
	if err != nil {
		return err
	}
	defer func() {
		closeErr := builtApp.Close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	server := httpserver.Server(cfg.HTTPAddr, builtApp.Handler)

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
