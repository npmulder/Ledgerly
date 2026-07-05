package jurisdiction

import (
	"testing"
	"time"
)

func TestFilingDeadlinesResolvesNextOccurrences(t *testing.T) {
	pack := loadTestPack(t)

	tests := []struct {
		name           string
		facts          CompanyFacts
		reference      time.Time
		key            string
		wantDue        time.Time
		wantLabel      string
		wantAuthority  string
		wantRecurrence string
	}{
		{
			name: "year end 31 March company tax return compound offset",
			facts: CompanyFacts{
				IncorporationDate: testDate(2020, time.January, 30),
				YearEnd:           YearEnd{Month: time.March, Day: 31},
			},
			reference:      testDate(2027, time.April, 2),
			key:            "company_tax_return",
			wantDue:        testDate(2028, time.April, 1),
			wantLabel:      "Company tax return",
			wantAuthority:  "Testland Revenue",
			wantRecurrence: "annual",
		},
		{
			name: "30 January incorporation annual return in non leap year",
			facts: CompanyFacts{
				IncorporationDate: testDate(2020, time.January, 30),
				YearEnd:           YearEnd{Month: time.March, Day: 31},
			},
			reference:      testDate(2026, time.January, 1),
			key:            "annual_return",
			wantDue:        testDate(2026, time.February, 28),
			wantLabel:      "Annual return",
			wantAuthority:  "Testland Companies Office",
			wantRecurrence: "annual",
		},
		{
			name: "30 January incorporation annual return in leap year",
			facts: CompanyFacts{
				IncorporationDate: testDate(2020, time.January, 30),
				YearEnd:           YearEnd{Month: time.March, Day: 31},
			},
			reference:      testDate(2028, time.January, 1),
			key:            "annual_return",
			wantDue:        testDate(2028, time.February, 29),
			wantLabel:      "Annual return",
			wantAuthority:  "Testland Companies Office",
			wantRecurrence: "annual",
		},
		{
			name: "VAT from mid second quarter",
			facts: CompanyFacts{
				IncorporationDate: testDate(2020, time.January, 30),
				YearEnd:           YearEnd{Month: time.March, Day: 31},
			},
			reference:      testDate(2026, time.May, 15),
			key:            "vat_return",
			wantDue:        testDate(2026, time.July, 30),
			wantLabel:      "VAT return",
			wantAuthority:  "Testland Customs",
			wantRecurrence: "quarterly",
		},
		{
			name: "VAT from mid third quarter",
			facts: CompanyFacts{
				IncorporationDate: testDate(2020, time.January, 30),
				YearEnd:           YearEnd{Month: time.March, Day: 31},
			},
			reference:      testDate(2026, time.August, 15),
			key:            "vat_return",
			wantDue:        testDate(2026, time.October, 30),
			wantLabel:      "VAT return",
			wantAuthority:  "Testland Customs",
			wantRecurrence: "quarterly",
		},
		{
			name: "accounting year end ignores periods before incorporation",
			facts: CompanyFacts{
				IncorporationDate: testDate(2020, time.January, 30),
				YearEnd:           YearEnd{Month: time.March, Day: 31},
			},
			reference:      testDate(2020, time.February, 1),
			key:            "company_tax_return",
			wantDue:        testDate(2021, time.April, 1),
			wantLabel:      "Company tax return",
			wantAuthority:  "Testland Revenue",
			wantRecurrence: "annual",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deadlines, err := resolveFilingDeadlines(pack, tt.facts, tt.reference)
			if err != nil {
				t.Fatalf("resolveFilingDeadlines() error = %v", err)
			}
			got, ok := deadlineByKey(deadlines, tt.key)
			if !ok {
				t.Fatalf("deadline %q missing from %#v", tt.key, deadlines)
			}
			if !got.DueDate.Equal(tt.wantDue) {
				t.Fatalf("%s DueDate = %s, want %s", tt.key, got.DueDate.Format(time.DateOnly), tt.wantDue.Format(time.DateOnly))
			}
			if got.Label != tt.wantLabel {
				t.Fatalf("%s Label = %q, want %q", tt.key, got.Label, tt.wantLabel)
			}
			if got.Authority != tt.wantAuthority {
				t.Fatalf("%s Authority = %q, want %q", tt.key, got.Authority, tt.wantAuthority)
			}
			if got.Recurrence != tt.wantRecurrence {
				t.Fatalf("%s Recurrence = %q, want %q", tt.key, got.Recurrence, tt.wantRecurrence)
			}
		})
	}
}

