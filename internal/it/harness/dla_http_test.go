package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/it/harness"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

func TestHTTPDLALedgerPaginationAndIntegerMoney(t *testing.T) {
	fixture := newDLAHTTPFixture(t, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	seedDLALedgerEntries(t, fixture, dla.DefaultLedgerLimit+1)

	first := performDLARequest(t, fixture.harness, nethttp.MethodGet, "/api/dla/ledger?from=2026-01-01&to=2026-04-30", nil, true)
	if first.StatusCode != nethttp.StatusOK {
		t.Fatalf("first ledger page status = %d, want %d; body=%s", first.StatusCode, nethttp.StatusOK, first.BodyString())
	}
	assertDLAAmountsAreJSONIntegers(t, first.Body)

	firstPage := decodeDLALedgerResponse(t, first)
	if len(firstPage.Entries) != dla.DefaultLedgerLimit {
		t.Fatalf("first ledger page count = %d, want %d", len(firstPage.Entries), dla.DefaultLedgerLimit)
	}
	if firstPage.NextCursor == nil || *firstPage.NextCursor == "" {
		t.Fatalf("first ledger next_cursor = %v, want cursor", firstPage.NextCursor)
	}
	assertDLAEntryResponse(t, firstPage.Entries[0], dlaEntryResponse{
		Date:           "2026-01-01",
		Kind:           string(dla.EntryKindDrawing),
		SourceRef:      "banking:dla-http-000",
		Amount:         dlaMoneyResponse{AmountMinor: 1000, Currency: "GBP"},
		OwedToYou:      dlaMoneyResponse{AmountMinor: 0, Currency: "GBP"},
		Drawn:          dlaMoneyResponse{AmountMinor: 1000, Currency: "GBP"},
		RunningBalance: dlaMoneyResponse{AmountMinor: -1000, Currency: "GBP"},
		BalanceSide:    string(dla.BalanceSideDebit),
	})
	assertDLAEntryResponse(t, firstPage.Entries[1], dlaEntryResponse{
		Date:           "2026-01-02",
		Kind:           string(dla.EntryKindExpenseOwed),
		SourceRef:      "manual:dla-http-001",
		Amount:         dlaMoneyResponse{AmountMinor: 250, Currency: "GBP"},
		OwedToYou:      dlaMoneyResponse{AmountMinor: 250, Currency: "GBP"},
		Drawn:          dlaMoneyResponse{AmountMinor: 0, Currency: "GBP"},
		RunningBalance: dlaMoneyResponse{AmountMinor: -750, Currency: "GBP"},
		BalanceSide:    string(dla.BalanceSideDebit),
	})

	second := performDLARequest(t, fixture.harness, nethttp.MethodGet, "/api/dla/ledger?from=2026-01-01&to=2026-04-30&cursor="+*firstPage.NextCursor, nil, true)
	if second.StatusCode != nethttp.StatusOK {
		t.Fatalf("second ledger page status = %d, want %d; body=%s", second.StatusCode, nethttp.StatusOK, second.BodyString())
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

	credit := performDLARequest(t, fixture.harness, nethttp.MethodGet, "/api/dla/balance", nil, true)
	if credit.StatusCode != nethttp.StatusOK {
		t.Fatalf("credit balance status = %d, want %d; body=%s", credit.StatusCode, nethttp.StatusOK, credit.BodyString())
	}
	creditBody := decodeDLABalanceResponse(t, credit)
	if creditBody.Balance != (dlaMoneyResponse{AmountMinor: 0, Currency: "GBP"}) || creditBody.Status != string(dla.StatusCredit) {
		t.Fatalf("credit balance = %#v/%s, want zero credit", creditBody.Balance, creditBody.Status)
	}
	if creditBody.Policy.BIKWarningKey != "benefit_in_kind_interest_free" || creditBody.Policy.Remedy != "clear_with_dividend" {
		t.Fatalf("credit policy = %#v, want Isle of Man DLA policy", creditBody.Policy)
	}
	if creditBody.SuggestedClearance != nil {
		t.Fatalf("credit suggested_clearance = %#v, want omitted", creditBody.SuggestedClearance)
	}

	fixture.fileDrawingFromBanking(t, dla.TxnRef{
		Ref:             "banking:balance-overdrawn",
		Date:            time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		Amount:          gbp(1234),
		CashAccountCode: dlaCashAccount,
		Description:     "HTTP DLA drawing fixture",
	})
	overdrawn := performDLARequest(t, fixture.harness, nethttp.MethodGet, "/api/dla/balance", nil, true)
	if overdrawn.StatusCode != nethttp.StatusOK {
		t.Fatalf("overdrawn balance status = %d, want %d; body=%s", overdrawn.StatusCode, nethttp.StatusOK, overdrawn.BodyString())
	}
	overdrawnBody := decodeDLABalanceResponse(t, overdrawn)
	if overdrawnBody.Balance != (dlaMoneyResponse{AmountMinor: -1234, Currency: "GBP"}) || overdrawnBody.Status != string(dla.StatusOverdrawn) {
		t.Fatalf("overdrawn balance = %#v/%s, want -1234 overdrawn", overdrawnBody.Balance, overdrawnBody.Status)
	}
	if overdrawnBody.SuggestedClearance == nil || *overdrawnBody.SuggestedClearance != (dlaMoneyResponse{AmountMinor: 1234, Currency: "GBP"}) {
		t.Fatalf("overdrawn suggested_clearance = %#v, want 1234 GBP", overdrawnBody.SuggestedClearance)
	}
}

func TestHTTPDLACreateManualEntries(t *testing.T) {
	fixture := newDLAHTTPFixture(t, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))

	expenseBody := map[string]any{
		"date":             "2026-07-05",
		"kind":             string(dla.EntryKindExpenseOwed),
		"description":      "Director paid software personally",
		"amount":           map[string]any{"amount_minor": 4900, "currency": "GBP"},
		"expense_category": "5010-software",
	}
	expense := performDLARequest(t, fixture.harness, nethttp.MethodPost, "/api/dla/entries", mustJSON(t, expenseBody), true)
	if expense.StatusCode != nethttp.StatusCreated {
		t.Fatalf("expense create status = %d, want %d; body=%s", expense.StatusCode, nethttp.StatusCreated, expense.BodyString())
	}
	expenseCreated := decodeDLAEntryCreatedResponse(t, expense)
	if !strings.HasPrefix(expenseCreated.SourceRef, "manual:") {
		t.Fatalf("expense source_ref = %q, want generated manual ref", expenseCreated.SourceRef)
	}

	repaymentBody := map[string]any{
		"date":              "2026-07-06",
		"kind":              string(dla.EntryKindRepayment),
		"description":       "Director repaid part of DLA",
		"amount":            map[string]any{"amount_minor": 1200, "currency": "GBP"},
		"cash_account_code": string(dlaCashAccount),
		"source_ref":        "manual:repayment-from-http",
	}
	repayment := performDLARequest(t, fixture.harness, nethttp.MethodPost, "/api/dla/entries", mustJSON(t, repaymentBody), true)
	if repayment.StatusCode != nethttp.StatusCreated {
		t.Fatalf("repayment create status = %d, want %d; body=%s", repayment.StatusCode, nethttp.StatusCreated, repayment.BodyString())
	}

	ledgerResponse := performDLARequest(t, fixture.harness, nethttp.MethodGet, "/api/dla/ledger", nil, true)
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
		"kind":              string(dla.EntryKindDrawing),
		"description":       "Cash withdrawal",
		"amount":            map[string]any{"amount_minor": 1000, "currency": "GBP"},
		"cash_account_code": string(dlaCashAccount),
	}
	drawing := performDLARequest(t, fixture.harness, nethttp.MethodPost, "/api/dla/entries", mustJSON(t, drawingBody), true)
	if drawing.StatusCode != nethttp.StatusUnprocessableEntity {
		t.Fatalf("drawing status = %d, want %d; body=%s", drawing.StatusCode, nethttp.StatusUnprocessableEntity, drawing.BodyString())
	}
	if got := drawing.Header.Get("Content-Type"); got != httpserver.ProblemContentType {
		t.Fatalf("drawing content-type = %q, want %s", got, httpserver.ProblemContentType)
	}
	drawingProblem := decodeDLAProblem(t, drawing)
	if !strings.Contains(drawingProblem.Detail, "drawings come from banking") {
		t.Fatalf("drawing detail = %q, want banking explanation", drawingProblem.Detail)
	}
	assertDLAProblemPointers(t, drawingProblem, "/kind")

	invalidBody := map[string]any{
		"date":        "2026-07-07",
		"kind":        string(dla.EntryKindExpenseOwed),
		"description": " ",
		"amount":      map[string]any{"amount_minor": 0, "currency": "GBP"},
	}
	invalid := performDLARequest(t, fixture.harness, nethttp.MethodPost, "/api/dla/entries", mustJSON(t, invalidBody), true)
	if invalid.StatusCode != nethttp.StatusUnprocessableEntity {
		t.Fatalf("invalid status = %d, want %d; body=%s", invalid.StatusCode, nethttp.StatusUnprocessableEntity, invalid.BodyString())
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
		response := performDLARequest(t, fixture.harness, request.method, request.path, request.body, false)
		if response.StatusCode != nethttp.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want %d; body=%s", request.method, request.path, response.StatusCode, nethttp.StatusUnauthorized, response.BodyString())
		}
		if got := response.Header.Get("Content-Type"); got != httpserver.ProblemContentType {
			t.Fatalf("%s %s Content-Type = %q, want %s", request.method, request.path, got, httpserver.ProblemContentType)
		}
	}
}

