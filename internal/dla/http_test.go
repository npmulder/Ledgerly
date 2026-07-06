package dla

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const dlaHTTPCashAccount ledger.AccountCode = "1000-dla-http-cash"

func TestMain(m *testing.M) {
	os.Exit(testdb.Main(m))
}

func TestHTTPDLALedgerPaginationAndIntegerMoney(t *testing.T) {
	fixture := newDLAHTTPFixture(t, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	seedDLALedgerEntries(t, fixture, dlaPageSize+1)

	first := performDLARequest(fixture.router, nethttp.MethodGet, "/api/dla/ledger?from=2026-01-01&to=2026-04-30", nil, true)
	if first.Code != nethttp.StatusOK {
		t.Fatalf("first ledger page status = %d, want %d; body=%s", first.Code, nethttp.StatusOK, first.Body.String())
	}
	assertDLAAmountsAreJSONIntegers(t, first.Body.Bytes())

	firstPage := decodeDLALedgerResponse(t, first)
	if len(firstPage.Entries) != dlaPageSize {
		t.Fatalf("first ledger page count = %d, want %d", len(firstPage.Entries), dlaPageSize)
	}
	if firstPage.NextCursor == nil || *firstPage.NextCursor == "" {
		t.Fatalf("first ledger next_cursor = %v, want cursor", firstPage.NextCursor)
	}
	assertDLAEntryResponse(t, firstPage.Entries[0], entryResponse{
		Date:           "2026-01-01",
		Kind:           string(EntryKindDrawing),
		SourceRef:      "banking:dla-http-000",
		Amount:         moneyResponse{AmountMinor: 1000, Currency: "GBP"},
		OwedToYou:      moneyResponse{AmountMinor: 0, Currency: "GBP"},
		Drawn:          moneyResponse{AmountMinor: 1000, Currency: "GBP"},
		RunningBalance: moneyResponse{AmountMinor: -1000, Currency: "GBP"},
		BalanceSide:    string(BalanceSideDebit),
	})
	assertDLAEntryResponse(t, firstPage.Entries[1], entryResponse{
		Date:           "2026-01-02",
		Kind:           string(EntryKindExpenseOwed),
		SourceRef:      "manual:dla-http-001",
		Amount:         moneyResponse{AmountMinor: 250, Currency: "GBP"},
		OwedToYou:      moneyResponse{AmountMinor: 250, Currency: "GBP"},
		Drawn:          moneyResponse{AmountMinor: 0, Currency: "GBP"},
		RunningBalance: moneyResponse{AmountMinor: -750, Currency: "GBP"},
		BalanceSide:    string(BalanceSideDebit),
	})

	second := performDLARequest(fixture.router, nethttp.MethodGet, "/api/dla/ledger?from=2026-01-01&to=2026-04-30&cursor="+*firstPage.NextCursor, nil, true)
	if second.Code != nethttp.StatusOK {
		t.Fatalf("second ledger page status = %d, want %d; body=%s", second.Code, nethttp.StatusOK, second.Body.String())
	}
	secondPage := decodeDLALedgerResponse(t, second)
	if len(secondPage.Entries) != 1 {
		t.Fatalf("second ledger page count = %d, want 1", len(secondPage.Entries))
	}
	if secondPage.NextCursor != nil {
		t.Fatalf("second ledger next_cursor = %q, want nil", *secondPage.NextCursor)
	}
	if secondPage.Entries[0].SourceRef != "manual:dla-http-100" {
		t.Fatalf("second ledger source_ref = %q, want manual:dla-http-100", secondPage.Entries[0].SourceRef)
	}
}

func TestHTTPDLABalancePayloadForCreditAndOverdrawn(t *testing.T) {
	fixture := newDLAHTTPFixture(t, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))

	credit := performDLARequest(fixture.router, nethttp.MethodGet, "/api/dla/balance", nil, true)
	if credit.Code != nethttp.StatusOK {
		t.Fatalf("credit balance status = %d, want %d; body=%s", credit.Code, nethttp.StatusOK, credit.Body.String())
	}
	creditBody := decodeDLABalanceResponse(t, credit)
	if creditBody.Balance != (moneyResponse{AmountMinor: 0, Currency: "GBP"}) || creditBody.Status != string(StatusCredit) {
		t.Fatalf("credit balance = %#v/%s, want zero credit", creditBody.Balance, creditBody.Status)
	}
	if creditBody.Policy.BIKWarningKey != "benefit_in_kind_interest_free" || creditBody.Policy.Remedy != "clear_with_dividend" {
		t.Fatalf("credit policy = %#v, want Isle of Man DLA policy", creditBody.Policy)
	}
	if creditBody.SuggestedClearance != nil {
		t.Fatalf("credit suggested_clearance = %#v, want omitted", creditBody.SuggestedClearance)
	}

	fixture.fileDrawing(t, "banking:balance-overdrawn", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), 1234)
	overdrawn := performDLARequest(fixture.router, nethttp.MethodGet, "/api/dla/balance", nil, true)
	if overdrawn.Code != nethttp.StatusOK {
		t.Fatalf("overdrawn balance status = %d, want %d; body=%s", overdrawn.Code, nethttp.StatusOK, overdrawn.Body.String())
	}
	overdrawnBody := decodeDLABalanceResponse(t, overdrawn)
	if overdrawnBody.Balance != (moneyResponse{AmountMinor: -1234, Currency: "GBP"}) || overdrawnBody.Status != string(StatusOverdrawn) {
		t.Fatalf("overdrawn balance = %#v/%s, want -1234 overdrawn", overdrawnBody.Balance, overdrawnBody.Status)
	}
	if overdrawnBody.SuggestedClearance == nil || *overdrawnBody.SuggestedClearance != (moneyResponse{AmountMinor: 1234, Currency: "GBP"}) {
		t.Fatalf("overdrawn suggested_clearance = %#v, want 1234 GBP", overdrawnBody.SuggestedClearance)
	}
}

