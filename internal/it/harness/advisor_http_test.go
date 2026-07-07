//go:build integration

package harness_test

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/advisor"
	"github.com/npmulder/ledgerly/internal/it/harness"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

func TestAdvisorHTTPInsightsDismissRefreshAndAuth(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)})
	seedAdvisorHTTPInsights(t, h)

	response := performAdvisorRequest(t, h, nethttp.MethodGet, "/api/advisor/insights?surface=dashboard", true)
	if response.StatusCode != nethttp.StatusOK {
		t.Fatalf("list dashboard insights status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusOK, response.BodyString())
	}
	list := decodeAdvisorInsightsResponse(t, response)
	assertAdvisorHTTPInsightKeys(t, list.Insights, []string{"amber-new", "amber-old", "teal-new"})
	if list.Insights[0].Severity != advisor.SeverityAmber || list.Insights[2].Severity != advisor.SeverityTeal {
		t.Fatalf("ordered severities = %#v, want amber first and teal last", list.Insights)
	}
	if list.Insights[0].CTA.Action != "navigate:/dividends?amount=150000" {
		t.Fatalf("first CTA action = %q, want navigate action", list.Insights[0].CTA.Action)
	}

	response = performAdvisorRequest(t, h, nethttp.MethodGet, "/api/advisor/insights?surface=reports", true)
	if response.StatusCode != nethttp.StatusOK {
		t.Fatalf("list reports insights status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusOK, response.BodyString())
	}
	assertAdvisorHTTPInsightKeys(t, decodeAdvisorInsightsResponse(t, response).Insights, []string{"reports-only"})

	response = performAdvisorRequest(t, h, nethttp.MethodGet, "/api/advisor/insights?surface=unknown", true)
	if response.StatusCode != nethttp.StatusBadRequest {
		t.Fatalf("invalid surface status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusBadRequest, response.BodyString())
	}

	response = performAdvisorRequest(t, h, nethttp.MethodPost, "/api/advisor/insights/amber-new/dismiss", true)
	if response.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("dismiss status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusNoContent, response.BodyString())
	}
	response = performAdvisorRequest(t, h, nethttp.MethodGet, "/api/advisor/insights?surface=dashboard", true)
	if response.StatusCode != nethttp.StatusOK {
		t.Fatalf("list after dismiss status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusOK, response.BodyString())
	}
	assertAdvisorHTTPInsightKeys(t, decodeAdvisorInsightsResponse(t, response).Insights, []string{"amber-old", "teal-new"})

	response = performAdvisorRequest(t, h, nethttp.MethodPost, "/api/advisor/insights/not-present/dismiss", true)
	if response.StatusCode != nethttp.StatusNotFound {
		t.Fatalf("dismiss missing status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusNotFound, response.BodyString())
	}

	response = performAdvisorRequest(t, h, nethttp.MethodPost, "/api/advisor/refresh", true)
	if response.StatusCode != nethttp.StatusOK {
		t.Fatalf("refresh status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusOK, response.BodyString())
	}
	refresh := decodeAdvisorRefreshResponse(t, response)
	if refresh.Run.Trigger != advisor.ManualRefreshTrigger || refresh.Run.ID == 0 {
		t.Fatalf("refresh run = %#v, want manual trigger with persisted id", refresh.Run)
	}

	for _, request := range []struct {
		method string
		path   string
	}{
		{method: nethttp.MethodGet, path: "/api/advisor/insights?surface=dashboard"},
		{method: nethttp.MethodPost, path: "/api/advisor/insights/amber-old/dismiss"},
		{method: nethttp.MethodPost, path: "/api/advisor/refresh"},
	} {
		response := performAdvisorRequest(t, h, request.method, request.path, false)
		if response.StatusCode != nethttp.StatusUnauthorized {
			t.Fatalf("%s %s unauthenticated status = %d, want %d; body=%s", request.method, request.path, response.StatusCode, nethttp.StatusUnauthorized, response.BodyString())
		}
		if got := response.Header.Get("Content-Type"); got != httpserver.ProblemContentType {
			t.Fatalf("%s %s Content-Type = %q, want %s", request.method, request.path, got, httpserver.ProblemContentType)
		}
	}

	openAPI := performAdvisorRequest(t, h, nethttp.MethodGet, "/api/openapi.json", false)
	if openAPI.StatusCode != nethttp.StatusOK {
		t.Fatalf("openapi status = %d, want %d; body=%s", openAPI.StatusCode, nethttp.StatusOK, openAPI.BodyString())
	}
	if body := openAPI.BodyString(); !strings.Contains(body, `"/api/advisor/insights"`) || !strings.Contains(body, `"/api/advisor/refresh"`) {
		t.Fatalf("openapi missing advisor paths: %s", body)
	}
}

func seedAdvisorHTTPInsights(t testing.TB, h *harness.Harness) {
	t.Helper()

	now := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)
	insights := []advisor.Insight{
		advisorHTTPInsight("teal-new", advisor.SeverityTeal, []advisor.Surface{advisor.SurfaceDashboard, advisor.SurfaceInvoices}, now.Add(3*time.Hour)),
		advisorHTTPInsight("amber-old", advisor.SeverityAmber, []advisor.Surface{advisor.SurfaceDashboard}, now.Add(time.Hour)),
		advisorHTTPInsight("amber-new", advisor.SeverityAmber, []advisor.Surface{advisor.SurfaceDashboard, advisor.SurfaceDLA}, now.Add(2*time.Hour)),
		advisorHTTPInsight("reports-only", advisor.SeverityAmber, []advisor.Surface{advisor.SurfaceReports}, now.Add(4*time.Hour)),
	}
	delta := advisor.Delta{
		Insights:         insights,
		EvaluatedRuleIDs: []string{"teal-new-rule", "amber-old-rule", "amber-new-rule", "reports-only-rule"},
		GeneratedAt:      now.Add(5 * time.Hour),
	}
	if err := (advisor.Store{}).Apply(context.Background(), h.AdvisorPool, delta); err != nil {
		t.Fatalf("seed advisor insights: %v", err)
	}
}

func advisorHTTPInsight(key string, severity advisor.Severity, surfaces []advisor.Surface, createdAt time.Time) advisor.Insight {
	return advisor.Insight{
		Key:          advisor.InsightKey(key),
		RuleID:       key + "-rule",
		FactHash:     "hash-" + key,
		Severity:     severity,
		Surfaces:     surfaces,
		RenderedText: "Rendered advisor text for " + key,
		Bindings:     map[string]any{"key": key},
		CTA: advisor.CTA{
			Label:  "Open",
			Action: "navigate:/dividends?amount=150000",
		},
		CreatedAt: createdAt,
	}
}

type advisorHTTPResponse struct {
	StatusCode int
	Header     nethttp.Header
	Body       []byte
}

func (r advisorHTTPResponse) BodyString() string {
	return string(r.Body)
}

type advisorInsightsHTTPResponse struct {
	Insights []advisorInsightHTTPResponse `json:"insights"`
}

type advisorInsightHTTPResponse struct {
	Key          string           `json:"key"`
	Severity     advisor.Severity `json:"severity"`
	RenderedText string           `json:"rendered_text"`
	CTA          advisor.CTA      `json:"cta"`
}

type advisorRefreshHTTPResponse struct {
	Run struct {
		ID      int64  `json:"id"`
		Trigger string `json:"trigger"`
	} `json:"run"`
}

func performAdvisorRequest(t testing.TB, h *harness.Harness, method string, path string, authenticated bool) advisorHTTPResponse {
	t.Helper()

	target := path
	if !authenticated {
		target = h.BaseURL + path
	}
	request, err := nethttp.NewRequestWithContext(context.Background(), method, target, nil)
	if err != nil {
		t.Fatalf("create %s %s request: %v", method, path, err)
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
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return advisorHTTPResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header,
		Body:       body,
	}
}

func decodeAdvisorInsightsResponse(t testing.TB, response advisorHTTPResponse) advisorInsightsHTTPResponse {
	t.Helper()
	var body advisorInsightsHTTPResponse
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatalf("decode advisor insights response: %v; body=%s", err, response.BodyString())
	}
	return body
}

func decodeAdvisorRefreshResponse(t testing.TB, response advisorHTTPResponse) advisorRefreshHTTPResponse {
	t.Helper()
	var body advisorRefreshHTTPResponse
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatalf("decode advisor refresh response: %v; body=%s", err, response.BodyString())
	}
	return body
}

func assertAdvisorHTTPInsightKeys(t testing.TB, insights []advisorInsightHTTPResponse, want []string) {
	t.Helper()
	if len(insights) != len(want) {
		t.Fatalf("advisor insight keys length = %d (%#v), want %d (%#v)", len(insights), insights, len(want), want)
	}
	for index, key := range want {
		if insights[index].Key != key {
			t.Fatalf("advisor insight key[%d] = %q, want %q; all=%#v", index, insights[index].Key, key, insights)
		}
	}
}
