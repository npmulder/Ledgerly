package reports

import (
	"context"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/platform/clock"
)

func TestVATReturnPreservesLocalQuarterBoundaries(t *testing.T) {
	service := New(newFakeLedger(), nil, fakeInvoicing{})

	bst := time.FixedZone("BST", 60*60)
	figures, err := service.VATReturn(context.Background(), Period{
		From: time.Date(2025, time.July, 1, 0, 0, 0, 0, bst),
		To:   time.Date(2025, time.September, 30, 0, 0, 0, 0, bst),
	})
	if err != nil {
		t.Fatalf("VATReturn() error = %v", err)
	}

	if want := testDate(2025, time.July, 1); !figures.Period.From.Equal(want) {
		t.Fatalf("Period.From = %s, want %s", figures.Period.From.Format(time.DateOnly), want.Format(time.DateOnly))
	}
	if want := testDate(2025, time.September, 30); !figures.Period.To.Equal(want) {
		t.Fatalf("Period.To = %s, want %s", figures.Period.To.Format(time.DateOnly), want.Format(time.DateOnly))
	}
}

func TestVATPositionDueDateMatchesReportedQuarter(t *testing.T) {
	loadIsleOfManPack(t)

	service := New(
		newFakeLedger(),
		fakeIdentity{yearEnd: identityYearEnd(time.March, 31), isVATRegistered: true},
		fakeInvoicing{},
		WithClock(clock.NewFake(testDate(2025, time.April, 15))),
	)

	position, err := service.VATPosition(context.Background())
	if err != nil {
		t.Fatalf("VATPosition() error = %v", err)
	}

	if want := testDate(2025, time.April, 1); !position.Period.From.Equal(want) {
		t.Fatalf("Period.From = %s, want %s", position.Period.From.Format(time.DateOnly), want.Format(time.DateOnly))
	}
	if position.DueDate == nil {
		t.Fatal("DueDate = nil, want current-quarter due date")
	}
	if want := testDate(2025, time.July, 30); !position.DueDate.Equal(want) {
		t.Fatalf("DueDate = %s, want %s", position.DueDate.Format(time.DateOnly), want.Format(time.DateOnly))
	}
}

func identityYearEnd(month time.Month, day int) identity.YearEnd {
	return identity.YearEnd{Month: month, Day: day}
}
