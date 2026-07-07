package ledger

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const (
	entriesPageSize      = DefaultEntriesLimit
	problemTypeBadLedger = "https://ledgerly.local/problems/bad-request"

	maxLedgerJSONBodyBytes       = 16 * 1024
	problemTypeLedgerConflict    = "https://ledgerly.local/problems/ledger/conflict"
	problemTypeLedgerPayload     = "https://ledgerly.local/problems/ledger/payload-too-large"
	problemTypeLedgerValidation  = "https://ledgerly.local/problems/ledger/validation"
	problemTypeLedgerUnsupported = "https://ledgerly.local/problems/ledger/unsupported-media-type"
)

var ledgerAccountCodePattern = regexp.MustCompile(`^[0-9]{4}-[a-z0-9]+(?:-[a-z0-9]+)*$`)

type ledgerHandler struct {
	service *Service
	clock   interface{ Now() time.Time }
}

type entriesResponse struct {
	Entries    []entryResponse `json:"entries"`
	NextCursor *string         `json:"next_cursor"`
}

type entryResponse struct {
	ID           int64             `json:"id"`
	Date         string            `json:"date"`
	Description  string            `json:"description"`
	SourceModule string            `json:"source_module"`
	SourceRef    string            `json:"source_ref"`
	ReversalOf   *int64            `json:"reversal_of"`
	Postings     []postingResponse `json:"postings"`
	CreatedAt    string            `json:"created_at"`
}

type postingResponse struct {
	AccountCode string        `json:"account_code"`
	Amount      moneyResponse `json:"amount"`
	AmountGBP   moneyResponse `json:"amount_gbp"`
}

type moneyResponse struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

type accountsResponse struct {
	Accounts []accountResponse `json:"accounts"`
}

type accountResponse struct {
	ID        int64   `json:"id"`
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	Currency  *string `json:"currency"`
	CreatedAt string  `json:"created_at"`
}

type createAccountRequest struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

type ledgerFieldError struct {
	Pointer string `json:"pointer"`
	Detail  string `json:"detail"`
}

type trialBalanceResponse struct {
	AsOf         string          `json:"as_of"`
	Status       string          `json:"status"`
	NativeTotals []moneyResponse `json:"native_totals"`
	AmountGBP    moneyResponse   `json:"amount_gbp"`
}

type entryCursorToken struct {
	Date string  `json:"date"`
	ID   EntryID `json:"id"`
}

// RegisterRoutes mounts ledger REST endpoints. Posting to the ledger remains
// intentionally absent from HTTP; modules write through the Go Ledger API.
func (m *Module) RegisterRoutes(r chi.Router) {
	h := ledgerHandler{service: m.service, clock: m.clock}
	r.Get("/entries", h.listEntries)
	r.Get("/accounts", h.listAccounts)
	r.Post("/accounts", h.createAccount)
	r.Get("/trial-balance", h.getTrialBalance)
}

func (h ledgerHandler) listEntries(w nethttp.ResponseWriter, r *nethttp.Request) {
	filter, err := parseEntryFilter(r)
	if err != nil {
		writeLedgerBadRequest(w, r, err)
		return
	}
	filter.Limit = entriesPageSize + 1

	entries, err := h.service.Entries(r.Context(), filter)
	if err != nil {
		writeLedgerError(w, r, err)
		return
	}

	var nextCursor *string
	if len(entries) > entriesPageSize {
		last := entries[entriesPageSize-1]
		cursor := encodeEntryCursor(last)
		nextCursor = &cursor
		entries = entries[:entriesPageSize]
	}
	writeLedgerJSON(w, nethttp.StatusOK, entriesToResponse(entries, nextCursor))
}

func (h ledgerHandler) listAccounts(w nethttp.ResponseWriter, r *nethttp.Request) {
	accountType, err := parseAccountTypeQuery(r)
	if err != nil {
		writeLedgerBadRequest(w, r, err)
		return
	}
	accounts, err := h.service.Accounts(r.Context())
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	if accountType != "" {
		accounts = filterAccountsByType(accounts, accountType)
	}
	writeLedgerJSON(w, nethttp.StatusOK, accountsToResponse(accounts))
}

func (h ledgerHandler) createAccount(w nethttp.ResponseWriter, r *nethttp.Request) {
	var request createAccountRequest
	if err := decodeLedgerJSON(w, r, &request); err != nil {
		writeLedgerDecodeError(w, r, err)
		return
	}

	fieldErrors := validateCreateExpenseAccountRequest(request)
	if len(fieldErrors) > 0 {
		writeLedgerValidation(w, r, fieldErrors)
		return
	}

	account, err := h.service.CreateExpenseAccount(r.Context(), AccountSpec{
		Code: AccountCode(strings.TrimSpace(request.Code)),
		Name: strings.TrimSpace(request.Name),
		Type: AccountTypeExpense,
	})
	if err != nil {
		writeLedgerError(w, r, err)
		return
	}
	writeLedgerJSON(w, nethttp.StatusCreated, accountToResponse(account))
}

