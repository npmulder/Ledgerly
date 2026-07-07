package dividends

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	ledgerapi "github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	"github.com/npmulder/ledgerly/internal/platform/db"
	"github.com/npmulder/ledgerly/internal/reports"
)

const gbpCurrency = "GBP"

const (
	defaultDeclarationDescription = "Dividend declared"
	dividendSourcePrefix          = "dividends:"
)

const (
	defaultDocumentRetryAttempts = 3
	defaultDocumentRetryBackoff  = 250 * time.Millisecond
)

// Service composes ledger, reports, jurisdiction, identity, and declaration
// storage into the live dividend headroom read model.
type Service struct {
	pool                 *pgxpool.Pool
	ledger               Ledger
	reports              reports.Reports
	identity             Identity
	dla                  DLA
	bus                  *bus.Bus
	clock                clock.Clock
	documentAssetStore   DividendDocumentAssetStore
	documentPDFEngine    DividendDocumentPDFEngine
	documentRetryBackoff time.Duration
	logger               *slog.Logger
	idGenerator          func() (DeclarationID, error)
	store                Store
}

var _ Dividends = (*Service)(nil)

// Option customizes a dividends service.
type Option func(*Service)

// WithClock injects the time source used to resolve the current financial year.
func WithClock(clk clock.Clock) Option {
	return func(s *Service) {
		s.clock = clk
	}
}

// WithDLA injects the DLA presentation-ledger append API.
func WithDLA(dlaAPI DLA) Option {
	return func(s *Service) {
		s.dla = dlaAPI
	}
}

// WithBus injects the event bus used to publish declaration facts.
func WithBus(eventBus *bus.Bus) Option {
	return func(s *Service) {
		s.bus = eventBus
	}
}

// WithIDGenerator injects declaration ID generation for deterministic tests.
func WithIDGenerator(generator func() (DeclarationID, error)) Option {
	return func(s *Service) {
		s.idGenerator = generator
	}
}

// WithDocumentAssetStore installs immutable dividend document PDF asset
// storage.
func WithDocumentAssetStore(store DividendDocumentAssetStore) Option {
	return func(s *Service) {
		if store != nil {
			s.documentAssetStore = store
		}
	}
}

// WithDocumentPDFEngine installs the engine used to render dividend document
// print payloads to PDF bytes.
func WithDocumentPDFEngine(engine DividendDocumentPDFEngine) Option {
	return func(s *Service) {
		if engine != nil {
			s.documentPDFEngine = engine
		}
	}
}

// WithDocumentPDFBaseURL installs the production chromedp engine when a custom
// engine was not supplied.
func WithDocumentPDFBaseURL(baseURL string) Option {
	return func(s *Service) {
		if s.documentPDFEngine == nil && strings.TrimSpace(baseURL) != "" {
			s.documentPDFEngine = NewChromeDocumentPDFEngine(baseURL)
		}
	}
}

// WithDocumentRetryBackoff overrides the declaration-time async retry backoff.
func WithDocumentRetryBackoff(backoff time.Duration) Option {
	return func(s *Service) {
		if backoff >= 0 {
			s.documentRetryBackoff = backoff
		}
	}
}

