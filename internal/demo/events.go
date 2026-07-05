package demo

import (
	"context"
	"fmt"

	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

// NoteCreatedName is the canonical bus name for note creation.
const NoteCreatedName = "demo.NoteCreated"

// NoteCreated is published inside the same transaction that inserts a note.
type NoteCreated struct {
	NoteID    string
	Body      string
	CreatedAt string
}

// Name implements bus.Event.
func (NoteCreated) Name() string {
	return NoteCreatedName
}

// AuditFailure optionally injects a subscriber error after the audit row has
// been written. Tests use this to prove transaction rollback across publisher
// and subscriber writes without exposing a failure trigger on the HTTP API.
type AuditFailure func(context.Context, db.Tx, NoteCreated) error

// AuditSubscriber writes an audit row for NoteCreated events.
type AuditSubscriber struct {
	store   Store
	failure AuditFailure
}

// NewAuditSubscriber creates the demo audit subscriber.
func NewAuditSubscriber(store Store, failure AuditFailure) *AuditSubscriber {
	return &AuditSubscriber{store: store, failure: failure}
}

// Subscribe registers the audit handler with the event bus.
func (s *AuditSubscriber) Subscribe(b *bus.Bus) {
	b.Subscribe(NoteCreatedName, s.Handle)
}

// Handle writes an audit row in the publisher's transaction.
func (s *AuditSubscriber) Handle(ctx context.Context, tx db.Tx, evt bus.Event) error {
	created, ok := evt.(NoteCreated)
	if !ok {
		return fmt.Errorf("demo: unexpected event %T", evt)
	}

	auditID, err := newRowID("audit")
	if err != nil {
		return err
	}
	if err := s.store.InsertAudit(ctx, tx, auditID, created); err != nil {
		return err
	}
	if s.failure != nil {
		return s.failure(ctx, tx, created)
	}
	return nil
}
