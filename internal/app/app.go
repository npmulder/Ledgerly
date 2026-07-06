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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq"

	"github.com/npmulder/ledgerly/internal/advisor"
	"github.com/npmulder/ledgerly/internal/banking"
	"github.com/npmulder/ledgerly/internal/dividends"
	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	"github.com/npmulder/ledgerly/internal/platform/config"
	platformcron "github.com/npmulder/ledgerly/internal/platform/cron"
	"github.com/npmulder/ledgerly/internal/platform/db"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
	"github.com/npmulder/ledgerly/internal/platform/mail"
	"github.com/npmulder/ledgerly/internal/reports"
	"github.com/npmulder/ledgerly/web"
)

// MigrationsDirEnv overrides automatic migration-directory discovery.
const MigrationsDirEnv = "LEDGERLY_MIGRATIONS_DIR"

const cronStopTimeout = 10 * time.Second

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
	Logger         *slog.Logger
	Clock          clock.Clock
	Bus            *bus.Bus
	DLAPool        *pgxpool.Pool
	MoneyFXPool    *pgxpool.Pool
	RateLocker     invoicing.RateLocker
	RateLockReader invoicing.RateLockReader
	InvoicingPool  *pgxpool.Pool
	TodayRate      invoicing.TodayRateFunc
	Ledger         ledger.Ledger
	LedgerPool     *pgxpool.Pool
	Identity       identity.Identity
	PDFAssetStore  invoicing.InvoicePDFAssetStore
	PDFEngine      invoicing.InvoicePDFEngine
	PDFBaseURL     string
	MailSender     mail.Sender
}

// Module is a module contribution to the HTTP router and in-process bus.
type Module struct {
	HTTPModule      httpserver.Module
	OpenAPIFragment httpserver.OpenAPIFragment
	ReadAPI         any
	SubscribeEvents func(*bus.Bus)
	ScheduledJobs   []ScheduledJob
}

// ScheduledJob is a module-owned deterministic cron job contribution.
type ScheduledJob struct {
	Name     string
	Schedule string
	Run      Job
}