// WithLogger installs the logger used for non-blocking document render
// failures.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Service) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// New returns the dividends read API.
func New(pool *pgxpool.Pool, ledgerAPI Ledger, reportsAPI reports.Reports, identityAPI Identity, opts ...Option) *Service {
	service := &Service{
		pool:                 pool,
		ledger:               ledgerAPI,
		reports:              reportsAPI,
		identity:             identityAPI,
		clock:                clock.New(),
		documentRetryBackoff: defaultDocumentRetryBackoff,
		logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
		idGenerator:          newDeclarationID,
	}
	for _, opt := range opts {
		opt(service)
	}
	if service.clock == nil {
		service.clock = clock.New()
	}
	if service.idGenerator == nil {
		service.idGenerator = newDeclarationID
	}
	if service.logger == nil {
		service.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return service
}

// Headroom returns the live distributable-reserves calculation. It stores no
// derived balance.
func (s *Service) Headroom(ctx context.Context) (HeadroomBreakdown, error) {
	return s.headroom(ctx, nil)
}

func (s *Service) headroom(ctx context.Context, tx db.Tx) (HeadroomBreakdown, error) {
	if s.ledger == nil {
		return HeadroomBreakdown{}, fmt.Errorf("ledger: %w", ErrMissingProvider)
	}
	if s.reports == nil {
		return HeadroomBreakdown{}, fmt.Errorf("reports: %w", ErrMissingProvider)
	}
	if s.identity == nil {
		return HeadroomBreakdown{}, fmt.Errorf("identity: %w", ErrMissingProvider)
	}

	facts, err := s.identity.CompanyFacts(ctx)
	if err != nil {
		return HeadroomBreakdown{}, err
	}
	asOf, err := normalizeDate(s.now())
	if err != nil {
		return HeadroomBreakdown{}, err
	}
	return s.headroomWithFacts(ctx, tx, facts, asOf)
}

func (s *Service) headroomWithFacts(
	ctx context.Context,
	tx db.Tx,
	facts identity.CompanyFacts,
	asOf time.Time,
) (HeadroomBreakdown, error) {
	financialYear, err := financialYearForDate(asOf, facts.YearEnd.Month, facts.YearEnd.Day)
	if err != nil {
		return HeadroomBreakdown{}, err
	}
	period, err := financialYearPeriod(financialYear, facts.YearEnd.Month, facts.YearEnd.Day)
	if err != nil {
		return HeadroomBreakdown{}, err
	}
	priorYearEnd := period.From.AddDate(0, 0, -1)

	var retainedBalance ledgerapi.AccountBalance
	if tx != nil {
		retainedBalance, err = s.ledger.AccountBalanceInTx(ctx, tx, RetainedEarningsAccountCode, priorYearEnd)
	} else {
		retainedBalance, err = s.ledger.AccountBalance(ctx, RetainedEarningsAccountCode, priorYearEnd)
	}
	if err != nil {
		return HeadroomBreakdown{}, err
	}
	retained, err := retainedEarningsAmount(retainedBalance.AmountGBP)
	if err != nil {
		return HeadroomBreakdown{}, err
	}

	ytdProfit, err := s.profitYTD(ctx, tx, financialYear)
	if err != nil {
		return HeadroomBreakdown{}, err
	}
	rate, err := jurisdiction.CorporateRate(financialYear)
	if err != nil {
		return HeadroomBreakdown{}, err
	}
	corporationTax, err := corporateTaxAmount(ytdProfit, rate)
	if err != nil {
		return HeadroomBreakdown{}, err
	}
	declared, err := s.declaredInYearWithFacts(ctx, tx, financialYear, facts)
	if err != nil {
		return HeadroomBreakdown{}, err
	}

	available, err := retained.Add(ytdProfit)
	if err != nil {
		return HeadroomBreakdown{}, fmt.Errorf("dividends: add YTD profit: %w", err)
	}
	available, err = available.Sub(corporationTax)
	if err != nil {
		return HeadroomBreakdown{}, fmt.Errorf("dividends: subtract corporation tax: %w", err)
	}
	available, err = available.Sub(declared)
	if err != nil {
		return HeadroomBreakdown{}, fmt.Errorf("dividends: subtract declared dividends: %w", err)
	}

	corporationTaxLine, err := corporationTax.Negate()
	if err != nil {
		return HeadroomBreakdown{}, fmt.Errorf("dividends: corporation tax line: %w", err)
	}
	declaredLine, err := declared.Negate()
	if err != nil {
		return HeadroomBreakdown{}, fmt.Errorf("dividends: declared dividends line: %w", err)
	}

	return HeadroomBreakdown{
		AsOf:          asOf,
		FinancialYear: financialYear,
		Lines: []MoneyLine{
			{Label: retainedEarningsLineLabel, Amount: retained},
			{Label: profitYTDLineLabel, Amount: ytdProfit},
			{Label: corporationTaxLabel(formatRatePercent(rate)), Amount: corporationTaxLine},
			{Label: dividendsDeclaredLabel, Amount: declaredLine},
			{Label: availableHeadroomLabel, Amount: available},
		},
		Available:     available,
		Distributable: available.Amount >= 0,
	}, nil
}

// Validate returns the validation-strip payload for a candidate declaration.
func (s *Service) Validate(ctx context.Context, amount money.Money) (ValidationResult, error) {
	asOf, err := normalizeDate(s.now())
	if err != nil {
		return ValidationResult{}, err
	}
	return s.validateAt(ctx, nil, amount, asOf)
}

// Declare appends a dividend declaration and all related side effects in one
// transaction.
func (s *Service) Declare(ctx context.Context, amount money.Money) (declaration Declaration, err error) {
	if s.pool == nil {
		return Declaration{}, fmt.Errorf("dividends: declare requires pool")
	}
	if s.dla == nil {
		return Declaration{}, fmt.Errorf("dla: %w", ErrMissingProvider)
	}
	if s.ledger == nil {
		return Declaration{}, fmt.Errorf("ledger: %w", ErrMissingProvider)
	}
	if s.identity == nil {
		return Declaration{}, fmt.Errorf("identity: %w", ErrMissingProvider)
	}
	if s.reports == nil {
		return Declaration{}, fmt.Errorf("reports: %w", ErrMissingProvider)
	}

	declaredDate, err := normalizeDate(s.now())
	if err != nil {
		return Declaration{}, err
	}
	id, err := s.idGenerator()
	if err != nil {
		return Declaration{}, err
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Declaration{}, fmt.Errorf("dividends: begin declaration transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	if err := lockDeclarationMutation(ctx, tx); err != nil {
		return Declaration{}, err
	}
	validation, err := s.validateAt(ctx, tx, amount, declaredDate)
	if err != nil {
		return Declaration{}, err
	}

	profile, err := s.identity.Profile(ctx)
	if err != nil {
		return Declaration{}, err
	}
	shareholder, err := declarationShareholder(profile)
	if err != nil {
		return Declaration{}, err
	}
	companySnapshot, err := declarationCompanySnapshot(profile, shareholder)
	if err != nil {
		return Declaration{}, err
	}
	shareholderSnapshot, err := declarationShareholderSnapshot(shareholder)
	if err != nil {
		return Declaration{}, err
	}
	headroomSnapshot := validation.Headroom
	withholdingSnapshot := WithholdingSnapshot{
		TaxYear: validation.Withholding.TaxYear,
		Policy:  validation.Withholding.Policy,
		Note:    dividendWithholdingNote(validation.Withholding.Policy),
	}
	perShare, err := perShareAmount(validation.Amount, shareholder.Shares)
	if err != nil {
		return Declaration{}, err
	}

	stored, err := s.store.InsertDeclaration(ctx, tx, Declaration{
		ID:                  id,
		DeclaredDate:        declaredDate,
		Amount:              validation.Amount,
		PerShare:            perShare,
		Shares:              shareholder.Shares,
		ShareholderName:     shareholder.Name,
		CompanySnapshot:     &companySnapshot,
		ShareholderSnapshot: &shareholderSnapshot,
		HeadroomSnapshot:    &headroomSnapshot,
		WithholdingSnapshot: &withholdingSnapshot,
	})
	if err != nil {
		return Declaration{}, err
	}
	if _, err := s.ledger.Post(ctx, tx, declarationJournalEntry(stored)); err != nil {
		return Declaration{}, err
	}
	if err := s.dla.RecordExternalCredit(
		ctx,
		tx,
		dividendSourceRef(stored.ID),
		stored.DeclaredDate,
		stored.Amount,
		defaultDeclarationDescription,
	); err != nil {
		return Declaration{}, err
	}
	if err := s.publishDeclared(ctx, tx, stored); err != nil {
		return Declaration{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Declaration{}, fmt.Errorf("dividends: commit declaration transaction: %w", err)
	}
	committed = true
	s.scheduleDeclarationDocumentsRender(stored.ID)
	return stored, nil
}

// RenderDeclarationDocumentsNow renders and stores both dividend declaration
// documents unless both immutable asset references are already present.
func (s *Service) RenderDeclarationDocumentsNow(ctx context.Context, id DeclarationID) (Declaration, error) {
	if err := s.renderAndStoreDeclarationDocuments(ctx, id); err != nil {
		return Declaration{}, err
	}
	return s.Declaration(ctx, id)
}

// Declaration returns one declaration by id.
func (s *Service) Declaration(ctx context.Context, id DeclarationID) (Declaration, error) {
	if s.pool == nil {
		return Declaration{}, fmt.Errorf("dividends: declaration lookup requires pool")
	}
	return s.store.Declaration(ctx, s.pool, id)
}

// DeclarationDocumentPayload returns the exact snapshot payload consumed by
// the React dividend print routes.
func (s *Service) DeclarationDocumentPayload(ctx context.Context, id DeclarationID) (DividendDocumentPayload, error) {
	declaration, err := s.Declaration(ctx, id)
	if err != nil {
		return DividendDocumentPayload{}, err
	}
	if err := requireDocumentSnapshots(declaration); err != nil {
		return DividendDocumentPayload{}, err
	}
	return DividendDocumentPayload{Declaration: declaration}, nil
}

func (s *Service) scheduleDeclarationDocumentsRender(id DeclarationID) {
	if s.documentPDFEngine == nil || s.documentAssetStore == nil {
		return
	}
	declarationID := DeclarationID(strings.TrimSpace(string(id)))
	if declarationID == "" {
		return
	}
	go s.renderDeclarationDocumentsWithRetry(declarationID)
}

func (s *Service) renderDeclarationDocumentsWithRetry(id DeclarationID) {
	backoff := s.documentRetryBackoff
	for attempt := 1; attempt <= defaultDocumentRetryAttempts; attempt++ {
		err := s.renderAndStoreDeclarationDocuments(context.Background(), id)
		if err == nil {
			return
		}
		s.logDocumentRenderFailure(id, attempt, err)
		if attempt == defaultDocumentRetryAttempts || backoff <= 0 {
			continue
		}
		time.Sleep(backoff * time.Duration(1<<(attempt-1)))
	}
}

func (s *Service) renderAndStoreDeclarationDocuments(ctx context.Context, id DeclarationID) error {
	if s.documentPDFEngine == nil {
		return fmt.Errorf("dividends: document PDF renderer is not configured")
	}
	if s.documentAssetStore == nil {
		return fmt.Errorf("dividends: document PDF asset store is not configured")
	}
	payload, err := s.DeclarationDocumentPayload(ctx, id)
	if err != nil {
		return err
	}
	if payload.Declaration.VoucherAsset != nil && strings.TrimSpace(string(*payload.Declaration.VoucherAsset)) != "" &&
		payload.Declaration.MinutesAsset != nil && strings.TrimSpace(string(*payload.Declaration.MinutesAsset)) != "" {
		return nil
	}

	voucherPDF, err := s.documentPDFEngine.RenderDividendVoucherPDF(ctx, payload)
	if err != nil {
		return err
	}
	minutesPDF, err := s.documentPDFEngine.RenderBoardMinutesPDF(ctx, payload)
	if err != nil {
		return err
	}
	voucherAsset, err := s.documentAssetStore.StoreDividendDocumentPDF(ctx, voucherPDF)
	if err != nil {
		return err
	}
	minutesAsset, err := s.documentAssetStore.StoreDividendDocumentPDF(ctx, minutesPDF)
	if err != nil {
		return err
	}
	if _, err := s.store.SetDeclarationDocumentAssets(ctx, s.pool, payload.Declaration.ID, voucherAsset, minutesAsset); err != nil {
		return err
	}
	return nil
}

func (s *Service) logDocumentRenderFailure(id DeclarationID, attempt int, err error) {
	logger := s.logger
	if logger == nil {
		return
	}
	logger.Error("dividend document render failed", "declaration_id", string(id), "attempt", attempt, "error", err)
}

func (s *Service) validateAt(
	ctx context.Context,
	tx db.Tx,
	amount money.Money,
	asOf time.Time,
) (ValidationResult, error) {
	normalized, err := normalizeDeclarationAmount(amount)
	if err != nil {
		return ValidationResult{}, err
	}
	if s.ledger == nil {
		return ValidationResult{}, fmt.Errorf("ledger: %w", ErrMissingProvider)
	}
	if s.reports == nil {
		return ValidationResult{}, fmt.Errorf("reports: %w", ErrMissingProvider)
	}
	if s.identity == nil {
		return ValidationResult{}, fmt.Errorf("identity: %w", ErrMissingProvider)
	}
	facts, err := s.identity.CompanyFacts(ctx)
	if err != nil {
		return ValidationResult{}, err
	}
	headroom, err := s.headroomWithFacts(ctx, tx, facts, asOf)
	if err != nil {
		return ValidationResult{}, err
	}
	taxYear, err := jurisdiction.TaxYearForDate(asOf)
	if err != nil {
		return ValidationResult{}, err
	}
	withholding, err := jurisdiction.DividendWithholding(taxYear)
	if err != nil {
		return ValidationResult{}, err
	}
	taxFrom, _, err := jurisdiction.TaxYearPeriod(taxYear)
	if err != nil {
		return ValidationResult{}, err
	}
	priorYTD, err := s.declaredInPeriod(ctx, tx, taxFrom, asOf)
	if err != nil {
		return ValidationResult{}, err
	}
	withDividend, err := priorYTD.Add(normalized)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("dividends: add candidate dividend to personal tax YTD: %w", err)
	}
	priorEstimate, err := jurisdiction.PersonalTaxEstimate(taxYear, priorYTD)
	if err != nil {
		return ValidationResult{}, err
	}
	totalEstimate, err := jurisdiction.PersonalTaxEstimate(taxYear, withDividend)
	if err != nil {
		return ValidationResult{}, err
	}
	marginal, err := totalEstimate.Total.Sub(priorEstimate.Total)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("dividends: marginal personal tax estimate: %w", err)
	}

	cmp, err := normalized.Cmp(headroom.Available)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("dividends: compare amount to headroom: %w", err)
	}
	result := ValidationResult{
		Amount:             normalized,
		Headroom:           headroom,
		WithinHeadroom:     headroom.Distributable && cmp <= 0,
		Distributable:      headroom.Distributable,
		DistributableTotal: headroom.Available,
		Withholding: WithholdingValidation{
			TaxYear:       taxYear,
			Policy:        withholding,
			Applies:       dividendWithholdingApplies(withholding),
			Informational: true,
		},
		PersonalTax: PersonalTaxValidation{
			TaxYear:       taxYear,
			PriorYTD:      priorYTD,
			WithDividend:  withDividend,
			PriorEstimate: priorEstimate,
			TotalEstimate: totalEstimate,
			Marginal:      marginal,
		},
	}
	result.PersonalTax.Message, err = jurisdiction.DividendPersonalTaxSetAsideMessage(taxYear, marginal)
	if err != nil {
		return ValidationResult{}, err
	}

	if !headroom.Distributable {
		return result, &NonDistributableYearError{
			FinancialYear: headroom.FinancialYear,
			Distributable: headroom.Available,
		}
	}
	if cmp > 0 {
		return result, &OverHeadroomError{
			Amount:        normalized,
			Distributable: headroom.Available,
		}
	}
	profile, err := s.identity.Profile(ctx)
	if err != nil {
		return ValidationResult{}, err
	}
	shareholder, err := declarationShareholder(profile)
	if err != nil {
		return ValidationResult{}, err
	}
	if _, err := perShareAmount(normalized, shareholder.Shares); err != nil {
		return ValidationResult{}, err
	}
	return result, nil
}

