package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const (
	problemTypeBadRequest         = "https://ledgerly.local/problems/bad-request"
	problemTypeRegistrationClosed = "https://ledgerly.local/problems/registration-closed"
	problemTypeRateLimited        = "https://ledgerly.local/problems/rate-limited"
)

type HTTPHandler struct {
	service      *Service
	loginLimiter *LoginRateLimiter
}

type HTTPOption func(*HTTPHandler)

func WithLoginRateLimiter(limiter *LoginRateLimiter) HTTPOption {
	return func(h *HTTPHandler) {
		if limiter != nil {
			h.loginLimiter = limiter
		}
	}
}

func NewHTTPHandler(service *Service, opts ...HTTPOption) *HTTPHandler {
	handler := &HTTPHandler{
		service:      service,
		loginLimiter: NewLoginRateLimiter(DefaultLoginRateLimit()),
	}
	for _, opt := range opts {
		opt(handler)
	}
	return handler
}

func HTTPModule(handler *HTTPHandler) httpserver.Module {
	return httpserver.Module{
		Name:           "identity",
		RegisterRoutes: handler.RegisterRoutes,
	}
}

func (h *HTTPHandler) RegisterRoutes(r chi.Router) {
	r.Post("/register", h.register)
	r.Post("/login", h.login)
	r.Post("/logout", h.logout)
	r.Get("/me", h.me)
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userResponse struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

func (h *HTTPHandler) register(w nethttp.ResponseWriter, r *nethttp.Request) {
	var request registerRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBadRequest(w, r, err)
		return
	}

	user, err := h.service.Register(r.Context(), RegisterInput{
		Email:    request.Email,
		Password: request.Password,
		Name:     request.Name,
	})
	if err != nil {
		if errors.Is(err, ErrRegistrationClosed) {
			httpserver.WriteProblem(w, r, httpserver.Problem{
				Type:   problemTypeRegistrationClosed,
				Title:  nethttp.StatusText(nethttp.StatusForbidden),
				Status: nethttp.StatusForbidden,
				Detail: "registration is closed",
			})
			return
		}
		if isValidationError(err) {
			writeBadRequest(w, r, err)
			return
		}
		httpserver.WriteError(w, r, err)
		return
	}

	writeJSON(w, nethttp.StatusCreated, userToResponse(user))
}

func (h *HTTPHandler) login(w nethttp.ResponseWriter, r *nethttp.Request) {
	ip := clientIP(r)
	if !h.loginLimiter.Allow(ip) {
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeRateLimited,
			Title:  "Too Many Requests",
			Status: nethttp.StatusTooManyRequests,
			Detail: "too many login attempts",
		})
		return
	}

	var request loginRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBadRequest(w, r, err)
		return
	}

	result, err := h.service.Login(r.Context(), LoginInput{
		Email:    request.Email,
		Password: request.Password,
	})
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			writeUnauthenticated(w, r)
			return
		}
		httpserver.WriteError(w, r, err)
		return
	}

	nethttp.SetCookie(w, SessionCookie(result.Token, result.ExpiresAt))
	writeJSON(w, nethttp.StatusOK, userToResponse(result.User))
}

func (h *HTTPHandler) logout(w nethttp.ResponseWriter, r *nethttp.Request) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		writeUnauthenticated(w, r)
		return
	}

	if err := h.service.Logout(r.Context(), cookie.Value); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}

	nethttp.SetCookie(w, ExpiredSessionCookie())
	w.WriteHeader(nethttp.StatusNoContent)
}

func (h *HTTPHandler) me(w nethttp.ResponseWriter, r *nethttp.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeUnauthenticated(w, r)
		return
	}

	writeJSON(w, nethttp.StatusOK, userToResponse(principal.User))
}

func SessionCookie(token string, expiresAt time.Time) *nethttp.Cookie {
	return &nethttp.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt.UTC(),
		MaxAge:   int(sessionDuration.Seconds()),
		Secure:   true,
		HttpOnly: true,
		SameSite: nethttp.SameSiteLaxMode,
	}
}

func ExpiredSessionCookie() *nethttp.Cookie {
	return &nethttp.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0).UTC(),
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: nethttp.SameSiteLaxMode,
	}
}

func decodeJSON(r *nethttp.Request, dst any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("decode JSON body: %w", err)
	}
	if decoder.Decode(&struct{}{}) == nil {
		return fmt.Errorf("JSON body must contain one object")
	}
	return nil
}

func writeJSON(w nethttp.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeBadRequest(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeBadRequest,
		Title:  nethttp.StatusText(nethttp.StatusBadRequest),
		Status: nethttp.StatusBadRequest,
		Detail: err.Error(),
	})
}

func isValidationError(err error) bool {
	detail := err.Error()
	return strings.Contains(detail, "required") || strings.Contains(detail, "invalid")
}

func userToResponse(user User) userResponse {
	return userResponse{
		ID:        user.ID,
		Email:     user.Email,
		Name:      user.Name,
		CreatedAt: user.CreatedAt.UTC().Format(time.RFC3339),
	}
}
