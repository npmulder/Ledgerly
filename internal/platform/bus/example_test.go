package bus_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

// internal/invoicing/events.go
type invoiceSettled struct {
	InvoiceID string
}

func (invoiceSettled) Name() string {
	return "invoicing.InvoiceSettled"
}

// internal/invoicing/api.go
type invoicingAPI struct {
	bus *bus.Bus
	tx  db.Tx
}

func (api invoicingAPI) MarkSettled(ctx context.Context, invoiceID string) error {
	return api.bus.Publish(ctx, api.tx, invoiceSettled{InvoiceID: invoiceID})
}

// internal/moneyfx/api.go
type moneyfxAPI struct{}

func (moneyfxAPI) SubscribeEvents(b *bus.Bus) {
	b.Subscribe("invoicing.InvoiceSettled", func(_ context.Context, tx db.Tx, evt bus.Event) error {
		settled := evt.(invoiceSettled)

		// Real code would post realised FX through the ledger API using tx.
		_ = tx
		fmt.Println("moneyfx handled", settled.InvoiceID)
		return nil
	})
}

func Example() {
	b := bus.New(bus.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	moneyfxAPI{}.SubscribeEvents(b)

	invoices := invoicingAPI{bus: b}
	_ = invoices.MarkSettled(context.Background(), "inv_123")

	// Output:
	// moneyfx handled inv_123
}
