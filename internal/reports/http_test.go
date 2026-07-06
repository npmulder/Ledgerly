package reports

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

func TestHTTPReportsEndpointsReturnDerivedJSON(t *testing.T) {
	loadReportsPack(t, "")
	router := newReportsHTTPTestRouter(t)

	plResult := performReportsRequest(router, http.MethodGet, "/api/reports/pl?from=2026-04-01&to=2026-06-30", true)
	if plResult.Code != http.StatusOK {
		t.Fatalf("P&L status = %d, want %d; body=%s", plResult.Code, http.StatusOK, plResult.Body.String())
	}
	assertReportsAmountsAreJSONIntegers(t, plResult.Body.Bytes())
	var pl plResponse
	if err := json.Unmarshal(plResult.Body.Bytes(), &pl); err != nil {
		t.Fatalf("decode P&L response: %v; body=%s", err, plResult.Body.String())
	}
	if pl.Period.From != "2026-04-01" || pl.Period.To != "2026-06-30" {
		t.Fatalf("P&L period = %+v, want Apr-Jun 2026", pl.Period)
	}
	if len(pl.Income) != 2 {
		t.Fatalf("P&L income = %+v, want two grouped income lines", pl.Income)
	}
	assertMoneyResponse(t, pl.Income[0].Amount, 100_000)
	if pl.Income[0].Label != otherIncomeLabel {
		t.Fatalf("first P&L income = %+v, want Other income", pl.Income[0])
	}
	assertMoneyResponse(t, pl.Income[1].Amount, 1_510_310)
	if pl.Income[1].ClientName != "Contoso GmbH" || pl.Income[1].Currency != "EUR" {
		t.Fatalf("second P&L income = %+v, want Contoso EUR line", pl.Income[1])
	}
	if pl.RealisedFXGains.Label != "Realised FX gains" {
		t.Fatalf("RealisedFXGains.Label = %q", pl.RealisedFXGains.Label)
	}
	assertMoneyResponse(t, pl.RealisedFXGains.Amount, 2_160)
	if pl.CorporateTax.Label != "IoM income tax at 0%" {
		t.Fatalf("CorporateTax.Label = %q, want IoM income tax at 0%%", pl.CorporateTax.Label)
	}
	assertMoneyResponse(t, pl.CorporateTax.Amount, 0)
	assertMoneyResponse(t, pl.NetProfit, 1_592_470)

	vatResult := performReportsRequest(router, http.MethodGet, "/api/reports/vat?period=2026-Q2", true)
	if vatResult.Code != http.StatusOK {
		t.Fatalf("VAT status = %d, want %d; body=%s", vatResult.Code, http.StatusOK, vatResult.Body.String())
	}
	var vat vatResponse
	if err := json.Unmarshal(vatResult.Body.Bytes(), &vat); err != nil {
		t.Fatalf("decode VAT response: %v; body=%s", err, vatResult.Body.String())
	}
	assertMoneyResponse(t, vat.Box1, 0)
	assertMoneyResponse(t, vat.Box4, 4_120)
	assertMoneyResponse(t, vat.Box6, 1_510_310)
	assertMoneyResponse(t, vat.NetPosition, -4_120)

	calendarResult := performReportsRequest(router, http.MethodGet, "/api/reports/calendar", true)
	if calendarResult.Code != http.StatusOK {
		t.Fatalf("calendar status = %d, want %d; body=%s", calendarResult.Code, http.StatusOK, calendarResult.Body.String())
	}
	var calendar filingCalendarResponse
	if err := json.Unmarshal(calendarResult.Body.Bytes(), &calendar); err != nil {
		t.Fatalf("decode calendar response: %v; body=%s", err, calendarResult.Body.String())
	}
	if len(calendar.Filings) != 4 {
		t.Fatalf("calendar filings = %d, want 4: %+v", len(calendar.Filings), calendar.Filings)
	}
	if calendar.Filings[0].Key != "vat_return" || calendar.Filings[0].Status != string(FilingStatusDueSoon) {
		t.Fatalf("first filing = %+v, want due-soon VAT return", calendar.Filings[0])
	}

	profitResult := performReportsRequest(router, http.MethodGet, "/api/reports/profit-ytd?taxYear=2026-27", true)
	if profitResult.Code != http.StatusOK {
		t.Fatalf("profit-ytd status = %d, want %d; body=%s", profitResult.Code, http.StatusOK, profitResult.Body.String())
	}
	var profit profitYTDResponse
	if err := json.Unmarshal(profitResult.Body.Bytes(), &profit); err != nil {
		t.Fatalf("decode profit-ytd response: %v; body=%s", err, profitResult.Body.String())
	}
	assertMoneyResponse(t, profit.Profit, 1_592_470)
}

func TestHTTPReportsRoutesRequireAuthentication(t *testing.T) {
	loadReportsPack(t, "")
	router := newReportsHTTPTestRouter(t)

	for _, path := range []string{
		"/api/reports/pl?from=2026-04-01&to=2026-06-30",
		"/api/reports/vat?period=2026-Q2",
		"/api/reports/calendar",
		"/api/reports/profit-ytd?taxYear=2026-27",
	} {
		response := performReportsRequest(router, http.MethodGet, path, false)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want %d; body=%s", path, response.Code, http.StatusUnauthorized, response.Body.String())
		}
		if got := response.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
			t.Fatalf("%s Content-Type = %q, want %s", path, got, httpserver.ProblemContentType)
		}
	}
}