func TestHTTPDLACreateManualEntries(t *testing.T) {
	fixture := newDLAHTTPFixture(t, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))

	expenseBody := map[string]any{
		"date":             "2026-07-05",
		"kind":             string(EntryKindExpenseOwed),
		"description":      "Director paid software personally",
		"amount":           map[string]any{"amount_minor": 4900, "currency": "GBP"},
		"expense_category": "5010-software",
	}
	expense := performDLARequest(fixture.router, nethttp.MethodPost, "/api/dla/entries", mustJSON(t, expenseBody), true)
	if expense.Code != nethttp.StatusCreated {
		t.Fatalf("expense create status = %d, want %d; body=%s", expense.Code, nethttp.StatusCreated, expense.Body.String())
	}
	expenseCreated := decodeDLAEntryCreatedResponse(t, expense)
	if !strings.HasPrefix(expenseCreated.SourceRef, "manual:") {
		t.Fatalf("expense source_ref = %q, want generated manual ref", expenseCreated.SourceRef)
	}

	repaymentBody := map[string]any{
		"date":              "2026-07-06",
		"kind":              string(EntryKindRepayment),
		"description":       "Director repaid part of DLA",
		"amount":            map[string]any{"amount_minor": 1200, "currency": "GBP"},
		"cash_account_code": string(dlaHTTPCashAccount),
		"source_ref":        "manual:repayment-from-http",
	}
	repayment := performDLARequest(fixture.router, nethttp.MethodPost, "/api/dla/entries", mustJSON(t, repaymentBody), true)
	if repayment.Code != nethttp.StatusCreated {
		t.Fatalf("repayment create status = %d, want %d; body=%s", repayment.Code, nethttp.StatusCreated, repayment.Body.String())
	}

	ledgerResponse := performDLARequest(fixture.router, nethttp.MethodGet, "/api/dla/ledger", nil, true)
	page := decodeDLALedgerResponse(t, ledgerResponse)
	gotRefs := []string{}
	for _, entry := range page.Entries {
		gotRefs = append(gotRefs, entry.SourceRef)
	}
	if wantRefs := []string{expenseCreated.SourceRef, "manual:repayment-from-http"}; !reflect.DeepEqual(gotRefs, wantRefs) {
		t.Fatalf("ledger source refs = %#v, want %#v", gotRefs, wantRefs)
	}
}

func TestHTTPDLADrawingRejectedAndInvalidEntriesHaveFieldPointers(t *testing.T) {
	fixture := newDLAHTTPFixture(t, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))

	drawingBody := map[string]any{
		"date":              "2026-07-05",
		"kind":              string(EntryKindDrawing),
		"description":       "Cash withdrawal",
		"amount":            map[string]any{"amount_minor": 1000, "currency": "GBP"},
		"cash_account_code": string(dlaHTTPCashAccount),
	}
	drawing := performDLARequest(fixture.router, nethttp.MethodPost, "/api/dla/entries", mustJSON(t, drawingBody), true)
	if drawing.Code != nethttp.StatusUnprocessableEntity {
		t.Fatalf("drawing status = %d, want %d; body=%s", drawing.Code, nethttp.StatusUnprocessableEntity, drawing.Body.String())
	}
	if got := drawing.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
		t.Fatalf("drawing content-type = %q, want %s", got, httpserver.ProblemContentType)
	}
	drawingProblem := decodeDLAProblem(t, drawing)
	if !strings.Contains(drawingProblem.Detail, "drawings come from banking") {
		t.Fatalf("drawing detail = %q, want banking explanation", drawingProblem.Detail)
	}
	assertDLAProblemPointers(t, drawingProblem, "/kind")

	invalidBody := map[string]any{
		"date":        "2026-07-07",
		"kind":        string(EntryKindExpenseOwed),
		"description": " ",
		"amount":      map[string]any{"amount_minor": 0, "currency": "GBP"},
	}
	invalid := performDLARequest(fixture.router, nethttp.MethodPost, "/api/dla/entries", mustJSON(t, invalidBody), true)
	if invalid.Code != nethttp.StatusUnprocessableEntity {
		t.Fatalf("invalid status = %d, want %d; body=%s", invalid.Code, nethttp.StatusUnprocessableEntity, invalid.Body.String())
	}
	invalidProblem := decodeDLAProblem(t, invalid)
	assertDLAProblemPointers(t, invalidProblem, "/date", "/description", "/amount/amount_minor", "/expense_category")
}

