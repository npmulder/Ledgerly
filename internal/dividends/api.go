package dividends

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	"github.com/npmulder/ledgerly/internal/platform/db"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
	"github.com/npmulder/ledgerly/internal/reports"
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

// CompanySnapshot is the declaration-time company identity used by dividend
// legal documents. It is never refreshed from live Settings data.
type CompanySnapshot struct {
	TradingName      string                    `json:"trading_name"`
	LegalName        string                    `json:"legal_name"`
	CompanyNumber    string                    `json:"company_number"`
	RegisteredOffice identity.RegisteredOffice `json:"registered_office"`
	DirectorName     string                    `json:"director_name"`
	LogoAssetID      *identity.AssetID         `json:"logo_asset_id,omitempty"`
	LogoAssetURL     *string                   `json:"logo_asset_url,omitempty"`
	LogoDataURI      *string                   `json:"logo_data_uri,omitempty"`
}

// ShareholderSnapshot is the declaration-time shareholding resolved by Declare.
type ShareholderSnapshot struct {
	Name   string `json:"name"`
	Shares int64  `json:"shares"`
	Class  string `json:"class"`
}

// WithholdingSnapshot captures the active pack dividend withholding policy and
// the exact note rendered on dividend vouchers.
type WithholdingSnapshot struct {
	TaxYear string `json:"tax_year"`
	Policy  string `json:"policy"`
	Note    string `json:"note"`
}

// DeclarationID identifies an immutable dividend declaration.
type DeclarationID string

// Declaration is the persisted declaration read model.
type Declaration struct {
	ID                  DeclarationID        `json:"id"`
	DeclaredDate        time.Time            `json:"declared_date"`
	Amount              money.Money          `json:"amount"`
	PerShare            money.Money          `json:"per_share"`
	Shares              int64                `json:"shares"`
	ShareholderName     string               `json:"shareholder_name"`
	CompanySnapshot     *CompanySnapshot     `json:"company_snapshot,omitempty"`
	ShareholderSnapshot *ShareholderSnapshot `json:"shareholder_snapshot,omitempty"`
	HeadroomSnapshot    *HeadroomBreakdown   `json:"headroom_snapshot,omitempty"`
	WithholdingSnapshot *WithholdingSnapshot `json:"withholding_snapshot,omitempty"`
	VoucherAsset        *identity.AssetID    `json:"voucher_asset,omitempty"`
	MinutesAsset        *identity.AssetID    `json:"minutes_asset,omitempty"`
	CreatedAt           time.Time            `json:"created_at"`
}

// DividendDocumentPayload is the single data contract consumed by the React
// dividend print routes and the chromedp renderer.
type DividendDocumentPayload struct {
	Declaration Declaration `json:"declaration"`
}

// DividendDocumentPDFEngine renders dividend document print payloads into PDF
// bytes.
type DividendDocumentPDFEngine interface {
	RenderDividendVoucherPDF(context.Context, DividendDocumentPayload) ([]byte, error)
	RenderBoardMinutesPDF(context.Context, DividendDocumentPayload) ([]byte, error)
}

// DividendDocumentAssetStore persists immutable PDF bytes and returns the
// identity asset id stored on declarations.voucher_asset/minutes_asset.
type DividendDocumentAssetStore interface {
	StoreDividendDocumentPDF(context.Context, []byte) (identity.AssetID, error)
}

// Dividends exposes the dividend read API for advisor/UI consumers.
type Dividends interface {
	Headroom(context.Context) (HeadroomBreakdown, error)
	Validate(context.Context, money.Money) (ValidationResult, error)
	Declare(context.Context, money.Money) (Declaration, error)
	RenderDeclarationDocumentsNow(context.Context, DeclarationID) (Declaration, error)
	DeclarationDocumentPayload(context.Context, DeclarationID) (DividendDocumentPayload, error)
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
	Asset(context.Context, identity.AssetID) (identity.Asset, error)
	CompanyFacts(context.Context) (identity.CompanyFacts, error)
}

// DLA is the presentation-ledger append surface dividends needs after posting
// the authoritative ledger entry.
type DLA interface {
	EnsureDirectorAccount(context.Context, db.Tx, dla.Director) (ledger.AccountCode, error)
	RecordExternalCredit(context.Context, db.Tx, dla.DirectorID, string, time.Time, money.Money, string) error
}

// Config contains the platform dependencies required by the dividends module.
type Config struct {
	Pool                 *pgxpool.Pool
	Clock                clock.Clock
	Ledger               Ledger
	Reports              reports.Reports
	Identity             Identity
	DLA                  DLA
	Bus                  *bus.Bus
	DocumentAssetStore   DividendDocumentAssetStore
	DocumentPDFEngine    DividendDocumentPDFEngine
	DocumentPDFBaseURL   string
	DocumentRetryBackoff time.Duration
	Logger               *slog.Logger
}

// Module is the dividends module wiring surface used by the app builder.
type Module struct {
	service *Service
}

// NewModule assembles the dividends module without registering side effects
// globally.
func NewModule(cfg Config) (*Module, error) {
	if cfg.Pool == nil {
		return nil, fmt.Errorf("dividends: pool is required")
	}
	return &Module{
		service: New(
			cfg.Pool,
			cfg.Ledger,
			cfg.Reports,
			cfg.Identity,
			WithClock(cfg.Clock),
			WithDLA(cfg.DLA),
			WithBus(cfg.Bus),
			WithDocumentAssetStore(cfg.DocumentAssetStore),
			WithDocumentPDFEngine(cfg.DocumentPDFEngine),
			WithDocumentPDFBaseURL(cfg.DocumentPDFBaseURL),
			WithDocumentRetryBackoff(cfg.DocumentRetryBackoff),
			WithLogger(cfg.Logger),
		),
	}, nil
}

// Service returns the module service for in-process consumers.
func (m *Module) Service() *Service {
	if m == nil {
		return nil
	}
	return m.service
}

// HTTPModule returns the platform route mount for this module.
func (m *Module) HTTPModule() httpserver.Module {
	return httpserver.Module{
		Name:           ModuleName,
		RegisterRoutes: m.RegisterRoutes,
	}
}

// OpenAPIFragment returns the module's OpenAPI contribution.
func (m *Module) OpenAPIFragment() httpserver.OpenAPIFragment {
	return OpenAPIFragment()
}

var (
	// ErrInvalidDeclaration reports malformed declaration data.
	ErrInvalidDeclaration = errors.New("dividends: invalid declaration")

	// ErrDeclarationNotFound reports a missing dividend declaration.
	ErrDeclarationNotFound = errors.New("dividends: declaration not found")

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
