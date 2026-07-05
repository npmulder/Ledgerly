package identity

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/npmulder/ledgerly/internal/platform/clock"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

func TestFirstRunRegisterOnceOnly(t *testing.T) {
	router, store, _ := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})

	first := performJSON(router, nethttp.MethodPost, "/api/identity/register", map[string]string{
		"email":    "OWNER@Example.COM",
		"password": "correct horse battery staple",
		"name":     "Owner",
	}, nil)
	if first.Code != nethttp.StatusCreated {
		t.Fatalf("first register status = %d, want %d; body=%s", first.Code, nethttp.StatusCreated, first.Body.String())
	}

	second := performJSON(router, nethttp.MethodPost, "/api/identity/register", map[string]string{
		"email":    "second@example.com",
		"password": "correct horse battery staple",
		"name":     "Second",
	}, nil)
	if second.Code != nethttp.StatusForbidden {
		t.Fatalf("second register status = %d, want %d; body=%s", second.Code, nethttp.StatusForbidden, second.Body.String())
	}
	if got := second.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
		t.Fatalf("second register Content-Type = %q, want %s", got, httpserver.ProblemContentType)
	}

	user := store.userByEmail("owner@example.com")
	if user.PasswordHash == "" {
		t.Fatal("password hash was not stored")
	}
	if !strings.HasPrefix(user.PasswordHash, "$argon2id$") {
		t.Fatalf("password hash = %q, want argon2id PHC string", user.PasswordHash)
	}
}

