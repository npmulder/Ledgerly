package dividends

import (
	"bytes"
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

func TestDividendsHTTPRoutesRequireAuthentication(t *testing.T) {
	router := httpserver.NewRouter(httpserver.Config{
		DB:      dividendsPingerFunc(func(context.Context) error { return nil }),
		APIAuth: dividendsTestAuthMiddleware,
		Modules: []httpserver.Module{
			{
				Name:           ModuleName,
				RegisterRoutes: (&Module{}).RegisterRoutes,
			},
		},
	})

	for _, request := range []struct {
		method string
		path   string
		body   *bytes.Reader
	}{
		{method: nethttp.MethodGet, path: "/api/dividends/headroom", body: bytes.NewReader(nil)},
		{method: nethttp.MethodPost, path: "/api/dividends/validate", body: bytes.NewReader([]byte(`{}`))},
		{method: nethttp.MethodPost, path: "/api/dividends/declare", body: bytes.NewReader([]byte(`{}`))},
		{method: nethttp.MethodGet, path: "/api/dividends/history", body: bytes.NewReader(nil)},
		{method: nethttp.MethodGet, path: "/api/dividends/dividend_1/voucher", body: bytes.NewReader(nil)},
		{method: nethttp.MethodGet, path: "/api/dividends/dividend_1/minutes", body: bytes.NewReader(nil)},
	} {
		req := httptest.NewRequest(request.method, request.path, request.body)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)

		if response.Code != nethttp.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want %d; body=%s", request.method, request.path, response.Code, nethttp.StatusUnauthorized, response.Body.String())
		}
		if got := response.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
			t.Fatalf("%s %s Content-Type = %q, want %s", request.method, request.path, got, httpserver.ProblemContentType)
		}
	}
}

func TestDividendsOpenAPIFragmentDocumentsScreen07Paths(t *testing.T) {
	document := httpserver.OpenAPIDocument("test", OpenAPIFragment())
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatalf("openapi paths missing or wrong type: %+v", document["paths"])
	}

	for _, path := range []string{
		"/api/dividends/headroom",
		"/api/dividends/validate",
		"/api/dividends/declare",
		"/api/dividends/history",
		"/api/dividends/{id}/voucher",
		"/api/dividends/{id}/minutes",
	} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("openapi path %s missing from %+v", path, paths)
		}
	}

	components, ok := document["components"].(map[string]any)
	if !ok {
		t.Fatalf("openapi components missing or wrong type: %+v", document["components"])
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatalf("openapi schemas missing or wrong type: %+v", components["schemas"])
	}
	for _, schema := range []string{
		"DividendsAmountRequest",
		"DividendsHistoryResponse",
		"DividendsValidationProblem",
		"DividendsValidationResult",
	} {
		if _, ok := schemas[schema]; !ok {
			t.Fatalf("openapi schema %s missing from %+v", schema, schemas)
		}
	}
}

type dividendsPingerFunc func(context.Context) error

func (f dividendsPingerFunc) PingContext(ctx context.Context) error {
	return f(ctx)
}

func dividendsTestAuthMiddleware(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if _, err := r.Cookie("test_session"); err == nil {
			next.ServeHTTP(w, r)
			return
		}
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   "https://ledgerly.local/problems/unauthenticated",
			Title:  "Authentication required",
			Status: nethttp.StatusUnauthorized,
			Detail: "test authentication required",
		})
	})
}
