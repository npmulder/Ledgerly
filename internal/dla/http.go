package dla

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const (
	dlaPageSize          = DefaultLedgerLimit
	maxEntryJSONBodySize = 64 * 1024

	problemTypeDLABadRequest      = "https://ledgerly.local/problems/dla/bad-request"
	problemTypeDLAValidation      = "https://ledgerly.local/problems/dla/validation"
	problemTypeDLAManualDrawing   = "https://ledgerly.local/problems/dla/manual-drawing"
	problemTypeDLADuplicateSource = "https://ledgerly.local/problems/dla/duplicate-source"
	problemTypeDLAPayloadTooLarge = "https://ledgerly.local/problems/dla/payload-too-large"
)

var errEntryRequestBodyTooLarge = errors.New("request body too large")

type dlaHandler struct {
	service *Service
	clock   interface{ Now() time.Time }
}

type ledgerResponse struct {
	Entries    []entryResponse `json:"entries"`
	NextCursor *string         `json:"next_cursor"`
}

type entryResponse struct {
	ID             int64         `json:"id"`
	DirectorID     string        `json:"director_id"`
	Date           string        `json:"date"`
	Kind           string        `json:"kind"`
	Description    string        `json:"description"`
	SourceRef      string        `json:"source_ref"`
	Amount         moneyResponse `json:"amount"`
	OwedToYou      moneyResponse `json:"owed_to_you"`
	Drawn          moneyResponse `json:"drawn"`
	RunningBalance moneyResponse `json:"running_balance"`
	BalanceSide    string        `json:"balance_side"`
	CreatedAt      string        `json:"created_at"`
}

type balanceResponse struct {
	DirectorID         string         `json:"director_id"`
	DirectorName       string         `json:"director_name,omitempty"`
	Balance            moneyResponse  `json:"balance"`
	Status             string         `json:"status"`
	Policy             policyResponse `json:"policy"`
	SuggestedClearance *moneyResponse `json:"suggested_clearance,omitempty"`
}

type policyResponse struct {
	S455Charge               bool   `json:"s455_charge"`
	CreditStatusText         string `json:"credit_status_text"`
	CreditExplainerTemplate  string `json:"credit_explainer_template"`
	BIKWarningKey            string `json:"bik_warning_key"`
	OverdrawnWarningTemplate string `json:"overdrawn_warning_template"`
	Remedy                   string `json:"remedy"`
}

type entryCreatedResponse struct {
	SourceRef string `json:"source_ref"`
}

type entryRequest struct {
	DirectorID      string        `json:"director_id"`
	Date            string        `json:"date"`
	Kind            EntryKind     `json:"kind"`
	Description     string        `json:"description"`
	Amount          moneyResponse `json:"amount"`
	SourceRef       string        `json:"source_ref"`
	CashAccountCode string        `json:"cash_account_code"`
	ExpenseCategory string        `json:"expense_category"`
}

type moneyResponse struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

type fieldError struct {
	Pointer string `json:"pointer"`
	Detail  string `json:"detail"`
}

type entryCursorToken struct {
	Date string  `json:"date"`
	ID   EntryID `json:"id"`
}

// RegisterRoutes mounts DLA REST endpoints.
func (m *Module) RegisterRoutes(r chi.Router) {
	h := dlaHandler{service: m.service, clock: m.clock}
	r.Get("/ledger", h.listLedger)
	r.Get("/balance", h.getBalance)
	r.Get("/statuses", h.listStatuses)
	r.Post("/entries", h.createEntry)
}

func (h dlaHandler) listLedger(w nethttp.ResponseWriter, r *nethttp.Request) {
	filter, err := parseLedgerFilter(r)
	if err != nil {
		writeDLABadRequest(w, r, err)
		return
	}
	filter.Limit = dlaPageSize + 1

	entries, err := h.service.Ledger(r.Context(), filter)
	if err != nil {
		writeDLAError(w, r, err, "")
		return
	}

	var nextCursor *string
	if len(entries) > dlaPageSize {
		last := entries[dlaPageSize-1]
		cursor := encodeEntryCursor(last)
		nextCursor = &cursor
		entries = entries[:dlaPageSize]
	}
	writeDLAJSON(w, nethttp.StatusOK, ledgerToResponse(entries, nextCursor))
}

