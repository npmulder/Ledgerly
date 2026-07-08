package audit

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestDiffValuesOnlyIncludesChangedFields(t *testing.T) {
	diff, err := DiffValues(
		map[string]any{
			"name":       "Contoso",
			"vat_number": nil,
		},
		map[string]any{
			"name":       "Contoso",
			"vat_number": "DE123",
		},
	)
	if err != nil {
		t.Fatalf("DiffValues() error = %v", err)
	}
	if len(diff) != 1 {
		t.Fatalf("diff length = %d, want 1: %#v", len(diff), diff)
	}
	change, ok := diff["vat_number"]
	if !ok {
		t.Fatalf("diff missing vat_number: %#v", diff)
	}
	if change.Before != nil || change.After != "DE123" {
		t.Fatalf("vat_number change = %#v, want nil -> DE123", change)
	}
}

func TestRecorderWritesChangedDiffWithActor(t *testing.T) {
	tx := &recordingTx{}
	recorder := NewRecorder(WithActor(func(context.Context) string {
		return "owner@example.com"
	}))

	err := recorder.Record(
		context.Background(),
		tx,
		"invoicing",
		"client",
		"client_contoso",
		map[string]any{"vat_number": nil},
		map[string]any{"vat_number": "DE123"},
	)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if tx.queryRowCalls != 1 {
		t.Fatalf("query row calls = %d, want 1", tx.queryRowCalls)
	}
	if tx.args[0] != "invoicing" || tx.args[1] != "client" || tx.args[2] != "client_contoso" || tx.args[3] != "owner@example.com" {
		t.Fatalf("insert args = %#v", tx.args)
	}
	var diff Diff
	if err := json.Unmarshal([]byte(tx.args[4].(string)), &diff); err != nil {
		t.Fatalf("unmarshal diff: %v", err)
	}
	if got := diff["vat_number"].After; got != "DE123" {
		t.Fatalf("diff vat_number after = %v, want DE123", got)
	}
}

func TestRecorderSkipsNoopDiff(t *testing.T) {
	tx := &recordingTx{}
	recorder := NewRecorder()

	if err := recorder.Record(context.Background(), tx, "identity", "profile", "1", map[string]any{"trading_name": "Ledgerly"}, map[string]any{"trading_name": "Ledgerly"}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if tx.queryRowCalls != 0 {
		t.Fatalf("query row calls = %d, want 0", tx.queryRowCalls)
	}
}

type recordingTx struct {
	args          []any
	queryRowCalls int
}

func (tx *recordingTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (tx *recordingTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}

func (tx *recordingTx) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	tx.queryRowCalls++
	tx.args = append([]any{}, args...)
	return entryRowStub{args: args}
}

type entryRowStub struct {
	args []any
}

func (row entryRowStub) Scan(dest ...any) error {
	*(dest[0].(*int64)) = 1
	*(dest[1].(*string)) = row.args[0].(string)
	*(dest[2].(*string)) = row.args[1].(string)
	*(dest[3].(*string)) = row.args[2].(string)
	*(dest[4].(*string)) = row.args[3].(string)
	*(dest[5].(*time.Time)) = time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	*(dest[6].(*[]byte)) = []byte(row.args[4].(string))
	return nil
}
