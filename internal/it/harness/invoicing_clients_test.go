//go:build integration

package harness_test

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"testing"

	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
)

func TestInvoicingClientsCRUDRoundTrip(t *testing.T) {
	t.Parallel()

	h := harness.New(t, harness.Options{})
	contoso := fixtures.Contoso(t, h)
	fabrikam := fixtures.Fabrikam(t, h)

	clients := listInvoicingClients(t, h, false)
	assertClientListed(t, clients, contoso.ID)
	assertClientListed(t, clients, fabrikam.ID)

	bodyBytes, status := doJSON(t, h, nethttp.MethodPatch, "/api/invoicing/clients/"+contoso.ID, map[string]any{
		"terms_days": 30,
		"retainer_amount": map[string]any{
			"amount_minor": int64(500000),
			"currency":     string(invoicing.CurrencyEUR),
		},
	})
	if status != nethttp.StatusOK {
		t.Fatalf("PATCH client status = %d, want %d; body=%s", status, nethttp.StatusOK, string(bodyBytes))
	}
	var updated invoicing.Client
	if err := json.Unmarshal(bodyBytes, &updated); err != nil {
		t.Fatalf("decode patched client: %v; body=%s", err, string(bodyBytes))
	}
	if updated.TermsDays != 30 || updated.RetainerAmount == nil || updated.RetainerAmount.AmountMinor != 500000 {
		t.Fatalf("patched client = %+v, want updated terms and retainer", updated)
	}

	archiveInvoicingClient(t, h, fabrikam.ID)
	activeClients := listInvoicingClients(t, h, false)
	assertClientNotListed(t, activeClients, fabrikam.ID)

	archived := getInvoicingClient(t, h, fabrikam.ID)
	if archived.ArchivedAt == nil {
		t.Fatalf("archived client ArchivedAt = nil, want timestamp")
	}
	allClients := listInvoicingClients(t, h, true)
	assertClientListed(t, allClients, fabrikam.ID)
}

func TestInvoicingClientsRejectReverseChargeWithoutVATNumber(t *testing.T) {
	t.Parallel()

	h := harness.New(t, harness.Options{})
	bodyBytes, status := doJSON(t, h, nethttp.MethodPost, "/api/invoicing/clients", map[string]any{
		"name": "No VAT GmbH",
		"address": map[string]string{
			"line1":       "1 Test Strasse",
			"line2":       "",
			"locality":    "Berlin",
			"region":      "",
			"postal_code": "10115",
			"country":     "DE",
		},
		"vat_number":       nil,
		"default_currency": string(invoicing.CurrencyEUR),
		"terms_days":       14,
		"vat_treatment":    string(invoicing.VATTreatmentReverseChargeEUB2B),
		"retainer_amount":  nil,
		"day_rate":         nil,
	})
	if status != nethttp.StatusUnprocessableEntity {
		t.Fatalf("POST reverse-charge without VAT status = %d, want %d; body=%s", status, nethttp.StatusUnprocessableEntity, string(bodyBytes))
	}
	if !jsonContainsPointer(t, bodyBytes, "/vat_number") {
		t.Fatalf("validation body = %s, want /vat_number error", string(bodyBytes))
	}
}

func listInvoicingClients(t *testing.T, h *harness.Harness, includeArchived bool) []invoicing.Client {
	t.Helper()

	path := "/api/invoicing/clients"
	if includeArchived {
		path += "?include_archived=true"
	}
	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, path, nil)
	if err != nil {
		t.Fatalf("create GET %s request: %v", path, err)
	}
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()

	var response struct {
		Clients []invoicing.Client `json:"clients"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("decode GET %s response: %v", path, err)
	}
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET %s status = %d, want %d", path, resp.StatusCode, nethttp.StatusOK)
	}
	return response.Clients
}

func getInvoicingClient(t *testing.T, h *harness.Harness, id string) invoicing.Client {
	t.Helper()

	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, "/api/invoicing/clients/"+id, nil)
	if err != nil {
		t.Fatalf("create GET client request: %v", err)
	}
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("GET client: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET client status = %d, want %d", resp.StatusCode, nethttp.StatusOK)
	}

	var client invoicing.Client
	if err := json.NewDecoder(resp.Body).Decode(&client); err != nil {
		t.Fatalf("decode GET client response: %v", err)
	}
	return client
}

func archiveInvoicingClient(t *testing.T, h *harness.Harness, id string) {
	t.Helper()

	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodPost, "/api/invoicing/clients/"+id+"/archive", nil)
	if err != nil {
		t.Fatalf("create archive client request: %v", err)
	}
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("archive client: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("archive client status = %d, want %d", resp.StatusCode, nethttp.StatusNoContent)
	}
}

func assertClientListed(t *testing.T, clients []invoicing.Client, id string) {
	t.Helper()
	for _, client := range clients {
		if client.ID == id {
			return
		}
	}
	t.Fatalf("client %s not found in list %+v", id, clients)
}

func assertClientNotListed(t *testing.T, clients []invoicing.Client, id string) {
	t.Helper()
	for _, client := range clients {
		if client.ID == id {
			t.Fatalf("client %s found in active list %+v", id, clients)
		}
	}
}

func jsonContainsPointer(t *testing.T, body []byte, pointer string) bool {
	t.Helper()

	var problem struct {
		Errors []invoicing.FieldError `json:"errors"`
	}
	if err := json.Unmarshal(body, &problem); err != nil {
		t.Fatalf("decode problem body: %v; body=%s", err, string(body))
	}
	for _, field := range problem.Errors {
		if field.Pointer == pointer {
			return true
		}
	}
	return false
}