func (h dlaHandler) getBalance(w nethttp.ResponseWriter, r *nethttp.Request) {
	director, err := directorFromQuery(r)
	if err != nil {
		writeDLABadRequest(w, r, err)
		return
	}
	status, err := h.service.CurrentStatus(r.Context(), director)
	if err != nil {
		writeDLAError(w, r, err, "")
		return
	}
	writeDLAJSON(w, nethttp.StatusOK, statusToResponse(status))
}

func (h dlaHandler) listStatuses(w nethttp.ResponseWriter, r *nethttp.Request) {
	statuses, err := h.service.Statuses(r.Context())
	if err != nil {
		writeDLAError(w, r, err, "")
		return
	}
	response := make([]balanceResponse, 0, len(statuses))
	for _, status := range statuses {
		response = append(response, statusToResponse(status))
	}
	writeDLAJSON(w, nethttp.StatusOK, map[string]any{"statuses": response})
}

func (h dlaHandler) createEntry(w nethttp.ResponseWriter, r *nethttp.Request) {
	var request entryRequest
	if err := decodeDLAJSON(w, r, &request); err != nil {
		writeDLADecodeError(w, r, err)
		return
	}

	entry, fields, manualDrawing := request.newEntry(h.today())
	if manualDrawing {
		writeManualDrawingProblem(w, r)
		return
	}
	if len(fields) == 0 {
		accountFields, err := h.validateEntryAccounts(r.Context(), entry)
		if err != nil {
			httpserver.WriteError(w, r, err)
			return
		}
		fields = append(fields, accountFields...)
	}
	if len(fields) > 0 {
		writeDLAValidation(w, r, fields)
		return
	}
	if entry.Source == "" {
		source, err := newManualSourceRef()
		if err != nil {
			httpserver.WriteError(w, r, err)
			return
		}
		entry.Source = source
	}

	if err := h.service.AddEntry(r.Context(), entry); err != nil {
		writeDLAError(w, r, err, accountPointerForEntry(entry))
		return
	}
	writeDLAJSON(w, nethttp.StatusCreated, entryCreatedResponse{SourceRef: entry.Source})
}

