package ledger

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

// EntryID identifies an immutable journal entry.
type EntryID int64

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

// NewJournalEntry describes a balanced entry that should be appended.
type NewJournalEntry struct {
	Date         time.Time
	Description  string
	SourceModule string
	SourceRef    string
	Postings     []NewPosting
}

// NewPosting describes one journal posting. Amount is native currency;
// AmountGBP is the caller-supplied presentational GBP value.
type NewPosting struct {
	AccountCode AccountCode
	Amount      money.Money
	AmountGBP   money.Money
}

// JournalEntry is an immutable stored entry with its postings.
type JournalEntry struct {
	ID           EntryID
	Date         time.Time
	Description  string
	SourceModule string
	SourceRef    string
	ReversalOf   *EntryID
	Postings     []Posting
	CreatedAt    time.Time
}

// Posting is a stored journal posting.
type Posting struct {
	AccountCode AccountCode
	Amount      money.Money
	AmountGBP   money.Money
}

// AccountBalance is a native-currency breakdown plus frozen presentational GBP
// total for either one account or one account type aggregate.
type AccountBalance struct {
	AccountCode AccountCode
	AccountName string
	AccountType AccountType
	Native      []money.Money
	AmountGBP   money.Money
}

// EntryCursor identifies the last entry from a previous Entries page. Entries
// returns rows ordered by date then id, and an EntryCursor resumes strictly
// after that tuple.
type EntryCursor struct {
	Date time.Time
	ID   EntryID
}

// EntryFilter constrains journal entry browsing/export. From and To are
// inclusive dates. AccountCode filters to entries touching that account while
// still returning every posting in each matched entry.
type EntryFilter struct {
	From         *time.Time
	To           *time.Time
	SourceModule string
	AccountCode  AccountCode
	After        *EntryCursor
	Limit        int
}

const (
	// DefaultEntriesLimit is used when EntryFilter.Limit is zero.
	DefaultEntriesLimit = 100

	// MaxEntriesLimit caps journal browse/export pages.
	MaxEntriesLimit = 500
)

// EntryPostedName is the canonical bus event name for journal entry creation.
const EntryPostedName = "ledger.EntryPosted"

// EntryPosted is published inside the same transaction that appends an entry.
type EntryPosted struct {
	EntryID      EntryID
	SourceModule string
	Accounts     []AccountCode
	Date         time.Time
}

// Name implements bus.Event.
func (EntryPosted) Name() string {
	return EntryPostedName
}

// Ledger exposes account management and append-only journal write operations.
type Ledger interface {
	Post(ctx context.Context, tx db.Tx, entry NewJournalEntry) (EntryID, error)
	Reverse(ctx context.Context, tx db.Tx, id EntryID, reason string) (EntryID, error)
	AccountBalance(ctx context.Context, code AccountCode, asOf time.Time) (AccountBalance, error)
	BalancesByType(ctx context.Context, from time.Time, to time.Time) ([]AccountBalance, error)
	Entries(ctx context.Context, filter EntryFilter) ([]JournalEntry, error)
	EnsureAccount(ctx context.Context, tx db.Tx, spec AccountSpec) (AccountCode, error)
	Accounts(ctx context.Context) ([]Account, error)
}

var (
	// ErrInvalidAccountSpec reports a malformed AccountSpec.
	ErrInvalidAccountSpec = errors.New("ledger: invalid account spec")

	// ErrAccountConflict reports an AccountSpec that conflicts with an existing account.
	ErrAccountConflict = errors.New("ledger: account spec conflicts with existing account")

	// ErrInvalidJournalEntry reports malformed journal entry input.
	ErrInvalidJournalEntry = errors.New("ledger: invalid journal entry")

	// ErrInvalidEntryFilter reports malformed journal entry query input.
	ErrInvalidEntryFilter = errors.New("ledger: invalid entry filter")

	// ErrInsufficientPostings reports an entry with fewer than two postings.
	ErrInsufficientPostings = errors.New("ledger: insufficient postings")

	// ErrInvalidEntryDate reports a journal entry date outside the accepted range.
	ErrInvalidEntryDate = errors.New("ledger: invalid entry date")

	// ErrInvalidMoney reports missing or malformed posting money values.
	ErrInvalidMoney = errors.New("ledger: invalid posting money")

	// ErrPostingSignMismatch reports native and GBP posting amounts on opposite sides.
	ErrPostingSignMismatch = errors.New("ledger: posting native and GBP signs mismatch")

	// ErrZeroPosting reports a posting with a zero native or GBP amount.
	ErrZeroPosting = errors.New("ledger: zero-amount posting")

	// ErrUnbalancedGBP reports an entry whose presentational GBP postings do not sum to zero.
	ErrUnbalancedGBP = errors.New("ledger: unbalanced GBP postings")

	// ErrUnbalancedCurrency reports an entry whose native postings do not sum to zero per currency.
	ErrUnbalancedCurrency = errors.New("ledger: unbalanced native postings")

	// ErrAccountNotFound reports a posting account code that is not in the chart of accounts.
	ErrAccountNotFound = errors.New("ledger: account not found")

	// ErrAccountCurrencyMismatch reports a posting whose native currency conflicts with the account.
	ErrAccountCurrencyMismatch = errors.New("ledger: account currency mismatch")

	// ErrEntryNotFound reports a missing journal entry.
	ErrEntryNotFound = errors.New("ledger: journal entry not found")

	// ErrInvalidReversal reports malformed reversal input or a reversal that cannot be built.
	ErrInvalidReversal = errors.New("ledger: invalid reversal")

	// ErrReversalOfReversal reports an attempt to reverse a reversal entry.
	ErrReversalOfReversal = errors.New("ledger: cannot reverse a reversal")

	// ErrEntryAlreadyReversed reports an attempt to reverse an entry more than once.
	ErrEntryAlreadyReversed = errors.New("ledger: entry already reversed")

	// ErrInvariantViolation reports a stored entry that fails the cheap per-entry balance check.
	ErrInvariantViolation = errors.New("ledger: entry invariant violation")
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

// AccountNotFoundError carries the missing account code.
type AccountNotFoundError struct {
	Code AccountCode
}

func (e *AccountNotFoundError) Error() string {
	return fmt.Sprintf("ledger: account %s was not found", e.Code)
}

func (e *AccountNotFoundError) Unwrap() error {
	return ErrAccountNotFound
}

// AccountCurrencyMismatchError carries the account currency conflict.
type AccountCurrencyMismatchError struct {
	Code      AccountCode
	Expected  string
	Requested string
}

func (e *AccountCurrencyMismatchError) Error() string {
	return fmt.Sprintf(
		"ledger: account %s currency is %q, posting requested %q",
		e.Code,
		e.Expected,
		e.Requested,
	)
}

func (e *AccountCurrencyMismatchError) Unwrap() error {
	return ErrAccountCurrencyMismatch
}

// EntryNotFoundError carries the missing journal entry ID.
type EntryNotFoundError struct {
	ID EntryID
}

func (e *EntryNotFoundError) Error() string {
	return fmt.Sprintf("ledger: journal entry %d was not found", e.ID)
}

func (e *EntryNotFoundError) Unwrap() error {
	return ErrEntryNotFound
}
