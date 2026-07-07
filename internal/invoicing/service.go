package invoicing

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	"github.com/npmulder/ledgerly/internal/platform/db"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
	"github.com/npmulder/ledgerly/internal/platform/mail"
)

const (
	problemTypeClientNotFound       = "https://ledgerly.local/problems/invoicing/client-not-found"
	problemTypeClientValidation     = "https://ledgerly.local/problems/invoicing/client-validation"
	problemTypeClientCurrencyLocked = "https://ledgerly.local/problems/invoicing/client-currency-locked"
	problemTypeInvoiceNotFound      = "https://ledgerly.local/problems/invoicing/invoice-not-found"
	problemTypeInvoiceValidation    = "https://ledgerly.local/problems/invoicing/invoice-validation"
	problemTypeInvoiceImmutable     = "https://ledgerly.local/problems/invoicing/invoice-immutable"
	problemTypeInvoiceWrongAmount   = "https://ledgerly.local/problems/invoicing/wrong-amount"
	problemTypeInvoiceRate          = "https://ledgerly.local/problems/invoicing/rate-unavailable"
	problemTypeInvoiceReminder      = "https://ledgerly.local/problems/invoicing/reminder-unavailable"
	problemTypeInvoiceReminderLimit = "https://ledgerly.local/problems/invoicing/reminder-rate-limited"
)

const (
	defaultPDFRetryAttempts = 3
	defaultPDFRetryBackoff  = 250 * time.Millisecond
)

// Service orchestrates invoicing client commands and queries.
type Service struct {
	pool               *pgxpool.Pool
	store              Store
	clock              clock.Clock
	todayRate          TodayRateFunc
	rateLocker         RateLocker
	rateLocks          RateLockReader
	ledger             LedgerJournal
	eventBus           *bus.Bus
	invoiceUsage       InvoiceUsageChecker
	identity           identity.Identity
	pdfAssetStore      InvoicePDFAssetStore
	pdfEngine          InvoicePDFEngine
	pdfRetryBackoff    time.Duration
	mailer             mail.Sender
	logger             *slog.Logger
	idGenerator        func() (string, error)
	invoiceIDGenerator func() (string, error)
	lineIDGenerator    func() (string, error)
}

type ServiceOption func(*Service)

// WithInvoiceUsageChecker installs the INV-2 currency-lock dependency. Until
// invoices exist, production uses the no-op checker.
func WithInvoiceUsageChecker(checker InvoiceUsageChecker) ServiceOption {
	return func(s *Service) {
		if checker != nil {
			s.invoiceUsage = checker
		}
	}
}

// WithClock installs the clock used for draft issue-date defaults.
func WithClock(clk clock.Clock) ServiceOption {
	return func(s *Service) {
		if clk != nil {
			s.clock = clk
		}
	}
}

// WithTodayRate installs the moneyfx rate lookup used for draft GBP notes.
func WithTodayRate(todayRate TodayRateFunc) ServiceOption {
	return func(s *Service) {
		if todayRate != nil {
			s.todayRate = todayRate
		}
	}
}

// WithRateLocker installs the moneyfx lock dependency used when sending invoices.
func WithRateLocker(locker RateLocker) ServiceOption {
	return func(s *Service) {
		if locker != nil {
			s.rateLocker = locker
			if reader, ok := locker.(RateLockReader); ok && s.rateLocks == nil {
				s.rateLocks = reader
			}
		}
	}
}

// WithRateLockReader installs the moneyfx lock read dependency used by list
// totals for sent and paid invoices.
func WithRateLockReader(reader RateLockReader) ServiceOption {
	return func(s *Service) {
		if reader != nil {
			s.rateLocks = reader
		}
	}
}

// WithLedger installs the ledger dependency used when sending and unsending invoices.
func WithLedger(journal LedgerJournal) ServiceOption {
	return func(s *Service) {
		if journal != nil {
			s.ledger = journal
		}
	}
}

// WithEventBus installs the transactional event bus used by lifecycle commands.
func WithEventBus(eventBus *bus.Bus) ServiceOption {
	return func(s *Service) {
		if eventBus != nil {
			s.eventBus = eventBus
		}
	}
}

// WithIdentity installs the identity profile API used to snapshot invoice
// render identity.
func WithIdentity(profile identity.Identity) ServiceOption {
	return func(s *Service) {
		if profile != nil {
			s.identity = profile
		}
	}
}

// WithInvoicePDFAssetStore installs immutable PDF asset storage.
func WithInvoicePDFAssetStore(store InvoicePDFAssetStore) ServiceOption {
	return func(s *Service) {
		if store != nil {
			s.pdfAssetStore = store
		}
	}
}

// WithInvoicePDFEngine installs the engine used to render invoice print
// payloads to PDF bytes.
func WithInvoicePDFEngine(engine InvoicePDFEngine) ServiceOption {
	return func(s *Service) {
		if engine != nil {
			s.pdfEngine = engine
		}
	}
}

// WithInvoicePDFBaseURL installs the production chromedp engine when a custom
// engine was not supplied.
func WithInvoicePDFBaseURL(baseURL string) ServiceOption {
	return func(s *Service) {
		if s.pdfEngine == nil && strings.TrimSpace(baseURL) != "" {
			s.pdfEngine = NewChromePDFEngine(baseURL)
		}
	}
}

// WithInvoicePDFRetryBackoff overrides the send-time async retry backoff.
func WithInvoicePDFRetryBackoff(backoff time.Duration) ServiceOption {
	return func(s *Service) {
		if backoff >= 0 {
			s.pdfRetryBackoff = backoff
		}
	}
}

// WithReminderMailer installs the sender used by manual overdue reminders.
func WithReminderMailer(sender mail.Sender) ServiceOption {
	return func(s *Service) {
		if sender != nil {
			s.mailer = sender
		}
	}
}

// WithLogger installs the logger used for non-blocking PDF render failures.
func WithLogger(logger *slog.Logger) ServiceOption {
	return func(s *Service) {
		if logger != nil {
			s.logger = logger
		}
	}
}

