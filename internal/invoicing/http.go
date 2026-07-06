package invoicing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const (
	maxClientJSONBodyBytes = 64 * 1024

	problemTypeBadRequest      = "https://ledgerly.local/problems/bad-request"
	problemTypePayloadTooLarge = "https://ledgerly.local/problems/payload-too-large"
)

var errClientRequestBodyTooLarge = errors.New("request body too large")

type clientHandler struct {
	service *Service
}

type clientRequest struct {
	Name            string       `json:"name"`
	Address         Address      `json:"address"`
	VATNumber       *string      `json:"vat_number"`
	DefaultCurrency Currency     `json:"default_currency"`
	TermsDays       int          `json:"terms_days"`
	VATTreatment    VATTreatment `json:"vat_treatment"`
	RetainerAmount  *MoneyAmount `json:"retainer_amount"`
	DayRate         *MoneyAmount `json:"day_rate"`
}

type clientsResponse struct {
	Clients []Client `json:"clients"`
}

type nullableStringPatch struct {
	set   bool
	value *string
}

type nullableMoneyPatch struct {
	set   bool
	value *MoneyAmount
}

type clientPatch struct {
	name            *string
	address         *Address
	vatNumber       nullableStringPatch
	defaultCurrency *Currency
	termsDays       *int
	vatTreatment    *VATTreatment
	retainerAmount  nullableMoneyPatch
	dayRate         nullableMoneyPatch
}

// RegisterRoutes mounts invoicing REST endpoints.
func (m *Module) RegisterRoutes(r chi.Router) {
	h := clientHandler{service: m.service}
	r.Get("/clients", h.listClients)
	r.Post("/clients", h.createClient)
	r.Get("/clients/{id}", h.getClient)
	r.Patch("/clients/{id}", h.patchClient)
	r.Post("/clients/{id}/archive", h.archiveClient)

	invoices := invoiceHandler{service: m.service}
	r.Get("/invoices", invoices.listInvoices)
	r.Post("/invoices", invoices.createDraftInvoice)
	r.Get("/invoices/{id}", invoices.getInvoice)
	r.Patch("/invoices/{id}", invoices.patchInvoice)
	r.Post("/invoices/{id}/send", invoices.sendInvoice)
	r.Post("/invoices/{id}/revert", invoices.revertInvoice)
	r.Get("/invoices/{id}/print", invoices.getInvoicePrintPayload)
	r.Get("/invoices/{id}/pdf/preview", invoices.previewInvoicePDF)
	r.Post("/invoices/{id}/pdf/render", invoices.renderInvoicePDF)
	r.Get("/invoices/{id}/pdf", invoices.getInvoicePDF)
}

func (h clientHandler) listClients(w nethttp.ResponseWriter, r *nethttp.Request) {
	includeArchived, err := parseIncludeArchived(r)
	if err != nil {
		writeClientBadRequest(w, r, err)
		return
	}

	var clients []Client
	if includeArchived {
		clients, err = h.service.ClientsIncludingArchived(r.Context())
	} else {
		clients, err = h.service.Clients(r.Context())
	}
	if err != nil {
		writeClientError(w, r, err)
		return
	}
	writeClientJSON(w, nethttp.StatusOK, clientsResponse{Clients: clients})
}

func (h clientHandler) createClient(w nethttp.ResponseWriter, r *nethttp.Request) {
	var request clientRequest
	if err := decodeClientJSON(w, r, &request); err != nil {
		writeClientDecodeError(w, r, err)
		return
	}

	client, err := h.service.SaveClient(r.Context(), request.client(""))
	if err != nil {
		writeClientError(w, r, err)
		return
	}
	writeClientJSON(w, nethttp.StatusCreated, client)
}

func (h clientHandler) getClient(w nethttp.ResponseWriter, r *nethttp.Request) {
	client, err := h.service.Client(r.Context(), clientIDParam(r))
	if err != nil {
		writeClientError(w, r, err)
		return
	}
	writeClientJSON(w, nethttp.StatusOK, client)
}