func TestDLAOpenAPIFragmentDocumentsHTTPPaths(t *testing.T) {
	document := httpserver.OpenAPIDocument("test", dla.OpenAPIFragment())
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
	harness *harness.Harness
	dlaFixture
}

type dlaHTTPResponse struct {
	StatusCode int
	Header     nethttp.Header
	Body       []byte
}

func (r dlaHTTPResponse) BodyString() string {
	return string(r.Body)
}

type dlaLedgerResponse struct {
	Entries    []dlaEntryResponse `json:"entries"`
	NextCursor *string            `json:"next_cursor"`
}

type dlaEntryResponse struct {
	ID             int64            `json:"id"`
	Date           string           `json:"date"`
	Kind           string           `json:"kind"`
	Description    string           `json:"description"`
	SourceRef      string           `json:"source_ref"`
	Amount         dlaMoneyResponse `json:"amount"`
	OwedToYou      dlaMoneyResponse `json:"owed_to_you"`
	Drawn          dlaMoneyResponse `json:"drawn"`
	RunningBalance dlaMoneyResponse `json:"running_balance"`
	BalanceSide    string           `json:"balance_side"`
	CreatedAt      string           `json:"created_at"`
}

type dlaBalanceResponse struct {
	Balance            dlaMoneyResponse  `json:"balance"`
	Status             string            `json:"status"`
	Policy             dlaPolicyResponse `json:"policy"`
	SuggestedClearance *dlaMoneyResponse `json:"suggested_clearance"`
}

