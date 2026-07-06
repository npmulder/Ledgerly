package dividends

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

// ModuleName is the database schema and app wiring key for dividends.
const ModuleName = "dividends"

// RetainedEarningsAccountCode is the seeded equity account used for headroom.
const RetainedEarningsAccountCode ledger.AccountCode = "3000-retained-earnings"

const (
	retainedEarningsLineLabel = "Retained earnings b/fwd"
	profitYTDLineLabel        = "Profit YTD (after expenses)"
	dividendsDeclaredLabel    = "Dividends already declared YTD"
	availableHeadroomLabel    = "Available to distribute"
)

// MoneyLine is one labeled monetary row in the headroom calculation.
type MoneyLine struct {
	Label  string      `json:"label"`
	Amount money.Money `json:"amount"`
}

// HeadroomBreakdown is the live distributable-reserves calculation.
type HeadroomBreakdown struct {
	AsOf          time.Time   `json:"as_of"`
	FinancialYear string      `json:"financial_year"`
	Lines         []MoneyLine `json:"lines"`
	Available     money.Money `json:"available"`
	Distributable bool        `json:"distributable"`
}

// DeclarationID identifies an immutable dividend declaration.
type DeclarationID string

// Declaration is the persisted declaration read model.
type Declaration struct {
	ID              DeclarationID     `json:"id"`
	DeclaredDate    time.Time         `json:"declared_date"`
	Amount          money.Money       `json:"amount"`
	PerShare        money.Money       `json:"per_share"`
	Shares          int64             `json:"shares"`
	ShareholderName string            `json:"shareholder_name"`
	VoucherAsset    *identity.AssetID `json:"voucher_asset,omitempty"`
	MinutesAsset    *identity.AssetID `json:"minutes_asset,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
}

// Dividends exposes the dividend read API for advisor/UI consumers.
type Dividends interface {
	Headroom(context.Context) (HeadroomBreakdown, error)
	DeclaredInYear(context.Context, string) (money.Money, error)
	History(context.Context) ([]Declaration, error)
}

// Ledger is the ledger read surface dividends needs for retained earnings.
type Ledger interface {
	AccountBalance(context.Context, ledger.AccountCode, time.Time) (ledger.AccountBalance, error)
}

// Identity is the identity fact surface dividends needs for financial years.
type Identity interface {
	CompanyFacts(context.Context) (identity.CompanyFacts, error)
}

var (
	// ErrInvalidDeclaration reports malformed declaration data.
	ErrInvalidDeclaration = errors.New("dividends: invalid declaration")

	// ErrInvalidFinancialYear reports a malformed financial-year key.
	ErrInvalidFinancialYear = errors.New("dividends: invalid financial year")

	// ErrMissingProvider reports a missing module dependency.
	ErrMissingProvider = errors.New("dividends: missing provider")
)

func corporationTaxLabel(rate string) string {
	return fmt.Sprintf("Corporation tax provision at %s", rate)
}
