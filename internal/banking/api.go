package banking

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

// ModuleName is the database schema and event namespace for banking.
const ModuleName = "banking"

type AccountID int64
type TransactionID int64
type ImportBatchID int64

type Provider string

const ProviderRevolut Provider = "revolut"

type TransactionState string

const TransactionStateUnreconciled TransactionState = "unreconciled"

var (
	ErrInvalidAccount      = errors.New("banking: invalid account")
	ErrUnsupportedProvider = errors.New("banking: unsupported provider")
	ErrUnsupportedCurrency = errors.New("banking: unsupported currency")
	ErrAccountNotFound     = errors.New("banking: account not found")
	ErrInvalidImport       = errors.New("banking: invalid import")
	ErrCurrencyMismatch    = errors.New("banking: currency mismatch")
)

// LedgerAccountEnsurer is the ledger capability banking needs when creating
// cash accounts. ledger.Service satisfies this interface.
type LedgerAccountEnsurer interface {
	EnsureAccount(context.Context, db.Tx, ledger.AccountSpec) (ledger.AccountCode, error)
}

type BankAccount struct {
	ID                AccountID
	Name              string
	Provider          Provider
	Currency          string
	LedgerAccountCode ledger.AccountCode
	CreatedAt         time.Time
}

type AccountInput struct {
	Name     string
	Provider Provider
	Currency string
}

type Transaction struct {
	ID            TransactionID
	AccountID     AccountID
	Date          time.Time
	Amount        money.Money
	Payee         string
	Reference     string
	ProviderMeta  map[string]string
	ImportBatchID ImportBatchID
	State         TransactionState
	CreatedAt     time.Time
}

type ImportFile struct {
	Filename string
	Reader   io.Reader
}

type BatchSummary struct {
	BatchID       ImportBatchID
	AccountID     AccountID
	Filename      string
	ImportedAt    time.Time
	TotalRows     int
	NewRows       int
	DuplicateRows int
}

// RawTxn is a parsed provider transaction before account-specific validation
// and dedupe are applied. Amount sign follows bank statement semantics:
// positive means money in, negative means money out.
type RawTxn struct {
	Date         time.Time
	Amount       money.Money
	Payee        string
	Reference    string
	ProviderMeta map[string]string
}

type StatementParser interface {
	Parse(io.Reader) ([]RawTxn, error)
}

type ParseRowError struct {
	Row int
	Err error
}

func (e *ParseRowError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("banking: CSV row %d: %v", e.Row, e.Err)
}

func (e *ParseRowError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type CurrencyMismatchError struct {
	AccountID AccountID
	Expected  string
	Actual    string
	Row       int
}

func (e *CurrencyMismatchError) Error() string {
	if e == nil {
		return ""
	}
	row := ""
	if e.Row > 0 {
		row = fmt.Sprintf(" row %d", e.Row)
	}
	return fmt.Sprintf("banking: account %d expects %s but import%s has %s", e.AccountID, e.Expected, row, e.Actual)
}

func (e *CurrencyMismatchError) Unwrap() error {
	return ErrCurrencyMismatch
}

type AccountNotFoundError struct {
	ID AccountID
}

func (e *AccountNotFoundError) Error() string {
	return fmt.Sprintf("banking: account %d was not found", e.ID)
}

func (e *AccountNotFoundError) Unwrap() error {
	return ErrAccountNotFound
}

var defaultParsers = map[Provider]StatementParser{}

func registerDefaultParser(provider Provider, parser StatementParser) {
	if parser == nil {
		panic("banking: nil parser")
	}
	defaultParsers[provider] = parser
}

func defaultParserSnapshot() map[Provider]StatementParser {
	parsers := make(map[Provider]StatementParser, len(defaultParsers))
	for provider, parser := range defaultParsers {
		parsers[provider] = parser
	}
	return parsers
}
