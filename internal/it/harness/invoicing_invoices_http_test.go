//go:build integration

package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

func TestInvoicingInvoiceHTTPRoundTripAutosaveSendPDFAndRevert(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	contoso := fixtures.Contoso(t, h)

	create := performInvoiceRequest(t, h, nethttp.MethodPost, "/api/invoicing/invoices", mustInvoiceJSON(t, map[string]any{
		"client_id": contoso.ID,
	}), true)
	if create.StatusCode != nethttp.StatusCreated {
		t.Fatalf("create invoice status = %d, want %d; body=%s", create.StatusCode, nethttp.StatusCreated, create.BodyString())
	}
	draft := decodeInvoiceResponse(t, create)
	if draft.Status != invoicing.InvoiceStatusDraft || draft.ClientID != contoso.ID {
		t.Fatalf("created invoice = status %q client %s, want draft for %s", draft.Status, draft.ClientID, contoso.ID)
	}
	if len(draft.Lines) != 0 {
		t.Fatalf("created invoice lines = %+v, want none", draft.Lines)
	}

	get := performInvoiceRequest(t, h, nethttp.MethodGet, "/api/invoicing/invoices/"+draft.ID, nil, true)
	if get.StatusCode != nethttp.StatusOK {
		t.Fatalf("get invoice status = %d, want %d; body=%s", get.StatusCode, nethttp.StatusOK, get.BodyString())
	}

	patch := performInvoiceRequest(t, h, nethttp.MethodPatch, "/api/invoicing/invoices/"+draft.ID, mustInvoiceJSON(t, map[string]any{
		"due_date": "2025-05-20",
		"lines": []map[string]any{
			{
				"id":          "line-client-a",
				"description": "Monthly retainer",
				"qty":         "1",
				"unit_price": map[string]any{
					"amount":   int64(450_000),
					"currency": string(invoicing.CurrencyEUR),
				},
			},
		},
	}), true)
	if patch.StatusCode != nethttp.StatusOK {
		t.Fatalf("patch invoice status = %d, want %d; body=%s", patch.StatusCode, nethttp.StatusOK, patch.BodyString())
	}
	updated := decodeInvoiceResponse(t, patch)
	if updated.UpdatedAt.IsZero() {
		t.Fatal("patched invoice UpdatedAt is zero, want echoed autosave timestamp")
	}
	if len(updated.Lines) != 1 || updated.Lines[0].ID != "line-client-a" {
		t.Fatalf("patched lines = %+v, want client-generated line id preserved", updated.Lines)
	}
	assertMoney(t, updated.Lines[0].LineTotal, 450_000, "EUR")
	assertMoney(t, updated.Totals.Total, 450_000, "EUR")

	replace := performInvoiceRequest(t, h, nethttp.MethodPatch, "/api/invoicing/invoices/"+draft.ID, mustInvoiceJSON(t, map[string]any{
		"lines": []map[string]any{
			{
				"id":          "line-client-b",
				"description": "Short support block",
				"qty":         "2",
				"unit_price": map[string]any{
					"amount":   int64(10_000),
					"currency": string(invoicing.CurrencyEUR),
				},
			},
		},
	}), true)
	if replace.StatusCode != nethttp.StatusOK {
		t.Fatalf("replace invoice lines status = %d, want %d; body=%s", replace.StatusCode, nethttp.StatusOK, replace.BodyString())
	}
	replaced := decodeInvoiceResponse(t, replace)
	if len(replaced.Lines) != 1 || replaced.Lines[0].ID != "line-client-b" {
		t.Fatalf("replaced lines = %+v, want last write to replace line array", replaced.Lines)
	}
	assertMoney(t, replaced.Totals.Total, 20_000, "EUR")

	list := performInvoiceRequest(t, h, nethttp.MethodGet, "/api/invoicing/invoices?status=draft&search=contoso&limit=10&offset=0", nil, true)
	if list.StatusCode != nethttp.StatusOK {
		t.Fatalf("list invoices status = %d, want %d; body=%s", list.StatusCode, nethttp.StatusOK, list.BodyString())
	}
	listBody := decodeInvoicesResponse(t, list)
	if listBody.TotalCount != 1 || len(listBody.Invoices) != 1 || listBody.Invoices[0].ID != draft.ID {
		t.Fatalf("list response = total %d invoices %+v, want one draft invoice %s", listBody.TotalCount, listBody.Invoices, draft.ID)
	}
	if counts := countsByStatus(listBody.Counts); counts[invoicing.InvoiceStatusDraft] != 1 {
		t.Fatalf("list counts = %#v, want draft count 1", counts)
	}
	assertMoney(t, listBody.Totals.Subtotals[0], 20_000, "EUR")
	assertMoney(t, listBody.Totals.TotalGBP, 17_000, "GBP")

	pdf := performInvoiceRequest(t, h, nethttp.MethodGet, "/api/invoicing/invoices/"+draft.ID+"/pdf", nil, true)
	if pdf.StatusCode != nethttp.StatusNotFound {
		t.Fatalf("invoice pdf status = %d, want %d before INV-8; body=%s", pdf.StatusCode, nethttp.StatusNotFound, pdf.BodyString())
	}
	if got := pdf.Header.Get("Content-Type"); got != httpserver.ProblemContentType {
		t.Fatalf("invoice pdf Content-Type = %q, want %s", got, httpserver.ProblemContentType)
	}

	send := performInvoiceRequest(t, h, nethttp.MethodPost, "/api/invoicing/invoices/"+draft.ID+"/send", nil, true)
	if send.StatusCode != nethttp.StatusOK {
		t.Fatalf("send invoice status = %d, want %d; body=%s", send.StatusCode, nethttp.StatusOK, send.BodyString())
	}
	sent := decodeSendInvoiceResponse(t, send)
	if sent.Number != "INV-2025-01" || sent.Invoice.Number == nil || *sent.Invoice.Number != sent.Number {
		t.Fatalf("send response number = %q invoice number %v, want INV-2025-01", sent.Number, sent.Invoice.Number)
	}
	if sent.LockedRate.ID <= 0 || sent.LockedRate.Rate != "0.85" {
		t.Fatalf("send locked rate = %+v, want 0.85", sent.LockedRate)
	}

	revert := performInvoiceRequest(t, h, nethttp.MethodPost, "/api/invoicing/invoices/"+draft.ID+"/revert", nil, true)
	if revert.StatusCode != nethttp.StatusOK {
		t.Fatalf("revert invoice status = %d, want %d; body=%s", revert.StatusCode, nethttp.StatusOK, revert.BodyString())
	}
	reverted := decodeInvoiceResponse(t, revert)
	if reverted.Status != invoicing.InvoiceStatusDraft || reverted.Number != nil || reverted.LockID != nil {
		t.Fatalf("reverted invoice = status %q number %v lock %v, want unlocked draft", reverted.Status, reverted.Number, reverted.LockID)
	}
}

