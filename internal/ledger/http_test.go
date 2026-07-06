package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/platform/clock"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

func TestHTTPEntriesFiltersPaginationAndIntegerMoney(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)
	service := New(ledgerPool, discardLedgerBus())
	seedHTTPEndpointEntries(t, ctx, ledgerPool, service, entriesPageSize+1)
	router := newLedgerHTTPTestRouter(t, ledgerPool, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))

	first := performLedgerRequest(router, http.MethodGet, "/api/ledger/entries?from=2026-01-01&to=2026-04-30&source=ledger-http-test&account=4000-sales", true)
	if first.Code != http.StatusOK {
		t.Fatalf("first entries page status = %d, want %d; body=%s", first.Code, http.StatusOK, first.Body.String())
	}
	assertLedgerAmountsAreJSONIntegers(t, first.Body.Bytes())

	firstPage := decodeEntriesResponse(t, first)
	if len(firstPage.Entries) != entriesPageSize {
		t.Fatalf("first entries page count = %d, want %d", len(firstPage.Entries), entriesPageSize)
	}
	if firstPage.NextCursor == nil || *firstPage.NextCursor == "" {
		t.Fatalf("first entries next_cursor = %v, want cursor", firstPage.NextCursor)
	}
	for _, entry := range firstPage.Entries {
		if entry.SourceModule != "ledger-http-test" {
			t.Fatalf("entry source_module = %q, want ledger-http-test", entry.SourceModule)
		}
		assertEntryResponseHasPosting(t, entry, "4000-sales")
		assertEntryResponseHasPosting(t, entry, "1101-debtors-gbp")
	}

	second := performLedgerRequest(router, http.MethodGet, "/api/ledger/entries?from=2026-01-01&to=2026-04-30&source=ledger-http-test&account=4000-sales&cursor="+*firstPage.NextCursor, true)
	if second.Code != http.StatusOK {
		t.Fatalf("second entries page status = %d, want %d; body=%s", second.Code, http.StatusOK, second.Body.String())
	}
	secondPage := decodeEntriesResponse(t, second)
	if len(secondPage.Entries) != 1 {
		t.Fatalf("second entries page count = %d, want 1", len(secondPage.Entries))
	}
	if secondPage.NextCursor != nil {
		t.Fatalf("second entries next_cursor = %q, want nil", *secondPage.NextCursor)
	}
	assertEntryResponseHasPosting(t, secondPage.Entries[0], "4000-sales")
}

func TestHTTPAccountsAndTrialBalance(t *testing.T) {
	_, _, ledgerPool := temporaryMigratedLedgerDatabase(t)
	router := newLedgerHTTPTestRouter(t, ledgerPool, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))

	accountResult := performLedgerRequest(router, http.MethodGet, "/api/ledger/accounts", true)
	if accountResult.Code != http.StatusOK {
		t.Fatalf("accounts status = %d, want %d; body=%s", accountResult.Code, http.StatusOK, accountResult.Body.String())
	}
	var accounts accountsResponse
	if err := json.Unmarshal(accountResult.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("decode accounts: %v; body=%s", err, accountResult.Body.String())
	}
	assertAccountResponse(t, accounts.Accounts, "4000-sales")

	trialBalanceResult := performLedgerRequest(router, http.MethodGet, "/api/ledger/trial-balance", true)
	if trialBalanceResult.Code != http.StatusOK {
		t.Fatalf("trial-balance status = %d, want %d; body=%s", trialBalanceResult.Code, http.StatusOK, trialBalanceResult.Body.String())
	}
	var trialBalance trialBalanceResponse
	if err := json.Unmarshal(trialBalanceResult.Body.Bytes(), &trialBalance); err != nil {
		t.Fatalf("decode trial balance: %v; body=%s", err, trialBalanceResult.Body.String())
	}
	if trialBalance.AsOf != "2026-07-06" || trialBalance.Status != "balanced" {
		t.Fatalf("trial balance = %+v, want as_of 2026-07-06 balanced", trialBalance)
	}
}

func TestHTTPRoutesRequireAuthentication(t *testing.T) {
	_, _, ledgerPool := temporaryMigratedLedgerDatabase(t)
	router := newLedgerHTTPTestRouter(t, ledgerPool, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))

	for _, path := range []string{"/api/ledger/entries", "/api/ledger/accounts", "/api/ledger/trial-balance"} {
		response := performLedgerRequest(router, http.MethodGet, path, false)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want %d; body=%s", path, response.Code, http.StatusUnauthorized, response.Body.String())
		}
		if got := response.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
			t.Fatalf("%s Content-Type = %q, want %s", path, got, httpserver.ProblemContentType)
		}
	}
}

