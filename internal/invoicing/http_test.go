package invoicing

import (
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

func TestDecodeClientJSONRejectsMalformedTrailingData(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(nethttp.MethodPost, "/clients", strings.NewReader(`{"name":"Contoso"} trailing`))
	recorder := httptest.NewRecorder()
	var payload map[string]string

	if err := decodeClientJSON(recorder, request, &payload); err == nil {
		t.Fatal("decodeClientJSON() error = nil, want malformed trailing data rejected")
	}
}

func TestInvoiceProblemForWrongAmountIncludesFieldPointer(t *testing.T) {
	t.Parallel()

	problem, ok := problemForError(ErrInvoiceSettlementAmountMismatch)
	if !ok {
		t.Fatal("problemForError(wrong amount) ok = false, want true")
	}
	if problem.Status != nethttp.StatusUnprocessableEntity {
		t.Fatalf("wrong amount status = %d, want %d", problem.Status, nethttp.StatusUnprocessableEntity)
	}
	errorsValue, ok := problem.Extensions["errors"].([]FieldError)
	if !ok || len(errorsValue) != 1 || errorsValue[0].Pointer != "/settled_amount" {
		t.Fatalf("wrong amount errors = %#v, want /settled_amount pointer", problem.Extensions["errors"])
	}

	if _, ok := problemForError(errors.New("untyped")); ok {
		t.Fatal("problemForError(untyped) ok = true, want false")
	}
}

func TestInvoicingOpenAPIFragmentDocumentsInvoiceHTTPPaths(t *testing.T) {
	t.Parallel()

	document := httpserver.OpenAPIDocument("test", OpenAPIFragment())
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatalf("openapi paths missing or wrong type: %+v", document["paths"])
	}
	for _, path := range []string{
		"/api/invoicing/invoices",
		"/api/invoicing/invoices/{id}",
		"/api/invoicing/invoices/{id}/send",
		"/api/invoicing/invoices/{id}/revert",
		"/api/invoicing/invoices/{id}/pdf",
	} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("openapi path %s missing from %+v", path, paths)
		}
	}
	if _, ok := paths["/api/invoicing/invoices/{id}/settle"]; ok {
		t.Fatal("openapi unexpectedly documents invoice settlement endpoint")
	}
}
