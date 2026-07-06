package invoicing

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

type invoiceHandler struct {
	service *Service
}

type createDraftInvoiceRequest struct {
	ClientID string `json:"client_id"`
}

type invoicesResponse struct {
	Invoices   []InvoiceListItem    `json:"invoices"`
	Counts     []InvoiceStatusCount `json:"counts"`
	TotalCount int                  `json:"total_count"`
	Totals     InvoiceTotalsSummary `json:"totals"`
	Limit      int                  `json:"limit"`
	Offset     int                  `json:"offset"`
}

type sendInvoiceResponse struct {
	Invoice    Invoice            `json:"invoice"`
	Number     string             `json:"number"`
	LockedRate lockedRateResponse `json:"locked_rate"`
}

type lockedRateResponse struct {
	ID   int64  `json:"id"`
	Rate string `json:"rate"`
}

type invoicePatch struct {
	issueDate    *time.Time
	dueDate      *time.Time
	currency     *Currency
	vatTreatment *VATTreatment
	lines        *[]InvoiceLineInput
}

func (h invoiceHandler) listInvoices(w nethttp.ResponseWriter, r *nethttp.Request) {
	filter, err := parseInvoiceListFilter(r)
	if err != nil {
		writeInvoiceBadRequest(w, r, err)
		return
	}

	result, err := h.service.List(r.Context(), filter)
	if err != nil {
		writeInvoiceError(w, r, err)
		return
	}
	totals, err := h.service.Totals(r.Context(), filter)
	if err != nil {
		writeInvoiceError(w, r, err)
		return
	}

	writeInvoiceJSON(w, nethttp.StatusOK, invoicesResponse{
		Invoices:   invoiceListItemsForResponse(result.Invoices),
		Counts:     invoiceStatusCountsForResponse(result.Counts),
		TotalCount: result.TotalCount,
		Totals:     invoiceTotalsSummaryForResponse(totals),
		Limit:      invoiceListLimitForResponse(filter),
		Offset:     filter.Offset,
	})
}

func (h invoiceHandler) createDraftInvoice(w nethttp.ResponseWriter, r *nethttp.Request) {
	var request createDraftInvoiceRequest
	if err := decodeClientJSON(w, r, &request); err != nil {
		writeInvoiceDecodeError(w, r, err)
		return
	}
	if strings.TrimSpace(request.ClientID) == "" {
		writeInvoiceValidation(w, r, []FieldError{{Pointer: "/client_id", Detail: "is required"}})
		return
	}

	invoice, err := h.service.CreateDraft(r.Context(), request.ClientID)
	if err != nil {
		writeInvoiceError(w, r, err)
		return
	}
	writeInvoiceJSON(w, nethttp.StatusCreated, invoiceForResponse(invoice))
}

func (h invoiceHandler) getInvoice(w nethttp.ResponseWriter, r *nethttp.Request) {
	invoice, err := h.service.Invoice(r.Context(), invoiceIDParam(r))
	if err != nil {
		writeInvoiceError(w, r, err)
		return
	}
	writeInvoiceJSON(w, nethttp.StatusOK, invoiceForResponse(invoice))
}

func (h invoiceHandler) patchInvoice(w nethttp.ResponseWriter, r *nethttp.Request) {
	patch, fieldErrors, err := decodeInvoicePatch(w, r)
	if err != nil {
		writeInvoiceDecodeError(w, r, err)
		return
	}
	if len(fieldErrors) > 0 {
		writeInvoiceValidation(w, r, fieldErrors)
		return
	}

	invoice, err := h.service.UpdateDraft(r.Context(), invoiceIDParam(r), patch.draftPatch())
	if err != nil {
		writeInvoiceError(w, r, err)
		return
	}
	writeInvoiceJSON(w, nethttp.StatusOK, invoiceForResponse(invoice))
}

func (h invoiceHandler) sendInvoice(w nethttp.ResponseWriter, r *nethttp.Request) {
	invoice, err := h.service.Send(r.Context(), invoiceIDParam(r))
	if err != nil {
		writeInvoiceError(w, r, err)
		return
	}
	if invoice.Number == nil || strings.TrimSpace(*invoice.Number) == "" {
		writeInvoiceError(w, r, fmt.Errorf("invoicing: sent invoice %s has no number", invoice.ID))
		return
	}
	lockedRate, err := lockedRateForSentInvoice(invoice)
	if err != nil {
		writeInvoiceError(w, r, err)
		return
	}
	writeInvoiceJSON(w, nethttp.StatusOK, sendInvoiceResponse{
		Invoice:    invoiceForResponse(invoice),
		Number:     *invoice.Number,
		LockedRate: lockedRate,
	})
}

func (h invoiceHandler) revertInvoice(w nethttp.ResponseWriter, r *nethttp.Request) {
	invoice, err := h.service.RevertToDraft(r.Context(), invoiceIDParam(r))
	if err != nil {
		writeInvoiceError(w, r, err)
		return
	}
	writeInvoiceJSON(w, nethttp.StatusOK, invoiceForResponse(invoice))
}