type dlaPolicyResponse struct {
	S455Charge    bool   `json:"s455_charge"`
	BIKWarningKey string `json:"bik_warning_key"`
	Remedy        string `json:"remedy"`
}

type dlaEntryCreatedResponse struct {
	SourceRef string `json:"source_ref"`
}

type dlaMoneyResponse struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

type dlaFieldError struct {
	Pointer string `json:"pointer"`
	Detail  string `json:"detail"`
}

func newDLAHTTPFixture(t *testing.T, now time.Time) dlaHTTPFixture {
	t.Helper()

	h := harness.New(t, harness.Options{ClockStart: now})
	return dlaHTTPFixture{
		harness:    h,
		dlaFixture: newDLAFixtureFromHarness(t, h),
	}
}

func seedDLALedgerEntries(t *testing.T, fixture dlaHTTPFixture, count int) {
	t.Helper()

	for i := 0; i < count; i++ {
		date := time.Date(2026, time.January, 1+i, 0, 0, 0, 0, time.UTC)
		switch i {
		case 0:
			fixture.fileDrawingFromBanking(t, dla.TxnRef{
				Ref:             "banking:dla-http-000",
				Date:            date,
				Amount:          gbp(1000),
				CashAccountCode: dlaCashAccount,
				Description:     "HTTP DLA drawing fixture",
			})
		case 1:
			fixture.addHTTPEntry(t, dla.NewEntry{
				Date:               date,
				Kind:               dla.EntryKindExpenseOwed,
				Description:        "HTTP DLA expense fixture",
				Amount:             gbp(250),
				Source:             "manual:dla-http-001",
				ExpenseAccountCode: "5010-software",
			})
		default:
			fixture.addHTTPEntry(t, dla.NewEntry{
				Date:            date,
				Kind:            dla.EntryKindRepayment,
				Description:     fmt.Sprintf("HTTP DLA repayment fixture %03d", i),
				Amount:          gbp(100),
				Source:          fmt.Sprintf("manual:dla-http-%03d", i),
				CashAccountCode: dlaCashAccount,
			})
		}
	}
}

