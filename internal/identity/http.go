package identity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const (
	problemTypeBadRequest         = "https://ledgerly.local/problems/bad-request"
	problemTypeNotFound           = "https://ledgerly.local/problems/not-found"
	problemTypePayloadTooLarge    = "https://ledgerly.local/problems/payload-too-large"
	problemTypeRegistrationClosed = "https://ledgerly.local/problems/registration-closed"
	problemTypeRateLimited        = "https://ledgerly.local/problems/rate-limited"
	problemTypeUnsupportedMedia   = "https://ledgerly.local/problems/unsupported-media-type"
	problemTypeValidation         = "https://ledgerly.local/problems/validation-error"

	maxJSONBodyBytes          = 64 * 1024
	maxLogoMultipartBodyBytes = MaxLogoAssetBytes + 32*1024
	logoMultipartFieldName    = "logo"
)

var errRequestBodyTooLarge = errors.New("request body too large")

type HTTPHandler struct {
	service      *Service
	profile      Identity
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

func WithProfileAPI(profile Identity) HTTPOption {
	return func(h *HTTPHandler) {
		h.profile = profile
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
	r.Get("/pats", h.listPATs)
	r.Post("/pats", h.createPAT)
	r.Delete("/pats/{id}", h.revokePAT)
	r.Get("/profile", h.getProfile)
	r.Patch("/profile", h.patchProfile)
	r.Put("/logo", h.putLogo)
	r.Get("/assets/{id}", h.getAsset)
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
	ID         int64     `json:"id"`
	Email      string    `json:"email"`
	Name       string    `json:"name"`
	CreatedAt  string    `json:"created_at"`
	TokenName  *string   `json:"token_name,omitempty"`
	TokenScope *PATScope `json:"token_scope,omitempty"`
}

type profileResponse struct {
	TradingName       string           `json:"trading_name"`
	LegalName         string           `json:"legal_name"`
	CompanyNumber     string           `json:"company_number"`
	RegisteredOffice  RegisteredOffice `json:"registered_office"`
	IncorporationDate string           `json:"incorporation_date"`
	YearEnd           yearEndResponse  `json:"year_end"`
	IsVATRegistered   bool             `json:"is_vat_registered"`
	VATNumber         *string          `json:"vat_number"`
	BankDetails       BankDetails      `json:"bank_details"`
	Shareholders      []Shareholder    `json:"shareholders"`
	LogoAssetID       *AssetID         `json:"logo_asset_id"`
	LogoAssetURL      *string          `json:"logo_asset_url"`
}

type yearEndResponse struct {
	Month int `json:"month"`
	Day   int `json:"day"`
}

type logoResponse struct {
	AssetID  AssetID `json:"asset_id"`
	AssetURL string  `json:"asset_url"`
}

type createPATRequest struct {
	Name      string   `json:"name"`
	Scope     PATScope `json:"scope"`
	ExpiresAt *string  `json:"expires_at"`
}

type patResponse struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Scope      PATScope `json:"scope"`
	CreatedAt  string   `json:"created_at"`
	LastUsedAt *string  `json:"last_used_at"`
	ExpiresAt  *string  `json:"expires_at"`
}

type createPATResponse struct {
	PersonalAccessToken patResponse `json:"personal_access_token"`
	Token               string      `json:"token"`
}

type listPATsResponse struct {
	Tokens []patResponse `json:"tokens"`
}

type fieldError struct {
	Pointer string `json:"pointer"`
	Detail  string `json:"detail"`
}

func (h *HTTPHandler) register(w nethttp.ResponseWriter, r *nethttp.Request) {
	var request registerRequest
	if err := decodeJSON(w, r, &request); err != nil {
		if errors.Is(err, errRequestBodyTooLarge) {
			writePayloadTooLarge(w, r)
			return
		}
		writeBadRequest(w, r, err)
		return
	}

	user, err := h.service.Register(r.Context(), RegisterInput(request))
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
	if err := decodeJSON(w, r, &request); err != nil {
		if errors.Is(err, errRequestBodyTooLarge) {
			writePayloadTooLarge(w, r)
			return
		}
		writeBadRequest(w, r, err)
		return
	}

	result, err := h.service.Login(r.Context(), LoginInput(request))
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

	writeJSON(w, nethttp.StatusOK, principalToUserResponse(principal))
}

func (h *HTTPHandler) listPATs(w nethttp.ResponseWriter, r *nethttp.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeUnauthenticated(w, r)
		return
	}

	tokens, err := h.service.ListPATs(r.Context(), principal)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}

	response := listPATsResponse{Tokens: make([]patResponse, 0, len(tokens))}
	for _, token := range tokens {
		response.Tokens = append(response.Tokens, patToResponse(token))
	}
	writeJSON(w, nethttp.StatusOK, response)
}

