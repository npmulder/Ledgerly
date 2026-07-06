package dla

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

// ModuleName is the database schema and app wiring key for the DLA module.
const ModuleName = "dla"

// DLAAccountCode is the seeded liability account backing the director's loan.
const DLAAccountCode ledger.AccountCode = "2300-directors-loan"

// EntryID identifies an immutable DLA presentation-ledger entry.
type EntryID int64

// EntryKind classifies a DLA entry and determines its ledger posting shape.
type EntryKind string

const (
	EntryKindDrawing     EntryKind = "drawing"
	EntryKindRepayment   EntryKind = "repayment"
	EntryKindExpenseOwed EntryKind = "expense-owed"
)

// BalanceSide labels a signed running balance using DLA convention.
type BalanceSide string

const (
	BalanceSideCredit BalanceSide = "CR"
	BalanceSideDebit  BalanceSide = "DR"
	BalanceSideZero   BalanceSide = "zero"
)

// TxnRef is the opaque banking-origin payload needed to file a director drawing.
// Amount must already be converted to GBP by the caller.
type TxnRef struct {
	Ref             string
	Date            time.Time
	Amount          money.Money
	CashAccountCode ledger.AccountCode
	Description     string
}

// NewEntry describes a manual DLA entry. AddEntry accepts repayment and
// expense-owed entries; banking-origin drawings use FileDrawing.
type NewEntry struct {
	Date               time.Time
	Kind               EntryKind
	Description        string
	Amount             money.Money
	Source             string
	CashAccountCode    ledger.AccountCode
	ExpenseAccountCode ledger.AccountCode
}

// EntryCursor identifies the last row from a previous Ledger page. Ledger rows
// are ordered by date then id.
type EntryCursor struct {
	Date time.Time
	ID   EntryID
}

// LedgerFilter constrains DLA presentation-ledger browsing.
type LedgerFilter struct {
	From  *time.Time
	To    *time.Time
	After *EntryCursor
	Limit int
}

const (
	// DefaultLedgerLimit is used when LedgerFilter.Limit is zero.
	DefaultLedgerLimit = 100

	// MaxLedgerLimit caps DLA ledger browse pages.
	MaxLedgerLimit = 500
)

// Entry is a stored DLA presentation-ledger row with derived display columns.
// RunningBalance is signed using DLA convention: positive is CR, negative is DR.
type Entry struct {
	ID             EntryID
	Date           time.Time
	Kind           EntryKind
	Description    string
	Amount         money.Money
	Source         string
	OwedToYou      money.Money
	Drawn          money.Money
	RunningBalance money.Money
	BalanceSide    BalanceSide
	CreatedAt      time.Time
}

// DLA exposes the director's loan core API.
type DLA interface {
	FileDrawing(ctx context.Context, tx db.Tx, src TxnRef) error
	AddEntry(ctx context.Context, e NewEntry) error
	Ledger(ctx context.Context, filter LedgerFilter) ([]Entry, error)
}

var (
	// ErrInvalidEntry reports malformed DLA entry input.
	ErrInvalidEntry = errors.New("dla: invalid entry")

	// ErrInvalidLedgerFilter reports malformed DLA ledger query input.
	ErrInvalidLedgerFilter = errors.New("dla: invalid ledger filter")

	// ErrDuplicateSource reports that an entry with the same opaque source ref exists.
	ErrDuplicateSource = errors.New("dla: duplicate source")
)

// DuplicateSourceError carries the duplicated source ref.
type DuplicateSourceError struct {
	Source string
}

func (e *DuplicateSourceError) Error() string {
	return fmt.Sprintf("dla: source %q is already filed", e.Source)
}

func (e *DuplicateSourceError) Unwrap() error {
	return ErrDuplicateSource
}