func TestWrongPasswordReturnsUnauthorized(t *testing.T) {
	router, _, _ := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	registerOwner(t, router)

	response := performJSON(router, nethttp.MethodPost, "/api/identity/login", map[string]string{
		"email":    "owner@example.com",
		"password": "wrong password",
	}, nil)
	if response.Code != nethttp.StatusUnauthorized {
		t.Fatalf("login status = %d, want %d; body=%s", response.Code, nethttp.StatusUnauthorized, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
		t.Fatalf("Content-Type = %q, want %s", got, httpserver.ProblemContentType)
	}
}

func TestSessionRoundTripViaMe(t *testing.T) {
	router, _, _ := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	registerOwner(t, router)

	cookie := loginOwner(t, router)
	if cookie.Name != SessionCookieName {
		t.Fatalf("cookie name = %q, want %s", cookie.Name, SessionCookieName)
	}
	if !cookie.Secure {
		t.Fatal("cookie Secure = false, want true")
	}
	if !cookie.HttpOnly {
		t.Fatal("cookie HttpOnly = false, want true")
	}
	if cookie.SameSite != nethttp.SameSiteLaxMode {
		t.Fatalf("cookie SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Fatalf("cookie Path = %q, want /", cookie.Path)
	}

	response := performJSON(router, nethttp.MethodGet, "/api/identity/me", nil, cookie)
	if response.Code != nethttp.StatusOK {
		t.Fatalf("me status = %d, want %d; body=%s", response.Code, nethttp.StatusOK, response.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("me response is not JSON: %v", err)
	}
	if body["email"] != "owner@example.com" {
		t.Fatalf("me email = %v, want owner@example.com", body["email"])
	}
	if refreshed := sessionCookieFrom(response); refreshed == nil {
		t.Fatal("me response did not refresh the session cookie")
	}
}

func TestExpiredSessionRejected(t *testing.T) {
	router, _, fakeClock := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	registerOwner(t, router)
	cookie := loginOwner(t, router)

	fakeClock.Advance(31 * 24 * time.Hour)

	response := performJSON(router, nethttp.MethodGet, "/api/identity/me", nil, cookie)
	if response.Code != nethttp.StatusUnauthorized {
		t.Fatalf("expired me status = %d, want %d; body=%s", response.Code, nethttp.StatusUnauthorized, response.Body.String())
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	router, _, _ := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	registerOwner(t, router)
	cookie := loginOwner(t, router)

	logout := performJSON(router, nethttp.MethodPost, "/api/identity/logout", nil, cookie)
	if logout.Code != nethttp.StatusNoContent {
		t.Fatalf("logout status = %d, want %d; body=%s", logout.Code, nethttp.StatusNoContent, logout.Body.String())
	}
	expired := sessionCookieFrom(logout)
	if expired == nil || expired.MaxAge >= 0 {
		t.Fatalf("logout cookie = %#v, want expired session cookie", expired)
	}

	me := performJSON(router, nethttp.MethodGet, "/api/identity/me", nil, cookie)
	if me.Code != nethttp.StatusUnauthorized {
		t.Fatalf("post-logout me status = %d, want %d; body=%s", me.Code, nethttp.StatusUnauthorized, me.Body.String())
	}
}

func TestAuthMiddlewareProtectsAPIRoutes(t *testing.T) {
	router, _, _ := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})

	healthz := performJSON(router, nethttp.MethodGet, "/healthz", nil, nil)
	if healthz.Code != nethttp.StatusOK {
		t.Fatalf("healthz status = %d, want %d; body=%s", healthz.Code, nethttp.StatusOK, healthz.Body.String())
	}

	openAPI := performJSON(router, nethttp.MethodGet, "/api/openapi.json", nil, nil)
	if openAPI.Code != nethttp.StatusOK {
		t.Fatalf("openapi status = %d, want %d; body=%s", openAPI.Code, nethttp.StatusOK, openAPI.Body.String())
	}

	withoutCookie := performJSON(router, nethttp.MethodGet, "/api/protected/ping", nil, nil)
	if withoutCookie.Code != nethttp.StatusUnauthorized {
		t.Fatalf("protected without cookie status = %d, want %d; body=%s", withoutCookie.Code, nethttp.StatusUnauthorized, withoutCookie.Body.String())
	}

	registerOwner(t, router)
	cookie := loginOwner(t, router)

	withCookie := performJSON(router, nethttp.MethodGet, "/api/protected/ping", nil, cookie)
	if withCookie.Code != nethttp.StatusOK {
		t.Fatalf("protected with cookie status = %d, want %d; body=%s", withCookie.Code, nethttp.StatusOK, withCookie.Body.String())
	}
}

func TestLoginAttemptsAreRateLimitedPerIP(t *testing.T) {
	router, _, _ := newTestRouter(t, LoginRateLimit{Capacity: 1, RefillEvery: time.Hour})

	first := performJSON(router, nethttp.MethodPost, "/api/identity/login", map[string]string{
		"email":    "missing@example.com",
		"password": "wrong password",
	}, nil)
	if first.Code != nethttp.StatusUnauthorized {
		t.Fatalf("first login status = %d, want %d; body=%s", first.Code, nethttp.StatusUnauthorized, first.Body.String())
	}

	second := performJSON(router, nethttp.MethodPost, "/api/identity/login", map[string]string{
		"email":    "missing@example.com",
		"password": "wrong password",
	}, nil)
	if second.Code != nethttp.StatusTooManyRequests {
		t.Fatalf("second login status = %d, want %d; body=%s", second.Code, nethttp.StatusTooManyRequests, second.Body.String())
	}
}

func TestClosedRegistrationDoesNotHashPassword(t *testing.T) {
	store := newMemoryStore()
	if _, err := store.CreateFirstUser(context.Background(), "owner@example.com", "hash", "Owner"); err != nil {
		t.Fatalf("CreateFirstUser() error = %v", err)
	}

	hashErr := errors.New("hash should not run")
	service := NewService(store, clock.NewFake(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)), WithTokenReader(errorReader{err: hashErr}))

	_, err := service.Register(context.Background(), RegisterInput{
		Email:    "second@example.com",
		Password: "correct horse battery staple",
		Name:     "Second",
	})
	if !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("Register() error = %v, want %v", err, ErrRegistrationClosed)
	}
}

func TestRegisterRejectsOversizedJSONBody(t *testing.T) {
	router, _, _ := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	body := `{"email":"owner@example.com","password":"` + strings.Repeat("x", maxJSONBodyBytes) + `","name":"Owner"}`

	response := performRaw(router, nethttp.MethodPost, "/api/identity/register", body, nil)
	if response.Code != nethttp.StatusRequestEntityTooLarge {
		t.Fatalf("oversized register status = %d, want %d; body=%s", response.Code, nethttp.StatusRequestEntityTooLarge, response.Body.String())
	}
}