// Dependencies optionally replace production dependencies. Nil fields use
// production defaults, so cmd/ledgerly and integration tests share this builder.
type Dependencies struct {
	Logger *slog.Logger
	Clock  clock.Clock

	HealthDB     httpserver.Pinger
	HealthCloser io.Closer

	IdentityPool  *pgxpool.Pool
	BankingPool   *pgxpool.Pool
	DLAPool       *pgxpool.Pool
	DividendsPool *pgxpool.Pool
	LedgerPool    *pgxpool.Pool
	MoneyFXPool   *pgxpool.Pool
	InvoicingPool *pgxpool.Pool
	AdvisorPool   *pgxpool.Pool

	Bus        *bus.Bus
	BusOptions []bus.Option

	OpenSQL  func(driverName, dataSourceName string) (*sql.DB, error)
	OpenPool func(context.Context, string, ...db.PoolOption) (*pgxpool.Pool, error)

	StaticAssets     fs.FS
	LoadStaticAssets func() (fs.FS, error)

	IdentityServiceOptions []identity.ServiceOption
	IdentityHTTPOptions    []identity.HTTPOption
	IdentityProfileOptions []identity.ProfileOption

	InvoicingPDFAssetStore invoicing.InvoicePDFAssetStore
	InvoicingPDFEngine     invoicing.InvoicePDFEngine
	InvoicingPDFBaseURL    string
	InvoicingMailSender    mail.Sender

	AdvisorOptions []advisor.ServiceOption

	JurisdictionLoader func(string) error

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
	BankingPool     *pgxpool.Pool
	DLAPool         *pgxpool.Pool
	DividendsPool   *pgxpool.Pool
	LedgerPool      *pgxpool.Pool
	MoneyFXPool     *pgxpool.Pool
	InvoicingPool   *pgxpool.Pool
	AdvisorPool     *pgxpool.Pool
	IdentityService *identity.Service
	AdvisorFacts    advisor.FactRegistry
	Advisor         *advisor.Service

	cron   *platformcron.Runner
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

	loadJurisdiction := deps.JurisdictionLoader
	if loadJurisdiction == nil {
		loadJurisdiction = jurisdiction.LoadActive
	}
	if err := loadJurisdiction(cfg.Runtime.Jurisdiction); err != nil {
		return nil, fmt.Errorf("load jurisdiction pack: %w", err)
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
	ledgerPool, err := modulePool(ctx, cfg.Runtime.DatabaseURL, ledger.ModuleName, deps.LedgerPool, openPool, &closeFuncs)
	if err != nil {
		return nil, err
	}
	dlaPool, err := modulePool(ctx, cfg.Runtime.DatabaseURL, dla.ModuleName, deps.DLAPool, openPool, &closeFuncs)
	if err != nil {
		return nil, err
	}
	moneyFXPool, err := modulePool(ctx, cfg.Runtime.DatabaseURL, moneyfx.ModuleName, deps.MoneyFXPool, openPool, &closeFuncs)
	if err != nil {
		return nil, err
	}
	invoicingPool, err := modulePool(ctx, cfg.Runtime.DatabaseURL, invoicing.ModuleName, deps.InvoicingPool, openPool, &closeFuncs)
	if err != nil {
		return nil, err
	}
	advisorPool, err := modulePool(ctx, cfg.Runtime.DatabaseURL, advisor.ModuleName, deps.AdvisorPool, openPool, &closeFuncs)
	if err != nil {
		return nil, err
	}
	bankingPool, err := modulePool(ctx, cfg.Runtime.DatabaseURL, banking.ModuleName, deps.BankingPool, openPool, &closeFuncs)
	if err != nil {
		return nil, err
	}
	dividendsPool, err := modulePool(ctx, cfg.Runtime.DatabaseURL, dividends.ModuleName, deps.DividendsPool, openPool, &closeFuncs)
	if err != nil {
		return nil, err
	}

	eventBus := deps.Bus
	if eventBus == nil {
		busOptions := []bus.Option{bus.WithLogger(logger)}
		busOptions = append(busOptions, deps.BusOptions...)
		eventBus = bus.New(busOptions...)
	}

	ledgerService := ledger.New(ledgerPool, eventBus)
	trialBalanceStatus := ledger.NewTrialBalanceStatus()
	dlaService := dla.NewWithBusAndClock(dlaPool, eventBus, clk, ledgerService)
	dlaConsistencyStatus := dla.NewConsistencyStatus()
	moneyFXFetcher, err := moneyfx.NewECBFetcher(moneyfx.ECBFetcherConfig{
		Pool:         moneyFXPool,
		Bus:          eventBus,
		Clock:        clk,
		HTTPTimeout:  cfg.Runtime.ECBHTTPTimeout,
		RetryBackoff: moneyfx.DefaultECBRetryBackoff,
	})
	if err != nil {
		return nil, err
	}
	cronRunner := platformcron.New(platformcron.Config{
		Logger: logger,
		Clock:  clk,
	})
	if err := cronRunner.Register(ledger.TrialBalanceJobName, "0 2 * * *", func(ctx context.Context) error {
		_, err := ledgerService.RunTrialBalanceInvariant(ctx, clk.Now(), logger, trialBalanceStatus)
		return err
	}); err != nil {
		return nil, err
	}
	if err := cronRunner.Register(dla.ConsistencyCheckJobName, "10 2 * * *", func(ctx context.Context) error {
		_, err := dlaService.RunConsistencyCheck(ctx, clk.Now(), logger, dlaConsistencyStatus)
		return err
	}); err != nil {
		return nil, err
	}
	if err := cronRunner.Register(moneyfx.ECBFetchJobName, moneyfx.ECBFetchSchedule, moneyFXFetcher.Run); err != nil {
		return nil, err
	}
	identityService := identity.NewService(
		identity.NewPostgresStore(identityPool),
		clk,
		deps.IdentityServiceOptions...,
	)
	profileOptions := []identity.ProfileOption{}
	if strings.TrimSpace(cfg.Runtime.DataDir) != "" {
		profileOptions = append(profileOptions, identity.WithDataDir(cfg.Runtime.DataDir))
	}
	profileOptions = append(profileOptions, deps.IdentityProfileOptions...)
	identityProfile := identity.NewTransactionalProfileService(identityPool, eventBus, profileOptions...)
	identityHTTPOptions := []identity.HTTPOption{identity.WithProfileAPI(identityProfile)}
	identityHTTPOptions = append(identityHTTPOptions, deps.IdentityHTTPOptions...)
	identityHandler := identity.NewHTTPHandler(identityService, identityHTTPOptions...)
	pdfAssetStore := deps.InvoicingPDFAssetStore
	if pdfAssetStore == nil && strings.TrimSpace(cfg.Runtime.DataDir) != "" {
		pdfAssetStore = identityInvoicePDFAssetStore{
			writer:  identity.NewAssetWriter(identityPool, cfg.Runtime.DataDir),
			profile: identityProfile,
		}
	}
	pdfBaseURL := strings.TrimSpace(deps.InvoicingPDFBaseURL)
	if pdfBaseURL == "" {
		pdfBaseURL = localHTTPBaseURL(cfg.Runtime.HTTPAddr)
	}
	mailSender := deps.InvoicingMailSender
	if mailSender == nil {
		mailSender = mail.NewSMTPSenderFromEnv()
	}

	jurisdictionFacts := func(ctx context.Context) (jurisdiction.CompanyFacts, error) {
		facts, err := identityProfile.CompanyFacts(ctx)
		if err != nil {
			return jurisdiction.CompanyFacts{}, err
		}
		return jurisdiction.CompanyFacts{
			IncorporationDate: facts.IncorporationDate,
			YearEnd: jurisdiction.YearEnd{
				Month: facts.YearEnd.Month,
				Day:   facts.YearEnd.Day,
			},
		}, nil
	}

	modules := []httpserver.Module{
		identity.HTTPModule(identityHandler),
		jurisdictionHTTPModule(jurisdictionFacts, clk),
	}
	fragments := []httpserver.OpenAPIFragment{
		identity.OpenAPIFragment(),
		jurisdictionOpenAPIFragment(),
	}

	moneyFXModule, err := moneyfx.New(moneyfx.Config{
		Pool:   moneyFXPool,
		Clock:  clk,
		Ledger: ledgerService,
	})
	if err != nil {
		return nil, err
	}
	moneyFXModule.SubscribeEvents(eventBus)
	modules = append(modules, moneyFXModule.HTTPModule())
	fragments = append(fragments, moneyFXModule.OpenAPIFragment())

	dashboardInvoicingService := invoicing.NewService(
		invoicingPool,
		invoicing.Store{},
		invoicing.WithClock(clk),
		invoicing.WithTodayRate(invoicingTodayRate(moneyFXModule)),
		invoicing.WithRateLocker(invoicingMoneyFXLocker{module: moneyFXModule}),
		invoicing.WithRateLockReader(invoicingMoneyFXLockReader{module: moneyFXModule}),
		invoicing.WithLedger(ledgerService),
		invoicing.WithEventBus(eventBus),
	)
	reportsService := reports.New(
		ledgerService,
		identityProfile,
		dashboardInvoicingService,
		reports.WithClock(clk),
	)
	dividendsService := dividends.New(
		dividendsPool,
		ledgerService,
		reportsService,
		identityProfile,
		dividends.WithClock(clk),
	)
	bankingService := banking.NewService(
		bankingPool,
		ledgerService,
		banking.WithLedgerJournal(ledgerService),
		banking.WithMoneyFX(moneyFXModule),
		banking.WithInvoicingSettler(dashboardInvoicingService),
		banking.WithDLAFileDrawer(dlaService),
		banking.WithEventBus(eventBus),
	)

	ledgerBuilder := buildLedgerModule
	if deps.ModuleBuilders != nil && deps.ModuleBuilders[ledger.ModuleName] != nil {
		ledgerBuilder = deps.ModuleBuilders[ledger.ModuleName]
	}
	ledgerModule, err := ledgerBuilder(ctx, ModuleDeps{
		Logger:        logger,
		Clock:         clk,
		Bus:           eventBus,
		InvoicingPool: invoicingPool,
		LedgerPool:    ledgerPool,
	})
	if err != nil {
		return nil, err
	}
	if ledgerModule.SubscribeEvents != nil {
		ledgerModule.SubscribeEvents(eventBus)
	}
	modules = append(modules, ledgerModule.HTTPModule)
	fragments = append(fragments, ledgerModule.OpenAPIFragment)

	dlaBuilder := buildDLAModule
	if deps.ModuleBuilders != nil && deps.ModuleBuilders[dla.ModuleName] != nil {
		dlaBuilder = deps.ModuleBuilders[dla.ModuleName]
	}
	dlaModule, err := dlaBuilder(ctx, ModuleDeps{
		Logger:     logger,
		Clock:      clk,
		Bus:        eventBus,
		DLAPool:    dlaPool,
		LedgerPool: ledgerPool,
	})
	if err != nil {
		return nil, err
	}
	if dlaModule.SubscribeEvents != nil {
		dlaModule.SubscribeEvents(eventBus)
	}
	modules = append(modules, dlaModule.HTTPModule)
	fragments = append(fragments, dlaModule.OpenAPIFragment)

	invoicingBuilder := buildInvoicingModule
	if deps.ModuleBuilders != nil && deps.ModuleBuilders[invoicing.ModuleName] != nil {
		invoicingBuilder = deps.ModuleBuilders[invoicing.ModuleName]
	}
	invoicingModule, err := invoicingBuilder(ctx, ModuleDeps{
		Logger:         logger,
		Clock:          clk,
		Bus:            eventBus,
		MoneyFXPool:    moneyFXPool,
		RateLocker:     invoicingMoneyFXLocker{module: moneyFXModule},
		RateLockReader: invoicingMoneyFXLockReader{module: moneyFXModule},
		InvoicingPool:  invoicingPool,
		TodayRate:      invoicingTodayRate(moneyFXModule),
		Ledger:         ledgerService,
		LedgerPool:     ledgerPool,
		Identity:       identityProfile,
		PDFAssetStore:  pdfAssetStore,
		PDFEngine:      deps.InvoicingPDFEngine,
		PDFBaseURL:     pdfBaseURL,
		MailSender:     mailSender,
	})
	if err != nil {
		return nil, err
	}
	if invoicingModule.SubscribeEvents != nil {
		invoicingModule.SubscribeEvents(eventBus)
	}
	if err := registerScheduledJobs(cronRunner, invoicingModule.ScheduledJobs); err != nil {
		return nil, err
	}
	modules = append(modules, invoicingModule.HTTPModule)
	fragments = append(fragments, invoicingModule.OpenAPIFragment)

	factProviders := []advisor.RegisteredFactProvider{}
	if invoicingRead, ok := invoicingModule.ReadAPI.(advisor.InvoicingReadAPI); ok {
		factProviders = append(factProviders, advisor.RegisteredFactProvider{
			Name:     invoicing.ModuleName,
			Provider: advisor.NewInvoicingFactProvider(invoicingRead),
		})
	}
	if dlaRead, ok := dlaModule.ReadAPI.(advisor.DLAReadAPI); ok {
		factProviders = append(factProviders, advisor.RegisteredFactProvider{
			Name:     dla.ModuleName,
			Provider: advisor.NewDLAFactProvider(dlaRead),
		})
	}
	factProviders = append(factProviders,
		advisor.RegisteredFactProvider{Name: dividends.ModuleName, Provider: advisor.NewDividendsFactProvider(dividendsService)},
		advisor.RegisteredFactProvider{Name: reports.ModuleName + ".vat", Provider: advisor.NewReportsVATFactProvider(reportsService)},
		advisor.RegisteredFactProvider{Name: reports.ModuleName + ".filings", Provider: advisor.NewReportsFilingFactProvider(reportsService)},
		advisor.RegisteredFactProvider{Name: moneyfx.ModuleName, Provider: advisor.NewMoneyFXFactProvider(moneyFXModule)},
		advisor.RegisteredFactProvider{Name: "identity", Provider: advisor.NewIdentityFactProvider(identityProfile)},
	)
	advisorFacts := advisor.NewFactRegistry(factProviders...)
	advisorOptions := []advisor.ServiceOption{
		advisor.WithClock(clk),
		advisor.WithLogger(logger),
	}
	advisorOptions = append(advisorOptions, deps.AdvisorOptions...)
	advisorService, err := advisor.NewService(advisor.ServiceConfig{
		Pool:  advisorPool,
		Facts: advisorFacts,
	}, advisorOptions...)
	if err != nil {
		return nil, err
	}
	if err := cronRunner.Register(advisor.EvaluateJobName, advisor.EvaluateSchedule, func(ctx context.Context) error {
		_, err := advisorService.RunEvaluation(ctx, advisor.EvaluateJobName)
		return err
	}); err != nil {
		return nil, err
	}
	subscribeAdvisorTriggers(eventBus, advisorService)
	stopAdvisorListener, err := advisorService.StartPostCommitListener(ctx)
	if err != nil {
		return nil, err
	}
	closeFuncs = append(closeFuncs, stopAdvisorListener)

	modules = append(modules, dashboardHTTPModule(dashboardDependencies{
		clock:     clk,
		ledger:    ledgerService,
		moneyFX:   moneyFXModule,
		invoicing: dashboardInvoicingService,
		dla:       dlaService,
		dividends: dividendsService,
		banking:   bankingService,
		identity:  identityProfile,
		principal: identity.PrincipalFromContext,
	}))
	fragments = append(fragments, dashboardOpenAPIFragment())

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
		HealthChecks: []httpserver.HealthCheck{
			{
				Name:  ledger.TrialBalanceJobName,
				Check: trialBalanceStatus.Check,
			},
			{
				Name:  dla.ConsistencyCheckJobName,
				Check: dlaConsistencyStatus.Check,
			},
		},
	})

	if deps.CronAutostart {
		cronRunner.Start()
	}

	return &App{
		Handler:         router,
		Bus:             eventBus,
		Clock:           clk,
		HealthDB:        healthDB,
		IdentityPool:    identityPool,
		BankingPool:     bankingPool,
		DLAPool:         dlaPool,
		DividendsPool:   dividendsPool,
		LedgerPool:      ledgerPool,
		MoneyFXPool:     moneyFXPool,
		InvoicingPool:   invoicingPool,
		AdvisorPool:     advisorPool,
		IdentityService: identityService,
		AdvisorFacts:    advisorFacts,
		Advisor:         advisorService,
		cron:            cronRunner,
		jobs:            copyJobs(deps.Jobs),
		closer: func() error {
			var err error
			if cronRunner != nil {
				stopCtx := cronRunner.Stop()
				waitCtx, cancel := context.WithTimeout(context.Background(), cronStopTimeout)
				select {
				case <-stopCtx.Done():
				case <-waitCtx.Done():
					err = errors.Join(err, fmt.Errorf("stop cron: %w", waitCtx.Err()))
				}
				cancel()
			}
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
		jurisdictionOpenAPIFragment(),
		moneyfx.OpenAPIFragment(),
		ledger.OpenAPIFragment(),
		dla.OpenAPIFragment(),
		invoicing.OpenAPIFragment(),
		dashboardOpenAPIFragment(),
	)
}