func TestInvoicingInvoiceHTTPErrorCases(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	fabrikam := fixtures.Fabrikam(t, h)

	missingClient := performInvoiceRequest(t, h, nethttp.MethodPost, "/api/invoicing/invoices", mustInvoiceJSON(t, map[string]any{}), true)
	if missingClient.StatusCode != nethttp.StatusUnprocessableEntity {
		t.Fatalf("missing client status = %d, want %d; body=%s", missingClient.StatusCode, nethttp.StatusUnprocessableEntity, missingClient.BodyString())
	}
	if !jsonContainsPointer(t, missingClient.Body, "/client_id") {
		t.Fatalf("missing client problem = %s, want /client_id pointer", missingClient.BodyString())
	}

	badList := performInvoiceRequest(t, h, nethttp.MethodGet, "/api/invoicing/invoices?limit=-1", nil, true)
	if badList.StatusCode != nethttp.StatusBadRequest {
		t.Fatalf("bad list status = %d, want %d; body=%s", badList.StatusCode, nethttp.StatusBadRequest, badList.BodyString())
	}

	missingInvoice := performInvoiceRequest(t, h, nethttp.MethodGet, "/api/invoicing/invoices/invoice_missing", nil, true)
	if missingInvoice.StatusCode != nethttp.StatusNotFound {
		t.Fatalf("missing invoice status = %d, want %d; body=%s", missingInvoice.StatusCode, nethttp.StatusNotFound, missingInvoice.BodyString())
	}

	draft := createDraftInvoiceViaHTTP(t, h, fabrikam.ID)
	incompleteSend := performInvoiceRequest(t, h, nethttp.MethodPost, "/api/invoicing/invoices/"+draft.ID+"/send", nil, true)
	if incompleteSend.StatusCode != nethttp.StatusUnprocessableEntity {
		t.Fatalf("incomplete send status = %d, want %d; body=%s", incompleteSend.StatusCode, nethttp.StatusUnprocessableEntity, incompleteSend.BodyString())
	}
	if !jsonContainsPointer(t, incompleteSend.Body, "/lines") {
		t.Fatalf("incomplete send problem = %s, want /lines pointer", incompleteSend.BodyString())
	}

	duplicateLines := performInvoiceRequest(t, h, nethttp.MethodPatch, "/api/invoicing/invoices/"+draft.ID, mustInvoiceJSON(t, map[string]any{
		"lines": []map[string]any{
			{
				"id":          "dup-line",
				"description": "First",
				"qty":         "1",
				"unit_price": map[string]any{
					"amount":   int64(10_000),
					"currency": string(invoicing.CurrencyGBP),
				},
			},
			{
				"id":          "dup-line",
				"description": "Second",
				"qty":         "1",
				"unit_price": map[string]any{
					"amount":   int64(10_000),
					"currency": string(invoicing.CurrencyGBP),
				},
			},
		},
	}), true)
	if duplicateLines.StatusCode != nethttp.StatusUnprocessableEntity {
		t.Fatalf("duplicate lines status = %d, want %d; body=%s", duplicateLines.StatusCode, nethttp.StatusUnprocessableEntity, duplicateLines.BodyString())
	}
	if !jsonContainsPointer(t, duplicateLines.Body, "/lines/1/id") {
		t.Fatalf("duplicate lines problem = %s, want /lines/1/id pointer", duplicateLines.BodyString())
	}

	complete := patchDraftInvoiceLinesViaHTTP(t, h, draft.ID, "line-ok")
	send := performInvoiceRequest(t, h, nethttp.MethodPost, "/api/invoicing/invoices/"+complete.ID+"/send", nil, true)
	if send.StatusCode != nethttp.StatusOK {
		t.Fatalf("send complete invoice status = %d, want %d; body=%s", send.StatusCode, nethttp.StatusOK, send.BodyString())
	}
	immutablePatch := performInvoiceRequest(t, h, nethttp.MethodPatch, "/api/invoicing/invoices/"+complete.ID, mustInvoiceJSON(t, map[string]any{
		"due_date": "2025-06-01",
	}), true)
	if immutablePatch.StatusCode != nethttp.StatusConflict {
		t.Fatalf("immutable patch status = %d, want %d; body=%s", immutablePatch.StatusCode, nethttp.StatusConflict, immutablePatch.BodyString())
	}
	if !jsonContainsPointer(t, immutablePatch.Body, "/status") {
		t.Fatalf("immutable patch problem = %s, want /status pointer", immutablePatch.BodyString())
	}
}

