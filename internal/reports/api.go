package reports

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/clock"
)

// ModuleName is the database schema and event namespace for reports.
const ModuleName = "reports"

const filingDeadlineFactQuery = "jurisdiction.filing_deadlines"

var (
	ErrInvalidPeriod = errors.New("reports: invalid period")
	ErrMissingConfig = errors.New("reports: missing config")

	filingDeadlineWindowPattern = regexp.MustCompile(`\Adays_until_due\s*<=\s*([0-9]+)\z`)
)

// CompanyFactsProvider is the identity fact surface reports needs to compose
// the filing calendar. Implementations must return fresh facts on each call.
type CompanyFactsProvider interface {
	CompanyFacts(context.Context) (identity.CompanyFacts, error)
}

// CompanyFactsFunc supplies REP-3 filing-calendar facts without coupling VAT
// tests or future advisor callers directly to identity.
type CompanyFactsFunc func(context.Context) (jurisdiction.CompanyFacts, error)

// LedgerReader is the ledger read capability used by reports.
type LedgerReader interface {
	Entries(context.Context, ledger.EntryFilter) ([]ledger.JournalEntry, error)
}

// InvoiceVATReader is the invoicing read capability used by reports.
type InvoiceVATReader interface {
	InvoiceVATContextBySendEntryID(context.Context, ledger.EntryID) (invoicing.InvoiceVATContext, error)
}

// Service composes reports read models from leaf modules.
type Service struct {
	identity CompanyFactsProvider
	clock    clock.Clock

	ledger           LedgerReader
	invoiceVATReader InvoiceVATReader
	vatCompanyFacts  CompanyFactsFunc
}

// Option customizes a reports service.
type Option func(*Service)

// WithClock injects the time source used for deadline status calculations.
func WithClock(clk clock.Clock) Option {
	return func(s *Service) {
		s.clock = clk
	}
}

// WithVATSources injects the module read APIs needed for VAT return figures.
func WithVATSources(ledgerReader LedgerReader, invoiceVATReader InvoiceVATReader) Option {
	return func(s *Service) {
		s.ledger = ledgerReader
		s.invoiceVATReader = invoiceVATReader
	}
}

// WithVATCompanyFacts injects jurisdiction company facts for VATPosition.
func WithVATCompanyFacts(companyFacts CompanyFactsFunc) Option {
	return func(s *Service) {
		s.vatCompanyFacts = companyFacts
	}
}

// Config contains the minimum dependencies needed for VAT-only reports.
type Config struct {
	Ledger           LedgerReader
	InvoiceVATReader InvoiceVATReader
	Clock            clock.Clock
	CompanyFacts     CompanyFactsFunc
}

// New creates a VAT-capable reports service without requiring identity-backed
// filing calendar dependencies.
func New(cfg Config) (*Service, error) {
	if cfg.Ledger == nil {
		return nil, fmt.Errorf("reports: ledger reader is required: %w", ErrMissingConfig)
	}
	if cfg.InvoiceVATReader == nil {
		return nil, fmt.Errorf("reports: invoice VAT reader is required: %w", ErrMissingConfig)
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.New()
	}
	return &Service{
		clock:            clk,
		ledger:           cfg.Ledger,
		invoiceVATReader: cfg.InvoiceVATReader,
		vatCompanyFacts:  cfg.CompanyFacts,
	}, nil
}

// NewService returns a reports service. Filing calendar data is informational
// in v1: reports does not store filing state or track filed/completed actions.
func NewService(identity CompanyFactsProvider, opts ...Option) (*Service, error) {
	if identity == nil {
		return nil, fmt.Errorf("reports: identity facts provider is required")
	}
	service := &Service{
		identity: identity,
		clock:    clock.New(),
	}
	for _, opt := range opts {
		opt(service)
	}
	if service.clock == nil {
		service.clock = clock.New()
	}
	return service, nil
}

// FilingStatus is the deadline state displayed by reports and consumed by
// advisor deadline facts.
type FilingStatus string

const (
	FilingStatusUpcoming FilingStatus = "upcoming"
	FilingStatusDueSoon  FilingStatus = "due-soon"
	FilingStatusOverdue  FilingStatus = "overdue"
)

// Filing is one filing-calendar row enriched for reports and advisor facts.
type Filing struct {
	Key       string
	Label     string
	Authority string
	DueDate   time.Time
	DaysUntil int
	Status    FilingStatus
}

// Period is an inclusive reporting date range. VATReturn requires this to be a
// calendar quarter until VAT cadence becomes configurable.
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
// REP-3 jurisdiction filing deadlines when company facts are available.
type VATPosition struct {
	Period  Period
	Figures VATFigures
	DueDate *time.Time
}

