package dividends

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
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

// WithholdingValidation is the informational dividend withholding rule for a
// validation strip.
type WithholdingValidation struct {
	TaxYear       string `json:"tax_year"`
	Policy        string `json:"policy"`
	Applies       bool   `json:"applies"`
	Informational bool   `json:"informational"`
}

// PersonalTaxValidation is the marginal personal tax set-aside for this
// dividend amount, including before/after estimate breakdowns.
type PersonalTaxValidation struct {
	TaxYear       string                `json:"tax_year"`
	PriorYTD      money.Money           `json:"prior_ytd"`
	WithDividend  money.Money           `json:"with_dividend"`
	PriorEstimate jurisdiction.Estimate `json:"prior_estimate"`
	TotalEstimate jurisdiction.Estimate `json:"total_estimate"`
	Marginal      money.Money           `json:"marginal"`
	Message       string                `json:"message"`
}

// ValidationResult is the validation-strip payload shown before declaration.
type ValidationResult struct {
	Amount             money.Money           `json:"amount"`
	Headroom           HeadroomBreakdown     `json:"headroom"`
	WithinHeadroom     bool                  `json:"within_headroom"`
	Distributable      bool                  `json:"distributable"`
	DistributableTotal money.Money           `json:"distributable_total"`
	Withholding        WithholdingValidation `json:"withholding"`
	PersonalTax        PersonalTaxValidation `json:"personal_tax"`
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
	Validate(context.Context, money.Money) (ValidationResult, error)
	Declare(context.Context, money.Money) (Declaration, error)
	DeclaredInYear(context.Context, string) (money.Money, error)
	History(context.Context) ([]Declaration, error)
}

// Ledger is the ledger read surface dividends needs for retained earnings.
type Ledger interface {
	Post(context.Context, db.Tx, ledger.NewJournalEntry) (ledger.EntryID, error)
	AccountBalance(context.Context, ledger.AccountCode, time.Time) (ledger.AccountBalance, error)
	AccountBalanceInTx(context.Context, db.Tx, ledger.AccountCode, time.Time) (ledger.AccountBalance, error)
}

// Identity is the identity fact surface dividends needs for financial years.
type Identity interface {
	Profile(context.Context) (identity.CompanyProfile, error)
	CompanyFacts(context.Context) (identity.CompanyFacts, error)
}

// DLA is the presentation-ledger append surface dividends needs after posting
// the authoritative ledger entry.
type DLA interface {
	RecordExternalCredit(context.Context, db.Tx, string, time.Time, money.Money, string) error
}

var (
	// ErrInvalidDeclaration reports malformed declaration data.
	ErrInvalidDeclaration = errors.New("dividends: invalid declaration")

	// ErrInvalidFinancialYear reports a malformed financial-year key.
	ErrInvalidFinancialYear = errors.New("dividends: invalid financial year")

	// ErrMissingProvider reports a missing module dependency.
	ErrMissingProvider = errors.New("dividends: missing provider")

	// ErrNonPositiveAmount rejects a zero or negative declaration amount.
	ErrNonPositiveAmount = errors.New("dividends: non-positive amount")

	// ErrOverHeadroom rejects a declaration above distributable headroom.
	ErrOverHeadroom = errors.New("dividends: over headroom")

	// ErrNonDistributableYear rejects declarations in a non-distributable year.
	ErrNonDistributableYear = errors.New("dividends: non-distributable year")
)

// OverHeadroomError carries the available distributable figure.
type OverHeadroomError struct {
	Amount        money.Money
	Distributable money.Money
}

func (e *OverHeadroomError) Error() string {
	return fmt.Sprintf(
		"dividends: declaration %s exceeds distributable reserves %s",
		e.Amount.Format(),
		e.Distributable.Format(),
	)
}

func (e *OverHeadroomError) Unwrap() error {
	return ErrOverHeadroom
}

// NonDistributableYearError carries the current distributable-reserves figure.
type NonDistributableYearError struct {
	FinancialYear string
	Distributable money.Money
}

func (e *NonDistributableYearError) Error() string {
	return fmt.Sprintf(
		"dividends: financial year %s is not distributable; distributable reserves %s",
		e.FinancialYear,
		e.Distributable.Format(),
	)
}

func (e *NonDistributableYearError) Unwrap() error {
	return ErrNonDistributableYear
}

func corporationTaxLabel(rate string) string {
	return fmt.Sprintf("Corporation tax provision at %s", rate)
}
