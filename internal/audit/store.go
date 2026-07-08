package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

// NewEntry is the payload persisted by Store.Insert.
type NewEntry struct {
	Module   string
	Entity   string
	EntityID string
	Actor    string
	Diff     Diff
}

// Store owns audit persistence.
type Store struct{}

func (Store) Insert(ctx context.Context, tx db.Tx, entry NewEntry) (Entry, error) {
	entry.Module = strings.TrimSpace(entry.Module)
	entry.Entity = strings.TrimSpace(entry.Entity)
	entry.EntityID = strings.TrimSpace(entry.EntityID)
	entry.Actor = strings.TrimSpace(entry.Actor)
	if entry.Module == "" || entry.Entity == "" || entry.EntityID == "" || entry.Actor == "" {
		return Entry{}, fmt.Errorf("audit: module, entity, entity id, and actor are required")
	}
	if len(entry.Diff) == 0 {
		return Entry{}, fmt.Errorf("audit: diff is required")
	}
	diff, err := json.Marshal(entry.Diff)
	if err != nil {
		return Entry{}, fmt.Errorf("audit: marshal diff: %w", err)
	}

	return scanEntryRow(tx.QueryRow(ctx, `
INSERT INTO audit.entries (module, entity, entity_id, actor, diff)
VALUES ($1, $2, $3, $4, $5::jsonb)
RETURNING id, module, entity, entity_id, actor, occurred_at, diff`,
		entry.Module,
		entry.Entity,
		entry.EntityID,
		entry.Actor,
		string(diff),
	))
}

func (Store) History(ctx context.Context, tx db.Tx, filter HistoryFilter) ([]Entry, error) {
	filter.Module = strings.TrimSpace(filter.Module)
	filter.Entity = strings.TrimSpace(filter.Entity)
	filter.EntityID = strings.TrimSpace(filter.EntityID)
	if filter.Module == "" || filter.Entity == "" || filter.EntityID == "" {
		return nil, fmt.Errorf("audit: module, entity, and entity id are required")
	}
	rows, err := tx.Query(ctx, `
SELECT id, module, entity, entity_id, actor, occurred_at, diff
FROM audit.entries
WHERE module = $1
	AND entity = $2
	AND entity_id = $3
ORDER BY occurred_at DESC, id DESC
LIMIT $4`,
		filter.Module,
		filter.Entity,
		filter.EntityID,
		normalizeHistoryLimit(filter.Limit),
	)
	if err != nil {
		return nil, fmt.Errorf("audit: list history: %w", err)
	}
	defer rows.Close()
	entries, err := pgx.CollectRows(rows, scanEntry)
	if err != nil {
		return nil, fmt.Errorf("audit: collect history: %w", err)
	}
	return entries, nil
}

func scanEntry(row pgx.CollectableRow) (Entry, error) {
	return scanEntryRow(row)
}

type entryRow interface {
	Scan(dest ...any) error
}

func scanEntryRow(row entryRow) (Entry, error) {
	var (
		entry Entry
		diff  []byte
	)
	err := row.Scan(
		&entry.ID,
		&entry.Module,
		&entry.Entity,
		&entry.EntityID,
		&entry.Actor,
		&entry.OccurredAt,
		&diff,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, err
	}
	if err != nil {
		return Entry{}, fmt.Errorf("audit: scan entry: %w", err)
	}
	if err := json.Unmarshal(diff, &entry.Diff); err != nil {
		return Entry{}, fmt.Errorf("audit: unmarshal diff: %w", err)
	}
	entry.OccurredAt = entry.OccurredAt.UTC()
	return entry, nil
}