func parseAccountTypeQuery(r *nethttp.Request) (AccountType, error) {
	value := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	if value == "" {
		return "", nil
	}
	accountType := AccountType(value)
	if !validAccountType(accountType) {
		return "", fmt.Errorf("type must be one of asset, liability, equity, income, expense")
	}
	return accountType, nil
}

func filterAccountsByType(accounts []Account, accountType AccountType) []Account {
	filtered := accounts[:0]
	for _, account := range accounts {
		if account.Type == accountType {
			filtered = append(filtered, account)
		}
	}
	return filtered
}

func (h ledgerHandler) getTrialBalance(w nethttp.ResponseWriter, r *nethttp.Request) {
	asOf := h.clock.Now().UTC()
	report, err := h.service.TrialBalance(r.Context(), asOf)
	if err != nil {
		var violation *TrialBalanceViolationError
		if errors.As(err, &violation) {
			writeLedgerJSON(w, nethttp.StatusOK, trialBalanceToResponse(violation.Report))
			return
		}
		writeLedgerError(w, r, err)
		return
	}
	writeLedgerJSON(w, nethttp.StatusOK, trialBalanceToResponse(report))
}

func parseEntryFilter(r *nethttp.Request) (EntryFilter, error) {
	query := r.URL.Query()
	filter := EntryFilter{
		SourceModule: strings.TrimSpace(query.Get("source")),
		AccountCode:  AccountCode(strings.TrimSpace(query.Get("account"))),
	}

	var err error
	if value := strings.TrimSpace(query.Get("from")); value != "" {
		filter.From, err = parseDatePointer("from", value)
		if err != nil {
			return EntryFilter{}, err
		}
	}
	if value := strings.TrimSpace(query.Get("to")); value != "" {
		filter.To, err = parseDatePointer("to", value)
		if err != nil {
			return EntryFilter{}, err
		}
	}
	if value := strings.TrimSpace(query.Get("cursor")); value != "" {
		cursor, err := decodeEntryCursor(value)
		if err != nil {
			return EntryFilter{}, err
		}
		filter.After = &cursor
	}
	return filter, nil
}

func parseDatePointer(name string, value string) (*time.Time, error) {
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return nil, fmt.Errorf("%s must be a date in YYYY-MM-DD format", name)
	}
	return &parsed, nil
}

func encodeEntryCursor(entry JournalEntry) string {
	payload, err := json.Marshal(entryCursorToken{
		Date: entry.Date.UTC().Format(time.DateOnly),
		ID:   entry.ID,
	})
	if err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeEntryCursor(value string) (EntryCursor, error) {
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return EntryCursor{}, fmt.Errorf("cursor is invalid")
	}
	var token entryCursorToken
	if err := json.Unmarshal(payload, &token); err != nil {
		return EntryCursor{}, fmt.Errorf("cursor is invalid")
	}
	date, err := time.Parse(time.DateOnly, token.Date)
	if err != nil || token.ID <= 0 {
		return EntryCursor{}, fmt.Errorf("cursor is invalid")
	}
	return EntryCursor{Date: date, ID: token.ID}, nil
}

func entriesToResponse(entries []JournalEntry, nextCursor *string) entriesResponse {
	response := entriesResponse{
		Entries:    make([]entryResponse, 0, len(entries)),
		NextCursor: nextCursor,
	}
	for _, entry := range entries {
		response.Entries = append(response.Entries, entryToResponse(entry))
	}
	return response
}