func TestInvoicingInvoiceHTTPClientLineIDsAreScopedPerInvoice(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	fabrikam := fixtures.Fabrikam(t, h)

	first := createDraftInvoiceViaHTTP(t, h, fabrikam.ID)
	second := createDraftInvoiceViaHTTP(t, h, fabrikam.ID)

	firstPatched := patchDraftInvoiceLinesViaHTTP(t, h, first.ID, "line-1")
	secondPatched := patchDraftInvoiceLinesViaHTTP(t, h, second.ID, "line-1")

	if len(firstPatched.Lines) != 1 || firstPatched.Lines[0].ID != "line-1" {
		t.Fatalf("first invoice lines = %+v, want client id line-1", firstPatched.Lines)
	}
	if len(secondPatched.Lines) != 1 || secondPatched.Lines[0].ID != "line-1" {
		t.Fatalf("second invoice lines = %+v, want client id line-1", secondPatched.Lines)
	}

	firstGet := performInvoiceRequest(t, h, nethttp.MethodGet, "/api/invoicing/invoices/"+first.ID, nil, true)
	if firstGet.StatusCode != nethttp.StatusOK {
		t.Fatalf("get first invoice status = %d, want %d; body=%s", firstGet.StatusCode, nethttp.StatusOK, firstGet.BodyString())
	}
	firstFetched := decodeInvoiceResponse(t, firstGet)
	if len(firstFetched.Lines) != 1 || firstFetched.Lines[0].ID != "line-1" {
		t.Fatalf("first fetched lines = %+v, want client id line-1", firstFetched.Lines)
	}
}

