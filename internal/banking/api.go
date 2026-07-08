package banking

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

// ModuleName is the database schema and event namespace for banking.
const ModuleName = "banking"

type AccountID int64
type TransactionID int64
type ImportBatchID int64
type SuggestionID int64
type PayeeRuleID int64
type TransactionStateChangeID int64
type MatchEngineRunID int64
type ReceiptID int64

type Provider string

const ProviderRevolut Provider = "revolut"

type TransactionState string

const (
	TransactionStateUnreconciled TransactionState = "unreconciled"
	TransactionStateSuggested    TransactionState = "suggested"
	TransactionStateReconciled   TransactionState = "reconciled"
	TransactionStateExcluded     TransactionState = "excluded"
)

type SuggestionKind string

const (
	SuggestionKindInvoiceMatch SuggestionKind = "invoice-match"
	SuggestionKindDLA          SuggestionKind = "dla"
	SuggestionKindPayeeRule    SuggestionKind = "payee-rule"
)

type PayeeRuleMatchMode string

const (
	PayeeRuleMatchExact    PayeeRuleMatchMode = "exact"
	PayeeRuleMatchContains PayeeRuleMatchMode = "contains"
)

type PayeeRuleCreatedFrom string

const (
	PayeeRuleCreatedFromRecode PayeeRuleCreatedFrom = "recode"
	PayeeRuleCreatedFromManual PayeeRuleCreatedFrom = "manual"
)

const (
	DefaultFeedLimit               = 100
	MaxFeedLimit                   = 500
	DefaultRecentlyReconciledLimit = 10
	MaxRecentlyReconciledLimit     = 100
	MaxReceiptBytes                = 2 * 1024 * 1024
)

var (
	ErrInvalidAccount            = errors.New("banking: invalid account")
	ErrUnsupportedProvider       = errors.New("banking: unsupported provider")
	ErrUnsupportedCurrency       = errors.New("banking: unsupported currency")
	ErrAccountNotFound           = errors.New("banking: account not found")
	ErrTransactionNotFound       = errors.New("banking: transaction not found")
	ErrInvalidImport             = errors.New("banking: invalid import")
	ErrCurrencyMismatch          = errors.New("banking: currency mismatch")
	ErrInvalidStateTransition    = errors.New("banking: invalid transaction state transition")
	ErrInvalidSuggestion         = errors.New("banking: invalid suggestion")
	ErrInvalidPayeeRule          = errors.New("banking: invalid payee rule")
	ErrInvalidTransactionFilter  = errors.New("banking: invalid transaction filter")
	ErrSuggestionNotFound        = errors.New("banking: suggestion not found")
	ErrPayeeRuleNotFound         = errors.New("banking: payee rule not found")
	ErrInvalidReconciliation     = errors.New("banking: invalid reconciliation")
	ErrAlreadyReconciled         = errors.New("banking: already reconciled")
	ErrNotReconciled             = errors.New("banking: transaction is not reconciled")
	ErrUnsupportedReconciliation = errors.New("banking: unsupported reconciliation kind")
	ErrReceiptNotFound           = errors.New("banking: receipt not found")
	ErrInvalidReceipt            = errors.New("banking: invalid receipt")
	ErrReceiptTooLarge           = errors.New("banking: receipt exceeds maximum size")
	ErrUnsupportedReceipt        = errors.New("banking: unsupported receipt MIME type")
)

// LedgerAccountEnsurer is the ledger capability banking needs when creating
// cash accounts. ledger.Service satisfies this interface.
type LedgerAccountEnsurer interface {
	EnsureAccount(context.Context, db.Tx, ledger.AccountSpec) (ledger.AccountCode, error)
}

// LedgerAccountCatalog is the optional ledger read capability banking uses to
// validate payee-rule target accounts when available.
type LedgerAccountCatalog interface {
	Accounts(context.Context) ([]ledger.Account, error)
}

// LedgerJournal is the ledger posting capability used by reconciliation
// commands. ledger.Service satisfies this interface.
type LedgerJournal interface {
	EntryBySource(context.Context, db.Tx, string, string) (ledger.JournalEntry, error)
	Post(context.Context, db.Tx, ledger.NewJournalEntry) (ledger.EntryID, error)
	Reverse(context.Context, db.Tx, ledger.EntryID, string) (ledger.EntryID, error)
}

