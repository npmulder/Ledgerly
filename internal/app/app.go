// Package app assembles the Ledgerly monolith from platform and module
// dependencies.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq"

	"github.com/npmulder/ledgerly/internal/demo"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	"github.com/npmulder/ledgerly/internal/platform/config"
	"github.com/npmulder/ledgerly/internal/platform/db"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
	"github.com/npmulder/ledgerly/web"
)

// MigrationsDirEnv overrides automatic migration-directory discovery.
const MigrationsDirEnv = "LEDGERLY_MIGRATIONS_DIR"

// Config is the runtime input required to build the in-process application.
type Config struct {
	Runtime config.Config
	Version string
}

// Job is a deterministic background unit that can be driven directly in tests.
type Job func(context.Context) error

// ModuleBuilder substitutes a default application module with a fake or custom
// implementation while preserving platform router and bus wiring.
type ModuleBuilder func(context.Context, ModuleDeps) (Module, error)

// ModuleDeps are the platform dependencies available to module builders.
type ModuleDeps struct {
	Logger   *slog.Logger
	Clock    clock.Clock
	Bus      *bus.Bus
	DemoPool *pgxpool.Pool
}

// Module is a module contribution to the HTTP router and in-process bus.
type Module struct {
	HTTPModule      httpserver.Module
	OpenAPIFragment httpserver.OpenAPIFragment
	SubscribeEvents func(*bus.Bus)
}

// Dependencies optionally replace production dependencies. Nil fields use
// production defaults, so cmd/ledgerly and integration tests share this builder.
type Dependencies struct {
	Logger *slog.Logger
	Clock  clock.Clock

	HealthDB     httpserver.Pinger
	HealthCloser io.Closer

	IdentityPool *pgxpool.Pool
	DemoPool     *pgxpool.Pool

	Bus        *bus.Bus
	BusOptions []bus.Option

	OpenSQL  func(driverName, dataSourceName string) (*sql.DB, error)
	OpenPool func(context.Context, string, ...db.PoolOption) (*pgxpool.Pool, error)

	StaticAssets     fs.FS
	LoadStaticAssets func() (fs.FS, error)

	IdentityServiceOptions []identity.ServiceOption
	IdentityHTTPOptions    []identity.HTTPOption

	ModuleBuilders map[string]ModuleBuilder

	Jobs map[string]Job
	// CronAutostart is reserved for future schedulers; tests leave it false so
	// background work is driven explicitly through RunJob.
	CronAutostart bool
}

// App is the fully wired in-process monolith.
type App struct {
	Handler nethttp.Handler
	Bus     *bus.Bus
	Clock   clock.Clock

	HealthDB        httpserver.Pinger
	IdentityPool    *pgxpool.Pool
	DemoPool        *pgxpool.Pool
	IdentityService *identity.Service

	jobs   map[string]Job
	closer func() error
}

// Build wires the Ledgerly platform and modules.
func Build(ctx context.Context, cfg Config, deps Dependencies) (_ *App, err error) {
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	clk := deps.Clock
	if clk == nil {
		clk = clock.New()
	}

	var closeFuncs []func() error
	defer func() {
		if err == nil {
			return
		}
		for i := len(closeFuncs) - 1; i >= 0; i-- {
			err = errors.Join(err, closeFuncs[i]())
		}
	}()

	healthDB, err := healthPinger(cfg.Runtime.DatabaseURL, deps, &closeFuncs)
	if err != nil {
		return nil, err
	}

	openPool := deps.OpenPool
	if openPool == nil {
		openPool = OpenPoolWithRetry
	}

	identityPool, err := modulePool(ctx, cfg.Runtime.DatabaseURL, "identity", deps.IdentityPool, openPool, &closeFuncs)
	if err != nil {
		return nil, err
	}
	demoPool, err := modulePool(ctx, cfg.Runtime.DatabaseURL, demo.ModuleName, deps.DemoPool, openPool, &closeFuncs)
	if err != nil {
		return nil, err
	}

	eventBus := deps.Bus
	if eventBus == nil {
		busOptions := []bus.Option{bus.WithLogger(logger)}
		busOptions = append(busOptions, deps.BusOptions...)
		eventBus = bus.New(busOptions...)
	}

	identityService := identity.NewService(
		identity.NewPostgresStore(identityPool),
		clk,
		deps.IdentityServiceOptions...,
	)
	identityHandler := identity.NewHTTPHandler(identityService, deps.IdentityHTTPOptions...)

	modules := []httpserver.Module{identity.HTTPModule(identityHandler)}
	fragments := []httpserver.OpenAPIFragment{identity.OpenAPIFragment()}

	demoBuilder := buildDemoModule
	if deps.ModuleBuilders != nil && deps.ModuleBuilders[demo.ModuleName] != nil {
		demoBuilder = deps.ModuleBuilders[demo.ModuleName]
	}
	demoModule, err := demoBuilder(ctx, ModuleDeps{
		Logger:   logger,
		Clock:    clk,
		Bus:      eventBus,
		DemoPool: demoPool,
	})
	if err != nil {
		return nil, err
	}
	if demoModule.SubscribeEvents != nil {
		demoModule.SubscribeEvents(eventBus)
	}
	modules = append(modules, demoModule.HTTPModule)
	fragments = append(fragments, demoModule.OpenAPIFragment)

	staticAssets, err := loadStaticAssets(deps)
	if err != nil {
		return nil, err
	}

	router := httpserver.NewRouter(httpserver.Config{
		Version:          cfg.Version,
		Logger:           logger,
		DB:               healthDB,
		Clock:            clk,
		APIAuth:          identity.AuthMiddleware(identityService),
		StaticAssets:     staticAssets,
		Modules:          modules,
		OpenAPIFragments: fragments,
	})

	return &App{
		Handler:         router,
		Bus:             eventBus,
		Clock:           clk,
		HealthDB:        healthDB,
		IdentityPool:    identityPool,
		DemoPool:        demoPool,
		IdentityService: identityService,
		jobs:            copyJobs(deps.Jobs),
		closer: func() error {
			var err error
			for i := len(closeFuncs) - 1; i >= 0; i-- {
				err = errors.Join(err, closeFuncs[i]())
			}
			return err
		},
	}, nil
}

