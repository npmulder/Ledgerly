package identity

import (
	"errors"
	nethttp "net/http"
	"strings"

	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const problemTypeUnauthenticated = "https://ledgerly.local/problems/unauthenticated"
const problemTypeForbidden = "https://ledgerly.local/problems/forbidden"

func AuthMiddleware(checker CredentialChecker) func(nethttp.Handler) nethttp.Handler {
	return func(next nethttp.Handler) nethttp.Handler {
		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			if authIsPublicPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			credential, ok := credentialFromRequest(r)
			if !ok {
				writeUnauthenticated(w, r)
				return
			}

			result, err := checker.CheckCredential(r.Context(), credential)
			if err != nil {
				if errors.Is(err, ErrUnauthenticated) {
					writeUnauthenticated(w, r)
					return
				}
				httpserver.WriteError(w, r, err)
				return
			}

			if result.SetCookie {
				nethttp.SetCookie(w, SessionCookie(result.Token, result.ExpiresAt))
			}
			if result.Principal.PAT != nil && result.Principal.PAT.Scope == PATScopeReadOnly && r.Method != nethttp.MethodGet {
				writeReadOnlyForbidden(w, r)
				return
			}
			ctx := contextWithPrincipal(r.Context(), result.Principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func credentialFromRequest(r *nethttp.Request) (Credential, bool) {
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); auth != "" {
		scheme, token, ok := strings.Cut(auth, " ")
		if ok && strings.EqualFold(scheme, "Bearer") && strings.HasPrefix(strings.TrimSpace(token), patTokenPrefix) {
			return Credential{Kind: CredentialKindPAT, Token: strings.TrimSpace(token)}, true
		}
	}

	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return Credential{}, false
	}
	return Credential{Kind: CredentialKindSessionCookie, Token: cookie.Value}, true
}

func authIsPublicPath(path string) bool {
	if !strings.HasPrefix(path, "/api/") {
		return true
	}

	switch path {
	case "/api/identity/register", "/api/identity/register-with-profile", "/api/identity/login", "/api/openapi.json":
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

func writeReadOnlyForbidden(w nethttp.ResponseWriter, r *nethttp.Request) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeForbidden,
		Title:  nethttp.StatusText(nethttp.StatusForbidden),
		Status: nethttp.StatusForbidden,
		Detail: "read-only personal access tokens cannot modify resources",
	})
}
