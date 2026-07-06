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