func (h *HTTPHandler) createPAT(w nethttp.ResponseWriter, r *nethttp.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeUnauthenticated(w, r)
		return
	}

	var request createPATRequest
	if err := decodeJSON(w, r, &request); err != nil {
		if errors.Is(err, errRequestBodyTooLarge) {
			writePayloadTooLarge(w, r)
			return
		}
		writeBadRequest(w, r, err)
		return
	}

	expiresAt, err := parseOptionalTime(request.ExpiresAt)
	if err != nil {
		writeBadRequest(w, r, err)
		return
	}
	result, err := h.service.CreatePAT(r.Context(), principal, CreatePATInput{
		Name:      request.Name,
		Scope:     request.Scope,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		if isValidationError(err) {
			writeBadRequest(w, r, err)
			return
		}
		httpserver.WriteError(w, r, err)
		return
	}

	writeJSON(w, nethttp.StatusCreated, createPATResponse{
		PersonalAccessToken: patToResponse(result.PersonalAccessToken),
		Token:               result.Token,
	})
}

func (h *HTTPHandler) revokePAT(w nethttp.ResponseWriter, r *nethttp.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeUnauthenticated(w, r)
		return
	}

	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || id <= 0 {
		writeBadRequest(w, r, fmt.Errorf("PAT id is required"))
		return
	}
	if err := h.service.RevokePAT(r.Context(), principal, id); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(nethttp.StatusNoContent)
}

func (h *HTTPHandler) getProfile(w nethttp.ResponseWriter, r *nethttp.Request) {
	profileAPI, ok := h.requireProfileAPI(w, r)
	if !ok {
		return
	}

	profile, err := profileAPI.Profile(r.Context())
	if err != nil {
		writeIdentityError(w, r, err)
		return
	}

	writeJSON(w, nethttp.StatusOK, profileToResponse(profile))
}

func (h *HTTPHandler) patchProfile(w nethttp.ResponseWriter, r *nethttp.Request) {
	profileAPI, ok := h.requireProfileAPI(w, r)
	if !ok {
		return
	}

	patch, fieldErrors, err := decodeProfilePatch(w, r)
	if err != nil {
		if errors.Is(err, errRequestBodyTooLarge) {
			writePayloadTooLarge(w, r)
			return
		}
		writeBadRequest(w, r, err)
		return
	}
	if len(fieldErrors) > 0 {
		writeValidationProblem(w, r, fieldErrors)
		return
	}

	if err := profileAPI.UpdateProfile(r.Context(), patch); err != nil {
		if fieldErrors := profileFieldErrorsFromError(err); len(fieldErrors) > 0 {
			writeValidationProblem(w, r, fieldErrors)
			return
		}
		writeIdentityError(w, r, err)
		return
	}

	profile, err := profileAPI.Profile(r.Context())
	if err != nil {
		writeIdentityError(w, r, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, profileToResponse(profile))
}

func (h *HTTPHandler) putLogo(w nethttp.ResponseWriter, r *nethttp.Request) {
	profileAPI, ok := h.requireProfileAPI(w, r)
	if !ok {
		return
	}

	upload, err := readLogoUpload(w, r)
	if err != nil {
		if errors.Is(err, errRequestBodyTooLarge) {
			writePayloadTooLarge(w, r)
			return
		}
		writeBadRequest(w, r, err)
		return
	}

	id, err := profileAPI.ReplaceLogo(r.Context(), upload)
	if err != nil {
		writeIdentityError(w, r, err)
		return
	}

	writeJSON(w, nethttp.StatusOK, logoResponse{
		AssetID:  id,
		AssetURL: assetURL(id),
	})
}

func (h *HTTPHandler) getAsset(w nethttp.ResponseWriter, r *nethttp.Request) {
	profileAPI, ok := h.requireProfileAPI(w, r)
	if !ok {
		return
	}

	id := AssetID(strings.TrimSpace(chi.URLParam(r, "id")))
	if id == "" {
		writeBadRequest(w, r, fmt.Errorf("asset id is required"))
		return
	}

	asset, err := profileAPI.Asset(r.Context(), id)
	if err != nil {
		writeIdentityError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", asset.MIME)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Length", strconv.FormatInt(int64(len(asset.Bytes)), 10))
	w.WriteHeader(nethttp.StatusOK)
	_, _ = w.Write(asset.Bytes)
}

func (h *HTTPHandler) requireProfileAPI(w nethttp.ResponseWriter, r *nethttp.Request) (Identity, bool) {
	if h.profile != nil {
		return h.profile, true
	}
	httpserver.WriteError(w, r, fmt.Errorf("identity profile API is not configured"))
	return nil, false
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

func decodeJSON(w nethttp.ResponseWriter, r *nethttp.Request, dst any) error {
	r.Body = nethttp.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	defer func() {
		_ = r.Body.Close()
	}()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		var maxBytesErr *nethttp.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return errRequestBodyTooLarge
		}
		return fmt.Errorf("decode JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		return fmt.Errorf("JSON body must contain one object")
	} else {
		var maxBytesErr *nethttp.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return errRequestBodyTooLarge
		}
	}
	return nil
}