// DeclaredInYear returns total declared dividends inside the company financial
// year identified by financialYear, using identity CompanyFacts boundaries.
func (s *Service) DeclaredInYear(ctx context.Context, financialYear string) (money.Money, error) {
	if s.identity == nil {
		return money.Money{}, fmt.Errorf("identity: %w", ErrMissingProvider)
	}
	facts, err := s.identity.CompanyFacts(ctx)
	if err != nil {
		return money.Money{}, err
	}
	return s.declaredInYearWithFacts(ctx, nil, financialYear, facts)
}

// History returns declarations newest first.
func (s *Service) History(ctx context.Context) ([]Declaration, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("dividends: history requires pool")
	}
	return s.store.Declarations(ctx, s.pool)
}

func (s *Service) profitYTD(ctx context.Context, tx db.Tx, financialYear string) (money.Money, error) {
	if tx == nil {
		return s.reports.ProfitYTD(ctx, financialYear)
	}
	reportsInTx, ok := s.reports.(interface {
		ProfitYTDInTx(context.Context, db.Tx, string) (money.Money, error)
	})
	if !ok {
		return money.Money{}, fmt.Errorf("reports: transaction-scoped ProfitYTD unavailable: %w", ErrMissingProvider)
	}
	return reportsInTx.ProfitYTDInTx(ctx, tx, financialYear)
}

