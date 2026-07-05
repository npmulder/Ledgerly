package ledger

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

// Service orchestrates ledger account commands and queries.
type Service struct {
	pool  *pgxpool.Pool
	store Store
}

// New creates a ledger service.
func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// EnsureAccount creates spec.Code or returns the existing account code when it is consistent.
func (s *Service) EnsureAccount(ctx context.Context, tx db.Tx, spec AccountSpec) (AccountCode, error) {
	if tx == nil {
		return "", fmt.Errorf("ledger: ensure account requires transaction: %w", ErrInvalidAccountSpec)
	}
	return s.store.EnsureAccount(ctx, tx, spec)
}

// Accounts lists the chart of accounts ordered by code.
func (s *Service) Accounts(ctx context.Context) ([]Account, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("ledger: list accounts requires pool")
	}
	return s.store.ListAccounts(ctx, s.pool)
}
