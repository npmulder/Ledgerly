package moneyfx

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	"github.com/npmulder/ledgerly/internal/platform/db"
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

// Lock resolves and appends an immutable ECB rate lock inside tx.
func (m *Module) Lock(ctx context.Context, tx db.Tx, ref LockRef, from string, to string, date time.Time) (RateLock, error) {
	return m.service.Lock(ctx, tx, ref, from, to, date)
}

// GetLock returns a stored immutable rate lock by id.
func (m *Module) GetLock(ctx context.Context, id LockID) (RateLock, error) {
	return m.service.GetLock(ctx, id)
}

// ActiveLockFor returns the newest lock row for ref.
func (m *Module) ActiveLockFor(ctx context.Context, ref LockRef) (RateLock, error) {
	return m.service.ActiveLockFor(ctx, ref)
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