func (h dlaHandler) today() time.Time {
	now := time.Now().UTC()
	if h.clock != nil {
		now = h.clock.Now().UTC()
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

func parseLedgerFilter(r *nethttp.Request) (LedgerFilter, error) {
	query := r.URL.Query()
	var filter LedgerFilter
	director, err := directorFromQuery(r)
	if err != nil {
		return LedgerFilter{}, err
	}
	filter.Director = director

	if value := strings.TrimSpace(query.Get("from")); value != "" {
		filter.From, err = parseDatePointer("from", value)
		if err != nil {
			return LedgerFilter{}, err
		}
	}
	if value := strings.TrimSpace(query.Get("to")); value != "" {
		filter.To, err = parseDatePointer("to", value)
		if err != nil {
			return LedgerFilter{}, err
		}
	}
	if value := strings.TrimSpace(query.Get("cursor")); value != "" {
		cursor, err := decodeEntryCursor(value)
		if err != nil {
			return LedgerFilter{}, err
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

func directorFromQuery(r *nethttp.Request) (DirectorID, error) {
	value := strings.TrimSpace(r.URL.Query().Get("director"))
	if value == "" {
		return DefaultDirectorID, nil
	}
	director, _, err := normalizeDirectorID(DirectorID(value))
	if err != nil {
		return "", err
	}
	return director, nil
}

func encodeEntryCursor(entry Entry) string {
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

func (r entryRequest) newEntry(today time.Time) (NewEntry, []fieldError, bool) {
	var fields []fieldError

	kind := EntryKind(strings.TrimSpace(string(r.Kind)))
	switch kind {
	case EntryKindDrawing:
		return NewEntry{}, nil, true
	case EntryKindRepayment, EntryKindExpenseOwed:
	default:
		fields = append(fields, fieldError{Pointer: "/kind", Detail: "must be repayment or expense-owed"})
	}

	director, _, err := normalizeDirectorID(DirectorID(r.DirectorID))
	if err != nil {
		fields = append(fields, fieldError{Pointer: "/director_id", Detail: "must be a director-N identifier"})
	}
	date, ok := parseEntryRequestDate(r.Date, today, &fields)
	description := strings.TrimSpace(r.Description)
	if description == "" {
		fields = append(fields, fieldError{Pointer: "/description", Detail: "is required"})
	}
	amount := parseEntryRequestAmount(r.Amount, &fields)

	source := strings.TrimSpace(r.SourceRef)
	if source != "" && !strings.HasPrefix(source, "manual:") {
		fields = append(fields, fieldError{Pointer: "/source_ref", Detail: "must be omitted or start with manual:"})
	}
	entry := NewEntry{
		Director:    director,
		Date:        date,
		Kind:        kind,
		Description: description,
		Amount:      amount,
		Source:      source,
	}
	switch kind {
	case EntryKindRepayment:
		code := ledger.AccountCode(strings.ToLower(strings.TrimSpace(r.CashAccountCode)))
		if code == "" {
			fields = append(fields, fieldError{Pointer: "/cash_account_code", Detail: "is required for repayment entries"})
		}
		entry.CashAccountCode = code
	case EntryKindExpenseOwed:
		code := ledger.AccountCode(strings.ToLower(strings.TrimSpace(r.ExpenseCategory)))
		if code == "" {
			fields = append(fields, fieldError{Pointer: "/expense_category", Detail: "is required for expense-owed entries"})
		}
		entry.ExpenseAccountCode = code
	}
	if !ok {
		entry.Date = time.Time{}
	}
	return entry, fields, false
}

func parseEntryRequestDate(value string, today time.Time, fields *[]fieldError) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		*fields = append(*fields, fieldError{Pointer: "/date", Detail: "is required"})
		return time.Time{}, false
	}
	date, err := time.Parse(time.DateOnly, value)
	if err != nil {
		*fields = append(*fields, fieldError{Pointer: "/date", Detail: "must be a date in YYYY-MM-DD format"})
		return time.Time{}, false
	}
	date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	if date.After(today) {
		*fields = append(*fields, fieldError{Pointer: "/date", Detail: "must not be in the future"})
		return time.Time{}, false
	}
	return date, true
}

func parseEntryRequestAmount(value moneyResponse, fields *[]fieldError) money.Money {
	currency := strings.ToUpper(strings.TrimSpace(value.Currency))
	if value.AmountMinor <= 0 {
		*fields = append(*fields, fieldError{Pointer: "/amount/amount_minor", Detail: "must be greater than zero"})
	}
	if currency != "GBP" {
		*fields = append(*fields, fieldError{Pointer: "/amount/currency", Detail: "must be GBP"})
	}
	return money.Money{Amount: value.AmountMinor, Currency: currency}
}

func (h dlaHandler) validateEntryAccounts(ctx context.Context, entry NewEntry) ([]fieldError, error) {
	if h.service == nil || h.service.ledger == nil {
		return nil, fmt.Errorf("dla: account validation requires ledger")
	}
	accounts, err := h.service.ledger.Accounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("dla: list ledger accounts for entry validation: %w", err)
	}
	byCode := make(map[ledger.AccountCode]ledger.Account, len(accounts))
	for _, account := range accounts {
		byCode[normalizeAccountCode(account.Code)] = account
	}

	switch entry.Kind {
	case EntryKindRepayment:
		return validateManualCounterparty(byCode, entry.CashAccountCode, entry.Amount.Currency, "/cash_account_code", isCashBankAssetAccount, "must be a cash or bank asset account"), nil
	case EntryKindExpenseOwed:
		return validateManualCounterparty(byCode, entry.ExpenseAccountCode, entry.Amount.Currency, "/expense_category", isExpenseAccount, "must be an expense account"), nil
	default:
		return nil, nil
	}
}

func validateManualCounterparty(
	accounts map[ledger.AccountCode]ledger.Account,
	code ledger.AccountCode,
	currency string,
	pointer string,
	accept func(ledger.Account) bool,
	detail string,
) []fieldError {
	normalized := normalizeAccountCode(code)
	if normalized == DLAAccountCode || strings.HasPrefix(string(normalized), string(DLAAccountCode)+"-") {
		return []fieldError{{Pointer: pointer, Detail: "must not be the DLA control account"}}
	}
	account, ok := accounts[normalized]
	if !ok {
		return []fieldError{{Pointer: pointer, Detail: "does not match a ledger account"}}
	}
	if !accept(account) {
		return []fieldError{{Pointer: pointer, Detail: detail}}
	}
	if account.Currency != nil && *account.Currency != currency {
		return []fieldError{{Pointer: pointer, Detail: "account currency does not match amount currency"}}
	}
	return nil
}

func isCashBankAssetAccount(account ledger.Account) bool {
	return account.Type == ledger.AccountTypeAsset && strings.HasPrefix(string(normalizeAccountCode(account.Code)), "1000-cash-")
}

func isExpenseAccount(account ledger.Account) bool {
	return account.Type == ledger.AccountTypeExpense
}

func accountPointerForEntry(entry NewEntry) string {
	switch entry.Kind {
	case EntryKindRepayment:
		return "/cash_account_code"
	case EntryKindExpenseOwed:
		return "/expense_category"
	default:
		return ""
	}
}

func ledgerToResponse(entries []Entry, nextCursor *string) ledgerResponse {
	response := ledgerResponse{
		Entries:    make([]entryResponse, 0, len(entries)),
		NextCursor: nextCursor,
	}
	for _, entry := range entries {
		response.Entries = append(response.Entries, entryToResponse(entry))
	}
	return response
}

func entryToResponse(entry Entry) entryResponse {
	return entryResponse{
		ID:             int64(entry.ID),
		DirectorID:     string(entry.Director),
		Date:           entry.Date.UTC().Format(time.DateOnly),
		Kind:           string(entry.Kind),
		Description:    entry.Description,
		SourceRef:      entry.Source,
		Amount:         moneyToResponse(entry.Amount),
		OwedToYou:      moneyToResponse(entry.OwedToYou),
		Drawn:          moneyToResponse(entry.Drawn),
		RunningBalance: moneyToResponse(entry.RunningBalance),
		BalanceSide:    string(entry.BalanceSide),
		CreatedAt:      entry.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func statusToResponse(status StatusPayload) balanceResponse {
	response := balanceResponse{
		DirectorID:   string(status.DirectorID),
		DirectorName: status.DirectorName,
		Balance:      moneyToResponse(status.Balance),
		Status:       string(status.Status),
		Policy: policyResponse{
			S455Charge:               status.Policy.S455Charge,
			CreditStatusText:         status.Policy.CreditStatusText,
			CreditExplainerTemplate:  status.Policy.CreditExplainerTemplate,
			BIKWarningKey:            status.Policy.BIKWarningTextKey,
			OverdrawnWarningTemplate: status.Policy.OverdrawnWarningTemplate,
			Remedy:                   status.Policy.Remedy,
		},
	}
	if status.Status == StatusOverdrawn {
		clearance := moneyToResponse(status.SuggestedClearanceAmount)
		response.SuggestedClearance = &clearance
	}
	return response
}

func moneyToResponse(value money.Money) moneyResponse {
	return moneyResponse{AmountMinor: value.Amount, Currency: value.Currency}
}

func decodeDLAJSON(w nethttp.ResponseWriter, r *nethttp.Request, dst any) error {
	r.Body = nethttp.MaxBytesReader(w, r.Body, maxEntryJSONBodySize)
	defer func() {
		_ = r.Body.Close()
	}()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		var maxBytesErr *nethttp.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return errEntryRequestBodyTooLarge
		}
		return fmt.Errorf("decode JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		return fmt.Errorf("JSON body must contain one object")
	} else {
		var maxBytesErr *nethttp.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return errEntryRequestBodyTooLarge
		}
		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("decode JSON body: %w", err)
		}
	}
	return nil
}

func newManualSourceRef() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("dla: generate source ref: %w", err)
	}
	return "manual:" + hex.EncodeToString(bytes[:]), nil
}

