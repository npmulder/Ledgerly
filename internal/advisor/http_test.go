package advisor

import (
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/npmulder/ledgerly/internal/identity"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

func TestAdvisorRoutesRequireAuthentication(t *testing.T) {
	router := httpserver.NewRouter(httpserver.Config{
		APIAuth: identity.AuthMiddleware(unauthAdvisorCredentialChecker{}),
		Modules: []httpserver.Module{
			NewHTTPModule(nil).HTTPModule(),
		},
	})

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: nethttp.MethodGet, path: "/api/advisor/insights?surface=dashboard"},
		{method: nethttp.MethodPost, path: "/api/advisor/insights/test-key/dismiss"},
		{method: nethttp.MethodPost, path: "/api/advisor/refresh"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, tc.path, nil)

			router.ServeHTTP(response, request)

			if response.Code != nethttp.StatusUnauthorized {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, nethttp.StatusUnauthorized, response.Body.String())
			}
		})
	}
}

type unauthAdvisorCredentialChecker struct{}

func (unauthAdvisorCredentialChecker) CheckCredential(context.Context, identity.Credential) (identity.CredentialCheckResult, error) {
	return identity.CredentialCheckResult{}, identity.ErrUnauthenticated
}