func (s *Service) declaredInYearWithFacts(
	ctx context.Context,
	tx db.Tx,
	financialYear string,
	facts identity.CompanyFacts,
) (money.Money, error) {
	period, err := financialYearPeriod(financialYear, facts.YearEnd.Month, facts.YearEnd.Day)
	if err != nil {
		return money.Money{}, err
	}
	return s.declaredInPeriod(ctx, tx, period.From, period.To)
}

func (s *Service) declaredInPeriod(ctx context.Context, tx db.Tx, from time.Time, to time.Time) (money.Money, error) {
	if tx != nil {
		return s.store.DeclaredInPeriod(ctx, tx, from, to)
	}
	if s.pool == nil {
		return money.Money{}, fmt.Errorf("dividends: declared-in-year requires pool")
	}
	return s.store.DeclaredInPeriod(ctx, s.pool, from, to)
}

func lockDeclarationMutation(ctx context.Context, tx db.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, int32(0x44495632), int32(1)); err != nil {
		return fmt.Errorf("dividends: lock declaration mutation: %w", err)
	}
	return nil
}

func normalizeDeclarationAmount(amount money.Money) (money.Money, error) {
	currency := strings.ToUpper(strings.TrimSpace(amount.Currency))
	if currency != gbpCurrency {
		return money.Money{}, fmt.Errorf("dividends: amount currency %q must be GBP: %w", amount.Currency, ErrInvalidDeclaration)
	}
	if amount.Amount <= 0 {
		return money.Money{}, fmt.Errorf("dividends: amount must be positive: %w", ErrNonPositiveAmount)
	}
	return money.Money{Amount: amount.Amount, Currency: gbpCurrency}, nil
}

