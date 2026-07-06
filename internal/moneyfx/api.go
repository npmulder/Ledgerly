package moneyfx

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

// Config contains the platform dependencies required by moneyfx.
type Config struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
}

// Module is the moneyfx module wiring surface used by the app builder.
type Module struct {
	service *Service
}

// New assembles the moneyfx module without registering side effects globally.
func New(cfg Config) (*Module, error) {
	if cfg.Pool == nil {
		return nil, fmt.Errorf("moneyfx: pool is required")
	}
	return &Module{
		service: NewService(NewStore(cfg.Pool), cfg.Clock),
	}, nil
}

// RateOn returns the exact FX multiplier for date.
func (m *Module) RateOn(ctx context.Context, date time.Time, from string, to string) (Rate, error) {
	return m.service.RateOn(ctx, date, from, to)
}

// TodayRate returns the latest stored rate and lookup timestamp.
func (m *Module) TodayRate(ctx context.Context, from string, to string) (Rate, time.Time, error) {
	return m.service.TodayRate(ctx, from, to)
}

// ToGBP converts m to presentational GBP using the rate for date.
func (m *Module) ToGBP(ctx context.Context, amount money.Money, date time.Time) (money.Money, error) {
	return m.service.ToGBP(ctx, amount, date)
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

// OpenAPIFragment returns the moneyfx module's OpenAPI contribution without
// requiring database-backed module construction.
func OpenAPIFragment() httpserver.OpenAPIFragment {
	return openAPIFragment()
}
