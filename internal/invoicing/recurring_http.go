package invoicing

import (
	nethttp "net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type recurringTemplateHandler struct {
	service *Service
}

type recurringTemplatesResponse struct {
	Templates []RecurringTemplate `json:"templates"`
}

type recurringTemplateRequest struct {
	ClientID       string             `json:"client_id"`
	Cadence        RecurringCadence   `json:"cadence"`
	DayOfMonth     int                `json:"day_of_month"`
	NextRunDate    string             `json:"next_run_date"`
	Currency       Currency           `json:"currency"`
	VATTreatment   VATTreatment       `json:"vat_treatment"`
	AutoSend       bool               `json:"auto_send"`
	MaxOccurrences *int               `json:"max_occurrences"`
	Lines          []InvoiceLineInput `json:"lines"`
}

type createRecurringFromInvoiceRequest struct {
	Cadence        RecurringCadence `json:"cadence"`
	DayOfMonth     int              `json:"day_of_month"`
	NextRunDate    string           `json:"next_run_date"`
	AutoSend       bool             `json:"auto_send"`
	MaxOccurrences *int             `json:"max_occurrences"`
}

func (h recurringTemplateHandler) listRecurringTemplates(w nethttp.ResponseWriter, r *nethttp.Request) {
	templates, err := h.service.RecurringTemplates(r.Context())
	if err != nil {
		writeInvoiceError(w, r, err)
		return
	}
	if templates == nil {
		templates = []RecurringTemplate{}
	}
	writeInvoiceJSON(w, nethttp.StatusOK, recurringTemplatesResponse{Templates: templates})
}

func (h recurringTemplateHandler) createRecurringTemplate(w nethttp.ResponseWriter, r *nethttp.Request) {
	var request recurringTemplateRequest
	if err := decodeClientJSON(w, r, &request); err != nil {
		writeInvoiceDecodeError(w, r, err)
		return
	}
	input, fieldErrors := request.input()
	if len(fieldErrors) > 0 {
		writeInvoiceValidation(w, r, fieldErrors)
		return
	}
	template, err := h.service.CreateRecurringTemplate(r.Context(), input)
	if err != nil {
		writeInvoiceError(w, r, err)
		return
	}
	writeInvoiceJSON(w, nethttp.StatusCreated, recurringTemplateForResponse(template))
}

func (h recurringTemplateHandler) createRecurringTemplateFromInvoice(w nethttp.ResponseWriter, r *nethttp.Request) {
	var request createRecurringFromInvoiceRequest
	if err := decodeClientJSON(w, r, &request); err != nil {
		writeInvoiceDecodeError(w, r, err)
		return
	}
	input, fieldErrors := request.input()
	if len(fieldErrors) > 0 {
		writeInvoiceValidation(w, r, fieldErrors)
		return
	}
	template, err := h.service.CreateRecurringTemplateFromInvoice(r.Context(), invoiceIDParam(r), input)
	if err != nil {
		writeInvoiceError(w, r, err)
		return
	}
	writeInvoiceJSON(w, nethttp.StatusCreated, recurringTemplateForResponse(template))
}

func (h recurringTemplateHandler) cancelRecurringTemplate(w nethttp.ResponseWriter, r *nethttp.Request) {
	template, err := h.service.CancelRecurringTemplate(r.Context(), recurringTemplateIDParam(r))
	if err != nil {
		writeInvoiceError(w, r, err)
		return
	}
	writeInvoiceJSON(w, nethttp.StatusOK, recurringTemplateForResponse(template))
}

func recurringTemplateIDParam(r *nethttp.Request) string {
	return strings.TrimSpace(chi.URLParam(r, "id"))
}

func (r recurringTemplateRequest) input() (RecurringTemplateInput, []FieldError) {
	nextRunDate, fieldErrors := parseRecurringDate(r.NextRunDate, "/next_run_date")
	return RecurringTemplateInput{
		ClientID:       r.ClientID,
		Cadence:        r.Cadence,
		DayOfMonth:     r.DayOfMonth,
		NextRunDate:    nextRunDate,
		Currency:       r.Currency,
		VATTreatment:   r.VATTreatment,
		AutoSend:       r.AutoSend,
		MaxOccurrences: r.MaxOccurrences,
		Lines:          r.Lines,
	}, fieldErrors
}

func (r createRecurringFromInvoiceRequest) input() (CreateRecurringFromInvoiceInput, []FieldError) {
	nextRunDate, fieldErrors := parseRecurringDate(r.NextRunDate, "/next_run_date")
	return CreateRecurringFromInvoiceInput{
		Cadence:        r.Cadence,
		DayOfMonth:     r.DayOfMonth,
		NextRunDate:    nextRunDate,
		AutoSend:       r.AutoSend,
		MaxOccurrences: r.MaxOccurrences,
	}, fieldErrors
}

func parseRecurringDate(value string, pointer string) (time.Time, []FieldError) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, []FieldError{{Pointer: pointer, Detail: "is required"}}
	}
	parsed, err := time.Parse(time.DateOnly, trimmed)
	if err != nil {
		return time.Time{}, []FieldError{{Pointer: pointer, Detail: "must be a date in YYYY-MM-DD format"}}
	}
	return parsed, nil
}

func recurringTemplateForResponse(template RecurringTemplate) RecurringTemplate {
	if template.Lines == nil {
		template.Lines = []RecurringTemplateLine{}
	}
	return template
}
