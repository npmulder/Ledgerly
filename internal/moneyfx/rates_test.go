package moneyfx

import (
	"context"
	"errors"
	"testing"
)

func TestRateRatParsesExactValue(t *testing.T) {
	t.Parallel()

	rat, err := (Rate{Value: "4/5"}).Rat()
	if err != nil {
		t.Fatalf("Rat() error = %v", err)
	}
	if rat.Num().Int64() != 4 || rat.Denom().Int64() != 5 {
		t.Fatalf("Rat() = %s, want 4/5", rat.String())
	}
}

func TestTodayRateIdentityAndUnavailable(t *testing.T) {
	t.Parallel()

	rate, _, err := TodayRate(context.Background(), "gbp", "GBP")
	if err != nil {
		t.Fatalf("TodayRate(identity) error = %v", err)
	}
	if rate.From != "GBP" || rate.To != "GBP" || rate.Value != "1" {
		t.Fatalf("identity rate = %+v, want GBP->GBP value 1", rate)
	}

	_, _, err = TodayRate(context.Background(), "EUR", "GBP")
	if !errors.Is(err, ErrRateUnavailable) {
		t.Fatalf("TodayRate(EUR,GBP) error = %v, want ErrRateUnavailable", err)
	}
}
