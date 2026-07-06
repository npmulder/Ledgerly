package reports

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

// ModuleName is the database schema and event namespace for reports.
const ModuleName = "reports"

var (
	ErrInvalidPeriod = errors.New("reports: invalid period")
	ErrMissingConfig = errors.New("reports: missing config")
)

// Period is an inclusive reporting date range. VATReturn requires this to be a
// calendar quarter until REP-3 exposes user-configurable VAT quarter cadence.
type Period struct {
	From time.Time
	To   time.Time
}

// VATFigures are the VAT return boxes needed for manual IoM filing in v1.
type VATFigures struct {
	Period      Period
	Box1        money.Money
	Box4        money.Money
	Box6        money.Money
	NetPosition money.Money
}

// VATPosition is the current-quarter advisor fact. DueDate is populated from
// REP-3 jurisdiction filing deadlines when Config.CompanyFacts is supplied.
type VATPosition struct {
	Period  Period
	Figures VATFigures
	DueDate *time.Time
}

// LedgerReader is the ledger read capability used by reports.
type LedgerReader interface {
	Entries(context.Context, ledger.EntryFilter) ([]ledger.JournalEntry, error)
}

// InvoiceVATReader is the invoicing read capability used by reports.
type InvoiceVATReader interface {
	InvoiceVATContextBySendEntryID(context.Context, ledger.EntryID) (invoicing.InvoiceVATContext, error)
}

// CompanyFactsFunc supplies REP-3 filing-calendar facts without coupling
// reports to identity.
type CompanyFactsFunc func(context.Context) (jurisdiction.CompanyFacts, error)

// Clock is the time source used by VATPosition.
type Clock interface {
	Now() time.Time
}

// Config contains reports read-model dependencies.
type Config struct {
	Ledger           LedgerReader
	InvoiceVATReader InvoiceVATReader
	Clock            Clock
	CompanyFacts     CompanyFactsFunc
}

// Service computes derived report figures from module read APIs.
type Service struct {
	ledger           LedgerReader
	invoiceVATReader InvoiceVATReader
	clock            Clock
	companyFacts     CompanyFactsFunc
}

// New creates a reports service.
func New(cfg Config) (*Service, error) {
	if cfg.Ledger == nil {
		return nil, fmt.Errorf("reports: ledger reader is required: %w", ErrMissingConfig)
	}
	if cfg.InvoiceVATReader == nil {
		return nil, fmt.Errorf("reports: invoice VAT reader is required: %w", ErrMissingConfig)
	}
	clk := cfg.Clock
	if clk == nil {
		clk = realClock{}
	}
	return &Service{
		ledger:           cfg.Ledger,
		invoiceVATReader: cfg.InvoiceVATReader,
		clock:            clk,
		companyFacts:     cfg.CompanyFacts,
	}, nil
}

// VATQuarterForDate returns the calendar VAT quarter containing date.
func VATQuarterForDate(date time.Time) Period {
	date = dateOnly(date)
	quarterStartMonth := time.Month(((int(date.Month())-1)/3)*3 + 1)
	from := time.Date(date.Year(), quarterStartMonth, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 3, -1)
	return Period{From: from, To: to}
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}