// FilingCalendar returns the current filing calendar using fresh identity
// facts. No filed/completed tracking is included in v1.
func (s *Service) FilingCalendar() ([]Filing, error) {
	return s.FilingCalendarContext(context.Background())
}

// FilingCalendarContext is the context-aware form for internal callers.
func (s *Service) FilingCalendarContext(ctx context.Context) ([]Filing, error) {
	if s == nil {
		return nil, fmt.Errorf("reports: service is nil")
	}
	if s.identity == nil {
		return nil, fmt.Errorf("reports: identity facts provider is required")
	}
	clk := s.clock
	if clk == nil {
		clk = clock.New()
	}

	facts, err := s.identity.CompanyFacts(ctx)
	if err != nil {
		return nil, err
	}
	warningWindowDays, err := filingDeadlineWarningWindowDays()
	if err != nil {
		return nil, err
	}
	jurisdictionFacts := toJurisdictionFacts(facts)
	today := dateOnly(clk.Now())
	deadlines, err := jurisdiction.FilingDeadlinesWithClock(jurisdictionFacts, fixedClock{now: today})
	if err != nil {
		return nil, err
	}
	lookback := today.AddDate(0, 0, -(warningWindowDays + 1))
	candidates, err := jurisdiction.FilingDeadlinesWithClock(jurisdictionFacts, fixedClock{now: lookback})
	if err != nil {
		return nil, err
	}
	candidatesByKey := make(map[string]jurisdiction.Deadline, len(candidates))
	for _, candidate := range candidates {
		candidatesByKey[candidate.Key] = candidate
	}

	filings := make([]Filing, 0, len(deadlines))
	for _, deadline := range deadlines {
		if candidate, ok := candidatesByKey[deadline.Key]; ok && candidate.DueDate.Before(today) {
			deadline = candidate
		}
		daysUntil := wholeDaysBetween(today, deadline.DueDate)
		filings = append(filings, Filing{
			Key:       deadline.Key,
			Label:     deadline.Label,
			Authority: deadline.Authority,
			DueDate:   deadline.DueDate,
			DaysUntil: daysUntil,
			Status:    filingStatus(daysUntil, warningWindowDays),
		})
	}
	sort.Slice(filings, func(i, j int) bool {
		if filings[i].DueDate.Equal(filings[j].DueDate) {
			return filings[i].Key < filings[j].Key
		}
		return filings[i].DueDate.Before(filings[j].DueDate)
	})
	return filings, nil
}

// VATQuarterForDate returns the calendar VAT quarter containing date.
func VATQuarterForDate(date time.Time) Period {
	date = dateOnly(date)
	quarterStartMonth := time.Month(((int(date.Month())-1)/3)*3 + 1)
	from := time.Date(date.Year(), quarterStartMonth, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 3, -1)
	return Period{From: from, To: to}
}

func toJurisdictionFacts(facts identity.CompanyFacts) jurisdiction.CompanyFacts {
	return jurisdiction.CompanyFacts{
		IncorporationDate: facts.IncorporationDate,
		YearEnd: jurisdiction.YearEnd{
			Month: facts.YearEnd.Month,
			Day:   facts.YearEnd.Day,
		},
	}
}

func filingDeadlineWarningWindowDays() (int, error) {
	for _, rule := range jurisdiction.AdvisorRules() {
		if strings.TrimSpace(rule.FactQuery) != filingDeadlineFactQuery {
			continue
		}
		condition := strings.TrimSpace(rule.Condition)
		matches := filingDeadlineWindowPattern.FindStringSubmatch(condition)
		if matches == nil {
			return 0, fmt.Errorf("reports: unsupported filing deadline advisor condition %q", rule.Condition)
		}
		days, err := strconv.Atoi(matches[1])
		if err != nil {
			return 0, fmt.Errorf("reports: filing deadline advisor window %q: %w", matches[1], err)
		}
		return days, nil
	}
	return 0, fmt.Errorf("reports: filing deadline advisor rule is not configured")
}

func filingStatus(daysUntil int, warningWindowDays int) FilingStatus {
	if daysUntil < 0 {
		return FilingStatusOverdue
	}
	if daysUntil <= warningWindowDays {
		return FilingStatusDueSoon
	}
	return FilingStatusUpcoming
}

func wholeDaysBetween(start time.Time, end time.Time) int {
	return int(dateOnly(end).Sub(dateOnly(start)) / (24 * time.Hour))
}

func dateOnly(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}