func TestHTTPDLARoutesRequireAuthentication(t *testing.T) {
	fixture := newDLAHTTPFixture(t, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))

	for _, request := range []struct {
		method string
		path   string
		body   io.Reader
	}{
		{method: nethttp.MethodGet, path: "/api/dla/ledger"},
		{method: nethttp.MethodGet, path: "/api/dla/balance"},
		{method: nethttp.MethodPost, path: "/api/dla/entries", body: mustJSON(t, map[string]any{})},
	} {
		response := performDLARequest(fixture.router, request.method, request.path, request.body, false)
		if response.Code != nethttp.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want %d; body=%s", request.method, request.path, response.Code, nethttp.StatusUnauthorized, response.Body.String())
		}
		if got := response.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
			t.Fatalf("%s %s Content-Type = %q, want %s", request.method, request.path, got, httpserver.ProblemContentType)
		}
	}
}

func TestDLAOpenAPIFragmentDocumentsHTTPPaths(t *testing.T) {
	document := httpserver.OpenAPIDocument("test", OpenAPIFragment())
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatalf("openapi paths missing or wrong type: %+v", document["paths"])
	}
	for _, path := range []string{"/api/dla/ledger", "/api/dla/balance", "/api/dla/entries"} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("openapi path %s missing from %+v", path, paths)
		}
	}
}

type dlaHTTPFixture struct {
	ctx         context.Context
	router      nethttp.Handler
	dlaPool     *pgxpool.Pool
	bankingPool *pgxpool.Pool
	service     *Service
}