func buildLedgerModule(_ context.Context, deps ModuleDeps) (Module, error) {
	ledgerModule, err := ledger.NewModule(ledger.Config{
		Pool:  deps.LedgerPool,
		Bus:   deps.Bus,
		Clock: deps.Clock,
	})
	if err != nil {
		return Module{}, err
	}
	return Module{
		HTTPModule:      ledgerModule.HTTPModule(),
		OpenAPIFragment: ledgerModule.OpenAPIFragment(),
		ReadAPI:         ledgerModule,
	}, nil
}

func buildDLAModule(_ context.Context, deps ModuleDeps) (Module, error) {
	dlaModule, err := dla.NewModule(dla.Config{
		Pool:   deps.DLAPool,
		Bus:    deps.Bus,
		Clock:  deps.Clock,
		Ledger: ledger.New(deps.LedgerPool, deps.Bus),
	})
	if err != nil {
		return Module{}, err
	}
	return Module{
		HTTPModule:      dlaModule.HTTPModule(),
		OpenAPIFragment: dlaModule.OpenAPIFragment(),
		ReadAPI:         dlaModule,
	}, nil
}

func buildInvoicingModule(_ context.Context, deps ModuleDeps) (Module, error) {
	invoicingModule, err := invoicing.New(invoicing.Config{
		Pool:           deps.InvoicingPool,
		Clock:          deps.Clock,
		TodayRate:      deps.TodayRate,
		RateLocker:     deps.RateLocker,
		RateLockReader: deps.RateLockReader,
		Ledger:         deps.Ledger,
		Bus:            deps.Bus,
		Identity:       deps.Identity,
		PDFAssetStore:  deps.PDFAssetStore,
		PDFEngine:      deps.PDFEngine,
		PDFBaseURL:     deps.PDFBaseURL,
		Mailer:         deps.MailSender,
		Logger:         deps.Logger,
	})
	if err != nil {
		return Module{}, err
	}
	return Module{
		HTTPModule:      invoicingModule.HTTPModule(),
		OpenAPIFragment: invoicingModule.OpenAPIFragment(),
		ReadAPI:         invoicingModule,
		ScheduledJobs: []ScheduledJob{{
			Name:     invoicing.OverdueSweepJobName,
			Schedule: invoicing.OverdueSweepSchedule,
			Run:      invoicingModule.RunOverdueSweep,
		}},
	}, nil
}

