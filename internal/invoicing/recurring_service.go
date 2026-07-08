package invoicing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

const recurringMaterializationLimit = 100
const recurringCatchupLimitPerTemplate = 24

// RecurringTemplates returns all recurring templates with client names and
// ordered lines for the management screen.
func (s *Service) RecurringTemplates(ctx context.Context) ([]RecurringTemplate, error) {
	templates, err := s.store.ListRecurringTemplates(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	return s.withComputedRecurringTemplates(templates)
}

// CreateRecurringTemplate creates a new active recurring invoice template from
// explicit invoice fields.
func (s *Service) CreateRecurringTemplate(ctx context.Context, input RecurringTemplateInput) (_ RecurringTemplate, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RecurringTemplate{}, fmt.Errorf("invoicing: begin create recurring template transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	template, err := s.recurringTemplateFromInput(ctx, tx, input, nil)
	if err != nil {
		return RecurringTemplate{}, err
	}
	created, err := s.store.InsertRecurringTemplate(ctx, tx, template)
	if err != nil {
		return RecurringTemplate{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return RecurringTemplate{}, fmt.Errorf("invoicing: commit create recurring template transaction: %w", err)
	}
	return s.withComputedRecurringTemplate(created)
}

// CreateRecurringTemplateFromInvoice copies client, currency, VAT treatment,
// and lines from an existing invoice, then applies the requested schedule.
func (s *Service) CreateRecurringTemplateFromInvoice(ctx context.Context, invoiceID string, input CreateRecurringFromInvoiceInput) (_ RecurringTemplate, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RecurringTemplate{}, fmt.Errorf("invoicing: begin create recurring template from invoice transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	source, err := s.store.Invoice(ctx, tx, strings.TrimSpace(invoiceID))
	if err != nil {
		return RecurringTemplate{}, err
	}
	lines := make([]InvoiceLineInput, 0, len(source.Lines))
	for _, line := range source.Lines {
		lines = append(lines, InvoiceLineInput{
			ID:          line.ID,
			Description: line.Description,
			Qty:         line.Qty,
			UnitPrice:   line.UnitPrice,
		})
	}
	createdFromID := source.ID
	template, err := s.recurringTemplateFromInput(ctx, tx, RecurringTemplateInput{
		ClientID:       source.ClientID,
		Cadence:        input.Cadence,
		DayOfMonth:     input.DayOfMonth,
		NextRunDate:    input.NextRunDate,
		Currency:       source.Currency,
		VATTreatment:   source.VATTreatment,
		AutoSend:       input.AutoSend,
		MaxOccurrences: input.MaxOccurrences,
		Lines:          lines,
	}, &createdFromID)
	if err != nil {
		return RecurringTemplate{}, err
	}
	created, err := s.store.InsertRecurringTemplate(ctx, tx, template)
	if err != nil {
		return RecurringTemplate{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return RecurringTemplate{}, fmt.Errorf("invoicing: commit create recurring template from invoice transaction: %w", err)
	}
	return s.withComputedRecurringTemplate(created)
}

// CancelRecurringTemplate stops future materialization while preserving history.
func (s *Service) CancelRecurringTemplate(ctx context.Context, id string) (_ RecurringTemplate, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RecurringTemplate{}, fmt.Errorf("invoicing: begin cancel recurring template transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	updated, err := s.store.CancelRecurringTemplate(ctx, tx, strings.TrimSpace(id), s.now())
	if err != nil {
		return RecurringTemplate{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return RecurringTemplate{}, fmt.Errorf("invoicing: commit cancel recurring template transaction: %w", err)
	}
	return s.withComputedRecurringTemplate(updated)
}

// RunRecurringInvoices creates due invoice drafts and sends only those from
// templates that explicitly opted into auto-send.
func (s *Service) RunRecurringInvoices(ctx context.Context) error {
	if s.pool == nil {
		return fmt.Errorf("invoicing: recurring invoices require pool")
	}
	if _, err := s.materializeDueRecurringTemplates(ctx); err != nil {
		return err
	}
	return s.sendPendingRecurringAutoSendDrafts(ctx)
}

// RecurringDraftInvoices returns advisor facts for generated drafts waiting to
// be sent.
func (s *Service) RecurringDraftInvoices(ctx context.Context) ([]RecurringDraftInvoiceFact, error) {
	facts, err := s.store.RecurringDraftInvoiceFacts(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	return facts, nil
}

func (s *Service) materializeDueRecurringTemplates(ctx context.Context) (_ []Invoice, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoicing: begin recurring materialization transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	today := dateOnly(s.now())
	templates, err := s.store.DueRecurringTemplatesForUpdate(ctx, tx, today, recurringMaterializationLimit)
	if err != nil {
		return nil, err
	}
	created := []Invoice{}
	for _, template := range templates {
		templateCreated := 0
		nextRun := dateOnly(template.NextRunDate)
		for !nextRun.After(today) && templateCreated < recurringCatchupLimitPerTemplate {
			if template.MaxOccurrences != nil && template.OccurrencesCreated+templateCreated >= *template.MaxOccurrences {
				break
			}
			invoice, createErr := s.createRecurringDraftInvoice(ctx, tx, template, nextRun)
			if createErr != nil {
				return nil, createErr
			}
			created = append(created, invoice)
			templateCreated++
			nextRun = nextRecurringRunDate(nextRun, template.Cadence, template.DayOfMonth)
		}
		if templateCreated > 0 {
			if _, err := s.store.AdvanceRecurringTemplate(ctx, tx, template.ID, nextRun, templateCreated, s.now()); err != nil {
				return nil, err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("invoicing: commit recurring materialization transaction: %w", err)
	}
	return created, nil
}

func (s *Service) sendPendingRecurringAutoSendDrafts(ctx context.Context) error {
	ids, err := s.store.PendingRecurringAutoSendDrafts(ctx, s.pool)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := s.Send(ctx, id); err != nil {
			return fmt.Errorf("invoicing: auto-send recurring invoice %s: %w", id, err)
		}
	}
	return nil
}

func (s *Service) createRecurringDraftInvoice(ctx context.Context, tx db.Tx, template RecurringTemplate, runDate time.Time) (Invoice, error) {
	client, err := s.store.Client(ctx, tx, template.ClientID)
	if err != nil {
		return Invoice{}, err
	}
	invoiceID, err := s.invoiceIDGenerator()
	if err != nil {
		return Invoice{}, err
	}
	runDate = dateOnly(runDate)
	templateID := template.ID
	draft := Invoice{
		ID:                  invoiceID,
		ClientID:            template.ClientID,
		Status:              InvoiceStatusDraft,
		IssueDate:           runDate,
		DueDate:             runDate.AddDate(0, 0, client.TermsDays),
		Currency:            template.Currency,
		VATTreatment:        template.VATTreatment,
		RecurringTemplateID: &templateID,
		RecurringRunDate:    &runDate,
	}
	inputs := make([]InvoiceLineInput, 0, len(template.Lines))
	for _, line := range template.Lines {
		inputs = append(inputs, InvoiceLineInput{
			ID:          line.ID,
			Description: line.Description,
			Qty:         line.Qty,
			UnitPrice:   line.UnitPrice,
		})
	}
	draft.Lines, err = s.normalizeLineInputs(draft.ID, draft.Currency, inputs)
	if err != nil {
		return Invoice{}, err
	}
	if err := validateDraftInvoice(draft); err != nil {
		return Invoice{}, err
	}
	created, err := s.store.InsertDraftInvoice(ctx, tx, draft)
	if err != nil {
		return Invoice{}, err
	}
	if err := s.store.ReplaceInvoiceLines(ctx, tx, created.ID, draft.Lines); err != nil {
		return Invoice{}, err
	}
	created.Lines = draft.Lines
	return s.withComputedTotals(ctx, created)
}

func (s *Service) recurringTemplateFromInput(ctx context.Context, tx db.Tx, input RecurringTemplateInput, createdFromInvoiceID *string) (RecurringTemplate, error) {
	templateID, err := s.templateIDGenerator()
	if err != nil {
		return RecurringTemplate{}, err
	}
	client, err := s.store.Client(ctx, tx, strings.TrimSpace(input.ClientID))
	if err != nil {
		return RecurringTemplate{}, err
	}
	if client.ArchivedAt != nil {
		return RecurringTemplate{}, invoiceValidationError([]FieldError{{Pointer: "/client_id", Detail: "must be an active client"}})
	}
	template := RecurringTemplate{
		ID:                   templateID,
		ClientID:             client.ID,
		ClientName:           client.Name,
		Status:               RecurringTemplateStatusActive,
		Cadence:              normalizeRecurringCadence(input.Cadence),
		DayOfMonth:           input.DayOfMonth,
		NextRunDate:          dateOnly(input.NextRunDate),
		Currency:             normalizeCurrencyValue(input.Currency),
		VATTreatment:         normalizeVATTreatmentValue(input.VATTreatment),
		AutoSend:             input.AutoSend,
		MaxOccurrences:       cloneInt(input.MaxOccurrences),
		CreatedFromInvoiceID: nullableTrimmedString(createdFromInvoiceID),
	}
	if err := validateRecurringTemplateCommand(template, input.Lines); err != nil {
		return RecurringTemplate{}, err
	}
	lines, err := s.normalizeRecurringTemplateLineInputs(template.ID, template.Currency, input.Lines)
	if err != nil {
		return RecurringTemplate{}, err
	}
	template.Lines = lines
	return template, nil
}

func validateRecurringTemplateCommand(template RecurringTemplate, lines []InvoiceLineInput) error {
	var fields []FieldError
	if strings.TrimSpace(template.ClientID) == "" {
		fields = append(fields, FieldError{Pointer: "/client_id", Detail: "is required"})
	}
	switch template.Cadence {
	case RecurringCadenceMonthly, RecurringCadenceQuarterly:
	default:
		fields = append(fields, FieldError{Pointer: "/cadence", Detail: "must be monthly or quarterly"})
	}
	if template.DayOfMonth < 1 || template.DayOfMonth > 31 {
		fields = append(fields, FieldError{Pointer: "/day_of_month", Detail: "must be between 1 and 31"})
	}
	if template.NextRunDate.IsZero() {
		fields = append(fields, FieldError{Pointer: "/next_run_date", Detail: "is required"})
	}
	if template.Currency != CurrencyEUR && template.Currency != CurrencyGBP {
		fields = append(fields, FieldError{Pointer: "/currency", Detail: "must be EUR or GBP"})
	}
	if template.VATTreatment != VATTreatmentDomestic && template.VATTreatment != VATTreatmentReverseChargeEUB2B {
		fields = append(fields, FieldError{Pointer: "/vat_treatment", Detail: "must be domestic or reverse-charge-eu-b2b"})
	}
	if template.MaxOccurrences != nil && *template.MaxOccurrences <= 0 {
		fields = append(fields, FieldError{Pointer: "/max_occurrences", Detail: "must be greater than zero"})
	}
	if len(lines) == 0 {
		fields = append(fields, FieldError{Pointer: "/lines", Detail: "must contain at least one line"})
	}
	return invoiceValidationError(fields)
}

func (s *Service) normalizeRecurringTemplateLineInputs(templateID string, currency Currency, inputs []InvoiceLineInput) ([]RecurringTemplateLine, error) {
	invoiceLines, err := s.normalizeLineInputs(templateID, currency, inputs)
	if err != nil {
		return nil, err
	}
	lines := make([]RecurringTemplateLine, 0, len(invoiceLines))
	for _, line := range invoiceLines {
		lines = append(lines, RecurringTemplateLine{
			ID:          line.ID,
			TemplateID:  templateID,
			Position:    line.Position,
			Description: line.Description,
			Qty:         line.Qty,
			UnitPrice:   line.UnitPrice,
			LineTotal:   line.LineTotal,
		})
	}
	return lines, nil
}

func (s *Service) withComputedRecurringTemplates(templates []RecurringTemplate) ([]RecurringTemplate, error) {
	for i := range templates {
		computed, err := s.withComputedRecurringTemplate(templates[i])
		if err != nil {
			return nil, err
		}
		templates[i] = computed
	}
	return templates, nil
}

func (s *Service) withComputedRecurringTemplate(template RecurringTemplate) (RecurringTemplate, error) {
	for i := range template.Lines {
		rat, err := template.Lines[i].Qty.rat()
		if err != nil {
			return RecurringTemplate{}, err
		}
		template.Lines[i].LineTotal = template.Lines[i].UnitPrice.MulRat(rat)
	}
	if template.Lines == nil {
		template.Lines = []RecurringTemplateLine{}
	}
	return template, nil
}

func nextRecurringRunDate(current time.Time, cadence RecurringCadence, dayOfMonth int) time.Time {
	months := 1
	if cadence == RecurringCadenceQuarterly {
		months = 3
	}
	current = dateOnly(current)
	year, month, _ := current.Date()
	target := time.Date(year, month+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	lastDay := lastDayOfMonth(target.Year(), target.Month())
	day := dayOfMonth
	if day > lastDay {
		day = lastDay
	}
	return time.Date(target.Year(), target.Month(), day, 0, 0, 0, 0, time.UTC)
}

func lastDayOfMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func nullableTrimmedString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
