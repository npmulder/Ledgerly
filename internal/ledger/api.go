package ledger

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

// AccountCode is the stable chart-of-accounts identifier used in postings.
type AccountCode string

// AccountID identifies an account row.
type AccountID int64

// AccountType classifies an account for reporting.
type AccountType string

const (
	AccountTypeAsset     AccountType = "asset"
	AccountTypeLiability AccountType = "liability"
	AccountTypeEquity    AccountType = "equity"
	AccountTypeIncome    AccountType = "income"
	AccountTypeExpense   AccountType = "expense"
)

// Account is a chart-of-accounts row.
type Account struct {
	ID        AccountID
	Code      AccountCode
	Name      string
	Type      AccountType
	Currency  *string
	CreatedAt time.Time
}

// AccountSpec describes an account that should exist.
type AccountSpec struct {
	Code     AccountCode
	Name     string
	Type     AccountType
	Currency *string
}

// Ledger exposes the LED-1 account-management surface.
type Ledger interface {
	EnsureAccount(ctx context.Context, tx db.Tx, spec AccountSpec) (AccountCode, error)
	Accounts(ctx context.Context) ([]Account, error)
}

var (
	// ErrInvalidAccountSpec reports a malformed AccountSpec.
	ErrInvalidAccountSpec = errors.New("ledger: invalid account spec")

	// ErrAccountConflict reports an AccountSpec that conflicts with an existing account.
	ErrAccountConflict = errors.New("ledger: account spec conflicts with existing account")
)

// AccountConflictError carries the conflicting field for an existing account.
type AccountConflictError struct {
	Code      AccountCode
	Field     string
	Existing  string
	Requested string
}

func (e *AccountConflictError) Error() string {
	return fmt.Sprintf(
		"ledger: account %s has %s %q, requested %q",
		e.Code,
		e.Field,
		e.Existing,
		e.Requested,
	)
}

func (e *AccountConflictError) Unwrap() error {
	return ErrAccountConflict
}