func (h invoiceHandler) getInvoicePrintPayload(w nethttp.ResponseWriter, r *nethttp.Request) {
	draftWatermark, err := parseInvoiceDraftWatermark(r)
	if err != nil {
		writeInvoiceBadRequest(w, r, err)
		return
	}
	payload, err := h.service.InvoicePrintPayload(r.Context(), invoiceIDParam(r), draftWatermark)
	if err != nil {
		writeInvoiceError(w, r, err)
		return
	}
	writeInvoiceJSON(w, nethttp.StatusOK, payload)
}

func (h invoiceHandler) previewInvoicePDF(w nethttp.ResponseWriter, r *nethttp.Request) {
	pdfBytes, err := h.service.PreviewDraftInvoicePDF(r.Context(), invoiceIDParam(r))
	if err != nil {
		writeInvoiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", invoicePDFMIME)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(pdfBytes)))
	w.WriteHeader(nethttp.StatusOK)
	_, _ = w.Write(pdfBytes)
}

func (h invoiceHandler) renderInvoicePDF(w nethttp.ResponseWriter, r *nethttp.Request) {
	invoice, err := h.service.RenderInvoicePDFNow(r.Context(), invoiceIDParam(r))
	if err != nil {
		writeInvoiceError(w, r, err)
		return
	}
	writeInvoiceJSON(w, nethttp.StatusOK, invoiceForResponse(invoice))
}

func (h invoiceHandler) getInvoicePDF(w nethttp.ResponseWriter, r *nethttp.Request) {
	invoice, err := h.service.Invoice(r.Context(), invoiceIDParam(r))
	if err != nil {
		writeInvoiceError(w, r, err)
		return
	}
	if invoice.PDFAsset == nil || strings.TrimSpace(*invoice.PDFAsset) == "" {
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeInvoiceNotFound,
			Title:  "Invoice PDF not found",
			Status: nethttp.StatusNotFound,
			Detail: "invoice PDF asset was not found",
			Extensions: map[string]any{
				"errors": []FieldError{{Pointer: "/pdf_asset", Detail: "is not available yet"}},
			},
		})
		return
	}
	nethttp.Redirect(w, r, strings.TrimSpace(*invoice.PDFAsset), nethttp.StatusFound)
}

func parseInvoiceDraftWatermark(r *nethttp.Request) (bool, error) {
	value := strings.TrimSpace(r.URL.Query().Get("draft"))
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("draft must be true or false")
	}
	return parsed, nil
}

func lockedRateForSentInvoice(invoice Invoice) (lockedRateResponse, error) {
	if invoice.sendRateLock == nil {
		return lockedRateResponse{}, fmt.Errorf("invoicing: sent invoice %s has no rate lock", invoice.ID)
	}
	return lockedRateResponse(*invoice.sendRateLock), nil
}

func invoiceIDParam(r *nethttp.Request) string {
	return strings.TrimSpace(chi.URLParam(r, "id"))
}

func parseInvoiceListFilter(r *nethttp.Request) (InvoiceListFilter, error) {
	query := r.URL.Query()
	filter := InvoiceListFilter{
		Search: strings.TrimSpace(query.Get("search")),
	}

	for _, value := range query["status"] {
		filter.Statuses = append(filter.Statuses, parseInvoiceStatusQueryValues(value)...)
	}
	for _, value := range query["statuses"] {
		filter.Statuses = append(filter.Statuses, parseInvoiceStatusQueryValues(value)...)
	}

	var err error
	if value := strings.TrimSpace(query.Get("limit")); value != "" {
		filter.Limit, err = strconv.Atoi(value)
		if err != nil {
			return InvoiceListFilter{}, fmt.Errorf("limit must be an integer")
		}
	}
	if value := strings.TrimSpace(query.Get("offset")); value != "" {
		filter.Offset, err = strconv.Atoi(value)
		if err != nil {
			return InvoiceListFilter{}, fmt.Errorf("offset must be an integer")
		}
	}
	if _, err := normalizeInvoiceListFilter(filter, true); err != nil {
		return InvoiceListFilter{}, err
	}
	return filter, nil
}

func parseInvoiceStatusQueryValues(value string) []InvoiceStatus {
	parts := strings.Split(value, ",")
	statuses := make([]InvoiceStatus, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			statuses = append(statuses, InvoiceStatus(trimmed))
		}
	}
	return statuses
}