func registerScheduledJobs(runner *platformcron.Runner, jobs []ScheduledJob) error {
	for _, job := range jobs {
		if err := runner.Register(job.Name, job.Schedule, platformcron.Job(job.Run)); err != nil {
			return err
		}
	}
	return nil
}

func subscribeAdvisorTriggers(eventBus *bus.Bus, service *advisor.Service) {
	if eventBus == nil || service == nil {
		return
	}
	for _, eventName := range []string{
		invoicing.InvoiceOverdueName,
		dla.WentOverdrawnName,
		dla.BackInCreditName,
		dividends.DeclaredName,
		ledger.EntryPostedName,
		moneyfx.RatesStaleName,
		identity.ProfileUpdatedEventName,
	} {
		eventName := eventName
		eventBus.Subscribe(eventName, func(ctx context.Context, tx db.Tx, _ bus.Event) error {
			service.TriggerAfterCommit(ctx, tx, eventName)
			return nil
		})
	}
}

type invoicingMoneyFXLocker struct {
	module *moneyfx.Module
}

func (l invoicingMoneyFXLocker) LockRate(ctx context.Context, tx db.Tx, ref invoicing.RateLockRef, from string, to string, date time.Time) (invoicing.RateLock, error) {
	if l.module == nil {
		return invoicing.RateLock{}, fmt.Errorf("app: moneyfx module is required for invoice rate locks")
	}
	lock, err := l.module.Lock(ctx, tx, moneyfx.LockRef{Module: ref.Module, Ref: ref.Ref}, from, to, date)
	if err != nil {
		return invoicing.RateLock{}, err
	}
	return invoicing.RateLock{
		ID:       int64(lock.ID),
		From:     lock.From,
		To:       lock.To,
		Rate:     lock.Rate,
		RateDate: lock.RateDate,
		Source:   lock.Source,
	}, nil
}

