package invoicing

import "time"

// InvoiceSettledName is the bus event name for invoice settlement facts.
const InvoiceSettledName = "invoicing.InvoiceSettled"

// InvoiceSettled is the contract consumed by moneyfx to post realised FX.
//
// Published by invoicing from CV-228 when an invoice settlement is recorded;
// this file intentionally contains only the shared event contract.
type InvoiceSettled struct {
	InvoiceID      string
	LockID         int64
	NativeAmount   Money
	SettlementDate time.Time
	SourceRef      string
}

// Name implements bus.Event.
func (InvoiceSettled) Name() string {
	return InvoiceSettledName
}