func decodeProfilePatch(w nethttp.ResponseWriter, r *nethttp.Request) (UpdateProfilePatch, []fieldError, error) {
	var raw map[string]json.RawMessage
	if err := decodeJSON(w, r, &raw); err != nil {
		return UpdateProfilePatch{}, nil, err
	}

	var (
		patch       UpdateProfilePatch
		fieldErrors []fieldError
	)

	for field, value := range raw {
		switch field {
		case "trading_name":
			assignStringPatch(value, "/trading_name", &patch.TradingName, &fieldErrors)
		case "legal_name":
			assignStringPatch(value, "/legal_name", &patch.LegalName, &fieldErrors)
		case "company_number":
			assignStringPatch(value, "/company_number", &patch.CompanyNumber, &fieldErrors)
		case "registered_office":
			if rejectJSONNull(value, "/registered_office", "must be an object with address fields", &fieldErrors) {
				continue
			}
			var office RegisteredOffice
			if err := decodeStrict(value, &office); err != nil {
				fieldErrors = append(fieldErrors, fieldError{Pointer: "/registered_office", Detail: "must be an object with address fields"})
				continue
			}
			patch.RegisteredOffice = &office
		case "incorporation_date":
			assignStringPatch(value, "/incorporation_date", &patch.IncorporationDate, &fieldErrors)
		case "year_end":
			if rejectJSONNull(value, "/year_end", "must be an object with month and day", &fieldErrors) {
				continue
			}
			var yearEnd yearEndResponse
			if err := decodeStrict(value, &yearEnd); err != nil {
				fieldErrors = append(fieldErrors, fieldError{Pointer: "/year_end", Detail: "must be an object with month and day"})
				continue
			}
			patch.YearEnd = &YearEnd{Month: time.Month(yearEnd.Month), Day: yearEnd.Day}
		case "is_vat_registered":
			if rejectJSONNull(value, "/is_vat_registered", "must be a boolean", &fieldErrors) {
				continue
			}
			var isVATRegistered bool
			if err := decodeStrict(value, &isVATRegistered); err != nil {
				fieldErrors = append(fieldErrors, fieldError{Pointer: "/is_vat_registered", Detail: "must be a boolean"})
				continue
			}
			patch.IsVATRegistered = &isVATRegistered
		case "vat_number":
			if isJSONNull(value) {
				vatNumber := ""
				patch.VATNumber = &vatNumber
				continue
			}
			assignStringPatch(value, "/vat_number", &patch.VATNumber, &fieldErrors)
		case "bank_details":
			if rejectJSONNull(value, "/bank_details", "must be an object with bank detail fields", &fieldErrors) {
				continue
			}
			var bankDetails BankDetails
			if err := decodeStrict(value, &bankDetails); err != nil {
				fieldErrors = append(fieldErrors, fieldError{Pointer: "/bank_details", Detail: "must be an object with bank detail fields"})
				continue
			}
			patch.BankDetails = &bankDetails
		case "shareholders":
			if rejectJSONNull(value, "/shareholders", "must be an array of shareholders", &fieldErrors) {
				continue
			}
			var shareholders []Shareholder
			if err := decodeStrict(value, &shareholders); err != nil {
				fieldErrors = append(fieldErrors, fieldError{Pointer: "/shareholders", Detail: "must be an array of shareholders"})
				continue
			}
			patch.Shareholders = &shareholders
		case "logo_asset_id":
			if isJSONNull(value) {
				logoAssetID := AssetID("")
				patch.LogoAssetID = &logoAssetID
				continue
			}
			var logoAssetID string
			if err := decodeStrict(value, &logoAssetID); err != nil {
				fieldErrors = append(fieldErrors, fieldError{Pointer: "/logo_asset_id", Detail: "must be a string or null"})
				continue
			}
			id := AssetID(logoAssetID)
			patch.LogoAssetID = &id
		default:
			return UpdateProfilePatch{}, nil, fmt.Errorf("unknown field %q", field)
		}
	}

	return patch, fieldErrors, nil
}