type invoicingMoneyFXLockReader struct {
	module *moneyfx.Module
}

func (r invoicingMoneyFXLockReader) RateLock(ctx context.Context, id int64) (invoicing.RateLock, error) {
	if r.module == nil {
		return invoicing.RateLock{}, fmt.Errorf("app: moneyfx module is required for invoice rate lock reads")
	}
	lock, err := r.module.GetLock(ctx, moneyfx.LockID(id))
	if err != nil {
		return invoicing.RateLock{}, err
	}
	return invoicing.RateLock{
		ID:       int64(lock.ID),
		From:     lock.From,
		To:       lock.To,
		Rate:     lock.Rate,
		RateDate: lock.RateDate,
		Source:   lock.Source,
	}, nil
}

func invoicingTodayRate(m *moneyfx.Module) invoicing.TodayRateFunc {
	return func(ctx context.Context, from string, to string) (invoicing.FXRate, time.Time, error) {
		rate, asOf, err := m.TodayRate(ctx, from, to)
		if err != nil {
			if errors.Is(err, moneyfx.ErrRateUnavailable) {
				return invoicing.FXRate{}, time.Time{}, invoicing.ErrRateUnavailable
			}
			return invoicing.FXRate{}, time.Time{}, err
		}
		return invoicing.FXRate{
			From:     rate.From,
			To:       rate.To,
			Value:    rate.Value,
			RateDate: rate.RateDate,
			Source:   rate.Source,
		}, asOf, nil
	}
}

