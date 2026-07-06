package dividends

import (
	"errors"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

func TestNormalizeDeclarationRejectsInconsistentShareTotal(t *testing.T) {
	_, err := normalizeDeclaration(Declaration{
		ID:              "div-1",
		DeclaredDate:    time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC),
		Amount:          money.Money{Amount: 100_000, Currency: "GBP"},
		PerShare:        money.Money{Amount: 333, Currency: "GBP"},
		Shares:          300,
		ShareholderName: "N. Meyer",
	})
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("normalizeDeclaration() error = %v, want ErrInvalidDeclaration", err)
	}
}

func TestPerShareAmountUsesDirectDivisibilityForLargeShareCounts(t *testing.T) {
	got, err := perShareAmount(money.Money{Amount: 9_223_372_036_854_775_806, Currency: "GBP"}, 9_223_372_036_854_775_806)
	if err != nil {
		t.Fatalf("perShareAmount() error = %v", err)
	}
	if got != (money.Money{Amount: 1, Currency: "GBP"}) {
		t.Fatalf("perShareAmount() = %+v, want GBP 0.01", got)
	}
}

func TestPerShareAmountRejectsNonUniformSplit(t *testing.T) {
	_, err := perShareAmount(money.Money{Amount: 100, Currency: "GBP"}, 3)
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("perShareAmount() error = %v, want ErrInvalidDeclaration", err)
	}
}
