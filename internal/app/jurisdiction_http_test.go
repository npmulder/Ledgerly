package app

import (
	"bytes"
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

func TestJurisdictionPackEndpointReturnsSummariesDerivedFromPackData(t *testing.T) {
	if err := jurisdiction.LoadActiveFromFS(mapJurisdictionPack(t, string(mustReadJurisdictionFixturePack(t))), "testland@0.1"); err != nil {
		t.Fatalf("LoadActiveFromFS() error = %v", err)
	}
	router := newJurisdictionTestRouter(t, npmLimitedFacts, fixedJurisdictionClock{})

	response := performJurisdictionRequest(router, nethttp.MethodGet, "/api/jurisdiction/pack", true)
	if response.Code != nethttp.StatusOK {
		t.Fatalf("pack status = %d, want %d; body=%s", response.Code, nethttp.StatusOK, response.Body.String())
	}

	body := decodeJurisdictionPackResponse(t, response)
	if body.Meta.ID != "testland" || body.Meta.Version != "0.1" || body.Meta.Name != "Testland" {
		t.Fatalf("meta = %+v, want testland@0.1 Testland", body.Meta)
	}
	if len(body.RuleSummaries) != 6 {
		t.Fatalf("rule_summaries length = %d, want 6", len(body.RuleSummaries))
	}
	assertJurisdictionSummary(t, body, "corporate_income_tax", "19% CIT (2025-26)")
	assertJurisdictionSummary(t, body, "personal_tax_dividends", "dividend WHT: test-withholding; personal allowance TST 12.34; bands 5% to TST 10, 15% to TST 20, then 25% (2025-26/2025-26)")
	assertJurisdictionSummary(t, body, "vat", "VAT 17% via Testland Customs; reverse charge via Test Article 42 (2025-26)")
	assertJurisdictionSummary(t, body, "annual_return", "due incorporation anniversary + 1 month with Testland Companies Office")
	assertJurisdictionSummary(t, body, "company_tax_return", "due accounting year end + 12 months + 1 day with Testland Revenue; required at zero rate")
	assertJurisdictionSummary(t, body, "director_loan", "s455 charge applies; overdrawn warning: test director loan warning; remedy: test clear or repay")

	mutated := strings.Replace(string(mustReadJurisdictionFixturePack(t)), `standard_rate: "0.19"`, `standard_rate: "0.33"`, 1)
	if err := jurisdiction.LoadActiveFromFS(mapJurisdictionPack(t, mutated), "testland@0.1"); err != nil {
		t.Fatalf("LoadActiveFromFS(mutated) error = %v", err)
	}
	mutatedResponse := performJurisdictionRequest(router, nethttp.MethodGet, "/api/jurisdiction/pack", true)
	if mutatedResponse.Code != nethttp.StatusOK {
		t.Fatalf("mutated pack status = %d, want %d; body=%s", mutatedResponse.Code, nethttp.StatusOK, mutatedResponse.Body.String())
	}
	assertJurisdictionSummary(t, decodeJurisdictionPackResponse(t, mutatedResponse), "corporate_income_tax", "33% CIT (2025-26)")
}

func TestJurisdictionDeadlinesEndpointUsesCompanyFacts(t *testing.T) {
	if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("LoadActive() error = %v", err)
	}
	router := newJurisdictionTestRouter(t, npmLimitedFacts, fixedJurisdictionClock{})

	response := performJurisdictionRequest(router, nethttp.MethodGet, "/api/jurisdiction/deadlines", true)
	if response.Code != nethttp.StatusOK {
		t.Fatalf("deadlines status = %d, want %d; body=%s", response.Code, nethttp.StatusOK, response.Body.String())
	}

	var body jurisdictionFilingDeadlinesResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode deadlines response: %v; body=%s", err, response.Body.String())
	}
	want := []jurisdictionFilingDeadlineResponse{
		{
			Key:        "vat_return",
			Label:      "VAT return",
			Authority:  "Isle of Man Customs & Excise",
			DueDate:    "2026-07-30",
			Recurrence: "quarterly",
		},
		{
			Key:        "annual_return",
			Label:      "Annual return",
			Authority:  "IoM Companies Registry",
			DueDate:    "2026-08-14",
			Recurrence: "annual",
		},
		{
			Key:        "personal_tax_return",
			Label:      "Personal tax return",
			Authority:  "IoM Income Tax Division",
			DueDate:    "2026-10-06",
			Recurrence: "annual",
		},
		{
			Key:        "company_tax_return",
			Label:      "Company tax return",
			Authority:  "",
			DueDate:    "2027-04-01",
			Recurrence: "annual",
		},
	}
	if len(body.Deadlines) != len(want) {
		t.Fatalf("deadlines length = %d, want %d: %+v", len(body.Deadlines), len(want), body.Deadlines)
	}
	for i := range want {
		if body.Deadlines[i] != want[i] {
			t.Fatalf("deadlines[%d] = %+v, want %+v", i, body.Deadlines[i], want[i])
		}
	}
}

func TestJurisdictionDeadlinesEndpointSkipsVATWhenUnregistered(t *testing.T) {
	if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("LoadActive() error = %v", err)
	}
	facts := npmLimitedFacts
	facts.IsVATRegistered = false
	router := newJurisdictionTestRouter(t, facts, fixedJurisdictionClock{})

	response := performJurisdictionRequest(router, nethttp.MethodGet, "/api/jurisdiction/deadlines", true)
	if response.Code != nethttp.StatusOK {
		t.Fatalf("deadlines status = %d, want %d; body=%s", response.Code, nethttp.StatusOK, response.Body.String())
	}

	var body jurisdictionFilingDeadlinesResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode deadlines response: %v; body=%s", err, response.Body.String())
	}
	for _, deadline := range body.Deadlines {
		if deadline.Key == "vat_return" {
			t.Fatalf("vat_return present for unregistered company: %+v", body.Deadlines)
		}
	}
}