func rejectJSONNull(value json.RawMessage, pointer string, detail string, fieldErrors *[]fieldError) bool {
	if !isJSONNull(value) {
		return false
	}
	*fieldErrors = append(*fieldErrors, fieldError{Pointer: pointer, Detail: detail})
	return true
}

func assignStringPatch(value json.RawMessage, pointer string, dst **string, fieldErrors *[]fieldError) {
	var decoded string
	if err := decodeStrict(value, &decoded); err != nil {
		*fieldErrors = append(*fieldErrors, fieldError{Pointer: pointer, Detail: "must be a string"})
		return
	}
	*dst = &decoded
}

func decodeStrict(raw json.RawMessage, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		return fmt.Errorf("JSON value must contain one value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.EqualFold(strings.TrimSpace(string(raw)), "null")
}

func readLogoUpload(w nethttp.ResponseWriter, r *nethttp.Request) (LogoUpload, error) {
	r.Body = nethttp.MaxBytesReader(w, r.Body, maxLogoMultipartBodyBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		return LogoUpload{}, fmt.Errorf("logo upload must be multipart/form-data with a %q file field", logoMultipartFieldName)
	}

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if isMaxBytesError(err) {
				return LogoUpload{}, errRequestBodyTooLarge
			}
			return LogoUpload{}, fmt.Errorf("read multipart logo: %w", err)
		}
		if part.FormName() != logoMultipartFieldName {
			_ = part.Close()
			continue
		}
		defer func() {
			_ = part.Close()
		}()

		data, err := io.ReadAll(io.LimitReader(part, MaxLogoAssetBytes+1))
		if err != nil {
			if isMaxBytesError(err) {
				return LogoUpload{}, errRequestBodyTooLarge
			}
			return LogoUpload{}, fmt.Errorf("read logo upload: %w", err)
		}
		if len(data) > MaxLogoAssetBytes {
			return LogoUpload{}, errRequestBodyTooLarge
		}
		mime := strings.TrimSpace(part.Header.Get("Content-Type"))
		if mime == "" {
			mime = nethttp.DetectContentType(data)
		}
		return LogoUpload{
			MIME:  mime,
			Bytes: data,
		}, nil
	}

	return LogoUpload{}, fmt.Errorf("logo upload must include a %q file field", logoMultipartFieldName)
}

func isMaxBytesError(err error) bool {
	var maxBytesErr *nethttp.MaxBytesError
	return errors.As(err, &maxBytesErr)
}

func writeJSON(w nethttp.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeValidationProblem(w nethttp.ResponseWriter, r *nethttp.Request, fieldErrors []fieldError) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeValidation,
		Title:  nethttp.StatusText(nethttp.StatusUnprocessableEntity),
		Status: nethttp.StatusUnprocessableEntity,
		Detail: "profile validation failed",
		Extensions: map[string]any{
			"errors": fieldErrors,
		},
	})
}

func writeBadRequest(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeBadRequest,
		Title:  nethttp.StatusText(nethttp.StatusBadRequest),
		Status: nethttp.StatusBadRequest,
		Detail: err.Error(),
	})
}

func writePayloadTooLarge(w nethttp.ResponseWriter, r *nethttp.Request) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypePayloadTooLarge,
		Title:  nethttp.StatusText(nethttp.StatusRequestEntityTooLarge),
		Status: nethttp.StatusRequestEntityTooLarge,
		Detail: "request body is too large",
	})
}

