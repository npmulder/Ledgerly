package invoicing

import "time"

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
