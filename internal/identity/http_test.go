package identity

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	nethttp "net/http"
	"net/http/httptest"
	"net/textproto"
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

func TestFirstRunRegisterWithProfileCreatesSessionAndProfile(t *testing.T) {
	router, store, _ := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})

	response := performJSON(router, nethttp.MethodPost, "/api/identity/register-with-profile", firstRunProfilePayload(), nil)
	if response.Code != nethttp.StatusCreated {
		t.Fatalf("register-with-profile status = %d, want %d; body=%s", response.Code, nethttp.StatusCreated, response.Body.String())
	}
	cookie := sessionCookieFrom(response)
	if cookie == nil || cookie.Value == "" {
		t.Fatalf("register-with-profile session cookie = %#v, want non-empty %s", cookie, SessionCookieName)
	}

	var body registerWithProfileResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode register-with-profile response: %v; body=%s", err, response.Body.String())
	}
	if body.User.Email != "owner@example.com" || body.User.Name != "Owner" {
		t.Fatalf("response user = %+v, want owner@example.com Owner", body.User)
	}
	if body.Profile.TradingName != "Acme Trading" || body.Profile.LegalName != "Acme Limited" {
		t.Fatalf("response profile = %+v, want Acme names", body.Profile)
	}
	if body.Profile.YearEnd.Month != 12 || body.Profile.YearEnd.Day != 31 {
		t.Fatalf("response year_end = %+v, want 31 December", body.Profile.YearEnd)
	}
	if body.Profile.Directors == nil || len(body.Profile.Directors) != 0 {
		t.Fatalf("response directors = %+v, want empty slice", body.Profile.Directors)
	}

	user := store.userByEmail("owner@example.com")
	if user.PasswordHash == "" || !strings.HasPrefix(user.PasswordHash, "$argon2id$") {
		t.Fatalf("stored password hash = %q, want argon2id hash", user.PasswordHash)
	}
	profile := store.profileForTest()
	if profile == nil || profile.TradingName != "Acme Trading" || profile.CompanyNumber != "ACME123" {
		t.Fatalf("stored profile = %+v, want Acme profile", profile)
	}

	me := performJSON(router, nethttp.MethodGet, "/api/identity/me", nil, cookie)
	if me.Code != nethttp.StatusOK {
		t.Fatalf("me after register-with-profile status = %d, want %d; body=%s", me.Code, nethttp.StatusOK, me.Body.String())
	}
}

func TestFirstRunRegisterWithProfileCapturesDirectors(t *testing.T) {
	router, store, _ := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	payload := firstRunProfilePayload()
	payload["directors"] = []map[string]any{
		{
			"name":           "N. Meyer",
			"appointed_date": "2020-07-14",
			"is_chair":       true,
		},
		{
			"name": "A. Patel",
		},
	}

	response := performJSON(router, nethttp.MethodPost, "/api/identity/register-with-profile", payload, nil)
	if response.Code != nethttp.StatusCreated {
		t.Fatalf("register-with-profile status = %d, want %d; body=%s", response.Code, nethttp.StatusCreated, response.Body.String())
	}

	var body registerWithProfileResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode register-with-profile response: %v; body=%s", err, response.Body.String())
	}
	if len(body.Profile.Directors) != 2 || body.Profile.Directors[0].Name != "N. Meyer" || body.Profile.Directors[0].AppointedDate == nil || *body.Profile.Directors[0].AppointedDate != "2020-07-14" || !body.Profile.Directors[0].IsChair || body.Profile.Directors[1].Name != "A. Patel" {
		t.Fatalf("response directors = %+v, want two directors", body.Profile.Directors)
	}
	profile := store.profileForTest()
	if profile == nil || len(profile.Directors) != 2 || profile.Directors[1].Name != "A. Patel" {
		t.Fatalf("stored directors = %+v, want two directors", profile)
	}
}

func TestRegisterWithProfileClosesWhenUserOrProfileExists(t *testing.T) {
	t.Run("existing user", func(t *testing.T) {
		router, _, _ := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
		registerOwner(t, router)

		response := performJSON(router, nethttp.MethodPost, "/api/identity/register-with-profile", firstRunProfilePayload(), nil)
		if response.Code != nethttp.StatusForbidden {
			t.Fatalf("register-with-profile with user status = %d, want %d; body=%s", response.Code, nethttp.StatusForbidden, response.Body.String())
		}
		if got := response.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
			t.Fatalf("Content-Type = %q, want %s", got, httpserver.ProblemContentType)
		}
	})

	t.Run("existing profile", func(t *testing.T) {
		router, store, _ := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
		store.setProfileForTest(npmProfile())

		response := performJSON(router, nethttp.MethodPost, "/api/identity/register-with-profile", firstRunProfilePayload(), nil)
		if response.Code != nethttp.StatusForbidden {
			t.Fatalf("register-with-profile with profile status = %d, want %d; body=%s", response.Code, nethttp.StatusForbidden, response.Body.String())
		}
		if got := response.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
			t.Fatalf("Content-Type = %q, want %s", got, httpserver.ProblemContentType)
		}
	})
}