type identityInvoicePDFAssetStore struct {
	writer  *identity.AssetWriter
	profile interface {
		Asset(context.Context, identity.AssetID) (identity.Asset, error)
	}
}

func (s identityInvoicePDFAssetStore) StoreInvoicePDF(ctx context.Context, pdf []byte) (string, error) {
	if s.writer == nil {
		return "", fmt.Errorf("app: identity asset writer is required for invoice PDFs")
	}
	id, err := s.writer.StoreAsset(ctx, identity.AssetUpload{
		MIME:  "application/pdf",
		Bytes: pdf,
	})
	if err != nil {
		return "", err
	}
	return "/api/identity/assets/" + string(id), nil
}

func (s identityInvoicePDFAssetStore) LoadInvoicePDF(ctx context.Context, assetURL string) ([]byte, error) {
	if s.profile == nil {
		return nil, fmt.Errorf("app: identity profile API is required for invoice PDFs")
	}
	id, err := identityAssetIDFromURL(assetURL)
	if err != nil {
		return nil, err
	}
	asset, err := s.profile.Asset(ctx, id)
	if err != nil {
		return nil, err
	}
	if asset.MIME != "application/pdf" || len(asset.Bytes) == 0 {
		return nil, fmt.Errorf("app: invoice PDF asset is not a PDF")
	}
	return append([]byte{}, asset.Bytes...), nil
}

