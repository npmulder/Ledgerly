package fixtures

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"testing"

	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/it/harness"
)

// Contoso creates the canonical Munich reverse-charge client through the HTTP
// API and returns the persisted response.
func Contoso(t testing.TB, h *harness.Harness) invoicing.Client {
	t.Helper()

	vatNumber := "DE 129 273 398"
	return createClient(t, h, map[string]any{
		"name": "Contoso GmbH",
		"address": map[string]string{
			"line1":       "Theresienhoehe 12",
			"line2":       "",
			"locality":    "Munich",
			"region":      "Bavaria",
			"postal_code": "80339",
			"country":     "DE",
		},
		"vat_number":       vatNumber,
		"default_currency": string(invoicing.CurrencyEUR),
		"terms_days":       14,
		"vat_treatment":    string(invoicing.VATTreatmentReverseChargeEUB2B),
		"retainer_amount": map[string]any{
			"amount_minor": int64(450000),
			"currency":     string(invoicing.CurrencyEUR),
		},
		"day_rate": nil,
	})
}

// Fabrikam creates the canonical Leeds domestic client through the HTTP API
// and returns the persisted response.
func Fabrikam(t testing.TB, h *harness.Harness) invoicing.Client {
	t.Helper()

	return createClient(t, h, map[string]any{
		"name": "Fabrikam Ltd",
		"address": map[string]string{
			"line1":       "1 Park Row",
			"line2":       "",
			"locality":    "Leeds",
			"region":      "West Yorkshire",
			"postal_code": "LS1 5AB",
			"country":     "GB",
		},
		"vat_number":       nil,
		"default_currency": string(invoicing.CurrencyGBP),
		"terms_days":       30,
		"vat_treatment":    string(invoicing.VATTreatmentDomestic),
		"retainer_amount":  nil,
		"day_rate": map[string]any{
			"amount_minor": int64(60000),
			"currency":     string(invoicing.CurrencyGBP),
		},
	})
}

func createClient(t testing.TB, h *harness.Harness, body map[string]any) invoicing.Client {
	t.Helper()

	responseBody := doJSON(t, h, nethttp.MethodPost, "/api/invoicing/clients", body, nethttp.StatusCreated)
	var client invoicing.Client
	if err := json.Unmarshal(responseBody, &client); err != nil {
		t.Fatalf("decode client fixture response: %v; body=%s", err, string(responseBody))
	}
	return client
}

func doJSON(t testing.TB, h *harness.Harness, method string, path string, requestBody any, wantStatus int) []byte {
	t.Helper()

	payload, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatalf("marshal fixture request body: %v", err)
	}
	req, err := nethttp.NewRequestWithContext(context.Background(), method, path, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create fixture request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read fixture response: %v", err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; body=%s", method, path, resp.StatusCode, wantStatus, string(bodyBytes))
	}
	return bodyBytes
}