func TestRegisterWithProfileValidationErrorsUseFieldPointers(t *testing.T) {
	router, _, _ := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	payload := firstRunProfilePayload()
	payload["email"] = "not-an-email"
	payload["trading_name"] = " "
	payload["year_end_month"] = 2
	payload["year_end_day"] = 30

	response := performJSON(router, nethttp.MethodPost, "/api/identity/register-with-profile", payload, nil)
	if response.Code != nethttp.StatusBadRequest {
		t.Fatalf("invalid register-with-profile status = %d, want %d; body=%s", response.Code, nethttp.StatusBadRequest, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
		t.Fatalf("Content-Type = %q, want %s", got, httpserver.ProblemContentType)
	}

	problem := decodeValidationProblem(t, response)
	if problem.Type != problemTypeValidation {
		t.Fatalf("problem type = %q, want %s", problem.Type, problemTypeValidation)
	}
	if problem.Status != nethttp.StatusBadRequest {
		t.Fatalf("problem status = %d, want %d", problem.Status, nethttp.StatusBadRequest)
	}
	wantPointers := map[string]bool{
		"/email":        false,
		"/trading_name": false,
		"/year_end_day": false,
	}
	for _, fieldErr := range problem.Errors {
		if _, ok := wantPointers[fieldErr.Pointer]; ok {
			wantPointers[fieldErr.Pointer] = true
		}
	}
	for pointer, found := range wantPointers {
		if !found {
			t.Fatalf("problem errors = %+v, missing pointer %s", problem.Errors, pointer)
		}
	}
}

func TestRegisterWithProfileDirectorValidationUsesDirectorsPointer(t *testing.T) {
	router, _, _ := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	payload := firstRunProfilePayload()
	payload["directors"] = []map[string]any{
		{"name": " "},
	}

	response := performJSON(router, nethttp.MethodPost, "/api/identity/register-with-profile", payload, nil)
	if response.Code != nethttp.StatusBadRequest {
		t.Fatalf("invalid register-with-profile directors status = %d, want %d; body=%s", response.Code, nethttp.StatusBadRequest, response.Body.String())
	}
	problem := decodeValidationProblem(t, response)
	if len(problem.Errors) != 1 || problem.Errors[0].Pointer != "/directors" {
		t.Fatalf("problem errors = %+v, want pointer /directors", problem.Errors)
	}
}

func TestRegisterWithProfileRequiresRegisteredOfficeFields(t *testing.T) {
	router, _, _ := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	tests := []struct {
		name    string
		office  any
		wantPtr []string
	}{
		{
			name:   "empty object",
			office: map[string]any{},
			wantPtr: []string{
				"/registered_office/line1",
				"/registered_office/line2",
				"/registered_office/locality",
				"/registered_office/region",
				"/registered_office/postal_code",
				"/registered_office/country",
			},
		},
		{
			name: "missing line and country",
			office: map[string]any{
				"line2":       "",
				"locality":    "Douglas",
				"region":      "",
				"postal_code": "",
			},
			wantPtr: []string{
				"/registered_office/line1",
				"/registered_office/country",
			},
		},
		{
			name: "field type mismatch",
			office: map[string]any{
				"line1":       123,
				"line2":       "",
				"locality":    "Douglas",
				"region":      "",
				"postal_code": "",
				"country":     "IM",
			},
			wantPtr: []string{"/registered_office/line1"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := firstRunProfilePayload()
			payload["registered_office"] = test.office

			response := performJSON(router, nethttp.MethodPost, "/api/identity/register-with-profile", payload, nil)
			if response.Code != nethttp.StatusBadRequest {
				t.Fatalf("register-with-profile status = %d, want %d; body=%s", response.Code, nethttp.StatusBadRequest, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
				t.Fatalf("Content-Type = %q, want %s", got, httpserver.ProblemContentType)
			}

			problem := decodeValidationProblem(t, response)
			got := make(map[string]bool, len(problem.Errors))
			for _, fieldErr := range problem.Errors {
				got[fieldErr.Pointer] = true
			}
			for _, pointer := range test.wantPtr {
				if !got[pointer] {
					t.Fatalf("problem errors = %+v, missing pointer %s", problem.Errors, pointer)
				}
			}
		})
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

func TestPATMintListUseAndRevokeRoundTrip(t *testing.T) {
	router, _, fakeClock := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	registerOwner(t, router)
	cookie := loginOwner(t, router)

	create := performJSON(router, nethttp.MethodPost, "/api/identity/pats", map[string]any{
		"name":       "Read API",
		"scope":      "read-only",
		"expires_at": fakeClock.Now().Add(24 * time.Hour).Format(time.RFC3339),
	}, cookie)
	if create.Code != nethttp.StatusCreated {
		t.Fatalf("create PAT status = %d, want %d; body=%s", create.Code, nethttp.StatusCreated, create.Body.String())
	}
	var createBody createPATResponse
	if err := json.Unmarshal(create.Body.Bytes(), &createBody); err != nil {
		t.Fatalf("decode create PAT response: %v; body=%s", err, create.Body.String())
	}
	if !strings.HasPrefix(createBody.Token, patTokenPrefix) {
		t.Fatalf("PAT token = %q, want %s prefix", createBody.Token, patTokenPrefix)
	}
	if createBody.PersonalAccessToken.Name != "Read API" || createBody.PersonalAccessToken.Scope != PATScopeReadOnly {
		t.Fatalf("created PAT metadata = %+v, want read-only Read API", createBody.PersonalAccessToken)
	}

	me := performBearerJSON(router, nethttp.MethodGet, "/api/identity/me", nil, createBody.Token)
	if me.Code != nethttp.StatusOK {
		t.Fatalf("PAT me status = %d, want %d; body=%s", me.Code, nethttp.StatusOK, me.Body.String())
	}
	if refreshed := sessionCookieFrom(me); refreshed != nil {
		t.Fatalf("PAT me set session cookie %#v, want none", refreshed)
	}
	var meBody userResponse
	if err := json.Unmarshal(me.Body.Bytes(), &meBody); err != nil {
		t.Fatalf("decode PAT me response: %v; body=%s", err, me.Body.String())
	}
	if meBody.TokenName == nil || *meBody.TokenName != "Read API" {
		t.Fatalf("PAT me token_name = %v, want Read API", meBody.TokenName)
	}
	if meBody.TokenScope == nil || *meBody.TokenScope != PATScopeReadOnly {
		t.Fatalf("PAT me token_scope = %v, want read-only", meBody.TokenScope)
	}

	list := performJSON(router, nethttp.MethodGet, "/api/identity/pats", nil, cookie)
	if list.Code != nethttp.StatusOK {
		t.Fatalf("list PAT status = %d, want %d; body=%s", list.Code, nethttp.StatusOK, list.Body.String())
	}
	var listBody listPATsResponse
	if err := json.Unmarshal(list.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list PAT response: %v; body=%s", err, list.Body.String())
	}
	if len(listBody.Tokens) != 1 {
		t.Fatalf("listed PATs = %+v, want one token", listBody.Tokens)
	}
	if listBody.Tokens[0].LastUsedAt == nil {
		t.Fatalf("listed PAT last_used_at = nil, want timestamp after bearer use")
	}

	post := performBearerJSON(router, nethttp.MethodPost, "/api/identity/pats", map[string]any{
		"name":  "blocked",
		"scope": "full",
	}, createBody.Token)
	if post.Code != nethttp.StatusForbidden {
		t.Fatalf("read-only POST status = %d, want %d; body=%s", post.Code, nethttp.StatusForbidden, post.Body.String())
	}
	if got := post.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
		t.Fatalf("read-only POST Content-Type = %q, want %s", got, httpserver.ProblemContentType)
	}

	revoke := performJSON(router, nethttp.MethodDelete, fmt.Sprintf("/api/identity/pats/%d", createBody.PersonalAccessToken.ID), nil, cookie)
	if revoke.Code != nethttp.StatusNoContent {
		t.Fatalf("revoke PAT status = %d, want %d; body=%s", revoke.Code, nethttp.StatusNoContent, revoke.Body.String())
	}
	afterRevoke := performBearerJSON(router, nethttp.MethodGet, "/api/identity/me", nil, createBody.Token)
	if afterRevoke.Code != nethttp.StatusUnauthorized {
		t.Fatalf("revoked PAT status = %d, want %d; body=%s", afterRevoke.Code, nethttp.StatusUnauthorized, afterRevoke.Body.String())
	}
}

func TestExpiredPATRejected(t *testing.T) {
	router, _, fakeClock := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	registerOwner(t, router)
	cookie := loginOwner(t, router)

	create := performJSON(router, nethttp.MethodPost, "/api/identity/pats", map[string]any{
		"name":       "Temporary API",
		"scope":      "full",
		"expires_at": fakeClock.Now().Add(time.Hour).Format(time.RFC3339),
	}, cookie)
	if create.Code != nethttp.StatusCreated {
		t.Fatalf("create PAT status = %d, want %d; body=%s", create.Code, nethttp.StatusCreated, create.Body.String())
	}
	var createBody createPATResponse
	if err := json.Unmarshal(create.Body.Bytes(), &createBody); err != nil {
		t.Fatalf("decode create PAT response: %v; body=%s", err, create.Body.String())
	}

	fakeClock.Advance(2 * time.Hour)
	response := performBearerJSON(router, nethttp.MethodGet, "/api/identity/me", nil, createBody.Token)
	if response.Code != nethttp.StatusUnauthorized {
		t.Fatalf("expired PAT status = %d, want %d; body=%s", response.Code, nethttp.StatusUnauthorized, response.Body.String())
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

func TestIdentityProfileRoutesRequireAuthentication(t *testing.T) {
	router, _, _ := newTestRouter(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{name: "profile get", method: nethttp.MethodGet, path: "/api/identity/profile"},
		{name: "profile patch", method: nethttp.MethodPatch, path: "/api/identity/profile", body: map[string]string{"trading_name": "NPM Trading"}},
		{name: "logo put", method: nethttp.MethodPut, path: "/api/identity/logo"},
		{name: "asset get", method: nethttp.MethodGet, path: "/api/identity/assets/" + string(testSeedLogoAssetID)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := performJSON(router, tc.method, tc.path, tc.body, nil)
			if response.Code != nethttp.StatusUnauthorized {
				t.Fatalf("%s %s status = %d, want %d; body=%s", tc.method, tc.path, response.Code, nethttp.StatusUnauthorized, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
				t.Fatalf("Content-Type = %q, want %s", got, httpserver.ProblemContentType)
			}
		})
	}
}

func TestProfileGetPatchRoundTrip(t *testing.T) {
	router, _, _, _ := newTestRouterWithProfile(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	registerOwner(t, router)
	cookie := loginOwner(t, router)

	initial := performJSON(router, nethttp.MethodGet, "/api/identity/profile", nil, cookie)
	if initial.Code != nethttp.StatusOK {
		t.Fatalf("initial profile status = %d, want %d; body=%s", initial.Code, nethttp.StatusOK, initial.Body.String())
	}
	initialProfile := decodeProfileResponse(t, initial)
	if initialProfile.TradingName != "NPM Limited" {
		t.Fatalf("initial trading_name = %q, want NPM Limited", initialProfile.TradingName)
	}
	if initialProfile.IsVATRegistered {
		t.Fatal("initial is_vat_registered = true, want false")
	}
	if initialProfile.LogoAssetURL == nil || *initialProfile.LogoAssetURL != assetURL(testSeedLogoAssetID) {
		t.Fatalf("initial logo_asset_url = %v, want %s", initialProfile.LogoAssetURL, assetURL(testSeedLogoAssetID))
	}

	patch := performJSON(router, nethttp.MethodPatch, "/api/identity/profile", map[string]any{
		"trading_name":      "NPM Trading",
		"is_vat_registered": true,
		"vat_number":        "IM1234567",
		"directors": []map[string]any{
			{
				"name":           "N. Meyer",
				"appointed_date": "2020-07-14",
				"is_chair":       true,
			},
			{
				"name": "A. Patel",
			},
		},
		"year_end": map[string]int{
			"month": 12,
			"day":   31,
		},
	}, cookie)
	if patch.Code != nethttp.StatusOK {
		t.Fatalf("patch profile status = %d, want %d; body=%s", patch.Code, nethttp.StatusOK, patch.Body.String())
	}
	patchedProfile := decodeProfileResponse(t, patch)
	if patchedProfile.TradingName != "NPM Trading" {
		t.Fatalf("patched trading_name = %q, want NPM Trading", patchedProfile.TradingName)
	}
	if patchedProfile.VATNumber == nil || *patchedProfile.VATNumber != "IM1234567" {
		t.Fatalf("patched vat_number = %v, want IM1234567", patchedProfile.VATNumber)
	}
	if !patchedProfile.IsVATRegistered {
		t.Fatal("patched is_vat_registered = false, want true")
	}
	if patchedProfile.YearEnd.Month != 12 || patchedProfile.YearEnd.Day != 31 {
		t.Fatalf("patched year_end = %+v, want month=12 day=31", patchedProfile.YearEnd)
	}
	if len(patchedProfile.Directors) != 2 || patchedProfile.Directors[0].Name != "N. Meyer" || patchedProfile.Directors[0].AppointedDate == nil || *patchedProfile.Directors[0].AppointedDate != "2020-07-14" || !patchedProfile.Directors[0].IsChair || patchedProfile.Directors[1].Name != "A. Patel" {
		t.Fatalf("patched directors = %+v, want two directors", patchedProfile.Directors)
	}

	roundTrip := performJSON(router, nethttp.MethodGet, "/api/identity/profile", nil, cookie)
	if roundTrip.Code != nethttp.StatusOK {
		t.Fatalf("round-trip profile status = %d, want %d; body=%s", roundTrip.Code, nethttp.StatusOK, roundTrip.Body.String())
	}
	roundTripProfile := decodeProfileResponse(t, roundTrip)
	if roundTripProfile.TradingName != "NPM Trading" {
		t.Fatalf("round-trip trading_name = %q, want NPM Trading", roundTripProfile.TradingName)
	}
	if !roundTripProfile.IsVATRegistered {
		t.Fatal("round-trip is_vat_registered = false, want true")
	}
	if len(roundTripProfile.Directors) != 2 || roundTripProfile.Directors[1].Name != "A. Patel" {
		t.Fatalf("round-trip directors = %+v, want two directors", roundTripProfile.Directors)
	}
}

func TestLogoUploadUpdatesProfileAndAssetIsImmutable(t *testing.T) {
	router, _, _, _ := newTestRouterWithProfile(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	registerOwner(t, router)
	cookie := loginOwner(t, router)

	logoBytes := testPNG(t)
	upload := performMultipartLogo(router, "/api/identity/logo", "logo.png", "image/png", logoBytes, cookie)
	if upload.Code != nethttp.StatusOK {
		t.Fatalf("logo upload status = %d, want %d; body=%s", upload.Code, nethttp.StatusOK, upload.Body.String())
	}
	var uploadBody logoResponse
	if err := json.Unmarshal(upload.Body.Bytes(), &uploadBody); err != nil {
		t.Fatalf("decode logo upload response: %v; body=%s", err, upload.Body.String())
	}
	if uploadBody.AssetURL == "" {
		t.Fatalf("logo upload response missing asset_url: %+v", uploadBody)
	}

	profile := performJSON(router, nethttp.MethodGet, "/api/identity/profile", nil, cookie)
	if profile.Code != nethttp.StatusOK {
		t.Fatalf("profile after logo upload status = %d, want %d; body=%s", profile.Code, nethttp.StatusOK, profile.Body.String())
	}
	profileBody := decodeProfileResponse(t, profile)
	if profileBody.LogoAssetURL == nil || *profileBody.LogoAssetURL != uploadBody.AssetURL {
		t.Fatalf("profile logo_asset_url = %v, want %s", profileBody.LogoAssetURL, uploadBody.AssetURL)
	}

	asset := performJSON(router, nethttp.MethodGet, uploadBody.AssetURL, nil, cookie)
	if asset.Code != nethttp.StatusOK {
		t.Fatalf("asset status = %d, want %d; body=%s", asset.Code, nethttp.StatusOK, asset.Body.String())
	}
	if got := asset.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("asset Content-Type = %q, want image/png", got)
	}
	if got := asset.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
		t.Fatalf("asset Cache-Control = %q, want private immutable directive", got)
	}
	if !bytes.Equal(asset.Body.Bytes(), logoBytes) {
		t.Fatal("asset bytes do not match uploaded PNG")
	}
}

func TestInvalidProfilePatchReturnsFieldPointers(t *testing.T) {
	router, _, _, _ := newTestRouterWithProfile(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	registerOwner(t, router)
	cookie := loginOwner(t, router)

	response := performJSON(router, nethttp.MethodPatch, "/api/identity/profile", map[string]string{
		"trading_name": " ",
	}, cookie)
	if response.Code != nethttp.StatusUnprocessableEntity {
		t.Fatalf("invalid patch status = %d, want %d; body=%s", response.Code, nethttp.StatusUnprocessableEntity, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
		t.Fatalf("Content-Type = %q, want %s", got, httpserver.ProblemContentType)
	}

	var problem struct {
		Type   string       `json:"type"`
		Status int          `json:"status"`
		Errors []fieldError `json:"errors"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode validation problem: %v; body=%s", err, response.Body.String())
	}
	if problem.Type != problemTypeValidation {
		t.Fatalf("problem type = %q, want %s", problem.Type, problemTypeValidation)
	}
	if problem.Status != nethttp.StatusUnprocessableEntity {
		t.Fatalf("problem status = %d, want %d", problem.Status, nethttp.StatusUnprocessableEntity)
	}
	if len(problem.Errors) != 1 || problem.Errors[0].Pointer != "/trading_name" {
		t.Fatalf("problem errors = %+v, want pointer /trading_name", problem.Errors)
	}
}

func TestProfilePatchDirectorValidationUsesDirectorsPointer(t *testing.T) {
	router, _, _, _ := newTestRouterWithProfile(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	registerOwner(t, router)
	cookie := loginOwner(t, router)

	response := performJSON(router, nethttp.MethodPatch, "/api/identity/profile", map[string]any{
		"directors": []map[string]any{
			{
				"name":           "N. Meyer",
				"appointed_date": "not-a-date",
			},
		},
	}, cookie)
	if response.Code != nethttp.StatusUnprocessableEntity {
		t.Fatalf("invalid profile directors status = %d, want %d; body=%s", response.Code, nethttp.StatusUnprocessableEntity, response.Body.String())
	}
	problem := decodeValidationProblem(t, response)
	if len(problem.Errors) != 1 || problem.Errors[0].Pointer != "/directors" {
		t.Fatalf("problem errors = %+v, want pointer /directors", problem.Errors)
	}
}

func TestProfilePatchRejectsNullStructuredFields(t *testing.T) {
	router, _, _, _ := newTestRouterWithProfile(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	registerOwner(t, router)
	cookie := loginOwner(t, router)

	for _, tc := range []struct {
		name    string
		body    map[string]any
		pointer string
	}{
		{name: "registered office", body: map[string]any{"registered_office": nil}, pointer: "/registered_office"},
		{name: "bank details", body: map[string]any{"bank_details": nil}, pointer: "/bank_details"},
		{name: "directors", body: map[string]any{"directors": nil}, pointer: "/directors"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := performJSON(router, nethttp.MethodPatch, "/api/identity/profile", tc.body, cookie)
			if response.Code != nethttp.StatusUnprocessableEntity {
				t.Fatalf("null structured field status = %d, want %d; body=%s", response.Code, nethttp.StatusUnprocessableEntity, response.Body.String())
			}
			problem := decodeValidationProblem(t, response)
			if len(problem.Errors) != 1 || problem.Errors[0].Pointer != tc.pointer {
				t.Fatalf("problem errors = %+v, want pointer %s", problem.Errors, tc.pointer)
			}
		})
	}
}

func TestProfilePatchRejectsUnknownLogoAssetID(t *testing.T) {
	router, _, _, _ := newTestRouterWithProfile(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	registerOwner(t, router)
	cookie := loginOwner(t, router)

	response := performJSON(router, nethttp.MethodPatch, "/api/identity/profile", map[string]string{
		"logo_asset_id": "17830098-8109-4a00-8b00-000000009999",
	}, cookie)
	if response.Code != nethttp.StatusUnprocessableEntity {
		t.Fatalf("unknown logo asset id status = %d, want %d; body=%s", response.Code, nethttp.StatusUnprocessableEntity, response.Body.String())
	}
	problem := decodeValidationProblem(t, response)
	if len(problem.Errors) != 1 || problem.Errors[0].Pointer != "/logo_asset_id" {
		t.Fatalf("problem errors = %+v, want pointer /logo_asset_id", problem.Errors)
	}
}

func TestLogoUploadRejectsOversizedPayloadAtHTTPBoundary(t *testing.T) {
	router, _, _, _ := newTestRouterWithProfile(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	registerOwner(t, router)
	cookie := loginOwner(t, router)

	response := performMultipartLogo(
		router,
		"/api/identity/logo",
		"huge.svg",
		"image/svg+xml",
		bytes.Repeat([]byte("x"), MaxLogoAssetBytes+1),
		cookie,
	)
	if response.Code != nethttp.StatusRequestEntityTooLarge {
		t.Fatalf("oversized logo status = %d, want %d; body=%s", response.Code, nethttp.StatusRequestEntityTooLarge, response.Body.String())
	}
}

func TestLogoUploadRejectsSVG(t *testing.T) {
	router, _, _, _ := newTestRouterWithProfile(t, LoginRateLimit{Capacity: 100, RefillEvery: time.Hour})
	registerOwner(t, router)
	cookie := loginOwner(t, router)

	response := performMultipartLogo(
		router,
		"/api/identity/logo",
		"logo.svg",
		"image/svg+xml",
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		cookie,
	)
	if response.Code != nethttp.StatusUnsupportedMediaType {
		t.Fatalf("svg logo status = %d, want %d; body=%s", response.Code, nethttp.StatusUnsupportedMediaType, response.Body.String())
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

	registerWithProfile := paths["/api/identity/register-with-profile"].(map[string]any)["post"].(map[string]any)
	if registerWithProfile["requestBody"] == nil {
		t.Fatal("register-with-profile requestBody missing from OpenAPI fragment")
	}

	profile := paths["/api/identity/profile"].(map[string]any)
	if profile["get"] == nil {
		t.Fatal("profile GET missing from OpenAPI fragment")
	}
	if profile["patch"].(map[string]any)["requestBody"] == nil {
		t.Fatal("profile PATCH requestBody missing from OpenAPI fragment")
	}

	logo := paths["/api/identity/logo"].(map[string]any)["put"].(map[string]any)
	if logo["requestBody"] == nil {
		t.Fatal("logo PUT multipart requestBody missing from OpenAPI fragment")
	}

	assets := paths["/api/identity/assets/{id}"].(map[string]any)["get"].(map[string]any)
	if assets["parameters"] == nil {
		t.Fatal("asset GET path parameters missing from OpenAPI fragment")
	}

	components := document["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	for _, schema := range []string{"Director", "IdentityRegisterWithProfileRequest", "IdentityRegisterWithProfileResult", "IdentityProfile", "IdentityProfilePatch", "IdentityLogoUploadResponse", "ValidationProblem"} {
		if schemas[schema] == nil {
			t.Fatalf("%s schema missing from OpenAPI fragment", schema)
		}
	}
}

func newTestRouter(t *testing.T, limit LoginRateLimit) (nethttp.Handler, *memoryStore, *clock.FakeClock) {
	t.Helper()

	router, store, fakeClock, _ := newTestRouterWithProfile(t, limit)
	return router, store, fakeClock
}

func newTestRouterWithProfile(t *testing.T, limit LoginRateLimit) (nethttp.Handler, *memoryStore, *clock.FakeClock, *memoryIdentity) {
	t.Helper()

	store := newMemoryStore()
	profile := newMemoryIdentity(t)
	fakeClock := clock.NewFake(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	service := NewService(store, fakeClock, WithPasswordParams(PasswordParams{
		MemoryKiB: 64,
		Time:      1,
		Threads:   1,
		SaltLen:   8,
		KeyLen:    16,
	}))
	handler := NewHTTPHandler(
		service,
		WithLoginRateLimiter(NewLoginRateLimiter(limit)),
		WithProfileAPI(profile),
	)

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
	return router, store, fakeClock, profile
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

func firstRunProfilePayload() map[string]any {
	return map[string]any{
		"email":              "OWNER@Example.COM",
		"password":           "correct horse battery staple",
		"name":               "Owner",
		"trading_name":       "Acme Trading",
		"legal_name":         "Acme Limited",
		"company_number":     "ACME123",
		"incorporation_date": "2024-01-15",
		"year_end_month":     12,
		"year_end_day":       31,
		"registered_office": map[string]any{
			"line1":       "1 Athol Street",
			"line2":       "",
			"locality":    "Douglas",
			"region":      "",
			"postal_code": "",
			"country":     "IM",
		},
	}
}

func performJSON(router nethttp.Handler, method, path string, payload any, cookie *nethttp.Cookie) *httptest.ResponseRecorder {
	var body bytes.Buffer
	if payload != nil {
		_ = json.NewEncoder(&body).Encode(payload)
	}
	return performRequest(router, method, path, &body, cookie)
}

func performBearerJSON(router nethttp.Handler, method, path string, payload any, token string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	if payload != nil {
		_ = json.NewEncoder(&body).Encode(payload)
	}
	request := httptest.NewRequest(method, path, &body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.RemoteAddr = "203.0.113.10:58124"

	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
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

func performMultipartLogo(router nethttp.Handler, path, filename, mime string, data []byte, cookie *nethttp.Cookie) *httptest.ResponseRecorder {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, logoMultipartFieldName, filename))
	header.Set("Content-Type", mime)
	part, err := writer.CreatePart(header)
	if err != nil {
		panic(err)
	}
	if _, err := part.Write(data); err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}

	request := httptest.NewRequest(nethttp.MethodPut, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.RemoteAddr = "203.0.113.10:58124"
	if cookie != nil {
		request.AddCookie(cookie)
	}

	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func decodeProfileResponse(t *testing.T, response *httptest.ResponseRecorder) profileResponse {
	t.Helper()

	var body profileResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode profile response: %v; body=%s", err, response.Body.String())
	}
	return body
}

func decodeValidationProblem(t *testing.T, response *httptest.ResponseRecorder) struct {
	Type   string       `json:"type"`
	Status int          `json:"status"`
	Errors []fieldError `json:"errors"`
} {
	t.Helper()

	var problem struct {
		Type   string       `json:"type"`
		Status int          `json:"status"`
		Errors []fieldError `json:"errors"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode validation problem: %v; body=%s", err, response.Body.String())
	}
	return problem
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

const (
	testSeedLogoAssetID AssetID = "17830098-8109-4a00-8b00-000000000111"
	testNextLogoAssetID AssetID = "17830098-8109-4a00-8b00-000000000222"
)

type memoryIdentity struct {
	mu      sync.Mutex
	profile CompanyProfile
	assets  map[AssetID]Asset
}

func newMemoryIdentity(t *testing.T) *memoryIdentity {
	t.Helper()

	logoID := testSeedLogoAssetID
	logoBytes := testPNG(t)
	seedAsset := Asset{
		ID:        logoID,
		SHA256:    sha256Hex(logoBytes),
		MIME:      "image/png",
		Size:      int64(len(logoBytes)),
		CreatedAt: time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC),
		Bytes:     append([]byte{}, logoBytes...),
	}
	return &memoryIdentity{
		profile: CompanyProfile{
			TradingName:   "NPM Limited",
			LegalName:     "NPM Limited",
			CompanyNumber: "137792C",
			RegisteredOffice: RegisteredOffice{
				Line1:      "18 Athol St",
				Line2:      "",
				Locality:   "Douglas",
				Region:     "",
				PostalCode: "",
				Country:    "IM",
			},
			IncorporationDate: time.Date(2020, 7, 14, 0, 0, 0, 0, time.UTC),
			YearEnd:           YearEnd{Month: time.March, Day: 31},
			BankDetails: BankDetails{
				IBAN:     "",
				BIC:      "",
				BankName: "",
			},
			Shareholders: []Shareholder{
				{Name: "N. Meyer", Shares: 100, Class: "ordinary GBP 1"},
			},
			Directors: []Director{
				{Name: "N. Meyer", IsChair: true},
				{Name: "A. Patel"},
			},
			LogoAssetID: &logoID,
		},
		assets: map[AssetID]Asset{
			logoID: seedAsset,
		},
	}
}

func (s *memoryIdentity) Profile(context.Context) (CompanyProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return cloneCompanyProfile(s.profile), nil
}

func (s *memoryIdentity) UpdateProfile(_ context.Context, patch UpdateProfilePatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if patch.LogoAssetID != nil {
		logoAssetID := AssetID(strings.TrimSpace(string(*patch.LogoAssetID)))
		if logoAssetID != "" {
			if _, ok := s.assets[logoAssetID]; !ok {
				return fmt.Errorf("identity: logo asset id %s was not found: %w", logoAssetID, ErrAssetNotFound)
			}
		}
	}

	updated, err := patch.apply(s.profile)
	if err != nil {
		return err
	}
	s.profile = cloneCompanyProfile(updated)
	return nil
}

func (s *memoryIdentity) ReplaceLogo(_ context.Context, upload LogoUpload) (AssetID, error) {
	validated, err := validateLogoUpload(upload)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id := testNextLogoAssetID
	s.assets[id] = Asset{
		ID:        id,
		SHA256:    validated.sha256,
		MIME:      validated.mime,
		Size:      validated.size,
		CreatedAt: time.Date(2026, 7, 5, 12, 1, 0, 0, time.UTC),
		Bytes:     append([]byte{}, validated.bytes...),
	}
	s.profile.LogoAssetID = &id
	return id, nil
}

func (s *memoryIdentity) Asset(_ context.Context, id AssetID) (Asset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	asset, ok := s.assets[id]
	if !ok {
		return Asset{}, ErrAssetNotFound
	}
	asset.Bytes = append([]byte{}, asset.Bytes...)
	return asset, nil
}

func (s *memoryIdentity) CompanyFacts(context.Context) (CompanyFacts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return CompanyFacts{
		IncorporationDate: s.profile.IncorporationDate,
		YearEnd:           s.profile.YearEnd,
		IsVATRegistered:   s.profile.IsVATRegistered,
		Directors:         append([]Director{}, s.profile.Directors...),
	}, nil
}

func (s *memoryIdentity) IsVATRegistered(context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.profile.IsVATRegistered, nil
}

func cloneCompanyProfile(profile CompanyProfile) CompanyProfile {
	clone := profile
	if profile.VATNumber != nil {
		vatNumber := *profile.VATNumber
		clone.VATNumber = &vatNumber
	}
	if profile.LogoAssetID != nil {
		logoID := *profile.LogoAssetID
		clone.LogoAssetID = &logoID
	}
	clone.Shareholders = append([]Shareholder{}, profile.Shareholders...)
	clone.Directors = append([]Director{}, profile.Directors...)
	return clone
}

type memoryStore struct {
	mu        sync.Mutex
	nextID    int64
	nextPATID int64
	users     map[string]storedUser
	profile   *CompanyProfile
	sessions  map[string]memorySession
	pats      map[string]memoryPAT
}

type memorySession struct {
	userID    int64
	expiresAt time.Time
	createdAt time.Time
}

type memoryPAT struct {
	id         int64
	userID     int64
	name       string
	scope      PATScope
	createdAt  time.Time
	lastUsedAt *time.Time
	expiresAt  *time.Time
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		nextID:    1,
		nextPATID: 1,
		users:     make(map[string]storedUser),
		sessions:  make(map[string]memorySession),
		pats:      make(map[string]memoryPAT),
	}
}

func (s *memoryStore) UsersExist(_ context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.users) > 0, nil
}

func (s *memoryStore) ProfileExists(_ context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.profile != nil, nil
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

func (s *memoryStore) CreateFirstUserWithProfile(
	ctx context.Context,
	email string,
	passwordHash string,
	name string,
	profile CompanyProfile,
	tokenHash []byte,
	expiresAt time.Time,
	publish profileUpdatedPublisher,
) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.users) > 0 || s.profile != nil {
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

	profileClone := cloneCompanyProfile(profile)
	session := memorySession{
		userID:    user.ID,
		expiresAt: expiresAt,
		createdAt: time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC),
	}
	if publish != nil {
		if err := publish(ctx, nil); err != nil {
			return User{}, err
		}
	}

	s.users[email] = user
	s.profile = &profileClone
	s.sessions[hashKey(tokenHash)] = session
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

func (s *memoryStore) CreatePAT(_ context.Context, userID int64, tokenHash []byte, name string, scope PATScope, expiresAt *time.Time) (PersonalAccessToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := hashKey(tokenHash)
	createdAt := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	record := memoryPAT{
		id:        s.nextPATID,
		userID:    userID,
		name:      name,
		scope:     scope,
		createdAt: createdAt,
		expiresAt: cloneTimePointer(expiresAt),
	}
	s.nextPATID++
	s.pats[key] = record
	return record.toPersonalAccessToken(), nil
}

func (s *memoryStore) ListPATs(_ context.Context, userID int64) ([]PersonalAccessToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tokens := []PersonalAccessToken{}
	for _, token := range s.pats {
		if token.userID == userID {
			tokens = append(tokens, token.toPersonalAccessToken())
		}
	}
	return tokens, nil
}

func (s *memoryStore) DeletePAT(_ context.Context, userID int64, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, token := range s.pats {
		if token.userID == userID && token.id == id {
			delete(s.pats, key)
			return nil
		}
	}
	return nil
}

func (s *memoryStore) DeletePATByTokenHash(_ context.Context, tokenHash []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.pats, hashKey(tokenHash))
	return nil
}

func (s *memoryStore) FindPATByTokenHash(_ context.Context, tokenHash []byte) (storedPAT, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	token, ok := s.pats[hashKey(tokenHash)]
	if !ok {
		return storedPAT{}, ErrUnauthenticated
	}
	for _, user := range s.users {
		if user.ID == token.userID {
			return storedPAT{
				PersonalAccessToken: token.toPersonalAccessToken(),
				User:                user.User,
			}, nil
		}
	}
	return storedPAT{}, ErrUnauthenticated
}

func (s *memoryStore) MarkPATUsed(_ context.Context, tokenHash []byte, usedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := hashKey(tokenHash)
	token, ok := s.pats[key]
	if !ok {
		return ErrUnauthenticated
	}
	used := usedAt.UTC()
	token.lastUsedAt = &used
	s.pats[key] = token
	return nil
}

func (s *memoryStore) userByEmail(email string) storedUser {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.users[email]
}

func (s *memoryStore) setProfileForTest(profile CompanyProfile) {
	s.mu.Lock()
	defer s.mu.Unlock()

	profileClone := cloneCompanyProfile(profile)
	s.profile = &profileClone
}

func (s *memoryStore) profileForTest() *CompanyProfile {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.profile == nil {
		return nil
	}
	profileClone := cloneCompanyProfile(*s.profile)
	return &profileClone
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

func (p memoryPAT) toPersonalAccessToken() PersonalAccessToken {
	return PersonalAccessToken{
		ID:         p.id,
		Name:       p.name,
		Scope:      p.scope,
		CreatedAt:  p.createdAt,
		LastUsedAt: cloneTimePointer(p.lastUsedAt),
		ExpiresAt:  cloneTimePointer(p.expiresAt),
	}
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
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