func TestFilingDeadlinesPreservesReferenceLocationDate(t *testing.T) {
	pack := loadTestPack(t)
	referenceLocation := time.FixedZone("ahead-of-utc", 60*60)
	deadlines, err := resolveFilingDeadlines(
		pack,
		CompanyFacts{
			IncorporationDate: testDate(2020, time.January, 30),
			YearEnd:           YearEnd{Month: time.March, Day: 31},
		},
		time.Date(2027, time.March, 1, 0, 30, 0, 0, referenceLocation),
	)
	if err != nil {
		t.Fatalf("resolveFilingDeadlines() error = %v", err)
	}

	got, ok := deadlineByKey(deadlines, "annual_return")
	if !ok {
		t.Fatalf("annual_return missing from %#v", deadlines)
	}
	if want := testDate(2028, time.February, 29); !got.DueDate.Equal(want) {
		t.Fatalf("annual_return DueDate = %s, want %s", got.DueDate.Format(time.DateOnly), want.Format(time.DateOnly))
	}
}

func TestDeadlineTaxYearEndUsesPackYearEnd(t *testing.T) {
	pack := &Pack{
		Tax: Tax{
			YearEnd: YearEnd{Month: time.June, Day: 30},
		},
		Filings: map[string]Filing{
			"tax_return": {
				Due:       "tax_year_end + 1 month",
				Authority: "Testland Revenue",
			},
		},
	}

	deadlines, err := resolveFilingDeadlines(
		pack,
		CompanyFacts{
			IncorporationDate: testDate(2020, time.January, 1),
			YearEnd:           YearEnd{Month: time.March, Day: 31},
		},
		testDate(2026, time.January, 1),
	)
	if err != nil {
		t.Fatalf("resolveFilingDeadlines() error = %v", err)
	}

	got, ok := deadlineByKey(deadlines, "tax_return")
	if !ok {
		t.Fatalf("tax_return missing from %#v", deadlines)
	}
	if want := testDate(2026, time.July, 30); !got.DueDate.Equal(want) {
		t.Fatalf("tax_return DueDate = %s, want %s", got.DueDate.Format(time.DateOnly), want.Format(time.DateOnly))
	}
}

func TestFilingDeadlinesWithClockUsesActivePack(t *testing.T) {
	if err := LoadActiveFromFS(testFixtureFS(t), "testland@0.1"); err != nil {
		t.Fatalf("LoadActiveFromFS() error = %v", err)
	}

	deadlines, err := FilingDeadlinesWithClock(
		CompanyFacts{
			IncorporationDate: testDate(2020, time.January, 30),
			YearEnd:           YearEnd{Month: time.March, Day: 31},
		},
		fixedClock{now: testDate(2026, time.May, 15)},
	)
	if err != nil {
		t.Fatalf("FilingDeadlinesWithClock() error = %v", err)
	}

	got, ok := deadlineByKey(deadlines, "vat_return")
	if !ok {
		t.Fatalf("vat_return missing from %#v", deadlines)
	}
	if want := testDate(2026, time.July, 30); !got.DueDate.Equal(want) {
		t.Fatalf("vat_return DueDate = %s, want %s", got.DueDate.Format(time.DateOnly), want.Format(time.DateOnly))
	}
}

func TestDeadlineExpressionMonthArithmetic(t *testing.T) {
	tests := []struct {
		name   string
		start  time.Time
		months int
		want   time.Time
	}{
		{
			name:   "31 January to non leap February",
			start:  testDate(2027, time.January, 31),
			months: 1,
			want:   testDate(2027, time.February, 28),
		},
		{
			name:   "31 January to leap February",
			start:  testDate(2028, time.January, 31),
			months: 1,
			want:   testDate(2028, time.February, 29),
		},
		{
			name:   "31 March plus 12 months before compound day offset",
			start:  testDate(2026, time.March, 31),
			months: 12,
			want:   testDate(2027, time.March, 31),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := addMonthsClamped(tt.start, tt.months); !got.Equal(tt.want) {
				t.Fatalf("addMonthsClamped() = %s, want %s", got.Format(time.DateOnly), tt.want.Format(time.DateOnly))
			}
		})
	}
}