func (f dlaHTTPFixture) addHTTPEntry(t *testing.T, entry dla.NewEntry) {
	t.Helper()
	if err := f.dla.AddEntry(f.ctx, entry); err != nil {
		t.Fatalf("AddEntry(%s) error = %v", entry.Source, err)
	}
}

func performDLARequest(t *testing.T, h *harness.Harness, method string, path string, body io.Reader, authenticated bool) dlaHTTPResponse {
	t.Helper()

	target := path
	if !authenticated {
		target = h.BaseURL + path
	}
	request, err := nethttp.NewRequestWithContext(context.Background(), method, target, body)
	if err != nil {
		t.Fatalf("create %s %s request: %v", method, path, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	var response *nethttp.Response
	if authenticated {
		response, err = h.Do(request)
	} else {
		response, err = nethttp.DefaultClient.Do(request)
	}
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() {
		_ = response.Body.Close()
	}()

	bodyBytes, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return dlaHTTPResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header,
		Body:       bodyBytes,
	}
}

func decodeDLALedgerResponse(t *testing.T, response dlaHTTPResponse) dlaLedgerResponse {
	t.Helper()
	var body dlaLedgerResponse
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatalf("decode ledger response: %v; body=%s", err, response.BodyString())
	}
	return body
}

func decodeDLABalanceResponse(t *testing.T, response dlaHTTPResponse) dlaBalanceResponse {
	t.Helper()
	var body dlaBalanceResponse
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatalf("decode balance response: %v; body=%s", err, response.BodyString())
	}
	return body
}

func decodeDLAEntryCreatedResponse(t *testing.T, response dlaHTTPResponse) dlaEntryCreatedResponse {
	t.Helper()
	var body dlaEntryCreatedResponse
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatalf("decode created response: %v; body=%s", err, response.BodyString())
	}
	return body
}

func decodeDLAProblem(t *testing.T, response dlaHTTPResponse) struct {
	Type   string          `json:"type"`
	Title  string          `json:"title"`
	Status int             `json:"status"`
	Detail string          `json:"detail"`
	Errors []dlaFieldError `json:"errors"`
} {
	t.Helper()

	var problem struct {
		Type   string          `json:"type"`
		Title  string          `json:"title"`
		Status int             `json:"status"`
		Detail string          `json:"detail"`
		Errors []dlaFieldError `json:"errors"`
	}
	if err := json.Unmarshal(response.Body, &problem); err != nil {
		t.Fatalf("decode problem response: %v; body=%s", err, response.BodyString())
	}
	return problem
}

func assertDLAProblemPointers(t *testing.T, problem struct {
	Type   string          `json:"type"`
	Title  string          `json:"title"`
	Status int             `json:"status"`
	Detail string          `json:"detail"`
	Errors []dlaFieldError `json:"errors"`
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

func assertDLAEntryResponse(t *testing.T, got dlaEntryResponse, want dlaEntryResponse) {
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

func walkJSON(t *testing.T, value any, visit func(key string, value any)) {
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