func declarationShareholder(profile identity.CompanyProfile) (identity.Shareholder, error) {
	if len(profile.Shareholders) != 1 {
		return identity.Shareholder{}, fmt.Errorf("dividends: expected exactly one shareholder snapshot, got %d: %w",
			len(profile.Shareholders),
			ErrInvalidDeclaration,
		)
	}
	shareholder := profile.Shareholders[0]
	shareholder.Name = strings.TrimSpace(shareholder.Name)
	if shareholder.Name == "" {
		return identity.Shareholder{}, fmt.Errorf("dividends: shareholder name is required: %w", ErrInvalidDeclaration)
	}
	if shareholder.Shares <= 0 {
		return identity.Shareholder{}, fmt.Errorf("dividends: shareholder shares must be positive: %w", ErrInvalidDeclaration)
	}
	shareholder.Class = strings.TrimSpace(shareholder.Class)
	if shareholder.Class == "" {
		return identity.Shareholder{}, fmt.Errorf("dividends: shareholder share class is required: %w", ErrInvalidDeclaration)
	}
	return shareholder, nil
}

func declarationCompanySnapshot(profile identity.CompanyProfile, shareholder identity.Shareholder) (CompanySnapshot, error) {
	snapshot := CompanySnapshot{
		TradingName:      strings.TrimSpace(profile.TradingName),
		LegalName:        strings.TrimSpace(profile.LegalName),
		CompanyNumber:    strings.TrimSpace(profile.CompanyNumber),
		RegisteredOffice: profile.RegisteredOffice,
		DirectorName:     strings.TrimSpace(shareholder.Name),
	}
	normalized, err := normalizeCompanySnapshot(&snapshot)
	if err != nil {
		return CompanySnapshot{}, err
	}
	return *normalized, nil
}

