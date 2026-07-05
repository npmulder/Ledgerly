package jurisdiction

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Clock is the minimal time source needed to resolve filing deadlines.
type Clock interface {
	Now() time.Time
}

// YearEnd stores a month/day year-end date. Company accounting year ends mirror
// identity facts without importing identity into this leaf package.
type YearEnd struct {
	Month time.Month
	Day   int
}

// CompanyFacts are caller-supplied company facts used to resolve declarative
// deadline expressions.
type CompanyFacts struct {
	IncorporationDate time.Time
	YearEnd           YearEnd
}

// Deadline is one concrete next filing deadline resolved from the active pack.
type Deadline struct {
	Key        string
	Label      string
	Authority  string
	DueDate    time.Time
	Recurrence string
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

// FilingDeadlines resolves the next occurrence per filing from the active pack.
func FilingDeadlines(facts CompanyFacts) ([]Deadline, error) {
	return FilingDeadlinesWithClock(facts, realClock{})
}

// FilingDeadlinesWithClock resolves filing deadlines with an injected clock.
func FilingDeadlinesWithClock(facts CompanyFacts, clock Clock) ([]Deadline, error) {
	if clock == nil {
		clock = realClock{}
	}

	activePack.RLock()
	pack := clonePack(activePack.pack)
	activePack.RUnlock()

	if pack == nil {
		return nil, fmt.Errorf("jurisdiction: active pack is not loaded")
	}
	return resolveFilingDeadlines(pack, facts, clock.Now())
}

func resolveFilingDeadlines(pack *Pack, facts CompanyFacts, reference time.Time) ([]Deadline, error) {
	if pack == nil {
		return nil, fmt.Errorf("jurisdiction: pack is nil")
	}
	if err := validateCompanyFacts(facts); err != nil {
		return nil, err
	}

	reference = dateOnly(reference)
	keys := make([]string, 0, len(pack.Filings))
	for key := range pack.Filings {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	deadlines := make([]Deadline, 0, len(keys))
	for _, key := range keys {
		filing := pack.Filings[key]
		dueExpression := filing.dueExpression
		if dueExpression == nil {
			parsed, err := parseDeadlineExpression(filing.Due)
			if err != nil {
				return nil, fmt.Errorf("jurisdiction: filing %s due expression: %w", key, err)
			}
			dueExpression = parsed
		}

		dueDate, err := dueExpression.nextDueDate(facts, reference, pack.Tax.YearEnd)
		if err != nil {
			return nil, fmt.Errorf("jurisdiction: filing %s due date: %w", key, err)
		}

		recurrence := filing.Cadence
		if strings.TrimSpace(recurrence) == "" {
			recurrence = dueExpression.defaultRecurrence()
		}

		deadlines = append(deadlines, Deadline{
			Key:        key,
			Label:      filingLabel(key),
			Authority:  filing.Authority,
			DueDate:    dueDate,
			Recurrence: recurrence,
		})
	}

	sort.Slice(deadlines, func(i, j int) bool {
		if deadlines[i].DueDate.Equal(deadlines[j].DueDate) {
			return deadlines[i].Key < deadlines[j].Key
		}
		return deadlines[i].DueDate.Before(deadlines[j].DueDate)
	})

	return deadlines, nil
}

func validateCompanyFacts(facts CompanyFacts) error {
	if facts.IncorporationDate.IsZero() {
		return fmt.Errorf("jurisdiction: incorporation date is required")
	}
	return validateYearEnd(facts.YearEnd)
}

func validateYearEnd(yearEnd YearEnd) error {
	month := int(yearEnd.Month)
	if month < 1 || month > 12 {
		return fmt.Errorf("jurisdiction: year-end month %d out of range", month)
	}
	if yearEnd.Day < 1 || yearEnd.Day > daysInMonth(2024, yearEnd.Month) {
		return fmt.Errorf("jurisdiction: year-end day %d out of range for month %d", yearEnd.Day, month)
	}
	return nil
}

type deadlineAnchor string

const (
	deadlineAnchorIncorporationAnniversary deadlineAnchor = "incorporation_anniversary"
	deadlineAnchorAccountingYearEnd        deadlineAnchor = "accounting_year_end"
	deadlineAnchorQuarterEnd               deadlineAnchor = "quarter_end"
	deadlineAnchorTaxYearEnd               deadlineAnchor = "tax_year_end"
)

type deadlineUnit string

const (
	deadlineUnitDay   deadlineUnit = "day"
	deadlineUnitMonth deadlineUnit = "month"
)

type deadlineOffset struct {
	amount int
	unit   deadlineUnit
}

type deadlineExpression struct {
	raw     string
	anchor  deadlineAnchor
	offsets []deadlineOffset
}

func parseDeadlineExpression(expression string) (*deadlineExpression, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return nil, fmt.Errorf("must not be empty")
	}

	parts := strings.Split(expression, "+")
	anchorToken := strings.TrimSpace(parts[0])
	if len(strings.Fields(anchorToken)) != 1 {
		return nil, fmt.Errorf("deadline anchor must be a single token")
	}
	anchor := deadlineAnchor(anchorToken)
	switch anchor {
	case deadlineAnchorIncorporationAnniversary, deadlineAnchorAccountingYearEnd, deadlineAnchorQuarterEnd, deadlineAnchorTaxYearEnd:
	default:
		return nil, fmt.Errorf("unknown deadline anchor %q", anchorToken)
	}

	offsets := make([]deadlineOffset, 0, len(parts)-1)
	for _, offset := range parts[1:] {
		fields := strings.Fields(strings.TrimSpace(offset))
		if len(fields) != 2 {
			return nil, fmt.Errorf("deadline offsets must use '<number> <unit>'")
		}
		amount, err := strconv.Atoi(fields[0])
		if err != nil || amount <= 0 {
			return nil, fmt.Errorf("deadline offset amount must be a positive integer")
		}
		unit, err := parseDeadlineUnit(fields[1])
		if err != nil {
			return nil, err
		}
		offsets = append(offsets, deadlineOffset{
			amount: amount,
			unit:   unit,
		})
	}

	return &deadlineExpression{
		raw:     expression,
		anchor:  anchor,
		offsets: offsets,
	}, nil
}

func parseDeadlineUnit(value string) (deadlineUnit, error) {
	switch value {
	case "day", "days":
		return deadlineUnitDay, nil
	case "month", "months":
		return deadlineUnitMonth, nil
	default:
		return "", fmt.Errorf("unknown deadline offset unit %q", value)
	}
}

func (e *deadlineExpression) nextDueDate(facts CompanyFacts, reference time.Time, taxYearEnd YearEnd) (time.Time, error) {
	incorporated := dateOnly(facts.IncorporationDate)
	switch e.anchor {
	case deadlineAnchorIncorporationAnniversary:
		return e.nextYearlyDueDate(reference, incorporated, func(year int) (time.Time, error) {
			_, month, day := incorporated.Date()
			return clampedDate(year, month, day)
		})
	case deadlineAnchorAccountingYearEnd:
		return e.nextYearlyDueDate(reference, incorporated, func(year int) (time.Time, error) {
			return clampedDate(year, facts.YearEnd.Month, facts.YearEnd.Day)
		})
	case deadlineAnchorTaxYearEnd:
		if err := validateYearEnd(taxYearEnd); err != nil {
			return time.Time{}, fmt.Errorf("tax year end: %w", err)
		}
		return e.nextYearlyDueDate(reference, incorporated, func(year int) (time.Time, error) {
			return clampedDate(year, taxYearEnd.Month, taxYearEnd.Day)
		})
	case deadlineAnchorQuarterEnd:
		return e.nextQuarterlyDueDate(reference)
	default:
		return time.Time{}, fmt.Errorf("unsupported deadline anchor %q", e.anchor)
	}
}

func (e *deadlineExpression) nextYearlyDueDate(reference, earliestBase time.Time, baseDate func(int) (time.Time, error)) (time.Time, error) {
	span := e.yearSearchSpan()
	startYear := reference.Year() - span
	if !earliestBase.IsZero() && startYear < earliestBase.Year()-1 {
		startYear = earliestBase.Year() - 1
	}

	for year := startYear; ; year++ {
		base, err := baseDate(year)
		if err != nil {
			return time.Time{}, err
		}
		if !earliestBase.IsZero() && base.Before(earliestBase) {
			continue
		}
		due := e.applyOffsets(base)
		if !due.Before(reference) {
			return due, nil
		}
	}
}

func (e *deadlineExpression) nextQuarterlyDueDate(reference time.Time) (time.Time, error) {
	span := e.quarterSearchSpan()
	currentQuarter := quarterIndexFor(reference)
	low := currentQuarter - span
	high := currentQuarter + 8

	for e.dueFromQuarterIndex(high).Before(reference) {
		low = high + 1
		high += span
	}
	for !e.dueFromQuarterIndex(low).Before(reference) {
		high = low
		low -= span
	}

	for low < high {
		mid := low + (high-low)/2
		if e.dueFromQuarterIndex(mid).Before(reference) {
			low = mid + 1
		} else {
			high = mid
		}
	}
	return e.dueFromQuarterIndex(low), nil
}

func (e *deadlineExpression) dueFromQuarterIndex(index int) time.Time {
	return e.applyOffsets(quarterEnd(index))
}

func (e *deadlineExpression) applyOffsets(date time.Time) time.Time {
	for _, offset := range e.offsets {
		switch offset.unit {
		case deadlineUnitDay:
			date = date.AddDate(0, 0, offset.amount)
		case deadlineUnitMonth:
			date = addMonthsClamped(date, offset.amount)
		}
	}
	return dateOnly(date)
}

func (e *deadlineExpression) defaultRecurrence() string {
	if e.anchor == deadlineAnchorQuarterEnd {
		return "quarterly"
	}
	return "annual"
}

func (e *deadlineExpression) yearSearchSpan() int {
	months, days := e.totalOffsets()
	span := 4 + int(months/12) + int(days/366)
	if months%12 != 0 {
		span++
	}
	if days%366 != 0 {
		span++
	}
	if span < 4 {
		return 4
	}
	return span
}

func (e *deadlineExpression) quarterSearchSpan() int {
	months, days := e.totalOffsets()
	span := 8 + int(months/3) + int(days/92)
	if months%3 != 0 {
		span++
	}
	if days%92 != 0 {
		span++
	}
	if span < 8 {
		return 8
	}
	return span
}

func (e *deadlineExpression) totalOffsets() (months int64, days int64) {
	for _, offset := range e.offsets {
		switch offset.unit {
		case deadlineUnitMonth:
			months += int64(offset.amount)
		case deadlineUnitDay:
			days += int64(offset.amount)
		}
	}
	return months, days
}

func dateOnly(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func clampedDate(year int, month time.Month, day int) (time.Time, error) {
	if month < time.January || month > time.December {
		return time.Time{}, fmt.Errorf("month %d out of range", month)
	}
	if day < 1 {
		return time.Time{}, fmt.Errorf("day %d out of range", day)
	}
	maxDay := daysInMonth(year, month)
	if day > maxDay {
		day = maxDay
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
}

// addMonthsClamped applies month offsets left-to-right and clamps overflow days
// to the destination month's end: 31 Jan + 1 month => 28/29 Feb; 29 Feb annual
// anchors clamp to 28 Feb in non-leap years; compounds such as +12 months +1 day
// apply the month clamp before the day offset.
func addMonthsClamped(date time.Time, months int) time.Time {
	year, month, day := date.UTC().Date()
	monthIndex := int(month) - 1 + months
	year += monthIndex / 12
	month = time.Month(monthIndex%12 + 1)

	if day > daysInMonth(year, month) {
		day = daysInMonth(year, month)
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func quarterIndexFor(date time.Time) int {
	year, month, _ := date.UTC().Date()
	return year*4 + (int(month)-1)/3
}

func quarterEnd(index int) time.Time {
	year := index / 4
	quarter := index % 4
	if quarter < 0 {
		quarter += 4
		year--
	}

	switch quarter {
	case 0:
		return time.Date(year, time.March, 31, 0, 0, 0, 0, time.UTC)
	case 1:
		return time.Date(year, time.June, 30, 0, 0, 0, 0, time.UTC)
	case 2:
		return time.Date(year, time.September, 30, 0, 0, 0, 0, time.UTC)
	default:
		return time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC)
	}
}

func filingLabel(key string) string {
	label := strings.ReplaceAll(key, "_", " ")
	if strings.HasPrefix(label, "vat ") {
		label = "VAT " + strings.TrimPrefix(label, "vat ")
	}
	if label == "" || strings.HasPrefix(label, "VAT ") {
		return label
	}
	return strings.ToUpper(label[:1]) + label[1:]
}
