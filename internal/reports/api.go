package reports

import (
	"context"
	"time"

	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/mail"
)

// ModuleName is the database schema and event namespace for reports.
const ModuleName = "reports"

// Period is an inclusive posting-date window.
type Period struct {
	From time.Time
	To   time.Time
}

// PL is the derived profit-and-loss read model.
type PL struct {
	Period          Period
	TaxYear         string
	Income          []IncomeLine
	IncomeTotal     money.Money
	RealisedFXGains LineItem
	Expenses        []ExpenseLine
	ExpenseTotal    money.Money
	ProfitBeforeTax money.Money
	CorporateTax    TaxLine
	NetProfit       money.Money
}

// IncomeLine is a GBP-presentational income row, grouped by client/currency or
// by the Other income fallback for non-invoice income.
type IncomeLine struct {
	Label      string
	ClientID   string
	ClientName string
	Currency   string
	Amount     money.Money
}

// ExpenseLine is a GBP-presentational expense row grouped by chart account.
type ExpenseLine struct {
	AccountCode ledger.AccountCode
	AccountName string
	Amount      money.Money
}

// ExpensesReport is the accountant drill-down for categorized expense
// postings in a period.
type ExpensesReport struct {
	Period       Period
	Categories   []ExpenseCategory
	TopPayees    []ExpensePayeeTotal
	Transactions []ExpenseTransaction
	Total        money.Money
}

// ExpenseCategory is a GBP-presentational expense total grouped by account.
type ExpenseCategory struct {
	AccountCode      ledger.AccountCode
	Category         string
	Amount           money.Money
	TransactionCount int
}

// ExpensePayeeTotal is a GBP-presentational total grouped by transaction payee.
type ExpensePayeeTotal struct {
	Payee            string
	Amount           money.Money
	TransactionCount int
}

// ExpenseTransaction is one categorized expense posting with transaction
// attribution where a banking source reference is available.
type ExpenseTransaction struct {
	EntryID      ledger.EntryID
	Date         time.Time
	Payee        string
	Reference    string
	Amount       money.Money
	AccountCode  ledger.AccountCode
	Category     string
	SourceModule string
	SourceRef    string
}

// LineItem is a named GBP-presentational P&L row.
type LineItem struct {
	Label  string
	Amount money.Money
}

// TaxLine is a data-driven corporate tax row sourced from jurisdiction packs.
type TaxLine struct {
	Label   string
	TaxYear string
	Rate    jurisdiction.Rate
	Amount  money.Money
}

// FilingStatus is the deadline state displayed by reports and consumed by
// advisor deadline facts.
type FilingStatus string

const (
	FilingStatusUpcoming FilingStatus = "upcoming"
	FilingStatusDueSoon  FilingStatus = "due-soon"
	FilingStatusOverdue  FilingStatus = "overdue"
)

// Filing is one filing-calendar row enriched for reports and advisor facts.
type Filing struct {
	Key        string
	Label      string
	Authority  string
	DueDate    time.Time
	DaysUntil  int
	Status     FilingStatus
	WarnWindow int
}

// VATFigures are the VAT return boxes needed for manual IoM filing in v1.
type VATFigures struct {
	Period      Period
	Box1        money.Money
	Box4        money.Money
	Box6        money.Money
	NetPosition money.Money
}

// VATRegistrationStatus describes whether VAT return figures are applicable.
type VATRegistrationStatus string

const (
	VATRegistrationRegistered    VATRegistrationStatus = "registered"
	VATRegistrationNotRegistered VATRegistrationStatus = "not_registered"
)

// VATReport is the reports VAT endpoint read model.
type VATReport struct {
	Period  Period
	Status  VATRegistrationStatus
	Figures *VATFigures
}

// VATPosition is the current-quarter advisor fact. DueDate is populated from
// REP-3 jurisdiction filing deadlines when company facts are available.
type VATPosition struct {
	Period  Period
	Status  VATRegistrationStatus
	Figures *VATFigures
	DueDate *time.Time
}

// ArchiveRef identifies an immutable export-pack archive asset.
type ArchiveRef struct {
	URL         string
	SHA256      string
	Size        int64
	DataVersion string
	GeneratedAt time.Time
}

// ShareStatus reports whether the export pack was attached or needs manual
// handling because it is too large for platform mail.
type ShareStatus string

const (
	ShareStatusSent       ShareStatus = "sent"
	ShareStatusManualSend ShareStatus = "manual-send"
)