func declarationShareholderSnapshot(shareholder identity.Shareholder) (ShareholderSnapshot, error) {
	snapshot := ShareholderSnapshot{
		Name:   strings.TrimSpace(shareholder.Name),
		Shares: shareholder.Shares,
		Class:  strings.TrimSpace(shareholder.Class),
	}
	normalized, err := normalizeShareholderSnapshot(&snapshot)
	if err != nil {
		return ShareholderSnapshot{}, err
	}
	return *normalized, nil
}

func dividendWithholdingNote(policy string) string {
	trimmed := strings.TrimSpace(policy)
	if trimmed == "" {
		return "Dividend withholding policy was not provided by the active jurisdiction pack."
	}
	if strings.EqualFold(trimmed, "none") {
		return "No dividend withholding tax is deducted under the active jurisdiction pack (withholding: none)."
	}
	return "Dividend withholding follows the active jurisdiction pack policy: " + trimmed + "."
}

func requireDocumentSnapshots(declaration Declaration) error {
	switch {
	case declaration.CompanySnapshot == nil:
		return fmt.Errorf("dividends: declaration %s missing company snapshot: %w", declaration.ID, ErrInvalidDeclaration)
	case declaration.ShareholderSnapshot == nil:
		return fmt.Errorf("dividends: declaration %s missing shareholder snapshot: %w", declaration.ID, ErrInvalidDeclaration)
	case declaration.HeadroomSnapshot == nil:
		return fmt.Errorf("dividends: declaration %s missing headroom snapshot: %w", declaration.ID, ErrInvalidDeclaration)
	case declaration.WithholdingSnapshot == nil:
		return fmt.Errorf("dividends: declaration %s missing withholding snapshot: %w", declaration.ID, ErrInvalidDeclaration)
	default:
		return nil
	}
}