func writeDLADecodeError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	if errors.Is(err, errEntryRequestBodyTooLarge) {
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeDLAPayloadTooLarge,
			Title:  nethttp.StatusText(nethttp.StatusRequestEntityTooLarge),
			Status: nethttp.StatusRequestEntityTooLarge,
			Detail: "request body is too large",
		})
		return
	}
	writeDLABadRequest(w, r, err)
}

func writeDLAError(w nethttp.ResponseWriter, r *nethttp.Request, err error, accountPointer string) {
	if errors.Is(err, ErrDuplicateSource) {
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeDLADuplicateSource,
			Title:  "Duplicate DLA source",
			Status: nethttp.StatusConflict,
			Detail: "an entry with the same source_ref already exists",
		})
		return
	}
	if errors.Is(err, ledger.ErrAccountNotFound) && accountPointer != "" {
		writeDLAValidation(w, r, []fieldError{{
			Pointer: accountPointer,
			Detail:  "does not match a ledger account",
		}})
		return
	}
	if errors.Is(err, ledger.ErrAccountCurrencyMismatch) && accountPointer != "" {
		writeDLAValidation(w, r, []fieldError{{
			Pointer: accountPointer,
			Detail:  "account currency does not match amount currency",
		}})
		return
	}
	if errors.Is(err, ErrInvalidEntry) {
		writeDLAValidation(w, r, []fieldError{{Pointer: "/", Detail: err.Error()}})
		return
	}
	if errors.Is(err, ErrInvalidLedgerFilter) {
		writeDLABadRequest(w, r, err)
		return
	}
	if errors.Is(err, ErrInvalidDirector) {
		writeDLAValidation(w, r, []fieldError{{Pointer: "/director_id", Detail: err.Error()}})
		return
	}
	httpserver.WriteError(w, r, err)
}

func writeManualDrawingProblem(w nethttp.ResponseWriter, r *nethttp.Request) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeDLAManualDrawing,
		Title:  "Manual drawing rejected",
		Status: nethttp.StatusUnprocessableEntity,
		Detail: "drawings come from banking; reconcile a bank transaction as a director drawing instead",
		Extensions: map[string]any{
			"errors": []fieldError{{
				Pointer: "/kind",
				Detail:  "manual drawing entries are not accepted",
			}},
		},
	})
}

func writeDLAValidation(w nethttp.ResponseWriter, r *nethttp.Request, fieldErrors []fieldError) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeDLAValidation,
		Title:  nethttp.StatusText(nethttp.StatusUnprocessableEntity),
		Status: nethttp.StatusUnprocessableEntity,
		Detail: "DLA entry validation failed",
		Extensions: map[string]any{
			"errors": fieldErrors,
		},
	})
}

func writeDLABadRequest(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeDLABadRequest,
		Title:  nethttp.StatusText(nethttp.StatusBadRequest),
		Status: nethttp.StatusBadRequest,
		Detail: err.Error(),
	})
}

func writeDLAJSON(w nethttp.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		panic(err)
	}
}