func TestClientIPIgnoresForwardedFor(t *testing.T) {
	request := httptest.NewRequest(nethttp.MethodPost, "/api/identity/login", nil)
	request.RemoteAddr = "203.0.113.10:58124"
	request.Header.Set("X-Forwarded-For", "198.51.100.4")

	if got := clientIP(request); got != "203.0.113.10" {
		t.Fatalf("clientIP() = %q, want remote address", got)
	}
}

func TestLoginPrunesExpiredSessions(t *testing.T) {
	router, store, fakeClock := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	registerOwner(t, router)
	store.insertSessionForTest("expired", 1, fakeClock.Now().Add(-time.Minute))

	loginOwner(t, router)

	if store.hasSession("expired") {
		t.Fatal("expired session was not pruned during login")
	}
}

func TestOpenAPIIncludesIdentityRequestBodies(t *testing.T) {
	document := httpserver.OpenAPIDocument("test-version", OpenAPIFragment())
	paths := document["paths"].(map[string]any)

	login := paths["/api/identity/login"].(map[string]any)["post"].(map[string]any)
	if login["requestBody"] == nil {
		t.Fatal("login requestBody missing from OpenAPI fragment")
	}

	register := paths["/api/identity/register"].(map[string]any)["post"].(map[string]any)
	if register["requestBody"] == nil {
		t.Fatal("register requestBody missing from OpenAPI fragment")
	}
}

func newTestRouter(t *testing.T, limit LoginRateLimit) (nethttp.Handler, *memoryStore, *clock.FakeClock) {
	t.Helper()

	store := newMemoryStore()
	fakeClock := clock.NewFake(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	service := NewService(store, fakeClock, WithPasswordParams(PasswordParams{
		MemoryKiB: 64,
		Time:      1,
		Threads:   1,
		SaltLen:   8,
		KeyLen:    16,
	}))
	handler := NewHTTPHandler(service, WithLoginRateLimiter(NewLoginRateLimiter(limit)))

	router := httpserver.NewRouter(httpserver.Config{
		Version: "test-version",
		DB:      pingerFunc(func(context.Context) error { return nil }),
		APIAuth: AuthMiddleware(service),
		Modules: []httpserver.Module{
			HTTPModule(handler),
			{
				Name: "protected",
				RegisterRoutes: func(r chi.Router) {
					r.Get("/ping", func(w nethttp.ResponseWriter, r *nethttp.Request) {
						if _, ok := PrincipalFromContext(r.Context()); !ok {
							t.Fatal("principal missing from protected route context")
						}
						w.WriteHeader(nethttp.StatusOK)
					})
				},
			},
		},
		OpenAPIFragments: []httpserver.OpenAPIFragment{OpenAPIFragment()},
	})
	return router, store, fakeClock
}

func registerOwner(t *testing.T, router nethttp.Handler) {
	t.Helper()

	response := performJSON(router, nethttp.MethodPost, "/api/identity/register", map[string]string{
		"email":    "owner@example.com",
		"password": "correct horse battery staple",
		"name":     "Owner",
	}, nil)
	if response.Code != nethttp.StatusCreated {
		t.Fatalf("register status = %d, want %d; body=%s", response.Code, nethttp.StatusCreated, response.Body.String())
	}
}

func loginOwner(t *testing.T, router nethttp.Handler) *nethttp.Cookie {
	t.Helper()

	response := performJSON(router, nethttp.MethodPost, "/api/identity/login", map[string]string{
		"email":    "owner@example.com",
		"password": "correct horse battery staple",
	}, nil)
	if response.Code != nethttp.StatusOK {
		t.Fatalf("login status = %d, want %d; body=%s", response.Code, nethttp.StatusOK, response.Body.String())
	}

	cookie := sessionCookieFrom(response)
	if cookie == nil {
		t.Fatal("login response did not set a session cookie")
	}
	if cookie.Value == "" {
		t.Fatal("login session cookie is empty")
	}
	return cookie
}

func performJSON(router nethttp.Handler, method, path string, payload any, cookie *nethttp.Cookie) *httptest.ResponseRecorder {
	var body bytes.Buffer
	if payload != nil {
		_ = json.NewEncoder(&body).Encode(payload)
	}
	return performRequest(router, method, path, &body, cookie)
}

func performRaw(router nethttp.Handler, method, path, body string, cookie *nethttp.Cookie) *httptest.ResponseRecorder {
	return performRequest(router, method, path, strings.NewReader(body), cookie)
}

func performRequest(router nethttp.Handler, method, path string, body io.Reader, cookie *nethttp.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "203.0.113.10:58124"
	if cookie != nil {
		request.AddCookie(cookie)
	}

	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func sessionCookieFrom(response *httptest.ResponseRecorder) *nethttp.Cookie {
	var found *nethttp.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == SessionCookieName {
			found = cookie
		}
	}
	return found
}

type memoryStore struct {
	mu       sync.Mutex
	nextID   int64
	users    map[string]storedUser
	sessions map[string]memorySession
}

type memorySession struct {
	userID    int64
	expiresAt time.Time
	createdAt time.Time
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		nextID:   1,
		users:    make(map[string]storedUser),
		sessions: make(map[string]memorySession),
	}
}

