package fixtures

import (
	"testing"
	"time"
)

func TestTaxYear2526Window(t *testing.T) {
	if TaxYear2526.Key != "2025-26" {
		t.Fatalf("TaxYear2526.Key = %q, want 2025-26", TaxYear2526.Key)
	}
	if TaxYear2526.Start != fixtureDate(2025, time.April, 6) {
		t.Fatalf("TaxYear2526.Start = %s, want 2025-04-06", TaxYear2526.Start.Format(time.DateOnly))
	}
	if TaxYear2526.EndInclusive != fixtureDate(2026, time.April, 5) {
		t.Fatalf("TaxYear2526.EndInclusive = %s, want 2026-04-05", TaxYear2526.EndInclusive.Format(time.DateOnly))
	}
	if TaxYear2526.EndExclusive != fixtureDate(2026, time.April, 6) {
		t.Fatalf("TaxYear2526.EndExclusive = %s, want 2026-04-06", TaxYear2526.EndExclusive.Format(time.DateOnly))
	}
}

func TestRandUsesStableSeedPerTestName(t *testing.T) {
	first := Rand(t).Int63()
	second := Rand(t).Int63()
	if first != second {
		t.Fatalf("Rand() first draw mismatch: %d != %d", first, second)
	}
}