func perShareAmount(amount money.Money, shares int64) (money.Money, error) {
	if shares <= 0 {
		return money.Money{}, fmt.Errorf("dividends: shares must be positive: %w", ErrInvalidDeclaration)
	}
	if amount.Amount%shares != 0 {
		return money.Money{}, fmt.Errorf("dividends: amount %s cannot be represented as a uniform per-share amount across %d shares: %w",
			amount.Format(),
			shares,
			ErrInvalidDeclaration,
		)
	}
	perShare := money.Money{Amount: amount.Amount / shares, Currency: amount.Currency}
	if perShare.Amount <= 0 {
		return money.Money{}, fmt.Errorf("dividends: per share amount must be positive: %w", ErrInvalidDeclaration)
	}
	return perShare, nil
}

func declarationJournalEntry(declaration Declaration) ledgerapi.NewJournalEntry {
	creditDLA := money.Money{Amount: -declaration.Amount.Amount, Currency: declaration.Amount.Currency}
	return ledgerapi.NewJournalEntry{
		Date:         declaration.DeclaredDate,
		Description:  defaultDeclarationDescription,
		SourceModule: ModuleName,
		SourceRef:    dividendSourceRef(declaration.ID),
		Postings: []ledgerapi.NewPosting{
			{AccountCode: RetainedEarningsAccountCode, Amount: declaration.Amount, AmountGBP: declaration.Amount},
			{AccountCode: dla.DLAAccountCode, Amount: creditDLA, AmountGBP: creditDLA},
		},
	}
}

func dividendSourceRef(id DeclarationID) string {
	return dividendSourcePrefix + string(id)
}

func dividendWithholdingApplies(policy string) bool {
	normalized := strings.ToLower(strings.TrimSpace(policy))
	return normalized != "" && normalized != "none"
}

func (s *Service) publishDeclared(ctx context.Context, tx db.Tx, declaration Declaration) error {
	if s.bus == nil {
		return nil
	}
	if err := s.bus.Publish(ctx, tx, Declared{
		DeclarationID: declaration.ID,
		Amount:        declaration.Amount,
	}); err != nil {
		return fmt.Errorf("dividends: publish declared: %w", err)
	}
	return nil
}

func newDeclarationID() (DeclarationID, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("dividends: generate declaration id: %w", err)
	}
	return DeclarationID("dividend_" + hex.EncodeToString(bytes[:])), nil
}

func (s *Service) now() time.Time {
	clk := s.clock
	if clk == nil {
		clk = clock.New()
	}
	return clk.Now()
}

func retainedEarningsAmount(balance money.Money) (money.Money, error) {
	if balance.Currency != gbpCurrency {
		return money.Money{}, fmt.Errorf("dividends: retained earnings currency %q, want GBP", balance.Currency)
	}
	retained, err := balance.Negate()
	if err != nil {
		return money.Money{}, fmt.Errorf("dividends: retained earnings presentation amount: %w", err)
	}
	retained.Currency = gbpCurrency
	return retained, nil
}

