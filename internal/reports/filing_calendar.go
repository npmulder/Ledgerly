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
	if s.facts == nil {
		return nil, fmt.Errorf("reports: identity facts provider is required")
	}
	clk := s.clock
	if clk == nil {
		clk = clock.New()
	}

	facts, err := s.facts.CompanyFacts(ctx)
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
		if deadline.RequiresVATRegistration && !facts.IsVATRegistered {
			continue
		}
		if candidate, ok := candidatesByKey[deadline.Key]; ok && candidate.DueDate.Before(today) {
			deadline = candidate
		}
		daysUntil := wholeDaysBetween(today, deadline.DueDate)
		filings = append(filings, Filing{
			Key:        deadline.Key,
			Label:      deadline.Label,
			Authority:  deadline.Authority,
			DueDate:    deadline.DueDate,
			DaysUntil:  daysUntil,
			Status:     filingStatus(daysUntil, warningWindowDays),
			WarnWindow: warningWindowDays,
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

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}