// OpenAPIDocument returns the full application OpenAPI document.
func OpenAPIDocument(version string) map[string]any {
	return httpserver.OpenAPIDocument(
		version,
		identity.OpenAPIFragment(),
		demo.OpenAPIFragment(),
	)
}

func buildDemoModule(_ context.Context, deps ModuleDeps) (Module, error) {
	demoModule, err := demo.New(demo.Config{
		Pool: deps.DemoPool,
		Bus:  deps.Bus,
	})
	if err != nil {
		return Module{}, err
	}
	return Module{
		HTTPModule:      demoModule.HTTPModule(),
		OpenAPIFragment: demoModule.OpenAPIFragment(),
		SubscribeEvents: demoModule.SubscribeEvents,
	}, nil
}

func loadStaticAssets(deps Dependencies) (fs.FS, error) {
	if deps.StaticAssets != nil {
		return deps.StaticAssets, nil
	}
	loader := deps.LoadStaticAssets
	if loader == nil {
		loader = web.Dist
	}
	staticAssets, err := loader()
	if err != nil {
		return nil, fmt.Errorf("load web assets: %w", err)
	}
	return staticAssets, nil
}

func healthPinger(databaseURL string, deps Dependencies, closeFuncs *[]func() error) (httpserver.Pinger, error) {
	if deps.HealthDB != nil {
		if deps.HealthCloser != nil {
			*closeFuncs = append(*closeFuncs, deps.HealthCloser.Close)
		}
		return deps.HealthDB, nil
	}
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("app: database URL is required for health checks")
	}

	openSQL := deps.OpenSQL
	if openSQL == nil {
		openSQL = sql.Open
	}
	sqlDB, err := openSQL("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database handle: %w", err)
	}
	*closeFuncs = append(*closeFuncs, sqlDB.Close)
	return sqlDB, nil
}

func modulePool(
	ctx context.Context,
	databaseURL string,
	module string,
	provided *pgxpool.Pool,
	openPool func(context.Context, string, ...db.PoolOption) (*pgxpool.Pool, error),
	closeFuncs *[]func() error,
) (*pgxpool.Pool, error) {
	if provided != nil {
		return provided, nil
	}
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("app: database URL is required for %s module pool", module)
	}
	pool, err := openPool(ctx, databaseURL, db.WithModule(module))
	if err != nil {
		return nil, fmt.Errorf("open %s database pool: %w", module, err)
	}
	*closeFuncs = append(*closeFuncs, func() error {
		pool.Close()
		return nil
	})
	return pool, nil
}

func copyJobs(jobs map[string]Job) map[string]Job {
	copied := make(map[string]Job, len(jobs))
	for name, job := range jobs {
		if strings.TrimSpace(name) != "" && job != nil {
			copied[name] = job
		}
	}
	return copied
}

// Close releases resources opened by Build. Injected pools remain caller-owned.
func (a *App) Close() error {
	if a == nil || a.closer == nil {
		return nil
	}
	return a.closer()
}

// RunJob runs a named deterministic job.
func (a *App) RunJob(ctx context.Context, name string) error {
	if a == nil {
		return fmt.Errorf("app: nil app")
	}
	job := a.jobs[strings.TrimSpace(name)]
	if job == nil {
		return fmt.Errorf("app: unknown job %q", name)
	}
	return job(ctx)
}

// OpenPoolWithRetry opens a module pool, retrying until ctx is cancelled.
func OpenPoolWithRetry(ctx context.Context, databaseURL string, opts ...db.PoolOption) (*pgxpool.Pool, error) {
	var lastErr error
	for {
		pool, err := db.OpenURL(ctx, databaseURL, opts...)
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

// ResolveMigrationsDir locates db/migrations from the environment or checkout.
func ResolveMigrationsDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(MigrationsDirEnv)); dir != "" {
		return dir, nil
	}

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
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}

		if migrationsDir, ok := findMigrationsDirFrom(dir); ok {
			return migrationsDir, nil
		}
	}

	return "", fmt.Errorf("locate db/migrations: set %s or run ledgerly from a repository checkout", MigrationsDirEnv)
}

func findMigrationsDirFrom(start string) (string, bool) {
	dir := start
	for {
		candidate := filepath.Join(dir, "db", "migrations")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