func writeIdentityError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	switch {
	case errors.Is(err, ErrProfileNotFound):
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeNotFound,
			Title:  nethttp.StatusText(nethttp.StatusNotFound),
			Status: nethttp.StatusNotFound,
			Detail: "company profile was not found",
		})
	case errors.Is(err, ErrAssetNotFound):
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeNotFound,
			Title:  nethttp.StatusText(nethttp.StatusNotFound),
			Status: nethttp.StatusNotFound,
			Detail: "asset was not found",
		})
	case errors.Is(err, ErrAssetTooLarge):
		writePayloadTooLarge(w, r)
	case errors.Is(err, ErrUnsupportedAsset):
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeUnsupportedMedia,
			Title:  nethttp.StatusText(nethttp.StatusUnsupportedMediaType),
			Status: nethttp.StatusUnsupportedMediaType,
			Detail: err.Error(),
		})
	default:
		httpserver.WriteError(w, r, err)
	}
}

func isValidationError(err error) bool {
	detail := err.Error()
	return strings.Contains(detail, "required") || strings.Contains(detail, "invalid") || strings.Contains(detail, "must be")
}

func profileFieldErrorsFromError(err error) []fieldError {
	detail := err.Error()
	switch {
	case strings.Contains(detail, "trading name"):
		return []fieldError{{Pointer: "/trading_name", Detail: detail}}
	case strings.Contains(detail, "legal name"):
		return []fieldError{{Pointer: "/legal_name", Detail: detail}}
	case strings.Contains(detail, "company number"):
		return []fieldError{{Pointer: "/company_number", Detail: detail}}
	case strings.Contains(detail, "incorporation date") || strings.Contains(detail, "parse date") || strings.Contains(detail, "date is required"):
		return []fieldError{{Pointer: "/incorporation_date", Detail: detail}}
	case strings.Contains(detail, "year-end month"):
		return []fieldError{{Pointer: "/year_end/month", Detail: detail}}
	case strings.Contains(detail, "year-end day"):
		return []fieldError{{Pointer: "/year_end/day", Detail: detail}}
	case strings.Contains(detail, "asset id"):
		return []fieldError{{Pointer: "/logo_asset_id", Detail: detail}}
	default:
		return nil
	}
}

func userToResponse(user User) userResponse {
	return userResponse{
		ID:        user.ID,
		Email:     user.Email,
		Name:      user.Name,
		CreatedAt: user.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func principalToUserResponse(principal Principal) userResponse {
	response := userToResponse(principal.User)
	if principal.PAT != nil {
		name := principal.PAT.Name
		scope := principal.PAT.Scope
		response.TokenName = &name
		response.TokenScope = &scope
	}
	return response
}

func patToResponse(token PersonalAccessToken) patResponse {
	return patResponse{
		ID:         token.ID,
		Name:       token.Name,
		Scope:      token.Scope,
		CreatedAt:  token.CreatedAt.UTC().Format(time.RFC3339),
		LastUsedAt: formatOptionalTime(token.LastUsedAt),
		ExpiresAt:  formatOptionalTime(token.ExpiresAt),
	}
}

func formatOptionalTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339)
	return &formatted
}

func parseOptionalTime(value *string) (*time.Time, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	for _, layout := range []string{time.RFC3339, dateLayout} {
		parsed, err := time.Parse(layout, trimmed)
		if err == nil {
			utc := parsed.UTC()
			return &utc, nil
		}
	}
	return nil, fmt.Errorf("expires_at must be an RFC3339 timestamp or date")
}

func profileToResponse(profile CompanyProfile) profileResponse {
	response := profileResponse{
		TradingName:       profile.TradingName,
		LegalName:         profile.LegalName,
		CompanyNumber:     profile.CompanyNumber,
		RegisteredOffice:  profile.RegisteredOffice,
		IncorporationDate: profile.IncorporationDate.UTC().Format(dateLayout),
		YearEnd: yearEndResponse{
			Month: int(profile.YearEnd.Month),
			Day:   profile.YearEnd.Day,
		},
		IsVATRegistered: profile.IsVATRegistered,
		VATNumber:       cloneStringPointer(profile.VATNumber),
		BankDetails:     profile.BankDetails,
		Shareholders:    append([]Shareholder{}, profile.Shareholders...),
	}
	if profile.LogoAssetID != nil {
		id := *profile.LogoAssetID
		url := assetURL(id)
		response.LogoAssetID = &id
		response.LogoAssetURL = &url
	}
	return response
}

func assetURL(id AssetID) string {
	return "/api/identity/assets/" + string(id)
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
