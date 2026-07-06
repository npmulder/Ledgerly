package ledger

import (
	"encoding/base64"
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
	entriesPageSize      = DefaultEntriesLimit
	problemTypeBadLedger = "https://ledgerly.local/problems/bad-request"
)

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
	accounts, err := h.service.Accounts(r.Context())
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	writeLedgerJSON(w, nethttp.StatusOK, accountsToResponse(accounts))
}

func (h ledgerHandler) getTrialBalance(w nethttp.ResponseWriter, r *nethttp.Request) {
	asOf := h.clock.Now().UTC()
	report, err := h.service.TrialBalance(r.Context(), asOf)
	if err != nil {
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
		response.Accounts = append(response.Accounts, accountResponse{
			ID:        int64(account.ID),
			Code:      string(account.Code),
			Name:      account.Name,
			Type:      string(account.Type),
			Currency:  account.Currency,
			CreatedAt: account.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return response
}

func trialBalanceToResponse(report TrialBalance) trialBalanceResponse {
	response := trialBalanceResponse{
		AsOf:         report.AsOf.UTC().Format(time.DateOnly),
		Status:       string(report.Status),
		NativeTotals: make([]moneyResponse, 0, len(report.Native)),
		AmountGBP:    moneyToResponse(report.AmountGBP.Amount, report.AmountGBP.Currency),
	}
	for _, amount := range report.Native {
		response.NativeTotals = append(response.NativeTotals, moneyToResponse(amount.Amount, amount.Currency))
	}
	return response
}

func moneyToResponse(amount int64, currency string) moneyResponse {
	return moneyResponse{AmountMinor: amount, Currency: currency}
}

func writeLedgerError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	if errors.Is(err, ErrInvalidEntryFilter) || errors.Is(err, ErrInvalidEntryDate) {
		writeLedgerBadRequest(w, r, err)
		return
	}
	httpserver.WriteError(w, r, err)
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
