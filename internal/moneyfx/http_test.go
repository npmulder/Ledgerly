package moneyfx

import (
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/platform/clock"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

func TestRateEndpointsReturnRateDateAndFetchedAt(t *testing.T) {
	t.Parallel()

	friday := time.Date(2030, 1, 4, 0, 0, 0, 0, time.UTC)
	now := time.Date(2030, 1, 7, 9, 30, 0, 0, time.UTC)
	router := newRateTestRouter(t, friday, now)

	historical := performRateRequest(router, "/api/moneyfx/rates?date=2030-01-07&from=USD&to=GBP", true)
	if historical.Code != nethttp.StatusOK {
		t.Fatalf("historical status = %d, want 200; body=%s", historical.Code, historical.Body.String())
	}
	historicalBody := decodeRateResponse(t, historical)
	if historicalBody.Rate != "0.64" {
		t.Fatalf("historical rate = %q, want 0.64", historicalBody.Rate)
	}
	if historicalBody.RateDate != friday.Format(time.DateOnly) {
		t.Fatalf("historical rate_date = %q, want %s", historicalBody.RateDate, friday.Format(time.DateOnly))
	}
	if !historicalBody.FetchedAt.Equal(now) {
		t.Fatalf("historical fetched_at = %s, want %s", historicalBody.FetchedAt, now)
	}

	today := performRateRequest(router, "/api/moneyfx/rates/today?from=EUR&to=GBP", true)
	if today.Code != nethttp.StatusOK {
		t.Fatalf("today status = %d, want 200; body=%s", today.Code, today.Body.String())
	}
	todayBody := decodeRateResponse(t, today)
	if todayBody.Rate != "0.8" {
		t.Fatalf("today rate = %q, want 0.8", todayBody.Rate)
	}
	if todayBody.RateDate != friday.Format(time.DateOnly) {
		t.Fatalf("today rate_date = %q, want %s", todayBody.RateDate, friday.Format(time.DateOnly))
	}
	if !todayBody.FetchedAt.Equal(now) {
		t.Fatalf("today fetched_at = %s, want %s", todayBody.FetchedAt, now)
	}
}

func TestRateEndpointsRequireAuthentication(t *testing.T) {
	t.Parallel()

	friday := time.Date(2030, 1, 4, 0, 0, 0, 0, time.UTC)
	now := time.Date(2030, 1, 7, 9, 30, 0, 0, time.UTC)
	router := newRateTestRouter(t, friday, now)

	for _, path := range []string{
		"/api/moneyfx/rates?date=2030-01-07&from=USD&to=GBP",
		"/api/moneyfx/rates/today?from=EUR&to=GBP",
	} {
		response := performRateRequest(router, path, false)
		if response.Code != nethttp.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401; body=%s", path, response.Code, response.Body.String())
		}
	}
}

func newRateTestRouter(t *testing.T, rateDate time.Time, now time.Time) nethttp.Handler {
	t.Helper()

	store := newMemoryRateReader()
	store.seed(t, rateDate, "GBP", "0.8")
	store.seed(t, rateDate, "USD", "1.25")
	module := &Module{service: NewService(store, clock.NewFake(now))}
	return httpserver.NewRouter(httpserver.Config{
		APIAuth: testRateAuth,
		Modules: []httpserver.Module{
			module.HTTPModule(),
		},
	})
}

func testRateAuth(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
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

func performRateRequest(handler nethttp.Handler, path string, authenticated bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(nethttp.MethodGet, path, nil)
	if authenticated {
		request.Header.Set("X-Test-Auth", "ok")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeRateResponse(t *testing.T, response *httptest.ResponseRecorder) rateResponse {
	t.Helper()

	var body rateResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode rate response: %v; body=%s", err, response.Body.String())
	}
	return body
}