func newReportsHTTPTestRouter(t *testing.T) http.Handler {
	t.Helper()

	fakeLedger := newFakeLedger(
		fakeEntry(1, "2026-04-10", "manual", "consulting-income", fakePosting("4000-sales", -100_000)),
		fakeEntry(2, "2026-05-02", "manual", "software", fakePosting("5010-software", 20_000)),
		fakeEntry(3, "2026-06-20", "manual", "fx", fakePosting(realisedFXAccount, -2_160)),
		fakeEntry(4, "2026-06-25", invoicing.ModuleName, "invoice:INV-2026-0009:send", fakePosting(salesAccount, -1_510_310)),
		fakeEntry(5, "2026-06-30", ModuleName, "manual-input-vat:q2-2026", ledger.Posting{
			AccountCode: vatControlAccount,
			Amount:      money.Money{Amount: 4_120, Currency: gbpCurrency},
			AmountGBP:   money.Money{Amount: 4_120, Currency: gbpCurrency},
		}),
	)
	fakeLedger.accounts = append(fakeLedger.accounts,
		ledger.Account{Code: vatControlAccount, Name: "VAT control", Type: ledger.AccountTypeLiability},
	)

	identityAPI := fakeIdentity{yearEnd: identityYearEnd(time.March, 31)}
	invoicingAPI := reportsHTTPInvoicing{}
	clk := clock.NewFake(testDate(2026, time.July, 5))
	service := New(fakeLedger, identityAPI, invoicingAPI, WithClock(clk))
	if _, err := service.ProfitAndLoss(context.Background(), Period{
		From: testDate(2026, time.April, 1),
		To:   testDate(2026, time.June, 30),
	}); err != nil {
		t.Fatalf("reports HTTP fixture P&L error = %v", err)
	}

	module, err := NewModule(Config{
		Ledger:    fakeLedger,
		Identity:  identityAPI,
		Invoicing: invoicingAPI,
		Clock:     clk,
	})
	if err != nil {
		t.Fatalf("NewModule() error = %v", err)
	}

	return httpserver.NewRouter(httpserver.Config{
		Version:          "test",
		DB:               reportsPingerFunc(func(context.Context) error { return nil }),
		APIAuth:          reportsTestAuthMiddleware,
		Modules:          []httpserver.Module{module.HTTPModule()},
		OpenAPIFragments: []httpserver.OpenAPIFragment{module.OpenAPIFragment()},
	})
}

type reportsHTTPInvoicing struct {
	fakeInvoicing
}

func (reportsHTTPInvoicing) Invoice(context.Context, string) (invoicing.Invoice, error) {
	return reportsHTTPInvoice(), nil
}

func (reportsHTTPInvoicing) InvoiceByNumber(_ context.Context, number string) (invoicing.Invoice, error) {
	if number != "INV-2026-0009" {
		return invoicing.Invoice{}, invoicing.ErrInvoiceNotFound
	}
	return reportsHTTPInvoice(), nil
}

func (reportsHTTPInvoicing) InvoiceVATContextBySendEntryID(_ context.Context, entryID ledger.EntryID) (invoicing.InvoiceVATContext, error) {
	if entryID != 4 {
		return invoicing.InvoiceVATContext{}, invoicing.ErrInvoiceNotFound
	}
	return invoicing.InvoiceVATContext{
		InvoiceID:    "inv_contoso_q2",
		VATTreatment: invoicing.VATTreatmentReverseChargeEUB2B,
	}, nil
}

func (reportsHTTPInvoicing) Client(_ context.Context, id string) (invoicing.Client, error) {
	if id != "client_contoso" {
		return invoicing.Client{}, invoicing.ErrClientNotFound
	}
	return invoicing.Client{
		ID:              "client_contoso",
		Name:            "Contoso GmbH",
		DefaultCurrency: invoicing.CurrencyEUR,
	}, nil
}

func reportsHTTPInvoice() invoicing.Invoice {
	return invoicing.Invoice{
		ID:       "inv_contoso_q2",
		ClientID: "client_contoso",
		Currency: invoicing.CurrencyEUR,
	}
}

func performReportsRequest(router http.Handler, method string, path string, authenticated bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if authenticated {
		request.AddCookie(&http.Cookie{Name: "test_session", Value: "ok"})
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func assertMoneyResponse(t *testing.T, got moneyResponse, wantAmount int64) {
	t.Helper()
	if got.AmountMinor != wantAmount || got.Currency != gbpCurrency {
		t.Fatalf("money = %+v, want %d GBP", got, wantAmount)
	}
}

func assertReportsAmountsAreJSONIntegers(t *testing.T, body []byte) {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("decode JSON with numbers: %v; body=%s", err, string(body))
	}

	amountCount := 0
	walkReportsJSON(decoded, func(key string, value any) {
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

func walkReportsJSON(value any, visit func(string, any)) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			visit(key, child)
			walkReportsJSON(child, visit)
		}
	case []any:
		for _, child := range typed {
			walkReportsJSON(child, visit)
		}
	}
}

func reportsTestAuthMiddleware(next http.Handler) http.Handler {
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

type reportsPingerFunc func(context.Context) error

func (f reportsPingerFunc) PingContext(ctx context.Context) error {
	return f(ctx)
}
