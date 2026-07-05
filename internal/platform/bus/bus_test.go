package bus_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

const invoiceSettledName = "invoicing.InvoiceSettled"

type testEvent struct {
	id string
}

func (testEvent) Name() string {
	return invoiceSettledName
}

func TestPublishDispatchesSubscribersInRegistrationOrder(t *testing.T) {
	b := bus.New(bus.WithLogger(discardLogger()))
	sharedTx := &fakeTx{}
	var calls []string

	for _, name := range []string{"first", "second", "third"} {
		name := name
		b.Subscribe(invoiceSettledName, func(_ context.Context, gotTx db.Tx, evt bus.Event) error {
			if gotTx != sharedTx {
				t.Fatalf("handler tx = %p, want shared tx %p", gotTx, sharedTx)
			}
			if got := evt.(testEvent).id; got != "inv_123" {
				t.Fatalf("event id = %q, want inv_123", got)
			}
			calls = append(calls, name)
			return nil
		})
	}

	if err := b.Publish(context.Background(), sharedTx, testEvent{id: "inv_123"}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	want := []string{"first", "second", "third"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("handler order = %v, want %v", calls, want)
	}
}

func TestPublishPropagatesHandlerError(t *testing.T) {
	b := bus.New(bus.WithLogger(discardLogger()))
	sentinel := errors.New("posting failed")
	calledAfterError := false

	b.Subscribe(invoiceSettledName, func(context.Context, db.Tx, bus.Event) error {
		return sentinel
	})
	b.Subscribe(invoiceSettledName, func(context.Context, db.Tx, bus.Event) error {
		calledAfterError = true
		return nil
	})

	err := b.Publish(context.Background(), &fakeTx{}, testEvent{id: "inv_123"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Publish() error = %v, want sentinel %v", err, sentinel)
	}
	if calledAfterError {
		t.Fatal("handler after error was called")
	}
}

func TestPublishRejectsUnknownEventModule(t *testing.T) {
	b := bus.New(bus.WithLogger(discardLogger()))
	called := false
	b.Subscribe(invoiceSettledName, func(context.Context, db.Tx, bus.Event) error {
		called = true
		return nil
	})

	err := b.Publish(context.Background(), &fakeTx{}, namedEvent("invoicng.InvoiceSettled"))
	if err == nil {
		t.Fatal("Publish() error = nil, want unknown module error")
	}
	if !strings.Contains(err.Error(), `unknown Ledgerly module "invoicng"`) {
		t.Fatalf("Publish() error = %v, want unknown module error", err)
	}
	if called {
		t.Fatal("handler was called for unknown event module")
	}
}

func TestPublishLogsDispatchTelemetry(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, nil))
	b := bus.New(bus.WithLogger(logger))
	b.Subscribe(invoiceSettledName, func(context.Context, db.Tx, bus.Event) error {
		return nil
	})

	if err := b.Publish(context.Background(), &fakeTx{}, testEvent{id: "inv_123"}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	var entry map[string]any
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
		t.Fatalf("dispatch log is not JSON: %v; output=%q", err, out.String())
	}

	if entry["module"] != "invoicing" {
		t.Fatalf("module attr = %v, want invoicing", entry["module"])
	}
	if entry["event"] != invoiceSettledName {
		t.Fatalf("event attr = %v, want %s", entry["event"], invoiceSettledName)
	}
	if entry["handler_count"] != float64(1) {
		t.Fatalf("handler_count attr = %v, want 1", entry["handler_count"])
	}
	if _, ok := entry["duration"]; !ok {
		t.Fatalf("duration attr missing from dispatch log: %v", entry)
	}
}

func TestPublishInsidePostgresTxRollsBackCallerAndHandlers(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("LEDGERLY_TEST_DB"))
	if databaseURL == "" {
		t.Skip("set LEDGERLY_TEST_DB to run Postgres rollback test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.OpenURL(ctx, databaseURL)
	if err != nil {
		t.Fatalf("OpenURL() error = %v", err)
	}
	defer pool.Close()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "CREATE TEMP TABLE bus_caller_writes (id text PRIMARY KEY) ON COMMIT PRESERVE ROWS"); err != nil {
		t.Fatalf("create caller temp table: %v", err)
	}
	if _, err := conn.Exec(ctx, "CREATE TEMP TABLE bus_handler_writes (id text PRIMARY KEY) ON COMMIT PRESERVE ROWS"); err != nil {
		t.Fatalf("create handler temp table: %v", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, "INSERT INTO bus_caller_writes (id) VALUES ($1)", "caller"); err != nil {
		t.Fatalf("insert caller write: %v", err)
	}

	b := bus.New(bus.WithLogger(discardLogger()))
	handlerErr := errors.New("force caller rollback")
	handlerRan := false
	b.Subscribe(invoiceSettledName, func(ctx context.Context, tx db.Tx, evt bus.Event) error {
		handlerRan = true
		if _, err := tx.Exec(ctx, "INSERT INTO bus_handler_writes (id) VALUES ($1)", evt.(testEvent).id); err != nil {
			return err
		}
		return handlerErr
	})

	err = b.Publish(ctx, tx, testEvent{id: "handler"})
	if !errors.Is(err, handlerErr) {
		t.Fatalf("Publish() error = %v, want handlerErr %v", err, handlerErr)
	}
	if !handlerRan {
		t.Fatal("handler was not called")
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	assertCount(t, ctx, conn, "bus_caller_writes", 0)
	assertCount(t, ctx, conn, "bus_handler_writes", 0)
}

func assertCount(t *testing.T, ctx context.Context, tx db.Tx, table string, want int) {
	t.Helper()

	var got int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("count %s = %d, want %d", table, got, want)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeTx struct{}

type namedEvent string

func (e namedEvent) Name() string {
	return string(e)
}

func (*fakeTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("fakeTx.Exec called")
}

func (*fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("fakeTx.Query called")
}

func (*fakeTx) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("fakeTx.QueryRow called")
}
