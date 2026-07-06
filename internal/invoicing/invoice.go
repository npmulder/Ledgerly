package invoicing

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

const (
	InvoiceStatusDraft InvoiceStatus = "draft"
	InvoiceStatusSent  InvoiceStatus = "sent"
	InvoiceStatusPaid  InvoiceStatus = "paid"
)

var (
	ErrInvoiceNotFound                 = errors.New("invoicing: invoice not found")
	ErrInvoiceImmutable                = errors.New("invoicing: invoice is immutable")
	ErrRateUnavailable                 = errors.New("invoicing: rate unavailable")
	ErrInvoicePartialPayment           = errors.New("invoicing: partial invoice payments are not supported")
	ErrInvoiceSettlementAmountMismatch = errors.New("invoicing: settlement amount does not match invoice total")
	ErrInvoicePostingNotFound          = errors.New("invoicing: send ledger posting not found")

	decimalQuantityPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
)

// Money is the exact minor-unit value type used by invoice lines and totals.
type Money = money.Money

// FXRate is the exact FX rate type used for draft GBP approximations.
type FXRate struct {
	From     string    `json:"from"`
	To       string    `json:"to"`
	Value    string    `json:"value"`
	RateDate time.Time `json:"rate_date"`
	Source   string    `json:"source"`
}

// Rat parses the exact rate value for use with money.MulRat.
func (r FXRate) Rat() (*big.Rat, error) {
	value := strings.TrimSpace(r.Value)
	if value == "" {
		return nil, fmt.Errorf("invoicing: rate value is required")
	}
	rat, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, fmt.Errorf("invoicing: parse rate %q", r.Value)
	}
	return rat, nil
}

// TodayRateFunc returns a presentation rate for draft GBP approximation.
type TodayRateFunc func(context.Context, string, string) (FXRate, time.Time, error)

// InvoiceStatus is the persisted invoice lifecycle state. Overdue is derived
// from sent invoices and due dates, not stored as a status.
type InvoiceStatus string

// Quantity is a positive decimal invoice line quantity.
type Quantity string

// Invoice is an invoice header, ordered lines, settlement metadata, and
// computed totals. Totals are never stored.
type Invoice struct {
	ID                string        `json:"id"`
	Number            *string       `json:"number"`
	ClientID          string        `json:"client_id"`
	Status            InvoiceStatus `json:"status"`
	IssueDate         time.Time     `json:"issue_date"`
	DueDate           time.Time     `json:"due_date"`
	Currency          Currency      `json:"currency"`
	LockID            *string       `json:"lock_id"`
	SendLedgerEntryID *int64        `json:"-"`
	SentAt            *time.Time    `json:"sent_at,omitempty"`
	VATTreatment      VATTreatment  `json:"vat_treatment"`
	SettlementTxnRef  *string       `json:"settlement_txn_ref"`
	SettledDate       *time.Time    `json:"settled_date"`
	SettledAmount     *Money        `json:"settled_amount"`
	PDFAsset          *string       `json:"pdf_asset"`
	Lines             []InvoiceLine `json:"lines"`
	Totals            InvoiceTotals `json:"totals"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

// InvoiceLine is an ordered invoice row. LineTotal is computed from quantity
// and unit price with money.MulRat round-half-even semantics.
type InvoiceLine struct {
	ID          string   `json:"id"`
	InvoiceID   string   `json:"invoice_id"`
	Position    int      `json:"position"`
	Description string   `json:"description"`
	Qty         Quantity `json:"qty"`
	UnitPrice   Money    `json:"unit_price"`
	LineTotal   Money    `json:"line_total"`
}

// InvoiceLineInput is the caller-supplied shape for replacing draft lines.
type InvoiceLineInput struct {
	Description string   `json:"description"`
	Qty         Quantity `json:"qty"`
	UnitPrice   Money    `json:"unit_price"`
}

// DraftPatch updates mutable draft fields. Nil fields are left unchanged.
// Lines, when set, replace the draft's lines as one ordered list.
type DraftPatch struct {
	IssueDate    *time.Time
	DueDate      *time.Time
	Currency     *Currency
	VATTreatment *VATTreatment
	Lines        *[]InvoiceLineInput
}

// InvoiceSettlement is the one mutable surface reserved for sent/paid invoices.
type InvoiceSettlement struct {
	TxnRef        *string
	SettledDate   *time.Time
	SettledAmount *Money
}

// InvoiceTotals are computed from current invoice lines and VAT treatment.
type InvoiceTotals struct {
	Subtotal  Money             `json:"subtotal"`
	VAT       Money             `json:"vat"`
	Total     Money             `json:"total"`
	ApproxGBP *InvoiceGBPApprox `json:"approx_gbp,omitempty"`
}

// InvoiceGBPApprox is a draft-only presentation note using moneyfx.TodayRate.
type InvoiceGBPApprox struct {
	Amount Money     `json:"amount"`
	Rate   FXRate    `json:"rate"`
	AsOf   time.Time `json:"as_of"`
	Locked bool      `json:"locked"`
}

// InvoiceValidationError collects invoice command validation failures.
type InvoiceValidationError struct {
	Fields []FieldError
}

func (e InvoiceValidationError) Error() string {
	if len(e.Fields) == 0 {
		return "invoice validation failed"
	}
	return fmt.Sprintf("invoice validation failed: %s %s", e.Fields[0].Pointer, e.Fields[0].Detail)
}

func invoiceValidationError(fields []FieldError) error {
	if len(fields) == 0 {
		return nil
	}
	return InvoiceValidationError{Fields: fields}
}

// ParseQuantity validates and normalizes a positive decimal quantity.
func ParseQuantity(value string) (Quantity, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("is required")
	}
	if !decimalQuantityPattern.MatchString(value) {
		return "", fmt.Errorf("must be a positive decimal")
	}
	rat, ok := new(big.Rat).SetString(value)
	if !ok || rat.Sign() <= 0 {
		return "", fmt.Errorf("must be greater than zero")
	}
	return Quantity(value), nil
}

// MustQuantity returns a parsed quantity or panics. It is intended for tests
// and fixtures.
func MustQuantity(value string) Quantity {
	qty, err := ParseQuantity(value)
	if err != nil {
		panic(err)
	}
	return qty
}

func (q Quantity) rat() (*big.Rat, error) {
	parsed, err := ParseQuantity(string(q))
	if err != nil {
		return nil, err
	}
	rat, _ := new(big.Rat).SetString(string(parsed))
	return rat, nil
}