func identityAssetIDFromURL(assetURL string) (identity.AssetID, error) {
	raw := strings.TrimSpace(assetURL)
	if raw == "" {
		return "", fmt.Errorf("app: invoice PDF asset URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("app: invalid invoice PDF asset URL: %w", err)
	}
	const prefix = "/api/identity/assets/"
	if !strings.HasPrefix(parsed.Path, prefix) {
		return "", fmt.Errorf("app: unsupported invoice PDF asset URL")
	}
	id := strings.TrimPrefix(parsed.Path, prefix)
	if id == "" || strings.Contains(id, "/") {
		return "", fmt.Errorf("app: invalid invoice PDF asset id")
	}
	return identity.AssetID(id), nil
}

func localHTTPBaseURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	if strings.HasPrefix(addr, ":") {
		return "http://127.0.0.1" + addr
	}
	if strings.HasPrefix(addr, "0.0.0.0:") {
		return "http://127.0.0.1:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}
	if strings.HasPrefix(addr, "[::]:") {
		return "http://127.0.0.1:" + strings.TrimPrefix(addr, "[::]:")
	}
	return "http://" + addr
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
	if a.cron != nil && a.cron.HasJob(name) {
		return a.cron.RunNow(ctx, name)
	}
	job := a.jobs[strings.TrimSpace(name)]
	if job == nil {
		return fmt.Errorf("app: unknown job %q", name)
	}
	return job(ctx)
}

// RefreshAdvisorNow runs the manual advisor refresh entry point.
func (a *App) RefreshAdvisorNow(ctx context.Context) (advisor.EvaluationRun, error) {
	if a == nil || a.Advisor == nil {
		return advisor.EvaluationRun{}, fmt.Errorf("app: advisor is not configured")
	}
	return a.Advisor.RefreshNow(ctx)
}

// WaitAdvisorIdle waits for debounced advisor work to drain.
func (a *App) WaitAdvisorIdle(ctx context.Context) error {
	if a == nil || a.Advisor == nil {
		return nil
	}
	return a.Advisor.WaitIdle(ctx)
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
