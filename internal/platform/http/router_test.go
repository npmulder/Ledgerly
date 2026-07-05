package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestHealthzReturnsVersionWhenDatabasePingSucceeds(t *testing.T) {
	router := NewRouter(Config{
		Version: "test-version",
		DB:      pingerFunc(func(context.Context) error { return nil }),
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/healthz", nil))

	if response.Code != nethttp.StatusOK {
		t.Fatalf("/healthz status = %d, want %d; body=%s", response.Code, nethttp.StatusOK, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var body healthResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("health response is not JSON: %v", err)
	}
	if body.Version != "test-version" {
		t.Fatalf("version = %q, want test-version", body.Version)
	}
	if body.Status != "ok" {
		t.Fatalf("status = %q, want ok", body.Status)
	}
	if body.Checks["db"].Status != "ok" {
		t.Fatalf("db status = %q, want ok", body.Checks["db"].Status)
	}
}

func TestHealthzReturnsProblemWhenDatabasePingFails(t *testing.T) {
	router := NewRouter(Config{
		Version: "test-version",
		DB:      pingerFunc(func(context.Context) error { return errors.New("connection refused") }),
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/healthz", nil))

	if response.Code != nethttp.StatusServiceUnavailable {
		t.Fatalf("/healthz status = %d, want %d; body=%s", response.Code, nethttp.StatusServiceUnavailable, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != ProblemContentType {
		t.Fatalf("Content-Type = %q, want %s", got, ProblemContentType)
	}

	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("problem response is not JSON: %v", err)
	}
	if body["status"] != float64(nethttp.StatusServiceUnavailable) {
		t.Fatalf("problem status = %v, want %d", body["status"], nethttp.StatusServiceUnavailable)
	}
	if body["version"] != "test-version" {
		t.Fatalf("problem version extension = %v, want test-version", body["version"])
	}
}

func TestReadyzUsesDatabasePing(t *testing.T) {
	var calls int
	router := NewRouter(Config{
		DB: pingerFunc(func(context.Context) error {
			calls++
			return nil
		}),
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/readyz", nil))

	if response.Code != nethttp.StatusOK {
		t.Fatalf("/readyz status = %d, want %d; body=%s", response.Code, nethttp.StatusOK, response.Body.String())
	}
	if calls != 1 {
		t.Fatalf("db ping calls = %d, want 1", calls)
	}
}

func TestPanicRecoveryReturnsProblemAndRouterStaysUsable(t *testing.T) {
	router := NewRouter(Config{
		DB: pingerFunc(func(context.Context) error { return nil }),
		Modules: []Module{
			{
				Name: "test",
				RegisterRoutes: func(r chi.Router) {
					r.Get("/panic", func(nethttp.ResponseWriter, *nethttp.Request) {
						panic("boom")
					})
					r.Get("/ok", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
						w.WriteHeader(nethttp.StatusNoContent)
					})
				},
			},
		},
	})

	panicResponse := httptest.NewRecorder()
	router.ServeHTTP(panicResponse, httptest.NewRequest(nethttp.MethodGet, "/api/test/panic", nil))

	if panicResponse.Code != nethttp.StatusInternalServerError {
		t.Fatalf("panic status = %d, want %d; body=%s", panicResponse.Code, nethttp.StatusInternalServerError, panicResponse.Body.String())
	}
	if got := panicResponse.Header().Get("Content-Type"); got != ProblemContentType {
		t.Fatalf("Content-Type = %q, want %s", got, ProblemContentType)
	}

	okResponse := httptest.NewRecorder()
	router.ServeHTTP(okResponse, httptest.NewRequest(nethttp.MethodGet, "/api/test/ok", nil))
	if okResponse.Code != nethttp.StatusNoContent {
		t.Fatalf("post-panic status = %d, want %d", okResponse.Code, nethttp.StatusNoContent)
	}
}

func TestDomainErrorMapsToProblemDetails(t *testing.T) {
	domainErr := NewDomainError(nethttp.StatusConflict, "https://ledgerly.local/problems/conflict", "Conflict", "already posted")
	router := NewRouter(Config{
		DB: pingerFunc(func(context.Context) error { return nil }),
		Modules: []Module{
			{
				Name: "ledger",
				RegisterRoutes: func(r chi.Router) {
					r.Get("/entries", func(w nethttp.ResponseWriter, r *nethttp.Request) {
						WriteError(w, r, domainErr)
					})
				},
			},
		},
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/api/ledger/entries", nil))

	if response.Code != nethttp.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, nethttp.StatusConflict)
	}
	if got := response.Header().Get("Content-Type"); got != ProblemContentType {
		t.Fatalf("Content-Type = %q, want %s", got, ProblemContentType)
	}

	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("problem response is not JSON: %v", err)
	}
	if problem.Type != "https://ledgerly.local/problems/conflict" {
		t.Fatalf("type = %q", problem.Type)
	}
	if problem.Detail != "already posted" {
		t.Fatalf("detail = %q", problem.Detail)
	}
}

func TestModuleRoutesAreNamespaced(t *testing.T) {
	router := NewRouter(Config{
		DB: pingerFunc(func(context.Context) error { return nil }),
		Modules: []Module{
			{
				Name: "banking",
				RegisterRoutes: func(r chi.Router) {
					r.Get("/transactions", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
						w.WriteHeader(nethttp.StatusAccepted)
					})
				},
			},
		},
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/api/banking/transactions", nil))

	if response.Code != nethttp.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.Code, nethttp.StatusAccepted)
	}
}

func TestAPIAuthMiddlewareWrapsModuleRoutes(t *testing.T) {
	router := NewRouter(Config{
		DB: pingerFunc(func(context.Context) error { return nil }),
		APIAuth: func(next nethttp.Handler) nethttp.Handler {
			return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				w.Header().Set("X-API-Auth", "checked")
				next.ServeHTTP(w, r)
			})
		},
		Modules: []Module{
			{
				Name: "banking",
				RegisterRoutes: func(r chi.Router) {
					r.Get("/transactions", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
						w.WriteHeader(nethttp.StatusAccepted)
					})
				},
			},
		},
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/api/banking/transactions", nil))

	if response.Code != nethttp.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.Code, nethttp.StatusAccepted)
	}
	if got := response.Header().Get("X-API-Auth"); got != "checked" {
		t.Fatalf("X-API-Auth = %q, want checked", got)
	}
}

func TestOpenAPISkeletonIncludesFragments(t *testing.T) {
	router := NewRouter(Config{
		Version: "test-version",
		DB:      pingerFunc(func(context.Context) error { return nil }),
		OpenAPIFragments: []OpenAPIFragment{
			{
				Paths: map[string]any{
					"/api/banking/transactions": map[string]any{
						"get": map[string]any{
							"summary": "List transactions",
						},
					},
				},
				Components: map[string]any{
					"schemas": map[string]any{
						"Transaction": map[string]any{"type": "object"},
					},
				},
			},
		},
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/api/openapi.json", nil))

	if response.Code != nethttp.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, nethttp.StatusOK, response.Body.String())
	}

	var document map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatalf("openapi response is not JSON: %v", err)
	}
	if document["openapi"] != "3.0.3" {
		t.Fatalf("openapi = %v, want 3.0.3", document["openapi"])
	}

	info := document["info"].(map[string]any)
	if info["version"] != "test-version" {
		t.Fatalf("info.version = %v, want test-version", info["version"])
	}

	paths := document["paths"].(map[string]any)
	healthz, ok := paths["/healthz"].(map[string]any)
	if !ok {
		t.Fatalf("/healthz missing from OpenAPI document: %v", paths)
	}
	healthzGet, ok := healthz["get"].(map[string]any)
	if !ok {
		t.Fatalf("/healthz get operation missing from OpenAPI document: %v", healthz)
	}
	responses := healthzGet["responses"].(map[string]any)
	if _, ok := responses["200"]; !ok {
		t.Fatalf("/healthz 200 response missing from OpenAPI document: %v", responses)
	}
	if _, ok := responses["503"]; !ok {
		t.Fatalf("/healthz 503 response missing from OpenAPI document: %v", responses)
	}

	if _, ok := paths["/api/banking/transactions"]; !ok {
		t.Fatalf("fragment path missing from OpenAPI document: %v", paths)
	}

	components := document["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	if _, ok := schemas["HealthResponse"]; !ok {
		t.Fatalf("HealthResponse schema missing from OpenAPI document: %v", schemas)
	}
}

func TestRequestIDMiddlewareUsesIncomingHeader(t *testing.T) {
	router := NewRouter(Config{
		DB: pingerFunc(func(context.Context) error { return nil }),
		Modules: []Module{
			{
				Name: "test",
				RegisterRoutes: func(r chi.Router) {
					r.Get("/request-id", func(w nethttp.ResponseWriter, r *nethttp.Request) {
						requestID, ok := RequestIDFromContext(r.Context())
						if !ok {
							t.Fatal("request ID missing from context")
						}
						_, _ = w.Write([]byte(requestID))
					})
				},
			},
		},
	})

	request := httptest.NewRequest(nethttp.MethodGet, "/api/test/request-id", nil)
	request.Header.Set("X-Request-ID", "test-request")

	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if got := response.Header().Get("X-Request-ID"); got != "test-request" {
		t.Fatalf("X-Request-ID response header = %q, want test-request", got)
	}
	if got := response.Body.String(); got != "test-request" {
		t.Fatalf("response body = %q, want test-request", got)
	}
}

func TestTimeoutMiddlewareSetsRequestDeadline(t *testing.T) {
	router := NewRouter(Config{
		DB:             pingerFunc(func(context.Context) error { return nil }),
		HandlerTimeout: time.Second,
		Modules: []Module{
			{
				Name: "test",
				RegisterRoutes: func(r chi.Router) {
					r.Get("/deadline", func(w nethttp.ResponseWriter, r *nethttp.Request) {
						if _, ok := r.Context().Deadline(); !ok {
							t.Fatal("request context has no deadline")
						}
						w.WriteHeader(nethttp.StatusNoContent)
					})
				},
			},
		},
		Logger: slog.Default(),
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/api/test/deadline", nil))

	if response.Code != nethttp.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, nethttp.StatusNoContent)
	}
}

type pingerFunc func(context.Context) error

func (f pingerFunc) PingContext(ctx context.Context) error {
	return f(ctx)
}
