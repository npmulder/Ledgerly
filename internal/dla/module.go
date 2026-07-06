package dla

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

// Config contains the platform dependencies required by the DLA HTTP module.
type Config struct {
	Pool   *pgxpool.Pool
	Bus    *bus.Bus
	Clock  clock.Clock
	Ledger ledger.Ledger
}

// Module is the DLA HTTP wiring surface used by the app builder.
type Module struct {
	service *Service
	clock   clock.Clock
}

// NewModule assembles the DLA module without registering side effects globally.
func NewModule(cfg Config) (*Module, error) {
	if cfg.Pool == nil {
		return nil, fmt.Errorf("dla: pool is required")
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.New()
	}

	var ledgerServices []ledger.Ledger
	if cfg.Ledger != nil {
		ledgerServices = append(ledgerServices, cfg.Ledger)
	}
	return &Module{
		service: NewWithBusAndClock(cfg.Pool, cfg.Bus, clk, ledgerServices...),
		clock:   clk,
	}, nil
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

// CurrentBalance returns the current DLA balance and advisor-facing status.
func (m *Module) CurrentBalance(ctx context.Context) (money.Money, Status, error) {
	return m.service.CurrentBalance(ctx)
}

// SuggestedClearanceAmount returns the amount needed to clear an overdrawn DLA.
func (m *Module) SuggestedClearanceAmount(ctx context.Context) (money.Money, error) {
	return m.service.SuggestedClearanceAmount(ctx)
}