// MoneyFX supplies transaction-date GBP conversion and same-transaction
// realised-FX lookup for reconciliation commands. moneyfx.Service and
// moneyfx.Module satisfy this interface.
type MoneyFX interface {
	ToGBP(context.Context, money.Money, time.Time) (money.Money, error)
	RealisedFXAmount(context.Context, db.Tx, string) (money.Money, error)
	ClearRealisedFX(context.Context, db.Tx, string, string) (money.Money, error)
}

// InvoiceSettler is the invoicing command banking calls when confirming an
// invoice match. invoicing.Service satisfies this interface.
type InvoiceSettler interface {
	MarkSettled(context.Context, db.Tx, string, string, time.Time, invoicing.Money) (invoicing.Invoice, error)
	ClearSettlementByTxnRef(context.Context, db.Tx, string) (invoicing.Invoice, error)
}

// DLAFileDrawer is the DLA command banking calls when filing a bank
// transaction as a director drawing. dla.Service satisfies this interface.
type DLAFileDrawer interface {
	FileDrawing(context.Context, db.Tx, dla.TxnRef) error
	RecordExternalCredit(context.Context, db.Tx, string, time.Time, money.Money, string) error
}

// ReceiptAssetStore persists immutable receipt bytes outside the banking
// schema and returns opaque references that banking can store safely.
type ReceiptAssetStore interface {
	StoreReceiptAsset(context.Context, ReceiptAssetUpload) (string, error)
	LoadReceiptAsset(context.Context, string) (ReceiptAsset, error)
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
	Receipt       *ReceiptMetadata
}

type TransactionStateChange struct {
	ID            TransactionStateChangeID
	TransactionID TransactionID
	From          TransactionState
	To            TransactionState
	ChangedAt     time.Time
	Actor         string
}

type Suggestion struct {
	ID            SuggestionID
	TransactionID TransactionID
	Kind          SuggestionKind
	Confidence    float64
	Target        string
	Explanation   string
	AutoPostable  bool
	CreatedBy     string
	CreatedAt     time.Time
	SupersededAt  *time.Time
}

type SuggestionInput struct {
	TransactionID TransactionID
	Kind          SuggestionKind
	Confidence    float64
	Target        string
	Explanation   string
	AutoPostable  bool
	CreatedBy     string
}

type PayeeRule struct {
	ID            PayeeRuleID
	Matcher       string
	MatchMode     PayeeRuleMatchMode
	AccountCode   ledger.AccountCode
	TimesApplied  int
	LastAppliedAt *time.Time
	CreatedFrom   PayeeRuleCreatedFrom
	CreatedAt     time.Time
}

type PayeeRuleInput struct {
	Matcher     string
	MatchMode   PayeeRuleMatchMode
	AccountCode ledger.AccountCode
	CreatedFrom PayeeRuleCreatedFrom
}

type PayeeRuleUpdateInput struct {
	Matcher     string
	MatchMode   PayeeRuleMatchMode
	AccountCode ledger.AccountCode
}

type FeedCursor struct {
	Date time.Time
	ID   TransactionID
}

type FeedFilter struct {
	AccountID AccountID
	State     TransactionState
	From      *time.Time
	To        *time.Time
	After     *FeedCursor
	Limit     int
}

type ReviewQueue struct {
	InvoiceMatches []ReviewQueueItem
	DLA            []ReviewQueueItem
	PayeeRules     []ReviewQueueItem
}

type ReviewQueueItem struct {
	Transaction Transaction
	Suggestion  Suggestion
}

type ReconciledTransaction struct {
	Transaction  Transaction
	ReconciledAt time.Time
	Actor        string
}

type Receipt struct {
	ID            ReceiptID
	TransactionID TransactionID
	AssetRef      string
	Filename      string
	MIME          string
	Size          int64
	UploadedAt    time.Time
}

type ReceiptMetadata struct {
	Filename   string
	MIME       string
	Size       int64
	UploadedAt time.Time
}

type ReceiptUpload struct {
	Filename string
	MIME     string
	Bytes    []byte
}

type ReceiptAssetUpload struct {
	MIME  string
	Bytes []byte
}

type ReceiptAsset struct {
	MIME  string
	Size  int64
	Bytes []byte
}

type ReceiptDocument struct {
	Transaction Transaction
	Receipt     Receipt
}

type ConfirmMatchResult struct {
	Transaction   Transaction
	Kind          SuggestionKind
	InvoiceID     string
	RealisedFXGBP money.Money
}

