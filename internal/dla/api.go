package dla

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/npmulder/ledgerly/internal/jurisdiction"
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

// Status is the advisor-facing DLA state. Zero balance counts as credit.
type Status string

const (
	StatusCredit    Status = "credit"
	StatusOverdrawn Status = "overdrawn"
)

// PolicyPayload is the pack-sourced context callers need to render DLA status
// without hard-coding jurisdiction rule keys.
type PolicyPayload struct {
	S455Charge               bool   `json:"s455_charge"`
	CreditStatusText         string `json:"credit_status_text"`
	CreditExplainerTemplate  string `json:"credit_explainer_template"`
	BIKWarningTextKey        string `json:"bik_warning_text_key"`
	OverdrawnWarningTemplate string `json:"overdrawn_warning_template"`
	Remedy                   string `json:"remedy"`
}

// StatusPayload is the advisor fact payload for the current DLA state.
type StatusPayload struct {
	Balance                  money.Money   `json:"balance"`
	Status                   Status        `json:"status"`
	Policy                   PolicyPayload `json:"policy"`
	SuggestedClearanceAmount money.Money   `json:"suggested_clearance_amount"`
}

// TxnRef is the opaque banking-origin payload needed to file a director drawing.
// Amount is the GBP DLA presentation amount. CashAmount is the positive native
// amount that left the bank account; zero defaults to Amount for GBP cash.
type TxnRef struct {
	Ref             string
	Date            time.Time
	Amount          money.Money
	CashAmount      money.Money
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
	CashAmount         money.Money
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
	RecordExternalCredit(ctx context.Context, tx db.Tx, ref string, date time.Time, amount money.Money, description string) error
	AddEntry(ctx context.Context, e NewEntry) error
	Ledger(ctx context.Context, filter LedgerFilter) ([]Entry, error)
	CurrentBalance(ctx context.Context) (money.Money, Status, error)
	CurrentStatus(ctx context.Context) (StatusPayload, error)
	SuggestedClearanceAmount(ctx context.Context) (money.Money, error)
}

var (
	// ErrInvalidEntry reports malformed DLA entry input.
	ErrInvalidEntry = errors.New("dla: invalid entry")

	// ErrInvalidLedgerFilter reports malformed DLA ledger query input.
	ErrInvalidLedgerFilter = errors.New("dla: invalid ledger filter")

	// ErrDuplicateSource reports that an entry with the same opaque source ref exists.
	ErrDuplicateSource = errors.New("dla: duplicate source")

	// ErrConsistencyViolation reports that the DLA presentation ledger and
	// ledger account disagree.
	ErrConsistencyViolation = errors.New("dla: consistency invariant violation")
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

func policyPayloadFromJurisdiction() PolicyPayload {
	policy := jurisdiction.DirectorLoanPolicy()
	return PolicyPayload{
		S455Charge:               policy.S455Charge,
		CreditStatusText:         policy.Credit.StatusText,
		CreditExplainerTemplate:  policy.Credit.ExplainerTemplate,
		BIKWarningTextKey:        policy.Overdrawn.Warn,
		OverdrawnWarningTemplate: policy.Overdrawn.WarningTemplate,
		Remedy:                   policy.Overdrawn.Remedy,
	}
}

func statusForBalance(balance money.Money) Status {
	if balance.Amount < 0 {
		return StatusOverdrawn
	}
	return StatusCredit
}

func clearanceAmountForBalance(balance money.Money) money.Money {
	if balance.Amount >= 0 {
		return money.Zero(balance.Currency)
	}
	return money.Money{Amount: -balance.Amount, Currency: balance.Currency}
}