func seedHTTPEndpointEntries(t *testing.T, ctx context.Context, pool *pgxpool.Pool, service *Service, count int) {
	t.Helper()

	for i := 0; i < count; i++ {
		postFixtureEntry(t, ctx, pool, service, NewJournalEntry{
			Date:         time.Date(2026, time.January, 1+i, 0, 0, 0, 0, time.UTC),
			Description:  fmt.Sprintf("HTTP browse fixture %03d", i),
			SourceModule: "ledger-http-test",
			SourceRef:    fmt.Sprintf("http-%03d", i),
			Postings: []NewPosting{
				{AccountCode: "1101-debtors-gbp", Amount: moneyAmount(1000+int64(i), "GBP"), AmountGBP: moneyAmount(1000+int64(i), "GBP")},
				{AccountCode: "4000-sales", Amount: moneyAmount(-1000-int64(i), "GBP"), AmountGBP: moneyAmount(-1000-int64(i), "GBP")},
			},
		})
	}
}

func newLedgerHTTPTestRouter(t *testing.T, pool *pgxpool.Pool, now time.Time) http.Handler {
	t.Helper()

	module, err := NewModule(Config{
		Pool:  pool,
		Bus:   discardLedgerBus(),
		Clock: clock.NewFake(now),
	})
	if err != nil {
		t.Fatalf("NewModule() error = %v", err)
	}

	return httpserver.NewRouter(httpserver.Config{
		Version:          "test",
		DB:               pingerFunc(func(context.Context) error { return nil }),
		APIAuth:          ledgerTestAuthMiddleware,
		Modules:          []httpserver.Module{module.HTTPModule()},
		OpenAPIFragments: []httpserver.OpenAPIFragment{module.OpenAPIFragment()},
	})
}

func performLedgerRequest(router http.Handler, method string, path string, authenticated bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if authenticated {
		request.AddCookie(&http.Cookie{Name: "test_session", Value: "ok"})
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func decodeEntriesResponse(t *testing.T, response *httptest.ResponseRecorder) entriesResponse {
	t.Helper()

	var body entriesResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode entries response: %v; body=%s", err, response.Body.String())
	}
	return body
}

func assertEntryResponseHasPosting(t *testing.T, entry entryResponse, accountCode string) {
	t.Helper()

	for _, posting := range entry.Postings {
		if posting.AccountCode == accountCode {
			return
		}
	}
	t.Fatalf("entry %d postings = %+v, want account %s", entry.ID, entry.Postings, accountCode)
}

func assertAccountResponse(t *testing.T, accounts []accountResponse, code string) {
	t.Helper()

	for _, account := range accounts {
		if account.Code == code {
			return
		}
	}
	t.Fatalf("account %q missing from %+v", code, accounts)
}

func assertLedgerAmountsAreJSONIntegers(t *testing.T, body []byte) {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("decode JSON with numbers: %v; body=%s", err, string(body))
	}

	amountCount := 0
	walkJSON(t, decoded, func(key string, value any) {
		if key != "amount_minor" {
			return
		}
		amountCount++
		number, ok := value.(json.Number)
		if !ok {
			t.Fatalf("amount_minor JSON value = %T(%v), want number", value, value)
		}
		if strings.Contains(number.String(), ".") {
			t.Fatalf("amount_minor JSON value %q contains decimal point", number.String())
		}
	})
	if amountCount == 0 {
		t.Fatalf("response contained no amount_minor fields: %s", string(body))
	}
}

func walkJSON(t *testing.T, value any, visit func(string, any)) {
	t.Helper()

	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			visit(key, child)
			walkJSON(t, child, visit)
		}
	case []any:
		for _, child := range typed {
			walkJSON(t, child, visit)
		}
	}
}

func ledgerTestAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz", "/api/openapi.json":
			next.ServeHTTP(w, r)
			return
		}
		if _, err := r.Cookie("test_session"); err != nil {
			httpserver.WriteProblem(w, r, httpserver.Problem{
				Type:   "https://ledgerly.local/problems/unauthenticated",
				Title:  http.StatusText(http.StatusUnauthorized),
				Status: http.StatusUnauthorized,
				Detail: "authentication required",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

type pingerFunc func(context.Context) error

func (f pingerFunc) PingContext(ctx context.Context) error {
	return f(ctx)
}