func NewService(pool *pgxpool.Pool, store Store, opts ...ServiceOption) *Service {
	service := &Service{
		pool:               pool,
		store:              store,
		clock:              clock.New(),
		todayRate:          defaultTodayRate,
		eventBus:           bus.New(),
		invoiceUsage:       noInvoiceUsageChecker{},
		pdfRetryBackoff:    defaultPDFRetryBackoff,
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		idGenerator:        newClientID,
		invoiceIDGenerator: newInvoiceID,
		lineIDGenerator:    newInvoiceLineID,
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

// Clients returns unarchived clients for picker and Settings list surfaces.
func (s *Service) Clients(ctx context.Context) ([]Client, error) {
	return s.store.ListClients(ctx, s.pool, false)
}

// ClientsIncludingArchived returns all clients for history/debug callers.
func (s *Service) ClientsIncludingArchived(ctx context.Context) ([]Client, error) {
	return s.store.ListClients(ctx, s.pool, true)
}

// Client returns a client by ID, including archived clients for historical
// invoice references.
func (s *Service) Client(ctx context.Context, id string) (Client, error) {
	return s.store.Client(ctx, s.pool, id)
}

// CreateDraft creates an unnumbered draft invoice with client defaults.
func (s *Service) CreateDraft(ctx context.Context, clientID string) (_ Invoice, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Invoice{}, fmt.Errorf("invoicing: begin create draft transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	client, err := s.store.Client(ctx, tx, strings.TrimSpace(clientID))
	if err != nil {
		return Invoice{}, err
	}
	invoiceID, err := s.invoiceIDGenerator()
	if err != nil {
		return Invoice{}, err
	}
	issueDate := dateOnly(s.now())
	draft := Invoice{
		ID:           invoiceID,
		ClientID:     client.ID,
		Status:       InvoiceStatusDraft,
		IssueDate:    issueDate,
		DueDate:      issueDate.AddDate(0, 0, client.TermsDays),
		Currency:     client.DefaultCurrency,
		VATTreatment: client.VATTreatment,
	}
	if err := validateDraftInvoice(draft); err != nil {
		return Invoice{}, err
	}
	created, err := s.store.InsertDraftInvoice(ctx, tx, draft)
	if err != nil {
		return Invoice{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Invoice{}, fmt.Errorf("invoicing: commit create draft transaction: %w", err)
	}
	return s.withComputedTotals(ctx, created)
}

// Invoice returns an invoice with computed line totals and invoice totals.
func (s *Service) Invoice(ctx context.Context, id string) (Invoice, error) {
	invoice, err := s.store.Invoice(ctx, s.pool, strings.TrimSpace(id))
	if err != nil {
		return Invoice{}, err
	}
	return s.withReadComputedTotals(ctx, invoice)
}

// InvoiceVATContextBySendEntryID returns the narrow VAT context for a sent
// invoice's ledger send entry.
func (s *Service) InvoiceVATContextBySendEntryID(ctx context.Context, entryID ledger.EntryID) (InvoiceVATContext, error) {
	if s.pool == nil {
		return InvoiceVATContext{}, fmt.Errorf("invoicing: invoice VAT context requires pool")
	}
	if entryID <= 0 {
		return InvoiceVATContext{}, fmt.Errorf("invoicing: send ledger entry id %d: %w", entryID, ErrInvoicePostingNotFound)
	}
	return s.store.InvoiceVATContextBySendEntryID(ctx, s.pool, int64(entryID))
}

// InvoiceByNumber returns an invoice by its immutable sent invoice number.
func (s *Service) InvoiceByNumber(ctx context.Context, number string) (Invoice, error) {
	invoice, err := s.store.InvoiceByNumber(ctx, s.pool, strings.TrimSpace(number))
	if err != nil {
		return Invoice{}, err
	}
	return s.withReadComputedTotals(ctx, invoice)
}

// UpdateDraft applies a patch to a mutable draft invoice only.
func (s *Service) UpdateDraft(ctx context.Context, id string, patch DraftPatch) (_ Invoice, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Invoice{}, fmt.Errorf("invoicing: begin update draft transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	existing, err := s.store.InvoiceForUpdate(ctx, tx, strings.TrimSpace(id))
	if err != nil {
		return Invoice{}, err
	}
	if existing.Status != InvoiceStatusDraft {
		return Invoice{}, ErrInvoiceImmutable
	}

	next := existing
	if patch.ClientID != nil {
		client, clientErr := s.store.Client(ctx, tx, *patch.ClientID)
		if clientErr != nil {
			return Invoice{}, clientErr
		}
		if client.ArchivedAt != nil {
			return Invoice{}, invoiceValidationError([]FieldError{{Pointer: "/client_id", Detail: "must be an active client"}})
		}
		next.ClientID = client.ID
	}
	if patch.IssueDate != nil {
		next.IssueDate = dateOnly(*patch.IssueDate)
	}
	if patch.DueDate != nil {
		next.DueDate = dateOnly(*patch.DueDate)
	}
	if patch.Currency != nil {
		next.Currency = normalizeCurrencyValue(*patch.Currency)
	}
	if patch.VATTreatment != nil {
		next.VATTreatment = normalizeVATTreatmentValue(*patch.VATTreatment)
	}
	if patch.Lines != nil {
		next.Lines, err = s.normalizeLineInputs(next.ID, next.Currency, *patch.Lines)
		if err != nil {
			return Invoice{}, err
		}
	}
	if err := validateDraftInvoice(next); err != nil {
		return Invoice{}, err
	}

	updated, err := s.store.UpdateDraftInvoice(ctx, tx, next)
	if err != nil {
		return Invoice{}, err
	}
	if patch.Lines != nil {
		if err := s.store.ReplaceInvoiceLines(ctx, tx, next.ID, next.Lines); err != nil {
			return Invoice{}, err
		}
		updated.Lines = next.Lines
	} else {
		updated.Lines = existing.Lines
	}
	if err = tx.Commit(ctx); err != nil {
		return Invoice{}, fmt.Errorf("invoicing: commit update draft transaction: %w", err)
	}
	return s.withComputedTotals(ctx, updated)
}

// Delete removes drafts only. Sent and paid invoices are immutable here.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.DeleteDraft(ctx, id)
}

// DeleteDraft removes a draft invoice and its lines.
func (s *Service) DeleteDraft(ctx context.Context, id string) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("invoicing: begin delete draft transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	if err := s.store.DeleteDraft(ctx, tx, strings.TrimSpace(id)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("invoicing: commit delete draft transaction: %w", err)
	}
	return nil
}

// Send assigns an immutable invoice number, locks the send-date FX rate, posts
// the debtor/sales ledger entry, and publishes InvoiceSent in one transaction.
func (s *Service) Send(ctx context.Context, id string) (_ Invoice, err error) {
	if s.pool == nil {
		return Invoice{}, fmt.Errorf("invoicing: send requires pool")
	}
	if s.rateLocker == nil {
		return Invoice{}, fmt.Errorf("invoicing: send requires rate locker")
	}
	if s.ledger == nil {
		return Invoice{}, fmt.Errorf("invoicing: send requires ledger")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Invoice{}, fmt.Errorf("invoicing: begin send transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	draft, err := s.store.InvoiceForUpdate(ctx, tx, strings.TrimSpace(id))
	if err != nil {
		return Invoice{}, err
	}
	if err := validateSendableDraft(draft); err != nil {
		return Invoice{}, err
	}
	vatRegisteredAtSend, err := s.invoiceCompanyVATRegistered(ctx, draft)
	if err != nil {
		return Invoice{}, err
	}
	draft.VATRegisteredAtSend = &vatRegisteredAtSend
	draft.VATRegistered = vatRegisteredAtSend
	draft, err = s.computeTotals(ctx, draft, false)
	if err != nil {
		return Invoice{}, err
	}

	number, err := s.store.NextNumber(ctx, tx, draft.IssueDate.Year())
	if err != nil {
		return Invoice{}, err
	}
	lock, err := s.rateLocker.LockRate(
		ctx,
		tx,
		RateLockRef{Module: ModuleName, Ref: number},
		string(draft.Currency),
		string(CurrencyGBP),
		draft.IssueDate,
	)
	if err != nil {
		return Invoice{}, err
	}

	lockID := strconv.FormatInt(lock.ID, 10)
	sentForPosting := draft
	sentForPosting.Number = &number
	sentForPosting.LockID = &lockID
	sentForPosting.Status = InvoiceStatusSent

	entry, err := invoiceSendJournalEntry(sentForPosting, lock)
	if err != nil {
		return Invoice{}, err
	}
	entryID, err := s.ledger.Post(ctx, tx, entry)
	if err != nil {
		return Invoice{}, fmt.Errorf("invoicing: post send ledger entry: %w", err)
	}
	sent, err := s.store.SetInvoiceSent(ctx, tx, draft.ID, number, lock.ID, int64(entryID), s.now(), vatRegisteredAtSend)
	if err != nil {
		return Invoice{}, err
	}
	sent.Lines = draft.Lines
	sent.Totals = draft.Totals
	sent.VATRegistered = vatRegisteredAtSend
	sent.sendRateLock = &lock
	if err := s.publish(ctx, tx, InvoiceSent{
		InvoiceID: sent.ID,
		Number:    number,
		ClientID:  sent.ClientID,
		Amount:    sent.Totals.Total,
		DueDate:   sent.DueDate,
	}); err != nil {
		return Invoice{}, fmt.Errorf("invoicing: publish invoice sent: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return Invoice{}, fmt.Errorf("invoicing: commit send transaction: %w", err)
	}
	s.scheduleInvoicePDFRender(sent.ID)
	return sent, nil
}

// RenderInvoicePDFNow renders and stores a sent invoice PDF unless the invoice
// already points at an immutable PDF asset.
func (s *Service) RenderInvoicePDFNow(ctx context.Context, id string) (Invoice, error) {
	if err := s.renderAndStoreInvoicePDF(ctx, strings.TrimSpace(id)); err != nil {
		return Invoice{}, err
	}
	return s.Invoice(ctx, id)
}

// PreviewDraftInvoicePDF renders a DRAFT-watermarked PDF without storing it.
func (s *Service) PreviewDraftInvoicePDF(ctx context.Context, id string) ([]byte, error) {
	if s.pdfEngine == nil {
		return nil, fmt.Errorf("invoicing: invoice PDF renderer is not configured")
	}
	payload, err := s.InvoicePrintPayload(ctx, strings.TrimSpace(id), true)
	if err != nil {
		return nil, err
	}
	if payload.Invoice.Status != InvoiceStatusDraft {
		return nil, ErrInvoiceImmutable
	}
	return s.pdfEngine.RenderInvoicePDF(ctx, payload)
}

func (s *Service) scheduleInvoicePDFRender(id string) {
	if s.pdfEngine == nil || s.pdfAssetStore == nil {
		return
	}
	invoiceID := strings.TrimSpace(id)
	if invoiceID == "" {
		return
	}
	go s.renderInvoicePDFWithRetry(invoiceID)
}

func (s *Service) renderInvoicePDFWithRetry(id string) {
	backoff := s.pdfRetryBackoff
	for attempt := 1; attempt <= defaultPDFRetryAttempts; attempt++ {
		err := s.renderAndStoreInvoicePDF(context.Background(), id)
		if err == nil {
			return
		}
		s.logPDFRenderFailure(id, attempt, err)
		if attempt == defaultPDFRetryAttempts || backoff <= 0 {
			continue
		}
		time.Sleep(backoff * time.Duration(1<<(attempt-1)))
	}
}

func (s *Service) renderAndStoreInvoicePDF(ctx context.Context, id string) error {
	if s.pdfEngine == nil {
		return fmt.Errorf("invoicing: invoice PDF renderer is not configured")
	}
	if s.pdfAssetStore == nil {
		return fmt.Errorf("invoicing: invoice PDF asset store is not configured")
	}
	payload, err := s.InvoicePrintPayload(ctx, strings.TrimSpace(id), false)
	if err != nil {
		return err
	}
	if payload.Invoice.PDFAsset != nil && strings.TrimSpace(*payload.Invoice.PDFAsset) != "" {
		return nil
	}
	switch payload.Invoice.Status {
	case InvoiceStatusSent, InvoiceStatusPaid, InvoiceStatusOverdue:
	default:
		return ErrInvoiceImmutable
	}

	pdfBytes, err := s.pdfEngine.RenderInvoicePDF(ctx, payload)
	if err != nil {
		return err
	}
	assetURL, err := s.pdfAssetStore.StoreInvoicePDF(ctx, pdfBytes)
	if err != nil {
		return err
	}
	if _, err := s.store.SetInvoicePDFAsset(ctx, s.pool, payload.Invoice.ID, assetURL); err != nil {
		return err
	}
	return nil
}

func (s *Service) logPDFRenderFailure(id string, attempt int, err error) {
	logger := s.logger
	if logger == nil {
		return
	}
	logger.Error("invoice PDF render failed", "invoice_id", id, "attempt", attempt, "error", err)
}

// InvoicePrintPayload returns the exact payload consumed by the React print route.
func (s *Service) InvoicePrintPayload(ctx context.Context, id string, draftWatermark bool) (InvoicePrintPayload, error) {
	if s.identity == nil {
		return InvoicePrintPayload{}, fmt.Errorf("invoicing: identity profile API is not configured")
	}
	invoice, err := s.Invoice(ctx, strings.TrimSpace(id))
	if err != nil {
		return InvoicePrintPayload{}, err
	}
	client, err := s.store.Client(ctx, s.pool, invoice.ClientID)
	if err != nil {
		return InvoicePrintPayload{}, err
	}
	profile, err := s.identity.Profile(ctx)
	if err != nil {
		return InvoicePrintPayload{}, err
	}
	vatRate, vatTaxYear, err := jurisdiction.VATStandardRateForDate(invoice.IssueDate)
	if err != nil {
		return InvoicePrintPayload{}, fmt.Errorf("invoicing: invoice print VAT rate: %w", err)
	}
	reverseChargeNote, err := reverseChargeInvoiceNote(invoice.VATTreatment)
	if err != nil {
		return InvoicePrintPayload{}, err
	}
	lockedRate, err := s.invoicePrintLockedRate(ctx, invoice)
	if err != nil {
		return InvoicePrintPayload{}, err
	}
	vatRegisteredForPrint := invoiceVATRegisteredForPrint(invoice, profile.IsVATRegistered)
	return InvoicePrintPayload{
		Invoice:           invoice,
		Client:            client,
		Identity:          s.invoicePrintIdentity(ctx, profile, vatRegisteredForPrint),
		VATRegistered:     vatRegisteredForPrint,
		VATRate:           vatRate.String(),
		VATTaxYear:        vatTaxYear,
		ReverseChargeNote: reverseChargeNote,
		LockedRate:        lockedRate,
		DraftWatermark:    draftWatermark,
	}, nil
}

func (s *Service) invoicePrintIdentity(ctx context.Context, profile identity.CompanyProfile, vatRegistered bool) InvoicePrintIdentity {
	var (
		logoAssetURL *string
		logoDataURI  *string
	)
	if profile.LogoAssetID != nil {
		url := "/api/identity/assets/" + string(*profile.LogoAssetID)
		logoAssetURL = &url
		if asset, err := s.identity.Asset(ctx, *profile.LogoAssetID); err == nil {
			dataURI := "data:" + asset.MIME + ";base64," + base64.StdEncoding.EncodeToString(asset.Bytes)
			logoDataURI = &dataURI
		}
	}
	vatNumber := profile.VATNumber
	if !vatRegistered {
		vatNumber = nil
	}
	return InvoicePrintIdentity{
		TradingName:   profile.TradingName,
		LegalName:     profile.LegalName,
		CompanyNumber: profile.CompanyNumber,
		Address: Address{
			Line1:      profile.RegisteredOffice.Line1,
			Line2:      profile.RegisteredOffice.Line2,
			Locality:   profile.RegisteredOffice.Locality,
			Region:     profile.RegisteredOffice.Region,
			PostalCode: profile.RegisteredOffice.PostalCode,
			Country:    profile.RegisteredOffice.Country,
		},
		VATNumber:    vatNumber,
		IBAN:         profile.BankDetails.IBAN,
		BIC:          profile.BankDetails.BIC,
		BankName:     profile.BankDetails.BankName,
		LogoAssetURL: logoAssetURL,
		LogoDataURI:  logoDataURI,
	}
}

func reverseChargeInvoiceNote(treatment VATTreatment) (*string, error) {
	semantics, err := jurisdiction.VATSemanticsForTreatment(string(treatment))
	if err != nil {
		return nil, fmt.Errorf("invoicing: invoice print VAT semantics: %w", err)
	}
	if strings.TrimSpace(semantics.ReverseChargeKind) == "" {
		return nil, nil
	}
	wording, err := jurisdiction.ReverseChargeWording(semantics.ReverseChargeKind)
	if err != nil {
		return nil, fmt.Errorf("invoicing: invoice print reverse-charge wording: %w", err)
	}
	note := strings.TrimSpace(wording.InvoiceWording)
	if note == "" {
		return nil, nil
	}
	return &note, nil
}

func (s *Service) invoicePrintLockedRate(ctx context.Context, invoice Invoice) (*InvoicePrintLockedRate, error) {
	if invoice.LockID == nil || strings.TrimSpace(*invoice.LockID) == "" || s.rateLocks == nil {
		return nil, nil
	}
	lockID, err := invoiceLockID(invoice)
	if err != nil {
		return nil, err
	}
	lock, err := s.rateLocks.RateLock(ctx, lockID)
	if err != nil {
		return nil, fmt.Errorf("invoicing: invoice print locked rate: %w", err)
	}
	return &InvoicePrintLockedRate{ID: lock.ID, Rate: lock.Rate}, nil
}

// MarkSettled records a full invoice settlement inside the caller's
// transaction and publishes InvoiceSettled for realised-FX handling.
func (s *Service) MarkSettled(ctx context.Context, tx db.Tx, id string, txnRef string, date time.Time, amount Money) (Invoice, error) {
	if tx == nil {
		return Invoice{}, fmt.Errorf("invoicing: mark settled requires transaction")
	}

	invoice, err := s.store.InvoiceForUpdate(ctx, tx, strings.TrimSpace(id))
	if err != nil {
		return Invoice{}, err
	}
	if invoice.Status != InvoiceStatusSent {
		return Invoice{}, ErrInvoiceImmutable
	}
	invoice, err = s.computeTotals(ctx, invoice, false)
	if err != nil {
		return Invoice{}, err
	}

	normalizedAmount, err := normalizeSettlementAmount(amount)
	if err != nil {
		return Invoice{}, err
	}
	if err := validateSettlementAmount(invoice.Totals.Total, normalizedAmount); err != nil {
		return Invoice{}, err
	}
	if strings.TrimSpace(txnRef) == "" {
		return Invoice{}, invoiceValidationError([]FieldError{{Pointer: "/settlement_txn_ref", Detail: "is required"}})
	}
	settlementDate := dateOnly(date)
	if settlementDate.IsZero() {
		return Invoice{}, invoiceValidationError([]FieldError{{Pointer: "/settled_date", Detail: "is required"}})
	}
	if invoice.Number == nil || strings.TrimSpace(*invoice.Number) == "" {
		return Invoice{}, fmt.Errorf("invoicing: sent invoice %s has no number", invoice.ID)
	}
	lockID, err := invoiceLockID(invoice)
	if err != nil {
		return Invoice{}, err
	}

	settled, err := s.store.SetInvoiceSettlement(ctx, tx, invoice.ID, InvoiceSettlement{
		TxnRef:        stringPtr(strings.TrimSpace(txnRef)),
		SettledDate:   &settlementDate,
		SettledAmount: &normalizedAmount,
	})
	if err != nil {
		return Invoice{}, err
	}
	settled.Lines = invoice.Lines
	settled.Totals = invoice.Totals
	settled.VATRegistered = invoice.VATRegistered

	if err := s.publish(ctx, tx, InvoiceSettled{
		InvoiceID:      invoice.ID,
		InvoiceNumber:  *invoice.Number,
		LockID:         lockID,
		NativeAmount:   normalizedAmount,
		SettlementDate: settlementDate,
		SourceRef:      invoiceSettlementSourceRef(invoice.ID),
	}); err != nil {
		return Invoice{}, fmt.Errorf("invoicing: publish invoice settled: %w", err)
	}
	return settled, nil
}

// RevertToDraft unsends a same-day, unsettled invoice by reversing the send
// ledger entry and returning the invoice to draft. The previous invoice number
// is deliberately not reused: the numbering row is left advanced, the draft's
// number and lock are cleared, and a later resend consumes a fresh number and
// creates a fresh FX lock.
func (s *Service) RevertToDraft(ctx context.Context, id string) (_ Invoice, err error) {
	if s.pool == nil {
		return Invoice{}, fmt.Errorf("invoicing: revert to draft requires pool")
	}
	if s.ledger == nil {
		return Invoice{}, fmt.Errorf("invoicing: revert to draft requires ledger")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Invoice{}, fmt.Errorf("invoicing: begin revert transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	invoice, err := s.store.InvoiceForUpdate(ctx, tx, strings.TrimSpace(id))
	if err != nil {
		return Invoice{}, err
	}
	if err := validateRevertibleInvoice(invoice, dateOnly(s.now())); err != nil {
		return Invoice{}, err
	}
	if invoice.SendLedgerEntryID == nil || *invoice.SendLedgerEntryID <= 0 {
		return Invoice{}, ErrInvoicePostingNotFound
	}
	entryID := ledger.EntryID(*invoice.SendLedgerEntryID)
	if _, err := s.ledger.Reverse(ctx, tx, entryID, "invoice reverted to draft"); err != nil {
		return Invoice{}, fmt.Errorf("invoicing: reverse send ledger entry: %w", err)
	}
	reverted, err := s.store.RevertSentToDraft(ctx, tx, invoice.ID)
	if err != nil {
		return Invoice{}, err
	}
	reverted.Lines = invoice.Lines
	reverted, err = s.withComputedTotals(ctx, reverted)
	if err != nil {
		return Invoice{}, err
	}

	if err = tx.Commit(ctx); err != nil {
		return Invoice{}, fmt.Errorf("invoicing: commit revert transaction: %w", err)
	}
	return reverted, nil
}

// SaveClient creates a new client when c.ID is empty, otherwise updates the
// existing client while preserving archived history fields.
func (s *Service) SaveClient(ctx context.Context, c Client) (_ Client, err error) {
	c, err = normalizeClient(c)
	if err != nil {
		return Client{}, err
	}

	if c.ID == "" {
		c.ID, err = s.idGenerator()
		if err != nil {
			return Client{}, err
		}
		return s.store.InsertClient(ctx, s.pool, c)
	}

	return s.updateClient(ctx, c.ID, func(Client) (Client, error) {
		return c, nil
	})
}

func (s *Service) patchClient(ctx context.Context, id string, apply func(Client) (Client, error)) (Client, error) {
	return s.updateClient(ctx, id, apply)
}

func (s *Service) updateClient(ctx context.Context, id string, build func(Client) (Client, error)) (_ Client, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Client{}, fmt.Errorf("invoicing: begin save client transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	existing, err := s.store.ClientForUpdate(ctx, tx, id)
	if err != nil {
		return Client{}, err
	}

	next, err := build(existing)
	if err != nil {
		return Client{}, err
	}
	next, err = normalizeClient(next)
	if err != nil {
		return Client{}, err
	}
	next.ID = existing.ID
	next.CreatedAt = existing.CreatedAt
	next.ArchivedAt = existing.ArchivedAt

	if err := s.ensureCurrencyMutable(ctx, existing, next); err != nil {
		return Client{}, err
	}

	updated, err := s.store.UpdateClient(ctx, tx, next)
	if err != nil {
		return Client{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Client{}, fmt.Errorf("invoicing: commit save client transaction: %w", err)
	}
	return updated, nil
}

// ArchiveClient soft-archives a client so invoices can keep referencing it.
func (s *Service) ArchiveClient(ctx context.Context, id string) error {
	return s.store.ArchiveClient(ctx, s.pool, id)
}

func (s *Service) ensureCurrencyMutable(ctx context.Context, existing Client, next Client) error {
	if existing.DefaultCurrency == next.DefaultCurrency {
		return nil
	}
	hasInvoices, err := s.invoiceUsage.ClientHasInvoices(ctx, existing.ID)
	if err != nil {
		return fmt.Errorf("invoicing: check client invoice usage: %w", err)
	}
	if hasInvoices {
		return ErrClientCurrencyLocked
	}
	return nil
}

func validateSendableDraft(invoice Invoice) error {
	if err := validateDraftInvoice(invoice); err != nil {
		return err
	}
	if len(invoice.Lines) == 0 {
		return invoiceValidationError([]FieldError{{Pointer: "/lines", Detail: "must include at least one line"}})
	}
	if invoice.Number != nil {
		return invoiceValidationError([]FieldError{{Pointer: "/number", Detail: "must be empty before send"}})
	}
	if invoice.LockID != nil {
		return invoiceValidationError([]FieldError{{Pointer: "/lock_id", Detail: "must be empty before send"}})
	}
	return nil
}

func validateRevertibleInvoice(invoice Invoice, today time.Time) error {
	if invoice.Status != InvoiceStatusSent {
		return ErrInvoiceImmutable
	}
	if invoice.SettlementTxnRef != nil || invoice.SettledDate != nil || invoice.SettledAmount != nil {
		return ErrInvoiceImmutable
	}
	if invoice.Number == nil || strings.TrimSpace(*invoice.Number) == "" {
		return fmt.Errorf("invoicing: sent invoice %s has no number", invoice.ID)
	}
	if invoice.SentAt == nil || !dateOnly(*invoice.SentAt).Equal(dateOnly(today)) {
		return ErrInvoiceImmutable
	}
	return nil
}

func normalizeSettlementAmount(amount Money) (Money, error) {
	normalized := Money{
		Amount:   amount.Amount,
		Currency: strings.ToUpper(strings.TrimSpace(amount.Currency)),
	}
	if normalized.Amount <= 0 {
		return Money{}, ErrInvoiceSettlementAmountMismatch
	}
	switch Currency(normalized.Currency) {
	case CurrencyEUR, CurrencyGBP:
	default:
		return Money{}, ErrInvoiceSettlementAmountMismatch
	}
	return normalized, nil
}

func validateSettlementAmount(total Money, amount Money) error {
	if amount.Currency != total.Currency {
		return ErrInvoiceSettlementAmountMismatch
	}
	if amount.Amount == total.Amount {
		return nil
	}
	if amount.Amount > 0 && amount.Amount < total.Amount {
		return ErrInvoicePartialPayment
	}
	return ErrInvoiceSettlementAmountMismatch
}

func invoiceLockID(invoice Invoice) (int64, error) {
	if invoice.LockID == nil || strings.TrimSpace(*invoice.LockID) == "" {
		return 0, fmt.Errorf("invoicing: sent invoice %s has no lock", invoice.ID)
	}
	lockID, err := strconv.ParseInt(strings.TrimSpace(*invoice.LockID), 10, 64)
	if err != nil || lockID <= 0 {
		return 0, fmt.Errorf("invoicing: sent invoice %s has invalid lock %q", invoice.ID, *invoice.LockID)
	}
	return lockID, nil
}

func invoiceSendJournalEntry(invoice Invoice, lock RateLock) (ledger.NewJournalEntry, error) {
	total := invoice.Totals.Total
	totalGBP, err := lockedAmountGBP(total, lock)
	if err != nil {
		return ledger.NewJournalEntry{}, err
	}
	postings := []ledger.NewPosting{{
		AccountCode: tradeDebtorsAccount(invoice.Currency),
		Amount:      total,
		AmountGBP:   totalGBP,
	}}
	creditPostings, err := invoiceSalesCreditPostings(invoice, lock, totalGBP)
	if err != nil {
		return ledger.NewJournalEntry{}, err
	}
	postings = append(postings, creditPostings...)
	number := strings.TrimSpace(*invoice.Number)
	return ledger.NewJournalEntry{
		Date:         invoice.IssueDate,
		Description:  "Invoice " + number,
		SourceModule: ModuleName,
		SourceRef:    invoiceSendSourceRef(number),
		Postings:     postings,
	}, nil
}

func invoiceSalesCreditPostings(invoice Invoice, lock RateLock, totalGBP Money) ([]ledger.NewPosting, error) {
	if invoice.VATTreatment == VATTreatmentDomestic && !invoice.Totals.VAT.IsZero() {
		subtotalGBP, err := lockedAmountGBP(invoice.Totals.Subtotal, lock)
		if err != nil {
			return nil, err
		}
		vatGBP, err := totalGBP.Sub(subtotalGBP)
		if err != nil {
			return nil, fmt.Errorf("invoicing: allocate locked VAT GBP: %w", err)
		}
		salesNative, err := invoice.Totals.Subtotal.Negate()
		if err != nil {
			return nil, fmt.Errorf("invoicing: negate invoice subtotal: %w", err)
		}
		salesGBP, err := subtotalGBP.Negate()
		if err != nil {
			return nil, fmt.Errorf("invoicing: negate locked GBP subtotal: %w", err)
		}
		vatNative, err := invoice.Totals.VAT.Negate()
		if err != nil {
			return nil, fmt.Errorf("invoicing: negate invoice VAT: %w", err)
		}
		vatCreditGBP, err := vatGBP.Negate()
		if err != nil {
			return nil, fmt.Errorf("invoicing: negate locked GBP VAT: %w", err)
		}
		return []ledger.NewPosting{
			{
				AccountCode: salesAccount,
				Amount:      salesNative,
				AmountGBP:   salesGBP,
			},
			{
				AccountCode: vatControlAccount,
				Amount:      vatNative,
				AmountGBP:   vatCreditGBP,
			},
		}, nil
	}

	creditNative, err := invoice.Totals.Total.Negate()
	if err != nil {
		return nil, fmt.Errorf("invoicing: negate invoice total: %w", err)
	}
	creditGBP, err := totalGBP.Negate()
	if err != nil {
		return nil, fmt.Errorf("invoicing: negate locked GBP total: %w", err)
	}
	return []ledger.NewPosting{{
		AccountCode: salesAccount,
		Amount:      creditNative,
		AmountGBP:   creditGBP,
	}}, nil
}

const (
	salesAccount      ledger.AccountCode = "4000-sales"
	vatControlAccount ledger.AccountCode = "2200-vat-control"
)

func tradeDebtorsAccount(currency Currency) ledger.AccountCode {
	switch currency {
	case CurrencyEUR:
		return "1100-debtors-eur"
	case CurrencyGBP:
		return "1101-debtors-gbp"
	default:
		return ledger.AccountCode("1100-debtors-" + strings.ToLower(string(currency)))
	}
}

func lockedAmountGBP(native Money, lock RateLock) (Money, error) {
	rat, ok := new(big.Rat).SetString(strings.TrimSpace(lock.Rate))
	if !ok {
		return Money{}, fmt.Errorf("invoicing: parse locked rate %q", lock.Rate)
	}
	converted := native.MulRat(rat)
	converted.Currency = string(CurrencyGBP)
	return converted, nil
}

func invoiceSendSourceRef(number string) string {
	return "invoice:" + strings.TrimSpace(number) + ":send"
}

func invoiceSettlementSourceRef(id string) string {
	return "invoice:" + strings.TrimSpace(id) + ":settlement"
}

func stringPtr(value string) *string {
	return &value
}

func (s *Service) publish(ctx context.Context, tx db.Tx, evt bus.Event) error {
	if s.eventBus == nil {
		return nil
	}
	return s.eventBus.Publish(ctx, tx, evt)
}

func newClientID() (string, error) {
	return newID("client_", "client id")
}

func newInvoiceID() (string, error) {
	return newID("invoice_", "invoice id")
}

func newInvoiceLineID() (string, error) {
	return newID("line_", "invoice line id")
}

func newID(prefix string, label string) (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("invoicing: generate %s: %w", label, err)
	}
	return prefix + hex.EncodeToString(bytes[:]), nil
}

func (s *Service) now() time.Time {
	if s.clock == nil {
		return time.Now()
	}
	return s.clock.Now()
}

func (s *Service) normalizeLineInputs(invoiceID string, currency Currency, inputs []InvoiceLineInput) ([]InvoiceLine, error) {
	lines := make([]InvoiceLine, 0, len(inputs))
	var fields []FieldError
	seenIDs := make(map[string]bool, len(inputs))
	for i, input := range inputs {
		prefix := fmt.Sprintf("/lines/%d", i)
		lineID := strings.TrimSpace(input.ID)
		if lineID == "" {
			var err error
			lineID, err = s.lineIDGenerator()
			if err != nil {
				return nil, err
			}
		}
		if seenIDs[lineID] {
			fields = append(fields, FieldError{Pointer: prefix + "/id", Detail: "must be unique"})
		}
		seenIDs[lineID] = true

		description := strings.TrimSpace(input.Description)
		if description == "" {
			fields = append(fields, FieldError{Pointer: prefix + "/description", Detail: "is required"})
		}
		qty, err := ParseQuantity(string(input.Qty))
		if err != nil {
			fields = append(fields, FieldError{Pointer: prefix + "/qty", Detail: err.Error()})
		}
		unitPrice := Money{
			Amount:   input.UnitPrice.Amount,
			Currency: strings.ToUpper(strings.TrimSpace(input.UnitPrice.Currency)),
		}
		fields = append(fields, validateInvoiceMoney(prefix+"/unit_price", currency, unitPrice)...)

		lines = append(lines, InvoiceLine{
			ID:          lineID,
			InvoiceID:   invoiceID,
			Position:    i + 1,
			Description: description,
			Qty:         qty,
			UnitPrice:   unitPrice,
		})
	}
	if err := invoiceValidationError(fields); err != nil {
		return nil, err
	}
	return lines, nil
}

func validateDraftInvoice(invoice Invoice) error {
	var fields []FieldError
	if strings.TrimSpace(invoice.ClientID) == "" {
		fields = append(fields, FieldError{Pointer: "/client_id", Detail: "is required"})
	}
	switch invoice.Status {
	case InvoiceStatusDraft:
	default:
		fields = append(fields, FieldError{Pointer: "/status", Detail: "must be draft"})
	}
	fields = append(fields, validateInvoiceDates(invoice.IssueDate, invoice.DueDate)...)
	switch invoice.Currency {
	case CurrencyEUR, CurrencyGBP:
	default:
		fields = append(fields, FieldError{Pointer: "/currency", Detail: "must be EUR or GBP"})
	}
	switch invoice.VATTreatment {
	case VATTreatmentDomestic, VATTreatmentReverseChargeEUB2B:
	default:
		fields = append(fields, FieldError{Pointer: "/vat_treatment", Detail: "must be domestic or reverse-charge-eu-b2b"})
	}
	fields = append(fields, validateInvoiceLines(invoice.Currency, invoice.Lines)...)
	return invoiceValidationError(fields)
}

func validateInvoiceDates(issueDate time.Time, dueDate time.Time) []FieldError {
	var fields []FieldError
	if issueDate.IsZero() {
		fields = append(fields, FieldError{Pointer: "/issue_date", Detail: "is required"})
	}
	if dueDate.IsZero() {
		fields = append(fields, FieldError{Pointer: "/due_date", Detail: "is required"})
	}
	if !issueDate.IsZero() && !dueDate.IsZero() && dateOnly(dueDate).Before(dateOnly(issueDate)) {
		fields = append(fields, FieldError{Pointer: "/due_date", Detail: "must be on or after issue_date"})
	}
	return fields
}

func validateInvoiceLines(currency Currency, lines []InvoiceLine) []FieldError {
	var fields []FieldError
	for i, line := range lines {
		prefix := fmt.Sprintf("/lines/%d", i)
		if line.Position != i+1 {
			fields = append(fields, FieldError{Pointer: prefix + "/position", Detail: "must match line order"})
		}
		if strings.TrimSpace(line.Description) == "" {
			fields = append(fields, FieldError{Pointer: prefix + "/description", Detail: "is required"})
		}
		if _, err := line.Qty.rat(); err != nil {
			fields = append(fields, FieldError{Pointer: prefix + "/qty", Detail: err.Error()})
		}
		fields = append(fields, validateInvoiceMoney(prefix+"/unit_price", currency, line.UnitPrice)...)
	}
	return fields
}

func validateInvoiceMoney(pointer string, invoiceCurrency Currency, amount Money) []FieldError {
	var fields []FieldError
	if amount.Amount <= 0 {
		fields = append(fields, FieldError{Pointer: pointer + "/amount", Detail: "must be greater than zero"})
	}
	switch Currency(amount.Currency) {
	case CurrencyEUR, CurrencyGBP:
	default:
		fields = append(fields, FieldError{Pointer: pointer + "/currency", Detail: "must be EUR or GBP"})
	}
	if invoiceCurrency == CurrencyEUR || invoiceCurrency == CurrencyGBP {
		if Currency(amount.Currency) != invoiceCurrency {
			fields = append(fields, FieldError{Pointer: pointer + "/currency", Detail: "must match invoice currency"})
		}
	}
	return fields
}

func (s *Service) withComputedTotals(ctx context.Context, invoice Invoice) (Invoice, error) {
	return s.computeTotals(ctx, invoice, invoice.Status == InvoiceStatusDraft)
}

func (s *Service) withReadComputedTotals(ctx context.Context, invoice Invoice) (Invoice, error) {
	invoice.Status = virtualInvoiceStatus(invoice, dateOnly(s.now()))
	return s.withComputedTotals(ctx, invoice)
}

func virtualInvoiceStatus(invoice Invoice, today time.Time) InvoiceStatus {
	if invoice.Status == InvoiceStatusSent && dateOnly(invoice.DueDate).Before(dateOnly(today)) {
		return InvoiceStatusOverdue
	}
	return invoice.Status
}

func (s *Service) computeTotals(ctx context.Context, invoice Invoice, includeDraftApprox bool) (Invoice, error) {
	subtotal := Money{Currency: string(invoice.Currency)}
	for i := range invoice.Lines {
		rat, err := invoice.Lines[i].Qty.rat()
		if err != nil {
			return Invoice{}, err
		}
		lineTotal := invoice.Lines[i].UnitPrice.MulRat(rat)
		invoice.Lines[i].LineTotal = lineTotal
		var addErr error
		subtotal, addErr = subtotal.Add(lineTotal)
		if addErr != nil {
			return Invoice{}, fmt.Errorf("invoicing: add line total: %w", addErr)
		}
	}

	vatRegistered, err := s.invoiceCompanyVATRegistered(ctx, invoice)
	if err != nil {
		return Invoice{}, err
	}
	invoice.VATRegistered = vatRegistered

	vat := Money{Currency: string(invoice.Currency)}
	if !subtotal.IsZero() && invoice.VATTreatment == VATTreatmentDomestic && vatRegistered {
		vatRate, _, err := jurisdiction.VATStandardRateForDate(invoice.IssueDate)
		if err != nil {
			return Invoice{}, fmt.Errorf("invoicing: VAT rate: %w", err)
		}
		rat, err := rateRat(vatRate.String())
		if err != nil {
			return Invoice{}, err
		}
		vat = subtotal.MulRat(rat)
	}
	total, err := subtotal.Add(vat)
	if err != nil {
		return Invoice{}, fmt.Errorf("invoicing: add VAT total: %w", err)
	}

	totals := InvoiceTotals{
		Subtotal: subtotal,
		VAT:      vat,
		Total:    total,
	}
	if includeDraftApprox && invoice.Status == InvoiceStatusDraft {
		approx, err := s.approxGBP(ctx, total)
		if err != nil {
			return Invoice{}, err
		}
		totals.ApproxGBP = approx
	}
	invoice.Totals = totals
	return invoice, nil
}

func (s *Service) invoiceCompanyVATRegistered(ctx context.Context, invoice Invoice) (bool, error) {
	if invoice.VATRegisteredAtSend != nil {
		return *invoice.VATRegisteredAtSend, nil
	}
	if invoice.Status != InvoiceStatusDraft {
		return sentInvoiceVATRegistered(invoice), nil
	}
	if s.identity == nil {
		return true, nil
	}
	registered, err := s.identity.IsVATRegistered(ctx)
	if err != nil {
		if errors.Is(err, identity.ErrProfileNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("invoicing: company VAT registration: %w", err)
	}
	return registered, nil
}

func invoiceVATRegisteredForPrint(invoice Invoice, currentProfileRegistered bool) bool {
	if invoice.Status != InvoiceStatusDraft {
		return sentInvoiceVATRegistered(invoice)
	}
	return currentProfileRegistered
}

func sentInvoiceVATRegistered(invoice Invoice) bool {
	if invoice.VATRegisteredAtSend != nil {
		return *invoice.VATRegisteredAtSend
	}
	return true
}

func (s *Service) approxGBP(ctx context.Context, total Money) (*InvoiceGBPApprox, error) {
	if s.todayRate == nil {
		return nil, nil
	}
	rate, asOf, err := s.todayRate(ctx, total.Currency, string(CurrencyGBP))
	if err != nil {
		if errors.Is(err, ErrRateUnavailable) {
			return nil, nil
		}
		return nil, fmt.Errorf("invoicing: today GBP rate: %w", err)
	}
	rat, err := rate.Rat()
	if err != nil {
		return nil, err
	}
	converted := total.MulRat(rat)
	return &InvoiceGBPApprox{
		Amount: Money{
			Amount:   converted.Amount,
			Currency: string(CurrencyGBP),
		},
		Rate:   rate,
		AsOf:   asOf.UTC(),
		Locked: false,
	}, nil
}

func rateRat(value string) (*big.Rat, error) {
	rat, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	if !ok {
		return nil, fmt.Errorf("invoicing: parse rate %q", value)
	}
	return rat, nil
}

func defaultTodayRate(ctx context.Context, from string, to string) (FXRate, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return FXRate{}, time.Time{}, err
	}
	normalizedFrom := strings.ToUpper(strings.TrimSpace(from))
	normalizedTo := strings.ToUpper(strings.TrimSpace(to))
	now := time.Now().UTC()
	if normalizedFrom == normalizedTo && normalizedFrom != "" {
		return FXRate{
			From:     normalizedFrom,
			To:       normalizedTo,
			Value:    "1",
			RateDate: dateOnly(now),
			Source:   "identity",
		}, now, nil
	}
	return FXRate{}, time.Time{}, ErrRateUnavailable
}

func normalizeCurrencyValue(currency Currency) Currency {
	return Currency(strings.ToUpper(strings.TrimSpace(string(currency))))
}

func normalizeVATTreatmentValue(treatment VATTreatment) VATTreatment {
	return VATTreatment(strings.TrimSpace(string(treatment)))
}

type storeInvoiceUsageChecker struct {
	pool  *pgxpool.Pool
	store Store
}

func (c storeInvoiceUsageChecker) ClientHasInvoices(ctx context.Context, clientID string) (bool, error) {
	return c.store.ClientHasInvoices(ctx, c.pool, clientID)
}

func problemForError(err error) (httpserver.Problem, bool) {
	var validation ValidationError
	var invoiceValidation InvoiceValidationError
	switch {
	case errors.As(err, &validation):
		return httpserver.Problem{
			Type:   problemTypeClientValidation,
			Title:  "Client validation failed",
			Status: 422,
			Detail: "client validation failed",
			Extensions: map[string]any{
				"errors": validation.Fields,
			},
		}, true
	case errors.As(err, &invoiceValidation):
		return httpserver.Problem{
			Type:   problemTypeInvoiceValidation,
			Title:  "Invoice validation failed",
			Status: 422,
			Detail: "invoice validation failed",
			Extensions: map[string]any{
				"errors": invoiceValidation.Fields,
			},
		}, true
	case errors.Is(err, ErrClientNotFound):
		return httpserver.Problem{
			Type:   problemTypeClientNotFound,
			Title:  "Client not found",
			Status: 404,
			Detail: "client was not found",
		}, true
	case errors.Is(err, ErrClientCurrencyLocked):
		return httpserver.Problem{
			Type:   problemTypeClientCurrencyLocked,
			Title:  "Client currency is locked",
			Status: 409,
			Detail: "default currency cannot be changed after a client has invoices",
		}, true
	case errors.Is(err, ErrInvoiceNotFound):
		return httpserver.Problem{
			Type:   problemTypeInvoiceNotFound,
			Title:  "Invoice not found",
			Status: 404,
			Detail: "invoice was not found",
		}, true
	case errors.Is(err, ErrInvoiceImmutable):
		return httpserver.Problem{
			Type:   problemTypeInvoiceImmutable,
			Title:  "Invoice is immutable",
			Status: 409,
			Detail: "invoice cannot be changed by this command",
			Extensions: map[string]any{
				"errors": []FieldError{{Pointer: "/status", Detail: "must allow this invoice command"}},
			},
		}, true
	case errors.Is(err, ErrInvoicePartialPayment):
		return invoiceWrongAmountProblem("partial invoice payments are not supported"), true
	case errors.Is(err, ErrInvoiceSettlementAmountMismatch):
		return invoiceWrongAmountProblem("settlement amount must match invoice total"), true
	case errors.Is(err, ErrRateUnavailable):
		return httpserver.Problem{
			Type:   problemTypeInvoiceRate,
			Title:  "Invoice rate unavailable",
			Status: 409,
			Detail: "required invoice exchange rate is unavailable",
			Extensions: map[string]any{
				"errors": []FieldError{{Pointer: "/issue_date", Detail: "rate is unavailable for invoice date"}},
			},
		}, true
	case errors.Is(err, ErrInvoicePostingNotFound):
		return httpserver.Problem{
			Type:   problemTypeInvoiceImmutable,
			Title:  "Invoice posting not found",
			Status: 409,
			Detail: "invoice send posting was not found",
			Extensions: map[string]any{
				"errors": []FieldError{{Pointer: "/send_ledger_entry_id", Detail: "is required to revert invoice"}},
			},
		}, true
	case errors.Is(err, ErrInvoiceReminderNotDue):
		return invoiceReminderProblem("Invoice is not overdue", "only sent overdue invoices can receive reminders", "/status"), true
	case errors.Is(err, ErrInvoiceReminderPDFMissing):
		return invoiceReminderProblem("Invoice PDF is required", "stored invoice PDF is required before sending a reminder", "/pdf_asset"), true
	case errors.Is(err, ErrInvoiceReminderRecipientMissing):
		return invoiceReminderProblem("Client email is required", "client email is required before sending a reminder", "/client_id"), true
	case errors.Is(err, ErrInvoiceReminderRateLimited):
		return httpserver.Problem{
			Type:   problemTypeInvoiceReminderLimit,
			Title:  "Reminder already sent today",
			Status: 409,
			Detail: "invoice already has a reminder recorded today",
			Extensions: map[string]any{
				"errors": []FieldError{{Pointer: "/reminders", Detail: "already sent today"}},
			},
		}, true
	default:
		return httpserver.Problem{}, false
	}
}

func invoiceReminderProblem(title string, detail string, pointer string) httpserver.Problem {
	return httpserver.Problem{
		Type:   problemTypeInvoiceReminder,
		Title:  title,
		Status: 409,
		Detail: detail,
		Extensions: map[string]any{
			"errors": []FieldError{{Pointer: pointer, Detail: detail}},
		},
	}
}

func invoiceWrongAmountProblem(detail string) httpserver.Problem {
	return httpserver.Problem{
		Type:   problemTypeInvoiceWrongAmount,
		Title:  "Invoice amount mismatch",
		Status: 422,
		Detail: detail,
		Extensions: map[string]any{
			"errors": []FieldError{{Pointer: "/settled_amount", Detail: detail}},
		},
	}
}
