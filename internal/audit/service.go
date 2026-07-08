package audit

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service reads audit history for the REST API.
type Service struct {
	pool  *pgxpool.Pool
	store Store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, store: Store{}}
}

func (s *Service) History(ctx context.Context, filter HistoryFilter) ([]Entry, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("audit: history requires pool")
	}
	return s.store.History(ctx, s.pool, filter)
}