// ShareResult is returned by POST /api/reports/share.
type ShareResult struct {
	Status  ShareStatus
	Archive ArchiveRef
	Message string
}

// ShareRequest describes one accountant export-pack email attempt.
type ShareRequest struct {
	Email  string
	Period Period
}

// StoredAsset is an immutable asset loaded for inclusion in the export pack.
type StoredAsset struct {
	Filename    string
	ContentType string
	Bytes       []byte
}

// StoredDocument is a named document already loaded as bytes.
type StoredDocument struct {
	Path        string
	ContentType string
	Bytes       []byte
}

// ExportArchiveStore persists export ZIP bytes and reloads existing immutable
// assets referenced by invoices/dividends.
type ExportArchiveStore interface {
	ExistingExportArchive(context.Context, string) (ArchiveRef, bool, error)
	StoreExportArchive(context.Context, string, []byte) (ArchiveRef, error)
	LoadAsset(context.Context, string) (StoredAsset, error)
}

// PLPDFEngine renders the reports print route into a PDF.
type PLPDFEngine interface {
	RenderPLPDF(context.Context, PLPrintPayload) ([]byte, error)
}

// PLPrintPayload is the stable payload consumed by the React P&L print route.
type PLPrintPayload struct {
	Report      plResponse `json:"pl"`
	CompanyName string     `json:"company_name"`
	GeneratedAt string     `json:"generated_at"`
	AppVersion  string     `json:"app_version"`
}

// DividendDocumentProvider supplies optional dividend documents without making
// reports import the dividends package, which would create a package cycle.
type DividendDocumentProvider interface {
	DividendDocuments(context.Context, Period) ([]StoredDocument, error)
}

// ReceiptDocumentProvider supplies optional banking receipt documents without
// making reports import the banking package.
type ReceiptDocumentProvider interface {
	ReceiptDocuments(context.Context, Period) ([]StoredDocument, error)
}

// Reports is the v1 reports read API.
type Reports interface {
	ProfitAndLoss(context.Context, Period) (PL, error)
	ExpensesByCategory(context.Context, Period) (ExpensesReport, error)
	ProfitYTD(context.Context, string) (money.Money, error)
	VATPosition(context.Context) (VATPosition, error)
	FilingCalendarContext(context.Context) ([]Filing, error)
	ExportPack(context.Context, Period) (ArchiveRef, error)
	ShareExportPack(context.Context, ShareRequest) (ShareResult, error)
}

type Ledger interface {
	ReadSnapshot(context.Context, ledger.ReadSnapshotFunc) error
	BalancesByType(context.Context, time.Time, time.Time) ([]ledger.AccountBalance, error)
	Entries(context.Context, ledger.EntryFilter) ([]ledger.JournalEntry, error)
	Accounts(context.Context) ([]ledger.Account, error)
}

type BankingTransactionID int64

// BankingTransaction is the banking attribution reports needs for expense
// drill-downs without importing the banking module implementation.
type BankingTransaction struct {
	Date      time.Time
	Payee     string
	Reference string
}

// Banking is the banking read surface reports needs for expense payee detail.
type Banking interface {
	Transaction(context.Context, BankingTransactionID) (BankingTransaction, error)
}

type Identity interface {
	Profile(context.Context) (identity.CompanyProfile, error)
	CompanyFacts(context.Context) (identity.CompanyFacts, error)
}

// CompanyFactsProvider is the identity fact surface reports needs to compose
// the filing calendar. Implementations must return fresh facts on each call.
type CompanyFactsProvider interface {
	CompanyFacts(context.Context) (identity.CompanyFacts, error)
}

type Invoicing interface {
	Invoice(context.Context, string) (invoicing.Invoice, error)
	InvoiceByNumber(context.Context, string) (invoicing.Invoice, error)
	InvoicesIssuedBetween(context.Context, time.Time, time.Time) ([]invoicing.Invoice, error)
	InvoiceVATContextBySendEntryID(context.Context, ledger.EntryID) (invoicing.InvoiceVATContext, error)
	Client(context.Context, string) (invoicing.Client, error)
}

// DLA is the director's loan presentation-ledger read surface used by export
// packs.
type DLA interface {
	Ledger(context.Context, dla.LedgerFilter) ([]dla.Entry, error)
}

// Mailer is the platform mail sender used for accountant sharing.
type Mailer interface {
	Send(context.Context, mail.Message) error
}
