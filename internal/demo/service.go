package demo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/platform/bus"
)

// ErrEmptyNoteBody reports an invalid create-note command.
var ErrEmptyNoteBody = errors.New("demo: note body is required")

// Service orchestrates demo commands and queries.
type Service struct {
	pool  *pgxpool.Pool
	bus   *bus.Bus
	store Store
}

// NewService creates a demo service.
func NewService(pool *pgxpool.Pool, b *bus.Bus, store Store) *Service {
	return &Service{pool: pool, bus: b, store: store}
}

// CreateNote writes a note and publishes NoteCreated in the same transaction.
func (s *Service) CreateNote(ctx context.Context, input CreateNoteInput) (_ Note, err error) {
	body := strings.TrimSpace(input.Body)
	if body == "" {
		return Note{}, ErrEmptyNoteBody
	}

	noteID, err := newRowID("note")
	if err != nil {
		return Note{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Note{}, fmt.Errorf("demo: begin create note transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	note, err := s.store.InsertNote(ctx, tx, noteID, body)
	if err != nil {
		return Note{}, err
	}

	if err := s.bus.Publish(ctx, tx, NoteCreated{
		NoteID:    note.ID,
		Body:      note.Body,
		CreatedAt: note.CreatedAt,
	}); err != nil {
		return Note{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Note{}, fmt.Errorf("demo: commit create note transaction: %w", err)
	}
	return note, nil
}

// ListNotes returns persisted note rows.
func (s *Service) ListNotes(ctx context.Context) ([]Note, error) {
	return s.store.ListNotes(ctx, s.pool)
}

func newRowID(prefix string) (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("demo: generate %s id: %w", prefix, err)
	}
	return prefix + "_" + hex.EncodeToString(bytes[:]), nil
}
