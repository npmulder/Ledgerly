package invoicing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// RecurringInvoicesJobName is the deterministic app job and cron
	// registration name for materializing due recurring templates.
	RecurringInvoicesJobName = "invoicing.recurring-invoices"

	// RecurringInvoicesSchedule runs before the overdue sweep so newly
	// generated drafts are visible to the daily advisor evaluation.
	RecurringInvoicesSchedule = "15 2 * * *"

	RecurringTemplateStatusActive   RecurringTemplateStatus = "active"
	RecurringTemplateStatusCanceled RecurringTemplateStatus = "canceled"

	RecurringCadenceMonthly   RecurringCadence = "monthly"
	RecurringCadenceQuarterly RecurringCadence = "quarterly"
)

var (
	ErrRecurringTemplateNotFound  = errors.New("invoicing: recurring template not found")
	ErrRecurringTemplateImmutable = errors.New("invoicing: recurring template is immutable")
)

// RecurringTemplateStatus is the persisted lifecycle for a recurring invoice
// template. Canceled templates are retained for audit and generated-invoice
// history.
type RecurringTemplateStatus string

// RecurringCadence is the supported v1 recurrence interval.
type RecurringCadence string

// RecurringTemplate stores the invoice shape and schedule used to materialize
// future drafts or sent invoices.
type RecurringTemplate struct {
	ID                   string                  `json:"id"`
	ClientID             string                  `json:"client_id"`
	ClientName           string                  `json:"client_name"`
	Status               RecurringTemplateStatus `json:"status"`
	Cadence              RecurringCadence        `json:"cadence"`
	DayOfMonth           int                     `json:"day_of_month"`
	NextRunDate          time.Time               `json:"next_run_date"`
	Currency             Currency                `json:"currency"`
	VATTreatment         VATTreatment            `json:"vat_treatment"`
	AutoSend             bool                    `json:"auto_send"`
	MaxOccurrences       *int                    `json:"max_occurrences"`
	OccurrencesCreated   int                     `json:"occurrences_created"`
	CreatedFromInvoiceID *string                 `json:"created_from_invoice_id"`
	CanceledAt           *time.Time              `json:"canceled_at"`
	Lines                []RecurringTemplateLine `json:"lines"`
	CreatedAt            time.Time               `json:"created_at"`
	UpdatedAt            time.Time               `json:"updated_at"`
}

// RecurringTemplateLine is the persisted, ordered line shape copied into each
// generated invoice.
type RecurringTemplateLine struct {
	ID          string   `json:"id"`
	TemplateID  string   `json:"template_id"`
	Position    int      `json:"position"`
	Description string   `json:"description"`
	Qty         Quantity `json:"qty"`
	UnitPrice   Money    `json:"unit_price"`
	LineTotal   Money    `json:"line_total"`
}

// RecurringTemplateInput is the API/service command for creating a template.
type RecurringTemplateInput struct {
	ClientID       string
	Cadence        RecurringCadence
	DayOfMonth     int
	NextRunDate    time.Time
	Currency       Currency
	VATTreatment   VATTreatment
	AutoSend       bool
	MaxOccurrences *int
	Lines          []InvoiceLineInput
}

// CreateRecurringFromInvoiceInput is the narrower command used by the "make
// recurring" action. Client, currency, VAT treatment, and lines are copied from
// the source invoice.
type CreateRecurringFromInvoiceInput struct {
	Cadence        RecurringCadence
	DayOfMonth     int
	NextRunDate    time.Time
	AutoSend       bool
	MaxOccurrences *int
}

// RecurringDraftInvoiceFact is the advisor-facing fact payload for generated
// recurring drafts that are waiting for a human to send.
type RecurringDraftInvoiceFact struct {
	InvoiceID  string    `json:"invoice_id"`
	ClientID   string    `json:"client_id"`
	ClientName string    `json:"client_name"`
	RunDate    time.Time `json:"run_date"`
	Amount     Money     `json:"amount"`
}

// RunRecurringInvoices materializes due templates and attempts auto-send for
// templates that explicitly opted into it.
func (m *Module) RunRecurringInvoices(ctx context.Context) error {
	if m == nil || m.service == nil {
		return fmt.Errorf("invoicing: recurring invoices require module service")
	}
	return m.service.RunRecurringInvoices(ctx)
}

func normalizeRecurringStatus(value RecurringTemplateStatus) RecurringTemplateStatus {
	switch RecurringTemplateStatus(strings.TrimSpace(string(value))) {
	case RecurringTemplateStatusCanceled:
		return RecurringTemplateStatusCanceled
	default:
		return RecurringTemplateStatusActive
	}
}

func normalizeRecurringCadence(value RecurringCadence) RecurringCadence {
	return RecurringCadence(strings.ToLower(strings.TrimSpace(string(value))))
}