func TestDeadlineExpressionLeapDayAnniversary(t *testing.T) {
	expr, err := parseDeadlineExpression("incorporation_anniversary")
	if err != nil {
		t.Fatalf("parseDeadlineExpression() error = %v", err)
	}

	facts := CompanyFacts{
		IncorporationDate: testDate(2020, time.February, 29),
		YearEnd:           YearEnd{Month: time.March, Day: 31},
	}
	tests := []struct {
		name      string
		reference time.Time
		want      time.Time
	}{
		{
			name:      "non leap year clamps to 28 February",
			reference: testDate(2027, time.January, 1),
			want:      testDate(2027, time.February, 28),
		},
		{
			name:      "leap year keeps 29 February",
			reference: testDate(2028, time.January, 1),
			want:      testDate(2028, time.February, 29),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expr.nextDueDate(facts, tt.reference, testTaxYearEnd())
			if err != nil {
				t.Fatalf("nextDueDate() error = %v", err)
			}
			if !got.Equal(tt.want) {
				t.Fatalf("nextDueDate() = %s, want %s", got.Format(time.DateOnly), tt.want.Format(time.DateOnly))
			}
		})
	}
}

func TestDeadlineExpressionCompoundOffset(t *testing.T) {
	expr, err := parseDeadlineExpression("accounting_year_end + 12 months + 1 day")
	if err != nil {
		t.Fatalf("parseDeadlineExpression() error = %v", err)
	}
	got, err := expr.nextDueDate(
		CompanyFacts{
			IncorporationDate: testDate(2020, time.January, 30),
			YearEnd:           YearEnd{Month: time.March, Day: 31},
		},
		testDate(2026, time.April, 2),
		testTaxYearEnd(),
	)
	if err != nil {
		t.Fatalf("nextDueDate() error = %v", err)
	}
	if want := testDate(2027, time.April, 1); !got.Equal(want) {
		t.Fatalf("nextDueDate() = %s, want %s", got.Format(time.DateOnly), want.Format(time.DateOnly))
	}
}

func FuzzResolveFilingDeadlinesNeverBeforeReference(f *testing.F) {
	pack := loadTestPack(f)
	f.Add(2020, 1, 30, 3, 31, 2026, 5, 15)
	f.Add(2020, 2, 29, 2, 29, 2027, 1, 1)
	f.Add(1999, 12, 31, 12, 31, 2099, 12, 31)

	f.Fuzz(func(t *testing.T, incYear int, incMonth int, incDay int, yearEndMonth int, yearEndDay int, refYear int, refMonth int, refDay int) {
		incYear = normalizedYear(incYear)
		incMonth = normalizedMonth(incMonth)
		incDay = normalizedDay(incYear, time.Month(incMonth), incDay)
		yearEndMonth = normalizedMonth(yearEndMonth)
		yearEndDay = normalizedDay(2024, time.Month(yearEndMonth), yearEndDay)
		refYear = normalizedYear(refYear)
		refMonth = normalizedMonth(refMonth)
		refDay = normalizedDay(refYear, time.Month(refMonth), refDay)

		reference := testDate(refYear, time.Month(refMonth), refDay)
		deadlines, err := resolveFilingDeadlines(pack, CompanyFacts{
			IncorporationDate: testDate(incYear, time.Month(incMonth), incDay),
			YearEnd:           YearEnd{Month: time.Month(yearEndMonth), Day: yearEndDay},
		}, reference)
		if err != nil {
			t.Fatalf("resolveFilingDeadlines() error = %v", err)
		}
		if len(deadlines) == 0 {
			t.Fatal("resolveFilingDeadlines() returned no deadlines")
		}
		for _, deadline := range deadlines {
			if deadline.DueDate.Before(reference) {
				t.Fatalf("%s due date = %s before reference %s", deadline.Key, deadline.DueDate.Format(time.DateOnly), reference.Format(time.DateOnly))
			}
		}
	})
}

func loadTestPack(t testing.TB) *Pack {
	t.Helper()

	pack, err := LoadFromFS(testFixtureFS(t), "testland@0.1")
	if err != nil {
		t.Fatalf("LoadFromFS() error = %v", err)
	}
	return pack
}

func deadlineByKey(deadlines []Deadline, key string) (Deadline, bool) {
	for _, deadline := range deadlines {
		if deadline.Key == key {
			return deadline, true
		}
	}
	return Deadline{}, false
}

func testDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func testTaxYearEnd() YearEnd {
	return YearEnd{Month: time.April, Day: 5}
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

func normalizedYear(year int) int {
	return 1900 + positiveMod(year, 201)
}

func normalizedMonth(month int) int {
	return 1 + positiveMod(month-1, 12)
}

func normalizedDay(year int, month time.Month, day int) int {
	return 1 + positiveMod(day-1, daysInMonth(year, month))
}

func positiveMod(value int, divisor int) int {
	value %= divisor
	if value < 0 {
		value += divisor
	}
	return value
}