func TestInvoicingInvoiceHTTPDetailDerivesOverdueStatus(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	fabrikam := fixtures.Fabrikam(t, h)

	draft := createDraftInvoiceViaHTTP(t, h, fabrikam.ID)
	patch := performInvoiceRequest(t, h, nethttp.MethodPatch, "/api/invoicing/invoices/"+draft.ID, mustInvoiceJSON(t, map[string]any{
		"due_date": "2025-05-02",
		"lines": []map[string]any{
			{
				"id":          "line-overdue",
				"description": "GBP delivery",
				"qty":         "1",
				"unit_price": map[string]any{
					"amount":   int64(10_000),
					"currency": string(invoicing.CurrencyGBP),
				},
			},
		},
	}), true)
	if patch.StatusCode != nethttp.StatusOK {
		t.Fatalf("patch invoice status = %d, want %d; body=%s", patch.StatusCode, nethttp.StatusOK, patch.BodyString())
	}
	patched := decodeInvoiceResponse(t, patch)
	send := performInvoiceRequest(t, h, nethttp.MethodPost, "/api/invoicing/invoices/"+patched.ID+"/send", nil, true)
	if send.StatusCode != nethttp.StatusOK {
		t.Fatalf("send invoice status = %d, want %d; body=%s", send.StatusCode, nethttp.StatusOK, send.BodyString())
	}

	h.Clock.Set(patched.DueDate.AddDate(0, 0, 1))
	detail := performInvoiceRequest(t, h, nethttp.MethodGet, "/api/invoicing/invoices/"+patched.ID, nil, true)
	if detail.StatusCode != nethttp.StatusOK {
		t.Fatalf("get overdue invoice status = %d, want %d; body=%s", detail.StatusCode, nethttp.StatusOK, detail.BodyString())
	}
	invoice := decodeInvoiceResponse(t, detail)
	if invoice.Status != invoicing.InvoiceStatusOverdue {
		t.Fatalf("detail invoice status = %q, want %q", invoice.Status, invoicing.InvoiceStatusOverdue)
	}
}

func TestInvoicingInvoiceHTTPRoutesRequireAuthentication(t *testing.T) {
	h := harness.New(t, harness.Options{})

	for _, request := range []struct {
		method string
		path   string
		body   io.Reader
	}{
		{method: nethttp.MethodGet, path: "/api/invoicing/invoices"},
		{method: nethttp.MethodPost, path: "/api/invoicing/invoices", body: mustInvoiceJSON(t, map[string]any{})},
		{method: nethttp.MethodGet, path: "/api/invoicing/invoices/invoice_unauth"},
		{method: nethttp.MethodPatch, path: "/api/invoicing/invoices/invoice_unauth", body: mustInvoiceJSON(t, map[string]any{})},
		{method: nethttp.MethodPost, path: "/api/invoicing/invoices/invoice_unauth/send"},
		{method: nethttp.MethodPost, path: "/api/invoicing/invoices/invoice_unauth/revert"},
		{method: nethttp.MethodGet, path: "/api/invoicing/invoices/invoice_unauth/pdf"},
	} {
		response := performInvoiceRequest(t, h, request.method, request.path, request.body, false)
		if response.StatusCode != nethttp.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want %d; body=%s", request.method, request.path, response.StatusCode, nethttp.StatusUnauthorized, response.BodyString())
		}
		if got := response.Header.Get("Content-Type"); got != httpserver.ProblemContentType {
			t.Fatalf("%s %s Content-Type = %q, want %s", request.method, request.path, got, httpserver.ProblemContentType)
		}
	}
}

