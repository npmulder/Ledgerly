package demo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

// Store owns demo persistence. All SQL relies on the module search_path.
type Store struct{}

// InsertNote writes the primary note row.
func (Store) InsertNote(ctx context.Context, tx db.Tx, id, body string) (Note, error) {
	const query = `
INSERT INTO notes (id, kind, body)
VALUES ($1, 'note', $2)
RETURNING id, body, created_at`

	note, err := scanSingleNote(tx.QueryRow(ctx, query, id, body))
	if err != nil {
		return Note{}, fmt.Errorf("demo: insert note: %w", err)
	}
	return note, nil
}

// InsertAudit writes the subscriber audit row for a created note.
func (Store) InsertAudit(ctx context.Context, tx db.Tx, id string, evt NoteCreated) error {
	const query = `
INSERT INTO notes (id, kind, note_id, body)
VALUES ($1, 'audit', $2, $3)`

	body := fmt.Sprintf("audit: note %s created with body %q", evt.NoteID, evt.Body)
	if _, err := tx.Exec(ctx, query, id, evt.NoteID, body); err != nil {
		return fmt.Errorf("demo: insert audit row: %w", err)
	}
	return nil
}

// ListNotes reads only user-facing note rows, not audit rows.
func (Store) ListNotes(ctx context.Context, tx db.Tx) ([]Note, error) {
	rows, err := tx.Query(ctx, `
SELECT id, body, created_at
FROM notes
WHERE kind = 'note'
ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("demo: list notes: %w", err)
	}
	defer rows.Close()

	notes, err := pgx.CollectRows(rows, scanNote)
	if err != nil {
		return nil, fmt.Errorf("demo: collect notes: %w", err)
	}
	return notes, nil
}

func scanNote(row pgx.CollectableRow) (Note, error) {
	return scanSingleNote(row)
}

type noteRow interface {
	Scan(dest ...any) error
}

func scanSingleNote(row noteRow) (Note, error) {
	var (
		note      Note
		createdAt time.Time
	)
	if err := row.Scan(&note.ID, &note.Body, &createdAt); err != nil {
		return Note{}, err
	}
	note.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	return note, nil
}
