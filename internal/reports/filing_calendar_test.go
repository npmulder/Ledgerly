package reports

import (
	"context"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/platform/clock"
)

func TestFilingCalendarUsesSeededNPMFacts(t *testing.T) {
	loadIsleOfManPack(t)

	now := testDate(2026, time.July, 5)
	service := newTestService(t, npmFacts(), clock.NewFake(now))

	got, err := service.FilingCalendar()
	if err != nil {
		t.Fatalf("FilingCalendar() error = %v", err)
	}

	want := []struct {
		key       string
		label     string
		authority string
		dueDate   time.Time
		status    FilingStatus
	}{
		{
			key:       "vat_return",
			label:     "VAT return",
			authority: "Isle of Man Customs & Excise",
			dueDate:   testDate(2026, time.July, 30),
			status:    FilingStatusDueSoon,
		},
		{
			key:       "annual_return",
			label:     "Annual return",
			authority: "IoM Companies Registry",
			dueDate:   testDate(2026, time.August, 14),
			status:    FilingStatusUpcoming,
		},
		{
			key:       "personal_tax_return",
			label:     "Personal tax return",
			authority: "IoM Income Tax Division",
			dueDate:   testDate(2026, time.October, 6),
			status:    FilingStatusUpcoming,
		},
		{
			key:     "company_tax_return",
			label:   "Company tax return",
			dueDate: testDate(2027, time.April, 1),
			status:  FilingStatusUpcoming,
		},
	}
	if len(got) != len(want) {
		t.Fatalf("FilingCalendar() length = %d, want %d: %+v", len(got), len(want), got)
	}
	for index, wantFiling := range want {
		assertFiling(t, got[index], wantFiling.key, wantFiling.label, wantFiling.authority, wantFiling.dueDate, daysBetween(now, wantFiling.dueDate), wantFiling.status)
	}
}

func TestFilingCalendarStatusTransitionsUsePackWindow(t *testing.T) {
	loadIsleOfManPack(t)

	window, err := filingDeadlineWarningWindowDays()
	if err != nil {
		t.Fatalf("filingDeadlineWarningWindowDays() error = %v", err)
	}
	dueDate := testDate(2026, time.August, 14)
	tests := []struct {
		name       string
		now        time.Time
		wantStatus FilingStatus
		wantDays   int
	}{
		{
			name:       "before warning window is upcoming",
			now:        dueDate.AddDate(0, 0, -(window + 1)),
			wantStatus: FilingStatusUpcoming,
			wantDays:   window + 1,
		},
		{
			name:       "at warning window is due soon",
			now:        dueDate.AddDate(0, 0, -window),
			wantStatus: FilingStatusDueSoon,
			wantDays:   window,
		},
		{
			name:       "on due date remains due soon",
			now:        dueDate,
			wantStatus: FilingStatusDueSoon,
			wantDays:   0,
		},
		{
			name:       "day after due date is overdue",
			now:        dueDate.AddDate(0, 0, 1),
			wantStatus: FilingStatusOverdue,
			wantDays:   -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newTestService(t, npmFacts(), clock.NewFake(tt.now))
			calendar, err := service.FilingCalendar()
			if err != nil {
				t.Fatalf("FilingCalendar() error = %v", err)
			}
			got, ok := filingByKey(calendar, "annual_return")
			if !ok {
				t.Fatalf("annual_return missing from %+v", calendar)
			}
			assertFiling(t, got, "annual_return", "Annual return", "IoM Companies Registry", dueDate, tt.wantDays, tt.wantStatus)
		})
	}
}

func TestFilingCalendarDaysUntilAcrossYearSweep(t *testing.T) {
	loadIsleOfManPack(t)

	window, err := filingDeadlineWarningWindowDays()
	if err != nil {
		t.Fatalf("filingDeadlineWarningWindowDays() error = %v", err)
	}
	fakeClock := clock.NewFake(testDate(2026, time.January, 1))
	service := newTestService(t, npmFacts(), fakeClock)

	for day := 0; day < 365; day++ {
		now := testDate(2026, time.January, 1).AddDate(0, 0, day)
		fakeClock.Set(now)
		calendar, err := service.FilingCalendar()
		if err != nil {
			t.Fatalf("FilingCalendar() on %s error = %v", now.Format(time.DateOnly), err)
		}
		if len(calendar) != 4 {
			t.Fatalf("FilingCalendar() on %s length = %d, want 4", now.Format(time.DateOnly), len(calendar))
		}
		for _, filing := range calendar {
			wantDays := daysBetween(now, filing.DueDate)
			if filing.DaysUntil != wantDays {
				t.Fatalf("%s on %s DaysUntil = %d, want %d", filing.Key, now.Format(time.DateOnly), filing.DaysUntil, wantDays)
			}
			if got, want := filing.Status, wantStatus(wantDays, window); got != want {
				t.Fatalf("%s on %s Status = %q, want %q", filing.Key, now.Format(time.DateOnly), got, want)
			}
		}
	}
}