type FileToDLAResult struct {
	Transaction Transaction
	Kind        SuggestionKind
	AmountGBP   money.Money
}

type RecodeResult struct {
	Transaction Transaction
	Kind        SuggestionKind
	Rule        PayeeRule
}

type UnreconcileResult struct {
	Transaction        Transaction
	Kind               SuggestionKind
	StateChange        TransactionStateChange
	ReversedEntryIDs   []ledger.EntryID
	ClearedRealisedFX  money.Money
	ClearedInvoiceID   string
	DLAPresentationRef string
}

type AlreadyReconciledError struct {
	TransactionID TransactionID
	State         TransactionState
}

func (e *AlreadyReconciledError) Error() string {
	if e == nil {
		return ErrAlreadyReconciled.Error()
	}
	if e.TransactionID <= 0 {
		return ErrAlreadyReconciled.Error()
	}
	if e.State == "" {
		return fmt.Sprintf("banking: transaction %d is already reconciled", e.TransactionID)
	}
	return fmt.Sprintf("banking: transaction %d is already %s", e.TransactionID, e.State)
}

func (e *AlreadyReconciledError) Unwrap() error {
	return ErrAlreadyReconciled
}

type NotReconciledError struct {
	TransactionID TransactionID
	State         TransactionState
}

func (e *NotReconciledError) Error() string {
	if e == nil || e.TransactionID <= 0 {
		return ErrNotReconciled.Error()
	}
	if e.State == "" {
		return fmt.Sprintf("banking: transaction %d is not reconciled", e.TransactionID)
	}
	return fmt.Sprintf("banking: transaction %d is %s, not reconciled", e.TransactionID, e.State)
}

func (e *NotReconciledError) Unwrap() error {
	return ErrNotReconciled
}

type UnsupportedReconciliationError struct {
	TransactionID TransactionID
}

func (e *UnsupportedReconciliationError) Error() string {
	if e == nil || e.TransactionID <= 0 {
		return ErrUnsupportedReconciliation.Error()
	}
	return fmt.Sprintf("banking: transaction %d reconciliation kind cannot be undone", e.TransactionID)
}

func (e *UnsupportedReconciliationError) Unwrap() error {
	return ErrUnsupportedReconciliation
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

type MatchEngineTrigger string

const (
	MatchEngineTriggerImportCompletion MatchEngineTrigger = "import-completion"
	MatchEngineTriggerInvoiceSent      MatchEngineTrigger = "invoicing.InvoiceSent"
	MatchEngineTriggerIdentityProfile  MatchEngineTrigger = "identity.ProfileUpdated"
	MatchEngineTriggerManualRefresh    MatchEngineTrigger = "manual-refresh"
)

type MatchEngineRun struct {
	ID            MatchEngineRunID
	Trigger       MatchEngineTrigger
	TxnsEvaluated []TransactionID
	Suggestions   []Suggestion
	CreatedAt     time.Time
}

type InvoiceMatchCandidate struct {
	InvoiceID  string
	Number     string
	ClientName string
	IssueDate  time.Time
	DueDate    time.Time
	TermsDays  int
	Amount     money.Money
	Status     string
	Settled    bool
}

// InvoiceCandidateSource supplies already-known invoice facts to banking. It
// keeps the scorer deterministic while avoiding a hard dependency on
// invoicing's internal store shape.
type InvoiceCandidateSource interface {
	InvoiceCandidates(ctx context.Context, tx db.Tx, currency string) ([]InvoiceMatchCandidate, error)
}

type DirectorNameSource interface {
	DirectorNames(ctx context.Context) ([]string, error)
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

type TransactionNotFoundError struct {
	ID TransactionID
}

func (e *TransactionNotFoundError) Error() string {
	return fmt.Sprintf("banking: transaction %d was not found", e.ID)
}

func (e *TransactionNotFoundError) Unwrap() error {
	return ErrTransactionNotFound
}

type InvalidStateTransitionError struct {
	TransactionID TransactionID
	From          TransactionState
	To            TransactionState
}

func (e *InvalidStateTransitionError) Error() string {
	return fmt.Sprintf("banking: transaction %d cannot transition from %s to %s", e.TransactionID, e.From, e.To)
}

func (e *InvalidStateTransitionError) Unwrap() error {
	return ErrInvalidStateTransition
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
