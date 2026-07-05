package clock

import (
	"testing"
	"time"
)

func TestFakeClockAdvance(t *testing.T) {
	start := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	clock := NewFake(start)

	if got := clock.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %s, want %s", got, start)
	}

	want := start.Add(90 * time.Minute)
	if got := clock.Advance(90 * time.Minute); !got.Equal(want) {
		t.Fatalf("Advance() = %s, want %s", got, want)
	}
	if got := clock.Now(); !got.Equal(want) {
		t.Fatalf("Now() after Advance() = %s, want %s", got, want)
	}

	reset := start.Add(-time.Hour)
	clock.Set(reset)
	if got := clock.Now(); !got.Equal(reset) {
		t.Fatalf("Now() after Set() = %s, want %s", got, reset)
	}
}
