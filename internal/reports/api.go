package reports

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/platform/clock"
)

const filingDeadlineRuleID = "filing_deadline_window"

var filingDeadlineWindowPattern = regexp.MustCompile(`\Adue_date\s*-\s*today\s*<=\s*([0-9]+)\z`)

// CompanyFactsProvider is the identity fact surface reports needs to compose
// the filing calendar. Implementations must return fresh facts on each call.
type CompanyFactsProvider interface {
	CompanyFacts(context.Context) (identity.CompanyFacts, error)
}

// Service composes reports read models from leaf modules.
type Service struct {
	identity CompanyFactsProvider
	clock    clock.Clock
}

// Option customizes a reports service.
type Option func(*Service)

// WithClock injects the time source used for deadline status calculations.
func WithClock(clk clock.Clock) Option {
	return func(s *Service) {
		s.clock = clk
	}
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
		if strings.TrimSpace(rule.ID) != filingDeadlineRuleID {
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
