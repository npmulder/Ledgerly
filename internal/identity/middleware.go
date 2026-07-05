package identity

import (
	"errors"
	nethttp "net/http"
	"strings"

	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const problemTypeUnauthenticated = "https://ledgerly.local/problems/unauthenticated"

func AuthMiddleware(checker CredentialChecker) func(nethttp.Handler) nethttp.Handler {
	return func(next nethttp.Handler) nethttp.Handler {
		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			if authIsPublicPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			cookie, err := r.Cookie(SessionCookieName)
			if err != nil || strings.TrimSpace(cookie.Value) == "" {
				writeUnauthenticated(w, r)
				return
			}

			result, err := checker.CheckCredential(r.Context(), Credential{
				Kind:  CredentialKindSessionCookie,
				Token: cookie.Value,
			})
			if err != nil {
				if errors.Is(err, ErrUnauthenticated) {
					writeUnauthenticated(w, r)
					return
				}
				httpserver.WriteError(w, r, err)
				return
			}

			nethttp.SetCookie(w, SessionCookie(result.Token, result.ExpiresAt))
			ctx := contextWithPrincipal(r.Context(), result.Principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func authIsPublicPath(path string) bool {
	if !strings.HasPrefix(path, "/api/") {
		return true
	}

	switch path {
	case "/api/identity/register", "/api/identity/login", "/api/openapi.json":
		return true
	default:
		return false
	}
}

func writeUnauthenticated(w nethttp.ResponseWriter, r *nethttp.Request) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeUnauthenticated,
		Title:  nethttp.StatusText(nethttp.StatusUnauthorized),
		Status: nethttp.StatusUnauthorized,
		Detail: "authentication required",
	})
}