func corporateTaxAmount(profit money.Money, rate jurisdiction.Rate) (money.Money, error) {
	if profit.Currency != gbpCurrency {
		return money.Money{}, fmt.Errorf("dividends: profit currency %q, want GBP", profit.Currency)
	}
	if profit.Amount <= 0 {
		return money.Zero(gbpCurrency), nil
	}
	rat, ok := new(big.Rat).SetString(strings.TrimSpace(string(rate)))
	if !ok {
		return money.Money{}, fmt.Errorf("dividends: parse corporate rate %q", rate)
	}
	tax := profit.MulRat(rat)
	tax.Currency = gbpCurrency
	return tax, nil
}

type financialPeriod struct {
	From time.Time
	To   time.Time
}

func financialYearForDate(date time.Time, month time.Month, day int) (string, error) {
	normalized, err := normalizeDate(date)
	if err != nil {
		return "", err
	}
	yearEnd, err := financialYearEndDate(normalized.Year(), month, day)
	if err != nil {
		return "", err
	}
	endYear := normalized.Year()
	if normalized.After(yearEnd) {
		endYear++
	}
	startYear := endYear - 1
	return fmt.Sprintf("%04d-%02d", startYear, endYear%100), nil
}

func financialYearPeriod(financialYear string, month time.Month, day int) (financialPeriod, error) {
	startYear, endYear, err := parseFinancialYear(financialYear)
	if err != nil {
		return financialPeriod{}, err
	}
	previousEnd, err := financialYearEndDate(startYear, month, day)
	if err != nil {
		return financialPeriod{}, err
	}
	end, err := financialYearEndDate(endYear, month, day)
	if err != nil {
		return financialPeriod{}, err
	}
	return financialPeriod{From: previousEnd.AddDate(0, 0, 1), To: end}, nil
}

func financialYearEndDate(year int, month time.Month, day int) (time.Time, error) {
	if month < time.January || month > time.December || day < 1 {
		return time.Time{}, fmt.Errorf("dividends: invalid year end %d-%02d: %w", month, day, ErrInvalidFinancialYear)
	}
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > lastDay {
		if month != time.February || day != 29 {
			return time.Time{}, fmt.Errorf("dividends: invalid year end %d-%02d: %w", month, day, ErrInvalidFinancialYear)
		}
		day = lastDay
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
}

func parseFinancialYear(financialYear string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(financialYear), "-")
	if len(parts) != 2 || len(parts[0]) != 4 || len(parts[1]) != 2 {
		return 0, 0, fmt.Errorf("dividends: financial year %q must look like 2025-26: %w", financialYear, ErrInvalidFinancialYear)
	}
	startYear, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("dividends: financial year %q start: %w", financialYear, ErrInvalidFinancialYear)
	}
	endSuffix, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("dividends: financial year %q end: %w", financialYear, ErrInvalidFinancialYear)
	}
	endYear := startYear/100*100 + endSuffix
	if endYear <= startYear {
		endYear += 100
	}
	if endYear != startYear+1 {
		return 0, 0, fmt.Errorf("dividends: financial year %q must span one year: %w", financialYear, ErrInvalidFinancialYear)
	}
	return startYear, endYear, nil
}

func formatRatePercent(rate jurisdiction.Rate) string {
	rat, ok := new(big.Rat).SetString(strings.TrimSpace(string(rate)))
	if !ok {
		return strings.TrimSpace(string(rate))
	}
	rat.Mul(rat, big.NewRat(100, 1))
	formatted := rat.FloatString(2)
	formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	if formatted == "" {
		formatted = "0"
	}
	return formatted + "%"
}

func normalizeDate(date time.Time) (time.Time, error) {
	if date.IsZero() {
		return time.Time{}, fmt.Errorf("dividends: date is required: %w", ErrInvalidDeclaration)
	}
	year, month, day := date.UTC().Date()
	if year < 1900 || year > 9999 {
		return time.Time{}, fmt.Errorf("dividends: date %04d-%02d-%02d is outside supported range: %w", year, month, day, ErrInvalidDeclaration)
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
}
