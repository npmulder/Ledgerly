// Package demo is a throwaway walking-skeleton module used to prove platform
// wiring before real domain modules are implemented.
package demo

import (
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/platform/bus"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

// ModuleName is the database schema, HTTP route segment, and event namespace.
const ModuleName = "demo"

// Note is the public API representation returned by demo HTTP endpoints.
type Note struct {
	ID        string `json:"id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

// CreateNoteInput is the public command shape for creating a note.
type CreateNoteInput struct {
	Body string
}

// Config contains the platform dependencies required by the demo module.
type Config struct {
	Pool         *pgxpool.Pool
	Bus          *bus.Bus
	AuditFailure AuditFailure
}

// Module is the reference module wiring surface used by cmd/ledgerly.
type Module struct {
	service *Service
	audit   *AuditSubscriber
}

// New assembles the demo module without registering side effects globally.
func New(cfg Config) (*Module, error) {
	if cfg.Pool == nil {
		return nil, fmt.Errorf("demo: pool is required")
	}
	if cfg.Bus == nil {
		return nil, fmt.Errorf("demo: bus is required")
	}

	store := Store{}
	return &Module{
		service: NewService(cfg.Pool, cfg.Bus, store),
		audit:   NewAuditSubscriber(store, cfg.AuditFailure),
	}, nil
}

// SubscribeEvents registers the module's transactional event subscribers.
func (m *Module) SubscribeEvents(b *bus.Bus) {
	m.audit.Subscribe(b)
}

// HTTPModule returns the platform route mount for this module.
func (m *Module) HTTPModule() httpserver.Module {
	return httpserver.Module{
		Name:           ModuleName,
		RegisterRoutes: m.RegisterRoutes,
	}
}

// OpenAPIFragment returns the module's OpenAPI contribution.
func (m *Module) OpenAPIFragment() httpserver.OpenAPIFragment {
	return OpenAPIFragment()
}

// OpenAPIFragment returns the demo module's OpenAPI contribution without
// requiring database-backed module construction.
func OpenAPIFragment() httpserver.OpenAPIFragment {
	return openAPIFragment()
}
