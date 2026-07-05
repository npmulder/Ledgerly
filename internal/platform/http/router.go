package httpserver

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/npmulder/ledgerly/internal/platform/clock"
)

const (
	defaultVersion        = "dev"
	defaultHandlerTimeout = 30 * time.Second
	defaultHealthTimeout  = 2 * time.Second
)

// Pinger is the database capability required by health and readiness checks.
type Pinger interface {
	PingContext(context.Context) error
}

// RegisterRoutes is the module route registration contract.
type RegisterRoutes func(chi.Router)

// Module describes a domain module router mounted by the platform.
type Module struct {
	Name           string
	RegisterRoutes RegisterRoutes
}

// Config controls router construction.
type Config struct {
	Version          string
	Logger           *slog.Logger
	DB               Pinger
	Clock            clock.Clock
	APIAuth          func(nethttp.Handler) nethttp.Handler
	StaticAssets     fs.FS
	Modules          []Module
	OpenAPIFragments []OpenAPIFragment
	HandlerTimeout   time.Duration
	HealthTimeout    time.Duration
}

// NewRouter builds the Ledgerly HTTP router.
func NewRouter(cfg Config) chi.Router {
	cfg = normalizeConfig(cfg)

	r := chi.NewRouter()
	r.Use(requestIDMiddleware)
	r.Use(requestLogMiddleware(cfg.Logger))
	r.Use(recoveryMiddleware(cfg.Logger))
	r.Use(timeoutMiddleware(cfg.HandlerTimeout))
	if cfg.APIAuth != nil {
		r.Use(cfg.APIAuth)
	}

	r.Get("/healthz", healthHandler(cfg))
	r.Get("/readyz", healthHandler(cfg))
	r.Get("/api/openapi.json", openAPIHandler(cfg))

	for _, module := range cfg.Modules {
		MountModule(r, module)
	}
	if cfg.StaticAssets != nil {
		r.NotFound(spaHandler(cfg.StaticAssets))
	}

	return r
}

// MountModule mounts a module's routes under /api/<module>.
func MountModule(r chi.Router, module Module) {
	if module.RegisterRoutes == nil {
		return
	}

	name := strings.Trim(module.Name, "/")
	if name == "" {
		return
	}

	r.Route("/api/"+name, func(moduleRouter chi.Router) {
		module.RegisterRoutes(moduleRouter)
	})
}

func normalizeConfig(cfg Config) Config {
	if cfg.Version == "" {
		cfg.Version = defaultVersion
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.New()
	}
	if cfg.HandlerTimeout == 0 {
		cfg.HandlerTimeout = defaultHandlerTimeout
	}
	if cfg.HealthTimeout == 0 {
		cfg.HealthTimeout = defaultHealthTimeout
	}
	return cfg
}

// Server returns a net/http server with Ledgerly's process-level timeouts.
func Server(addr string, handler nethttp.Handler) *nethttp.Server {
	return &nethttp.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