func TestFilingCalendarReadsEditedFactsOnNextCall(t *testing.T) {
	loadIsleOfManPack(t)

	provider := &mutableFactsProvider{facts: npmFacts()}
	service, err := NewService(provider, WithClock(clock.NewFake(testDate(2026, time.July, 5))))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	initial, err := service.FilingCalendar()
	if err != nil {
		t.Fatalf("initial FilingCalendar() error = %v", err)
	}
	initialCompanyTax, ok := filingByKey(initial, "company_tax_return")
	if !ok {
		t.Fatalf("company_tax_return missing from %+v", initial)
	}
	if want := testDate(2027, time.April, 1); !initialCompanyTax.DueDate.Equal(want) {
		t.Fatalf("initial company_tax_return DueDate = %s, want %s", initialCompanyTax.DueDate.Format(time.DateOnly), want.Format(time.DateOnly))
	}

	provider.facts.YearEnd = identity.YearEnd{Month: time.December, Day: 31}
	updated, err := service.FilingCalendar()
	if err != nil {
		t.Fatalf("updated FilingCalendar() error = %v", err)
	}
	updatedCompanyTax, ok := filingByKey(updated, "company_tax_return")
	if !ok {
		t.Fatalf("company_tax_return missing from %+v", updated)
	}
	if want := testDate(2027, time.January, 1); !updatedCompanyTax.DueDate.Equal(want) {
		t.Fatalf("updated company_tax_return DueDate = %s, want %s", updatedCompanyTax.DueDate.Format(time.DateOnly), want.Format(time.DateOnly))
	}
	if provider.calls != 2 {
		t.Fatalf("CompanyFacts() calls = %d, want 2", provider.calls)
	}
}

func newTestService(t *testing.T, facts identity.CompanyFacts, clk clock.Clock) *Service {
	t.Helper()

	service, err := NewService(&mutableFactsProvider{facts: facts}, WithClock(clk))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func loadIsleOfManPack(t *testing.T) {
	t.Helper()

	if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("LoadActive(%q) error = %v", jurisdiction.DefaultSelector, err)
	}
}

func npmFacts() identity.CompanyFacts {
	return identity.CompanyFacts{
		IncorporationDate: testDate(2020, time.July, 14),
		YearEnd:           identity.YearEnd{Month: time.March, Day: 31},
	}
}

func testDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func daysBetween(start time.Time, end time.Time) int {
	return int(dateOnly(end).Sub(dateOnly(start)) / (24 * time.Hour))
}

func wantStatus(daysUntil int, window int) FilingStatus {
	if daysUntil < 0 {
		return FilingStatusOverdue
	}
	if daysUntil <= window {
		return FilingStatusDueSoon
	}
	return FilingStatusUpcoming
}

func filingByKey(calendar []Filing, key string) (Filing, bool) {
	for _, filing := range calendar {
		if filing.Key == key {
			return filing, true
		}
	}
	return Filing{}, false
}

func assertFiling(t *testing.T, got Filing, key string, label string, authority string, dueDate time.Time, daysUntil int, status FilingStatus) {
	t.Helper()

	if got.Key != key {
		t.Fatalf("Key = %q, want %q", got.Key, key)
	}
	if got.Label != label {
		t.Fatalf("%s Label = %q, want %q", key, got.Label, label)
	}
	if got.Authority != authority {
		t.Fatalf("%s Authority = %q, want %q", key, got.Authority, authority)
	}
	if !got.DueDate.Equal(dueDate) {
		t.Fatalf("%s DueDate = %s, want %s", key, got.DueDate.Format(time.DateOnly), dueDate.Format(time.DateOnly))
	}
	if got.DaysUntil != daysUntil {
		t.Fatalf("%s DaysUntil = %d, want %d", key, got.DaysUntil, daysUntil)
	}
	if got.Status != status {
		t.Fatalf("%s Status = %q, want %q", key, got.Status, status)
	}
}

type mutableFactsProvider struct {
	facts identity.CompanyFacts
	calls int
}

func (p *mutableFactsProvider) CompanyFacts(context.Context) (identity.CompanyFacts, error) {
	p.calls++
	return p.facts, nil
}
