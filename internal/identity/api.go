package identity

import (
	"context"
	"errors"
	"time"
)

const (
	// SessionCookieName uses the __Host- prefix so browsers require Secure,
	// Path=/, and no Domain attribute.
	SessionCookieName = "__Host-ledgerly_session"

	sessionDuration = 30 * 24 * time.Hour
)

var (
	ErrRegistrationClosed = errors.New("registration is closed")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthenticated    = errors.New("unauthenticated")
	ErrUserNotFound       = errors.New("user not found")
)

// User is the authenticated account shape shared across identity HTTP and
// module boundaries.
type User struct {
	ID        int64
	Email     string
	Name      string
	CreatedAt time.Time
}

// RegisterInput creates the first local owner account.
type RegisterInput struct {
	Email    string
	Password string
	Name     string
}

// LoginInput verifies a password and opens a session.
type LoginInput struct {
	Email    string
	Password string
}

// LoginResult is returned after a successful password login.
type LoginResult struct {
	User      User
	Token     string
	ExpiresAt time.Time
}

type CredentialKind string

const CredentialKindSessionCookie CredentialKind = "session_cookie"

// Credential is the parsed authentication material supplied by middleware.
type Credential struct {
	Kind  CredentialKind
	Token string
}

// Principal is the authenticated caller attached to protected request contexts.
type Principal struct {
	User      User
	ExpiresAt time.Time
}

// CredentialCheckResult lets middleware refresh browser credentials after a
// successful check.
type CredentialCheckResult struct {
	Principal Principal
	Token     string
	ExpiresAt time.Time
}

// CredentialChecker is intentionally the only auth dependency used by the
// middleware. CV-242 PAT support should add another credential parser/checker
// behind this interface and reuse the same protected-route middleware.
type CredentialChecker interface {
	CheckCredential(ctx context.Context, credential Credential) (CredentialCheckResult, error)
}

type principalContextKey struct{}

func contextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// PrincipalFromContext returns the authenticated caller attached by middleware.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}
