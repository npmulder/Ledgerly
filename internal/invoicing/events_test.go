package invoicing

import (
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/platform/bus"
)

func TestInvoiceSentFromEvent(t *testing.T) {
	want := InvoiceSent{
		InvoiceID: "invoice-123",
		Number:    "INV-2026-123",
		ClientID:  "client-123",
		Amount:    Money{Amount: 123_00, Currency: "GBP"},
		DueDate:   time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
	}

	for _, evt := range []bus.Event{want, &want} {
		got, err := InvoiceSentFromEvent(evt)
		if err != nil {
			t.Fatalf("InvoiceSentFromEvent(%T) error = %v", evt, err)
		}
		if got != want {
			t.Fatalf("InvoiceSentFromEvent(%T) = %#v, want %#v", evt, got, want)
		}
	}
}

func TestInvoiceSettledFromEvent(t *testing.T) {
	want := InvoiceSettled{
		InvoiceID:      "invoice-123",
		InvoiceNumber:  "INV-2026-123",
		LockID:         42,
		NativeAmount:   Money{Amount: 450_000, Currency: "EUR"},
		SettlementDate: time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC),
		SourceRef:      "banking:txn-42",
	}

	for _, evt := range []bus.Event{want, &want} {
		got, err := InvoiceSettledFromEvent(evt)
		if err != nil {
			t.Fatalf("InvoiceSettledFromEvent(%T) error = %v", evt, err)
		}
		if got != want {
			t.Fatalf("InvoiceSettledFromEvent(%T) = %#v, want %#v", evt, got, want)
		}
	}
}

func TestInvoiceEventAccessorsRejectWrongPayload(t *testing.T) {
	if _, err := InvoiceSentFromEvent(InvoiceSettled{}); err == nil {
		t.Fatal("InvoiceSentFromEvent(InvoiceSettled) error = nil")
	}
	if _, err := InvoiceSettledFromEvent(InvoiceSent{}); err == nil {
		t.Fatal("InvoiceSettledFromEvent(InvoiceSent) error = nil")
	}
}
