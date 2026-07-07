package invoicing

import (
	"errors"
	"fmt"
	"time"

	"github.com/npmulder/ledgerly/internal/platform/bus"
)

// InvoiceSentName is the bus event name for sent invoice facts.
const InvoiceSentName = "invoicing.InvoiceSent"

// InvoiceSettledName is the bus event name for invoice settlement facts.
const InvoiceSettledName = "invoicing.InvoiceSettled"

// InvoiceOverdueName is the bus event name for invoices that cross the
// overdue boundary.
const InvoiceOverdueName = "invoicing.InvoiceOverdue"

// InvoiceSent is published after a draft invoice is numbered, FX-locked, and
// posted to the ledger inside the send transaction.
type InvoiceSent struct {
	InvoiceID string
	Number    string
	ClientID  string
	Amount    Money
	DueDate   time.Time
}

// Name implements bus.Event.
func (InvoiceSent) Name() string {
	return InvoiceSentName
}

// InvoiceSentFromEvent reads an InvoiceSent payload from the platform bus event.
func InvoiceSentFromEvent(evt bus.Event) (InvoiceSent, error) {
	switch e := evt.(type) {
	case InvoiceSent:
		return e, nil
	case *InvoiceSent:
		if e == nil {
			return InvoiceSent{}, errors.New("invoicing: nil InvoiceSent event")
		}
		return *e, nil
	default:
		return InvoiceSent{}, fmt.Errorf("invoicing: got %T, want InvoiceSent", evt)
	}
}

// InvoiceSettled is the contract consumed by moneyfx to post realised FX.
//
// InvoiceNumber is populated by INV-3 lifecycle publishing so moneyfx can
// validate locks that are owned by immutable invoice numbers. Older contract
// tests that omit it continue to validate against InvoiceID.
type InvoiceSettled struct {
	InvoiceID      string
	InvoiceNumber  string
	LockID         int64
	NativeAmount   Money
	SettlementDate time.Time
	SourceRef      string
}

// Name implements bus.Event.
func (InvoiceSettled) Name() string {
	return InvoiceSettledName
}

// InvoiceSettledFromEvent reads an InvoiceSettled payload from the platform bus event.
func InvoiceSettledFromEvent(evt bus.Event) (InvoiceSettled, error) {
	switch e := evt.(type) {
	case InvoiceSettled:
		return e, nil
	case *InvoiceSettled:
		if e == nil {
			return InvoiceSettled{}, errors.New("invoicing: nil InvoiceSettled event")
		}
		return *e, nil
	default:
		return InvoiceSettled{}, fmt.Errorf("invoicing: got %T, want InvoiceSettled", evt)
	}
}

// InvoiceOverdue is published once per invoice per due-date crossing by the
// overdue sweep.
type InvoiceOverdue struct {
	InvoiceID   string
	DaysOverdue int
}

// Name implements bus.Event.
func (InvoiceOverdue) Name() string {
	return InvoiceOverdueName
}