func TestJurisdictionDeadlinesEndpointMapsMissingProfileToNotFound(t *testing.T) {
	if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("LoadActive() error = %v", err)
	}
	router := newJurisdictionTestRouterWithProvider(
		t,
		func(context.Context) (jurisdiction.CompanyFacts, error) {
			return jurisdiction.CompanyFacts{}, identity.ErrProfileNotFound
		},
		fixedJurisdictionClock{},
	)

	response := performJurisdictionRequest(router, nethttp.MethodGet, "/api/jurisdiction/deadlines", true)
	if response.Code != nethttp.StatusNotFound {
		t.Fatalf("deadlines status = %d, want %d; body=%s", response.Code, nethttp.StatusNotFound, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
		t.Fatalf("Content-Type = %q, want %s", got, httpserver.ProblemContentType)
	}
	var problem httpserver.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem response: %v; body=%s", err, response.Body.String())
	}
	if problem.Detail != "company profile was not found" {
		t.Fatalf("problem detail = %q, want company profile was not found", problem.Detail)
	}
}

func TestJurisdictionRoutesRequireAuthentication(t *testing.T) {
	if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("LoadActive() error = %v", err)
	}
	router := newJurisdictionTestRouter(t, npmLimitedFacts, fixedJurisdictionClock{})

	for _, path := range []string{"/api/jurisdiction/pack", "/api/jurisdiction/deadlines"} {
		response := performJurisdictionRequest(router, nethttp.MethodGet, path, false)
		if response.Code != nethttp.StatusUnauthorized {
			t.Fatalf("%s status = %d, want %d; body=%s", path, response.Code, nethttp.StatusUnauthorized, response.Body.String())
		}
		if got := response.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
			t.Fatalf("%s Content-Type = %q, want %s", path, got, httpserver.ProblemContentType)
		}
	}
}

func newJurisdictionTestRouter(t *testing.T, facts jurisdiction.CompanyFacts, clock jurisdiction.Clock) nethttp.Handler {
	t.Helper()

	return newJurisdictionTestRouterWithProvider(
		t,
		func(context.Context) (jurisdiction.CompanyFacts, error) { return facts, nil },
		clock,
	)
}

func newJurisdictionTestRouterWithProvider(
	t *testing.T,
	companyFacts jurisdictionCompanyFactsFunc,
	clock jurisdiction.Clock,
) nethttp.Handler {
	t.Helper()

	router := httpserver.NewRouter(httpserver.Config{
		Version: "test",
		DB:      pingerFunc(func(context.Context) error { return nil }),
		APIAuth: testAuthMiddleware,
		Modules: []httpserver.Module{
			jurisdictionHTTPModule(companyFacts, clock),
		},
		OpenAPIFragments: []httpserver.OpenAPIFragment{jurisdictionOpenAPIFragment()},
	})
	return router
}

func performJurisdictionRequest(router nethttp.Handler, method, path string, authenticated bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewReader(nil))
	if authenticated {
		request.AddCookie(&nethttp.Cookie{Name: "test_session", Value: "ok"})
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func decodeJurisdictionPackResponse(t *testing.T, response *httptest.ResponseRecorder) jurisdictionPackResponse {
	t.Helper()

	var body jurisdictionPackResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode pack response: %v; body=%s", err, response.Body.String())
	}
	return body
}

func assertJurisdictionSummary(t *testing.T, response jurisdictionPackResponse, id string, want string) {
	t.Helper()

	for _, summary := range response.RuleSummaries {
		if summary.ID == id {
			if summary.Summary != want {
				t.Fatalf("%s summary = %q, want %q", id, summary.Summary, want)
			}
			return
		}
	}
	t.Fatalf("summary %q not found in %+v", id, response.RuleSummaries)
}

func testAuthMiddleware(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz", "/api/openapi.json":
			next.ServeHTTP(w, r)
			return
		}
		if _, err := r.Cookie("test_session"); err != nil {
			httpserver.WriteProblem(w, r, httpserver.Problem{
				Type:   "https://ledgerly.local/problems/unauthenticated",
				Title:  nethttp.StatusText(nethttp.StatusUnauthorized),
				Status: nethttp.StatusUnauthorized,
				Detail: "authentication required",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

type fixedJurisdictionClock struct{}

func (fixedJurisdictionClock) Now() time.Time {
	return time.Date(2026, time.July, 5, 12, 0, 0, 0, time.UTC)
}

var npmLimitedFacts = jurisdiction.CompanyFacts{
	IncorporationDate: time.Date(2020, time.July, 14, 0, 0, 0, 0, time.UTC),
	YearEnd:           jurisdiction.YearEnd{Month: time.March, Day: 31},
	IsVATRegistered:   true,
}

type pingerFunc func(context.Context) error

func (f pingerFunc) PingContext(ctx context.Context) error {
	return f(ctx)
}

func mustReadJurisdictionFixturePack(t *testing.T) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "jurisdiction", "testdata", "packs", "testland", "0.1", "pack.yaml"))
	if err != nil {
		t.Fatalf("read fixture pack: %v", err)
	}
	return data
}

func mapJurisdictionPack(t *testing.T, pack string) fstest.MapFS {
	t.Helper()

	return fstest.MapFS{
		jurisdiction.PackPath("testland", "0.1"): {
			Data: []byte(pack),
		},
	}
}
