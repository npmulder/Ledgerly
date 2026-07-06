package fixtures

import "time"

// TaxYearWindow names an inclusive tax-year date range.
type TaxYearWindow struct {
	Key          string
	Start        time.Time
	EndInclusive time.Time
	EndExclusive time.Time
}

// TaxYear2526 is the Isle of Man pack's 2025-26 window.
var TaxYear2526 = TaxYearWindow{
	Key:          "2025-26",
	Start:        fixtureDate(2025, time.April, 6),
	EndInclusive: fixtureDate(2026, time.April, 5),
	EndExclusive: fixtureDate(2026, time.April, 6),
}

func fixtureDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