func newDLAHTTPFixture(t *testing.T, now time.Time) dlaHTTPFixture {
	t.Helper()

	if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("LoadActive() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	dlaPool := testdb.AsModule(t, ModuleName)
	bankingPool := testdb.AsModule(t, "banking")
	ledgerPool := testdb.AsModule(t, ledger.ModuleName)
	eventBus := bus.New()
	ledgerService := ledger.New(ledgerPool, eventBus)
	ensureDLATestLedgerAccount(t, ctx, ledgerPool, ledgerService)

	module, err := NewModule(Config{
		Pool:   dlaPool,
		Bus:    eventBus,
		Clock:  clock.NewFake(now),
		Ledger: ledgerService,
	})
	if err != nil {
		t.Fatalf("NewModule() error = %v", err)
	}
	router := httpserver.NewRouter(httpserver.Config{
		APIAuth:          dlaTestAuthMiddleware,
		Modules:          []httpserver.Module{module.HTTPModule()},
		OpenAPIFragments: []httpserver.OpenAPIFragment{module.OpenAPIFragment()},
	})
	return dlaHTTPFixture{
		ctx:         ctx,
		router:      router,
		dlaPool:     dlaPool,
		bankingPool: bankingPool,
		service:     module.service,
	}
}

func seedDLALedgerEntries(t *testing.T, fixture dlaHTTPFixture, count int) {
	t.Helper()

	for i := 0; i < count; i++ {
		date := time.Date(2026, time.January, 1+i, 0, 0, 0, 0, time.UTC)
		switch i {
		case 0:
			fixture.fileDrawing(t, "banking:dla-http-000", date, 1000)
		case 1:
			fixture.addEntry(t, NewEntry{
				Date:               date,
				Kind:               EntryKindExpenseOwed,
				Description:        "HTTP DLA expense fixture",
				Amount:             gbp(250),
				Source:             "manual:dla-http-001",
				ExpenseAccountCode: "5010-software",
			})
		default:
			fixture.addEntry(t, NewEntry{
				Date:            date,
				Kind:            EntryKindRepayment,
				Description:     fmt.Sprintf("HTTP DLA repayment fixture %03d", i),
				Amount:          gbp(100),
				Source:          fmt.Sprintf("manual:dla-http-%03d", i),
				CashAccountCode: dlaHTTPCashAccount,
			})
		}
	}
}

func (f dlaHTTPFixture) addEntry(t *testing.T, entry NewEntry) {
	t.Helper()
	if err := f.service.AddEntry(f.ctx, entry); err != nil {
		t.Fatalf("AddEntry(%s) error = %v", entry.Source, err)
	}
}

func (f dlaHTTPFixture) fileDrawing(t *testing.T, source string, date time.Time, amount int64) {
	t.Helper()

	tx, err := f.bankingPool.Begin(f.ctx)
	if err != nil {
		t.Fatalf("Begin() drawing error = %v", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()
	if err := f.service.FileDrawing(f.ctx, tx, TxnRef{
		Ref:             source,
		Date:            date,
		Amount:          gbp(amount),
		CashAccountCode: dlaHTTPCashAccount,
		Description:     "HTTP DLA drawing fixture",
	}); err != nil {
		t.Fatalf("FileDrawing(%s) error = %v", source, err)
	}
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatalf("Commit() drawing error = %v", err)
	}
}

func ensureDLATestLedgerAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, service *ledger.Service) {
	t.Helper()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() ensure account error = %v", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()
	currency := "GBP"
	if _, err := service.EnsureAccount(ctx, tx, ledger.AccountSpec{
		Code:     dlaHTTPCashAccount,
		Name:     "DLA HTTP cash account",
		Type:     ledger.AccountTypeAsset,
		Currency: &currency,
	}); err != nil {
		t.Fatalf("EnsureAccount(%s) error = %v", dlaHTTPCashAccount, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit() ensure account error = %v", err)
	}
}

func performDLARequest(handler nethttp.Handler, method string, path string, body io.Reader, authenticated bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, body)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		request.Header.Set("X-Test-Auth", "ok")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func dlaTestAuthMiddleware(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz", "/api/openapi.json":
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("X-Test-Auth") != "ok" {
			httpserver.WriteProblem(w, r, httpserver.Problem{
				Type:   "https://ledgerly.local/problems/test-auth",
				Title:  nethttp.StatusText(nethttp.StatusUnauthorized),
				Status: nethttp.StatusUnauthorized,
				Detail: "authentication required",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func decodeDLALedgerResponse(t *testing.T, response *httptest.ResponseRecorder) ledgerResponse {
	t.Helper()
	var body ledgerResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode ledger response: %v; body=%s", err, response.Body.String())
	}
	return body
}

func decodeDLABalanceResponse(t *testing.T, response *httptest.ResponseRecorder) balanceResponse {
	t.Helper()
	var body balanceResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode balance response: %v; body=%s", err, response.Body.String())
	}
	return body
}

func decodeDLAEntryCreatedResponse(t *testing.T, response *httptest.ResponseRecorder) entryCreatedResponse {
	t.Helper()
	var body entryCreatedResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode created response: %v; body=%s", err, response.Body.String())
	}
	return body
}

func decodeDLAProblem(t *testing.T, response *httptest.ResponseRecorder) struct {
	Type   string       `json:"type"`
	Title  string       `json:"title"`
	Status int          `json:"status"`
	Detail string       `json:"detail"`
	Errors []fieldError `json:"errors"`
} {
	t.Helper()

	var problem struct {
		Type   string       `json:"type"`
		Title  string       `json:"title"`
		Status int          `json:"status"`
		Detail string       `json:"detail"`
		Errors []fieldError `json:"errors"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem response: %v; body=%s", err, response.Body.String())
	}
	return problem
}

func assertDLAProblemPointers(t *testing.T, problem struct {
	Type   string       `json:"type"`
	Title  string       `json:"title"`
	Status int          `json:"status"`
	Detail string       `json:"detail"`
	Errors []fieldError `json:"errors"`
}, want ...string) {
	t.Helper()

	got := make([]string, 0, len(problem.Errors))
	for _, field := range problem.Errors {
		got = append(got, field.Pointer)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("problem pointers = %#v, want %#v; problem=%+v", got, want, problem)
	}
}

func assertDLAEntryResponse(t *testing.T, got entryResponse, want entryResponse) {
	t.Helper()

	if got.Date != want.Date ||
		got.Kind != want.Kind ||
		got.SourceRef != want.SourceRef ||
		got.Amount != want.Amount ||
		got.OwedToYou != want.OwedToYou ||
		got.Drawn != want.Drawn ||
		got.RunningBalance != want.RunningBalance ||
		got.BalanceSide != want.BalanceSide {
		t.Fatalf("DLA entry = %#v, want matching %#v", got, want)
	}
}

func assertDLAAmountsAreJSONIntegers(t *testing.T, body []byte) {
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

func mustJSON(t *testing.T, body map[string]any) io.Reader {
	t.Helper()

	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal JSON body: %v", err)
	}
	return bytes.NewReader(encoded)
}

func gbp(amount int64) money.Money {
	return money.Money{Amount: amount, Currency: "GBP"}
}