func createDraftInvoiceViaHTTP(t *testing.T, h *harness.Harness, clientID string) invoicing.Invoice {
	t.Helper()
	response := performInvoiceRequest(t, h, nethttp.MethodPost, "/api/invoicing/invoices", mustInvoiceJSON(t, map[string]any{
		"client_id": clientID,
	}), true)
	if response.StatusCode != nethttp.StatusCreated {
		t.Fatalf("create draft status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusCreated, response.BodyString())
	}
	return decodeInvoiceResponse(t, response)
}

func patchDraftInvoiceLinesViaHTTP(t *testing.T, h *harness.Harness, id string, lineID string) invoicing.Invoice {
	t.Helper()
	response := performInvoiceRequest(t, h, nethttp.MethodPatch, "/api/invoicing/invoices/"+id, mustInvoiceJSON(t, map[string]any{
		"lines": []map[string]any{
			{
				"id":          lineID,
				"description": "GBP delivery",
				"qty":         "1",
				"unit_price": map[string]any{
					"amount":   int64(10_000),
					"currency": string(invoicing.CurrencyGBP),
				},
			},
		},
	}), true)
	if response.StatusCode != nethttp.StatusOK {
		t.Fatalf("patch draft status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusOK, response.BodyString())
	}
	return decodeInvoiceResponse(t, response)
}

type invoiceHTTPResponse struct {
	StatusCode int
	Header     nethttp.Header
	Body       []byte
}

func (r invoiceHTTPResponse) BodyString() string {
	return string(r.Body)
}

type invoiceSendBody struct {
	Invoice    invoicing.Invoice `json:"invoice"`
	Number     string            `json:"number"`
	LockedRate struct {
		ID   int64  `json:"id"`
		Rate string `json:"rate"`
	} `json:"locked_rate"`
}

type invoicesListBody struct {
	Invoices   []invoicing.InvoiceListItem    `json:"invoices"`
	Counts     []invoicing.InvoiceStatusCount `json:"counts"`
	TotalCount int                            `json:"total_count"`
	Totals     invoicing.InvoiceTotalsSummary `json:"totals"`
	Limit      int                            `json:"limit"`
	Offset     int                            `json:"offset"`
}

func performInvoiceRequest(t *testing.T, h *harness.Harness, method string, path string, body io.Reader, authenticated bool) invoiceHTTPResponse {
	t.Helper()

	target := path
	if !authenticated {
		target = h.BaseURL + path
	}
	request, err := nethttp.NewRequestWithContext(context.Background(), method, target, body)
	if err != nil {
		t.Fatalf("create %s %s request: %v", method, path, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	var response *nethttp.Response
	if authenticated {
		response, err = h.Do(request)
	} else {
		response, err = nethttp.DefaultClient.Do(request)
	}
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() {
		_ = response.Body.Close()
	}()

	bodyBytes, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return invoiceHTTPResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header,
		Body:       bodyBytes,
	}
}

func decodeInvoiceResponse(t *testing.T, response invoiceHTTPResponse) invoicing.Invoice {
	t.Helper()
	var invoice invoicing.Invoice
	if err := json.Unmarshal(response.Body, &invoice); err != nil {
		t.Fatalf("decode invoice response: %v; body=%s", err, response.BodyString())
	}
	return invoice
}

func decodeSendInvoiceResponse(t *testing.T, response invoiceHTTPResponse) invoiceSendBody {
	t.Helper()
	var body invoiceSendBody
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatalf("decode send invoice response: %v; body=%s", err, response.BodyString())
	}
	return body
}

func decodeInvoicesResponse(t *testing.T, response invoiceHTTPResponse) invoicesListBody {
	t.Helper()
	var body invoicesListBody
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatalf("decode invoices response: %v; body=%s", err, response.BodyString())
	}
	return body
}

func mustInvoiceJSON(t *testing.T, body map[string]any) io.Reader {
	t.Helper()

	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal JSON body: %v", err)
	}
	return bytes.NewReader(encoded)
}