func entryToResponse(entry JournalEntry) entryResponse {
	var reversalOf *int64
	if entry.ReversalOf != nil {
		value := int64(*entry.ReversalOf)
		reversalOf = &value
	}

	response := entryResponse{
		ID:           int64(entry.ID),
		Date:         entry.Date.UTC().Format(time.DateOnly),
		Description:  entry.Description,
		SourceModule: entry.SourceModule,
		SourceRef:    entry.SourceRef,
		ReversalOf:   reversalOf,
		Postings:     make([]postingResponse, 0, len(entry.Postings)),
		CreatedAt:    entry.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	for _, posting := range entry.Postings {
		response.Postings = append(response.Postings, postingResponse{
			AccountCode: string(posting.AccountCode),
			Amount:      moneyToResponse(posting.Amount.Amount, posting.Amount.Currency),
			AmountGBP:   moneyToResponse(posting.AmountGBP.Amount, posting.AmountGBP.Currency),
		})
	}
	return response
}

func accountsToResponse(accounts []Account) accountsResponse {
	response := accountsResponse{Accounts: make([]accountResponse, 0, len(accounts))}
	for _, account := range accounts {
		response.Accounts = append(response.Accounts, accountToResponse(account))
	}
	return response
}

func accountToResponse(account Account) accountResponse {
	return accountResponse{
		ID:        int64(account.ID),
		Code:      string(account.Code),
		Name:      account.Name,
		Type:      string(account.Type),
		Currency:  account.Currency,
		CreatedAt: account.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func trialBalanceToResponse(report Report) trialBalanceResponse {
	response := trialBalanceResponse{
		AsOf:         report.AsOf.UTC().Format(time.DateOnly),
		Status:       trialBalanceStatus(report),
		NativeTotals: make([]moneyResponse, 0, len(report.CurrencySums)),
		AmountGBP:    moneyToResponse(report.GBPTotal, "GBP"),
	}
	for _, sum := range report.CurrencySums {
		response.NativeTotals = append(response.NativeTotals, moneyToResponse(sum.Amount, sum.Currency))
	}
	return response
}

func trialBalanceStatus(report Report) string {
	if report.Balanced {
		return "balanced"
	}
	return "out_of_balance"
}

func moneyToResponse(amount int64, currency string) moneyResponse {
	return moneyResponse{AmountMinor: amount, Currency: currency}
}

func validateCreateExpenseAccountRequest(request createAccountRequest) []ledgerFieldError {
	var fieldErrors []ledgerFieldError

	code := strings.ToLower(strings.TrimSpace(request.Code))
	if code == "" {
		fieldErrors = append(fieldErrors, ledgerFieldError{Pointer: "/code", Detail: "is required"})
	} else if !ledgerAccountCodePattern.MatchString(code) {
		fieldErrors = append(fieldErrors, ledgerFieldError{Pointer: "/code", Detail: "must match ####-slug"})
	}

	if strings.TrimSpace(request.Name) == "" {
		fieldErrors = append(fieldErrors, ledgerFieldError{Pointer: "/name", Detail: "is required"})
	}

	accountType := strings.ToLower(strings.TrimSpace(request.Type))
	if accountType != "" && accountType != string(AccountTypeExpense) {
		fieldErrors = append(fieldErrors, ledgerFieldError{Pointer: "/type", Detail: "must be expense"})
	}

	return fieldErrors
}

func decodeLedgerJSON(w nethttp.ResponseWriter, r *nethttp.Request, target any) error {
	if contentType := strings.TrimSpace(r.Header.Get("Content-Type")); contentType != "" {
		mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
		if mediaType != "application/json" {
			return errLedgerUnsupportedMedia
		}
	}
	r.Body = nethttp.MaxBytesReader(w, r.Body, maxLedgerJSONBodyBytes)
	defer func() {
		_ = r.Body.Close()
	}()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maxBytesErr *nethttp.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return errLedgerRequestBodyTooLarge
		}
		return fmt.Errorf("decode JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		return fmt.Errorf("JSON body must contain one object")
	} else {
		var maxBytesErr *nethttp.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return errLedgerRequestBodyTooLarge
		}
		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("decode JSON body: %w", err)
		}
	}
	return nil
}

var (
	errLedgerRequestBodyTooLarge = errors.New("ledger: request body is too large")
	errLedgerUnsupportedMedia    = errors.New("ledger: application/json is required")
)

func writeLedgerError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	if errors.Is(err, ErrInvalidEntryFilter) || errors.Is(err, ErrInvalidEntryDate) {
		writeLedgerBadRequest(w, r, err)
		return
	}
	if errors.Is(err, ErrInvalidAccountSpec) {
		writeLedgerValidation(w, r, []ledgerFieldError{{Pointer: "/", Detail: err.Error()}})
		return
	}
	if errors.Is(err, ErrAccountAlreadyExists) || errors.Is(err, ErrAccountConflict) {
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeLedgerConflict,
			Title:  nethttp.StatusText(nethttp.StatusConflict),
			Status: nethttp.StatusConflict,
			Detail: err.Error(),
		})
		return
	}
	httpserver.WriteError(w, r, err)
}

func writeLedgerDecodeError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	if errors.Is(err, errLedgerRequestBodyTooLarge) {
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeLedgerPayload,
			Title:  nethttp.StatusText(nethttp.StatusRequestEntityTooLarge),
			Status: nethttp.StatusRequestEntityTooLarge,
			Detail: "request body is too large",
		})
		return
	}
	if errors.Is(err, errLedgerUnsupportedMedia) {
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeLedgerUnsupported,
			Title:  nethttp.StatusText(nethttp.StatusUnsupportedMediaType),
			Status: nethttp.StatusUnsupportedMediaType,
			Detail: "application/json is required",
		})
		return
	}
	writeLedgerBadRequest(w, r, err)
}

func writeLedgerValidation(w nethttp.ResponseWriter, r *nethttp.Request, fieldErrors []ledgerFieldError) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeLedgerValidation,
		Title:  nethttp.StatusText(nethttp.StatusUnprocessableEntity),
		Status: nethttp.StatusUnprocessableEntity,
		Detail: "ledger account validation failed",
		Extensions: map[string]any{
			"errors": fieldErrors,
		},
	})
}

func writeLedgerBadRequest(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeBadLedger,
		Title:  nethttp.StatusText(nethttp.StatusBadRequest),
		Status: nethttp.StatusBadRequest,
		Detail: err.Error(),
	})
}

func writeLedgerJSON(w nethttp.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		panic(err)
	}
}
