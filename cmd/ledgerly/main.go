package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/app"
	"github.com/npmulder/ledgerly/internal/cli"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx"
	"github.com/npmulder/ledgerly/internal/platform/chrome"
	"github.com/npmulder/ledgerly/internal/platform/config"
	"github.com/npmulder/ledgerly/internal/platform/db"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
	platformlog "github.com/npmulder/ledgerly/internal/platform/log"
)

var version = "dev"

var loadActiveJurisdiction = jurisdiction.LoadActive
var fetchRatesRunner = runFetchRates

const (
	migrationsDirEnv = app.MigrationsDirEnv
	devSeedLogoPath  = "docs/design_handoff_keel/uploads/invoice_brand-1783009881094.png"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runWithIO(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if _, writeErr := fmt.Fprintln(os.Stderr, err); writeErr != nil {
			os.Exit(1)
		}
		os.Exit(exitCode(err))
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	return runWithIO(ctx, args, stdout, io.Discard)
}

func runWithIO(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return printVersion(stdout)
	}

	if !isOperatorCommand(args[0]) {
		return cli.Execute(ctx, args, stdout, stderr, cli.WithVersion(version))
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
	case "openapi":
		if len(args) != 1 {
			return fmt.Errorf("usage: ledgerly openapi")
		}
		return runOpenAPI(stdout)
	case "check":
		return runCheck(ctx, args[1:], stdout)
	case "fetch-rates":
		if len(args) != 1 {
			return fmt.Errorf("usage: ledgerly fetch-rates")
		}
		return fetchRatesRunner(ctx, stdout)
	case "version", "--version", "-v":
		return printVersion(stdout)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func isOperatorCommand(command string) bool {
	switch command {
	case "migrate", "serve", "chrome-smoke", "openapi", "check", "fetch-rates", "version", "--version", "-v":
		return true
	default:
		return false
	}
}

func exitCode(err error) int {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return 1
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

func runOpenAPI(stdout io.Writer) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(app.OpenAPIDocument(version))
}

func runCheck(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) != 1 || args[0] != "trial-balance" {
		return fmt.Errorf("usage: ledgerly check trial-balance")
	}
	return runCheckTrialBalance(ctx, stdout)
}

func runCheckTrialBalance(ctx context.Context, stdout io.Writer) error {
	databaseURL := strings.TrimSpace(os.Getenv("LEDGERLY_DATABASE_URL"))
	if databaseURL == "" {
		databaseURL = db.DefaultDevDatabaseURL
	}

	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	pool, err := app.OpenPoolWithRetry(ctx, databaseURL, db.WithModule(ledger.ModuleName))
	if err != nil {
		return err
	}
	defer pool.Close()

	report, err := ledger.New(pool).TrialBalance(ctx, time.Now())
	if err != nil && !errors.Is(err, ledger.ErrTrialBalanceViolation) {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if encodeErr := encoder.Encode(report); encodeErr != nil {
		return encodeErr
	}
	return err
}

func runFetchRates(ctx context.Context, stdout io.Writer) (err error) {
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
		Logger:             logger,
		JurisdictionLoader: loadActiveJurisdiction,
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

	if err := builtApp.RunJob(ctx, moneyfx.ECBFetchJobName); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "fetched ECB rates")
	return err
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
		Logger:             logger,
		JurisdictionLoader: loadActiveJurisdiction,
		CronAutostart:      true,
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
	if cfg.Env == config.EnvDev {
		if err := seedDevIdentityLogo(startupCtx, builtApp.IdentityPool, cfg.DataDir); err != nil {
			return fmt.Errorf("seed dev identity logo: %w", err)
		}
	}

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

func seedDevIdentityLogo(ctx context.Context, pool *pgxpool.Pool, dataDir string) (err error) {
	sourcePath, err := resolveRepoFile(devSeedLogoPath)
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin identity seed transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	if _, err = identity.SeedDevLogoAsset(ctx, tx, dataDir, sourcePath); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit identity seed transaction: %w", err)
	}
	return nil
}

func resolveRepoFile(relativePath string) (string, error) {
	var starts []string
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	if executable, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(executable))
	}

	seen := make(map[string]struct{}, len(starts))
	for _, start := range starts {
		dir, err := filepath.Abs(start)
		if err != nil {
			continue
		}
		for {
			if _, ok := seen[dir]; ok {
				break
			}
			seen[dir] = struct{}{}

			candidate := filepath.Join(dir, relativePath)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}

			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	return "", fmt.Errorf("locate %s: run ledgerly from a repository checkout", relativePath)
}