func (s *memoryStore) UsersExist(_ context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.users) > 0, nil
}

func (s *memoryStore) CreateFirstUser(_ context.Context, email, passwordHash, name string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.users) > 0 {
		return User{}, ErrRegistrationClosed
	}

	user := storedUser{
		User: User{
			ID:        s.nextID,
			Email:     email,
			Name:      name,
			CreatedAt: time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC),
		},
		PasswordHash: passwordHash,
	}
	s.nextID++
	s.users[email] = user
	return user.User, nil
}

func (s *memoryStore) FindUserByEmail(_ context.Context, email string) (storedUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[email]
	if !ok {
		return storedUser{}, ErrUserNotFound
	}
	return user, nil
}

func (s *memoryStore) CreateSession(_ context.Context, userID int64, tokenHash []byte, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[hashKey(tokenHash)] = memorySession{
		userID:    userID,
		expiresAt: expiresAt,
		createdAt: time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC),
	}
	return nil
}

func (s *memoryStore) FindSessionByTokenHash(_ context.Context, tokenHash []byte) (storedSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[hashKey(tokenHash)]
	if !ok {
		return storedSession{}, ErrUnauthenticated
	}
	for _, user := range s.users {
		if user.ID == session.userID {
			return storedSession{
				User:      user.User,
				ExpiresAt: session.expiresAt,
				CreatedAt: session.createdAt,
			}, nil
		}
	}
	return storedSession{}, ErrUnauthenticated
}

func (s *memoryStore) RefreshSession(_ context.Context, tokenHash []byte, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := hashKey(tokenHash)
	session, ok := s.sessions[key]
	if !ok {
		return ErrUnauthenticated
	}
	session.expiresAt = expiresAt
	s.sessions[key] = session
	return nil
}

func (s *memoryStore) DeleteSession(_ context.Context, tokenHash []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, hashKey(tokenHash))
	return nil
}

func (s *memoryStore) DeleteExpiredSessions(_ context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, session := range s.sessions {
		if !session.expiresAt.After(now) {
			delete(s.sessions, key)
		}
	}
	return nil
}

func (s *memoryStore) userByEmail(email string) storedUser {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.users[email]
}

func (s *memoryStore) insertSessionForTest(key string, userID int64, expiresAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[key] = memorySession{
		userID:    userID,
		expiresAt: expiresAt,
		createdAt: expiresAt.Add(-time.Hour),
	}
}

func (s *memoryStore) hasSession(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.sessions[key]
	return ok
}

func hashKey(hash []byte) string {
	return hex.EncodeToString(hash)
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

type pingerFunc func(context.Context) error

func (f pingerFunc) PingContext(ctx context.Context) error {
	if f == nil {
		return errors.New("database is not configured")
	}
	return f(ctx)
}
