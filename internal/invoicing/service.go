package invoicing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const (
	problemTypeClientNotFound       = "https://ledgerly.local/problems/invoicing/client-not-found"
	problemTypeClientValidation     = "https://ledgerly.local/problems/invoicing/client-validation"
	problemTypeClientCurrencyLocked = "https://ledgerly.local/problems/invoicing/client-currency-locked"
)

// Service orchestrates invoicing client commands and queries.
type Service struct {
	pool               *pgxpool.Pool
	store              Store
	clock              clock.Clock
	todayRate          TodayRateFunc
	invoiceUsage       InvoiceUsageChecker
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

func NewService(pool *pgxpool.Pool, store Store, opts ...ServiceOption) *Service {
	service := &Service{
		pool:               pool,
		store:              store,
		clock:              clock.New(),
		todayRate:          defaultTodayRate,
		invoiceUsage:       noInvoiceUsageChecker{},
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
	return s.withComputedTotals(ctx, invoice)
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
	for i, input := range inputs {
		prefix := fmt.Sprintf("/lines/%d", i)
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

		lineID, err := s.lineIDGenerator()
		if err != nil {
			return nil, err
		}
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

	vat := Money{Currency: string(invoice.Currency)}
	if !subtotal.IsZero() {
		vatRate, _, err := jurisdiction.VATStandardRateForDate(invoice.IssueDate)
		if err != nil {
			return Invoice{}, fmt.Errorf("invoicing: VAT rate: %w", err)
		}
		if invoice.VATTreatment == VATTreatmentDomestic {
			rat, err := rateRat(vatRate.String())
			if err != nil {
				return Invoice{}, err
			}
			vat = subtotal.MulRat(rat)
		}
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
	if invoice.Status == InvoiceStatusDraft {
		approx, err := s.approxGBP(ctx, total)
		if err != nil {
			return Invoice{}, err
		}
		totals.ApproxGBP = approx
	}
	invoice.Totals = totals
	return invoice, nil
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
	default:
		return httpserver.Problem{}, false
	}
}
