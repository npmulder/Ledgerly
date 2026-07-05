package invoicing

import (
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