func (h clientHandler) patchClient(w nethttp.ResponseWriter, r *nethttp.Request) {
	patch, fieldErrors, err := decodeClientPatch(w, r)
	if err != nil {
		writeClientDecodeError(w, r, err)
		return
	}
	if len(fieldErrors) > 0 {
		writeClientValidation(w, r, fieldErrors)
		return
	}

	client, err := h.service.patchClient(r.Context(), clientIDParam(r), func(client Client) (Client, error) {
		return patch.apply(client), nil
	})
	if err != nil {
		writeClientError(w, r, err)
		return
	}
	writeClientJSON(w, nethttp.StatusOK, client)
}

func (h clientHandler) archiveClient(w nethttp.ResponseWriter, r *nethttp.Request) {
	if err := h.service.ArchiveClient(r.Context(), clientIDParam(r)); err != nil {
		writeClientError(w, r, err)
		return
	}
	w.WriteHeader(nethttp.StatusNoContent)
}

func clientIDParam(r *nethttp.Request) string {
	return strings.TrimSpace(chi.URLParam(r, "id"))
}

func parseIncludeArchived(r *nethttp.Request) (bool, error) {
	value := strings.TrimSpace(r.URL.Query().Get("include_archived"))
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("include_archived must be true or false")
	}
	return parsed, nil
}

func (r clientRequest) client(id string) Client {
	return Client{
		ID:              id,
		Name:            r.Name,
		Address:         r.Address,
		VATNumber:       r.VATNumber,
		DefaultCurrency: r.DefaultCurrency,
		TermsDays:       r.TermsDays,
		VATTreatment:    r.VATTreatment,
		RetainerAmount:  r.RetainerAmount,
		DayRate:         r.DayRate,
	}
}

func (p clientPatch) apply(client Client) Client {
	if p.name != nil {
		client.Name = *p.name
	}
	if p.address != nil {
		client.Address = *p.address
	}
	if p.vatNumber.set {
		client.VATNumber = p.vatNumber.value
	}
	if p.defaultCurrency != nil {
		client.DefaultCurrency = *p.defaultCurrency
	}
	if p.termsDays != nil {
		client.TermsDays = *p.termsDays
	}
	if p.vatTreatment != nil {
		client.VATTreatment = *p.vatTreatment
	}
	if p.retainerAmount.set {
		client.RetainerAmount = p.retainerAmount.value
	}
	if p.dayRate.set {
		client.DayRate = p.dayRate.value
	}
	return client
}

func decodeClientPatch(w nethttp.ResponseWriter, r *nethttp.Request) (clientPatch, []FieldError, error) {
	var raw map[string]json.RawMessage
	if err := decodeClientJSON(w, r, &raw); err != nil {
		return clientPatch{}, nil, err
	}

	var (
		patch       clientPatch
		fieldErrors []FieldError
	)
	for field, value := range raw {
		switch field {
		case "name":
			assignStringPatch(value, "/name", &patch.name, &fieldErrors)
		case "address":
			if rejectClientJSONNull(value, "/address", "must be an object with address fields", &fieldErrors) {
				continue
			}
			var address Address
			if err := decodeClientStrict(value, &address); err != nil {
				fieldErrors = append(fieldErrors, FieldError{Pointer: "/address", Detail: "must be an object with address fields"})
				continue
			}
			patch.address = &address
		case "vat_number":
			patch.vatNumber.set = true
			if isClientJSONNull(value) {
				continue
			}
			var vatNumber string
			if err := decodeClientStrict(value, &vatNumber); err != nil {
				fieldErrors = append(fieldErrors, FieldError{Pointer: "/vat_number", Detail: "must be a string or null"})
				continue
			}
			patch.vatNumber.value = &vatNumber
		case "default_currency":
			var currency Currency
			if err := decodeClientStrict(value, &currency); err != nil {
				fieldErrors = append(fieldErrors, FieldError{Pointer: "/default_currency", Detail: "must be a string"})
				continue
			}
			patch.defaultCurrency = &currency
		case "terms_days":
			var termsDays int
			if err := decodeClientStrict(value, &termsDays); err != nil {
				fieldErrors = append(fieldErrors, FieldError{Pointer: "/terms_days", Detail: "must be an integer"})
				continue
			}
			patch.termsDays = &termsDays
		case "vat_treatment":
			var treatment VATTreatment
			if err := decodeClientStrict(value, &treatment); err != nil {
				fieldErrors = append(fieldErrors, FieldError{Pointer: "/vat_treatment", Detail: "must be a string"})
				continue
			}
			patch.vatTreatment = &treatment
		case "retainer_amount":
			patch.retainerAmount.set = true
			if isClientJSONNull(value) {
				continue
			}
			var amount MoneyAmount
			if err := decodeClientStrict(value, &amount); err != nil {
				fieldErrors = append(fieldErrors, FieldError{Pointer: "/retainer_amount", Detail: "must be a money object or null"})
				continue
			}
			patch.retainerAmount.value = &amount
		case "day_rate":
			patch.dayRate.set = true
			if isClientJSONNull(value) {
				continue
			}
			var amount MoneyAmount
			if err := decodeClientStrict(value, &amount); err != nil {
				fieldErrors = append(fieldErrors, FieldError{Pointer: "/day_rate", Detail: "must be a money object or null"})
				continue
			}
			patch.dayRate.value = &amount
		default:
			return clientPatch{}, nil, fmt.Errorf("unknown field %q", field)
		}
	}
	return patch, fieldErrors, nil
}

