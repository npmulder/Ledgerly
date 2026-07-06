package reports

import (
	"fmt"

	"github.com/npmulder/ledgerly/internal/platform/clock"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

// Config contains the read APIs required by the reports HTTP module.
type Config struct {
	Ledger    Ledger
	Identity  Identity
	Invoicing Invoicing
	Clock     clock.Clock
}

// Module is the reports HTTP wiring surface used by the app builder.
type Module struct {
	service *Service
}

// NewModule assembles the reports module without registering side effects
// globally. Reports v1 is derived reads only and owns no persistence.
func NewModule(cfg Config) (*Module, error) {
	if cfg.Ledger == nil {
		return nil, fmt.Errorf("reports: ledger provider is required")
	}
	if cfg.Identity == nil {
		return nil, fmt.Errorf("reports: identity provider is required")
	}
	if cfg.Invoicing == nil {
		return nil, fmt.Errorf("reports: invoicing provider is required")
	}
	return &Module{
		service: New(cfg.Ledger, cfg.Identity, cfg.Invoicing, WithClock(cfg.Clock)),
	}, nil
}

// HTTPModule returns the platform route mount for reports read endpoints.
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