func decodeInvoicePatch(w nethttp.ResponseWriter, r *nethttp.Request) (invoicePatch, []FieldError, error) {
	var raw map[string]json.RawMessage
	if err := decodeClientJSON(w, r, &raw); err != nil {
		return invoicePatch{}, nil, err
	}

	var (
		patch       invoicePatch
		fieldErrors []FieldError
	)
	for field, value := range raw {
		switch field {
		case "issue_date":
			assignDatePatch(value, "/issue_date", &patch.issueDate, &fieldErrors)
		case "due_date":
			assignDatePatch(value, "/due_date", &patch.dueDate, &fieldErrors)
		case "currency":
			var currency Currency
			if err := decodeClientStrict(value, &currency); err != nil {
				fieldErrors = append(fieldErrors, FieldError{Pointer: "/currency", Detail: "must be a string"})
				continue
			}
			patch.currency = &currency
		case "vat_treatment":
			var treatment VATTreatment
			if err := decodeClientStrict(value, &treatment); err != nil {
				fieldErrors = append(fieldErrors, FieldError{Pointer: "/vat_treatment", Detail: "must be a string"})
				continue
			}
			patch.vatTreatment = &treatment
		case "lines":
			if rejectClientJSONNull(value, "/lines", "must be an array of invoice lines", &fieldErrors) {
				continue
			}
			var lines []InvoiceLineInput
			if err := decodeClientStrict(value, &lines); err != nil {
				fieldErrors = append(fieldErrors, FieldError{Pointer: "/lines", Detail: "must be an array of invoice lines"})
				continue
			}
			patch.lines = &lines
		default:
			return invoicePatch{}, nil, fmt.Errorf("unknown field %q", field)
		}
	}
	return patch, fieldErrors, nil
}

func assignDatePatch(value json.RawMessage, pointer string, dst **time.Time, fieldErrors *[]FieldError) {
	if rejectClientJSONNull(value, pointer, "must be a date in YYYY-MM-DD format", fieldErrors) {
		return
	}
	var raw string
	if err := decodeClientStrict(value, &raw); err != nil {
		*fieldErrors = append(*fieldErrors, FieldError{Pointer: pointer, Detail: "must be a date in YYYY-MM-DD format"})
		return
	}
	parsed, err := time.Parse(time.DateOnly, strings.TrimSpace(raw))
	if err != nil {
		*fieldErrors = append(*fieldErrors, FieldError{Pointer: pointer, Detail: "must be a date in YYYY-MM-DD format"})
		return
	}
	*dst = &parsed
}

func (p invoicePatch) draftPatch() DraftPatch {
	return DraftPatch{
		IssueDate:    p.issueDate,
		DueDate:      p.dueDate,
		Currency:     p.currency,
		VATTreatment: p.vatTreatment,
		Lines:        p.lines,
	}
}

func invoiceForResponse(invoice Invoice) Invoice {
	if invoice.Lines == nil {
		invoice.Lines = []InvoiceLine{}
	}
	return invoice
}

func invoiceListItemsForResponse(items []InvoiceListItem) []InvoiceListItem {
	if items == nil {
		return []InvoiceListItem{}
	}
	return items
}

func invoiceStatusCountsForResponse(counts []InvoiceStatusCount) []InvoiceStatusCount {
	if counts == nil {
		return []InvoiceStatusCount{}
	}
	return counts
}

func invoiceTotalsSummaryForResponse(totals InvoiceTotalsSummary) InvoiceTotalsSummary {
	if totals.Subtotals == nil {
		totals.Subtotals = []Money{}
	}
	return totals
}

func invoiceListLimitForResponse(filter InvoiceListFilter) int {
	if filter.Limit == 0 {
		return DefaultInvoiceListLimit
	}
	return filter.Limit
}

func writeInvoiceDecodeError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	if errors.Is(err, errClientRequestBodyTooLarge) {
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypePayloadTooLarge,
			Title:  nethttp.StatusText(nethttp.StatusRequestEntityTooLarge),
			Status: nethttp.StatusRequestEntityTooLarge,
			Detail: "request body is too large",
		})
		return
	}
	if errors.Is(err, io.EOF) {
		writeInvoiceBadRequest(w, r, fmt.Errorf("JSON body is required"))
		return
	}
	writeInvoiceBadRequest(w, r, err)
}

func writeInvoiceBadRequest(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeBadRequest,
		Title:  nethttp.StatusText(nethttp.StatusBadRequest),
		Status: nethttp.StatusBadRequest,
		Detail: err.Error(),
	})
}

func writeInvoiceValidation(w nethttp.ResponseWriter, r *nethttp.Request, fieldErrors []FieldError) {
	problem, _ := problemForError(InvoiceValidationError{Fields: fieldErrors})
	httpserver.WriteProblem(w, r, problem)
}

func writeInvoiceError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	if errors.Is(err, ErrInvalidInvoiceListFilter) {
		writeInvoiceBadRequest(w, r, err)
		return
	}
	if problem, ok := problemForError(err); ok {
		httpserver.WriteProblem(w, r, problem)
		return
	}
	httpserver.WriteError(w, r, err)
}

func writeInvoiceJSON(w nethttp.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