func assignStringPatch(value json.RawMessage, pointer string, dst **string, fieldErrors *[]FieldError) {
	var decoded string
	if err := decodeClientStrict(value, &decoded); err != nil {
		*fieldErrors = append(*fieldErrors, FieldError{Pointer: pointer, Detail: "must be a string"})
		return
	}
	*dst = &decoded
}

func rejectClientJSONNull(value json.RawMessage, pointer string, detail string, fieldErrors *[]FieldError) bool {
	if !isClientJSONNull(value) {
		return false
	}
	*fieldErrors = append(*fieldErrors, FieldError{Pointer: pointer, Detail: detail})
	return true
}

func isClientJSONNull(raw json.RawMessage) bool {
	return strings.EqualFold(strings.TrimSpace(string(raw)), "null")
}

func decodeClientJSON(w nethttp.ResponseWriter, r *nethttp.Request, dst any) error {
	r.Body = nethttp.MaxBytesReader(w, r.Body, maxClientJSONBodyBytes)
	defer func() {
		_ = r.Body.Close()
	}()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		var maxBytesErr *nethttp.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return errClientRequestBodyTooLarge
		}
		return fmt.Errorf("decode JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		return fmt.Errorf("JSON body must contain one object")
	} else {
		var maxBytesErr *nethttp.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return errClientRequestBodyTooLarge
		}
		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("decode JSON body: %w", err)
		}
	}
	return nil
}

func decodeClientStrict(raw json.RawMessage, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		return fmt.Errorf("JSON value must contain one value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func writeClientDecodeError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	if errors.Is(err, errClientRequestBodyTooLarge) {
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypePayloadTooLarge,
			Title:  nethttp.StatusText(nethttp.StatusRequestEntityTooLarge),
			Status: nethttp.StatusRequestEntityTooLarge,
			Detail: "request body is too large",
		})
		return
	}
	writeClientBadRequest(w, r, err)
}

func writeClientBadRequest(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeBadRequest,
		Title:  nethttp.StatusText(nethttp.StatusBadRequest),
		Status: nethttp.StatusBadRequest,
		Detail: err.Error(),
	})
}

func writeClientValidation(w nethttp.ResponseWriter, r *nethttp.Request, fieldErrors []FieldError) {
	problem, _ := problemForError(ValidationError{Fields: fieldErrors})
	httpserver.WriteProblem(w, r, problem)
}

func writeClientError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	if problem, ok := problemForError(err); ok {
		httpserver.WriteProblem(w, r, problem)
		return
	}
	httpserver.WriteError(w, r, err)
}

func writeClientJSON(w nethttp.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
