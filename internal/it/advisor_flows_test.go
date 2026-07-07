//go:build integration

package it_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/advisor"
	"github.com/npmulder/ledgerly/internal/dividends"
	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/invoicing"
	it "github.com/npmulder/ledgerly/internal/it"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx"
)

func TestAdvisorFlows(t *testing.T) {
	t.Run("overdue invoice fires on invoice and dashboard surfaces then resolves after settlement", testAdvisorFlowsOverdueInvoice)
	t.Run("DLA overdrawn BIK insight uses pack wording and clears via dividend", testAdvisorFlowsDLAOverdrawn)
	t.Run("filing deadline exposes due badge facts and resolves outside window", testAdvisorFlowsFilingDeadline)
	t.Run("dividend headroom set-aside uses hand-computed JUR-4 marginal estimate", testAdvisorFlowsHeadroom)
	t.Run("stale rates insight resolves after successful ECB fetch", testAdvisorFlowsRatesStale)
	t.Run("dismissal survives same facts and new headroom facts reappear", testAdvisorFlowsDismissalSemantics)
	t.Run("consecutive evaluations are idempotent", testAdvisorFlowsIdempotency)
	t.Run("dashboard ordering is stable amber-first and capped at four", testAdvisorFlowsDashboardOrdering)
	t.Run("settlement-triggered advisor failure does not roll back settlement", testAdvisorFlowsPostCommitIsolation)
}

func testAdvisorFlowsOverdueInvoice(t *testing.T) {
	issueDate := day(2025, time.May, 1)
	settleDate := day(2025, time.May, 7)
	h := newAdvisorFlowsHarness(t, harness.Options{ClockStart: issueDate.Add(9 * time.Hour)}, fixtures.RatesStep(map[time.Time]string{
		issueDate:  "0.8500",
		settleDate: "0.8600",
	}))
	overdue := advisorFlowsCreateSentEURInvoice(t, h, "2025-05-04", 125_000)
	h.Clock.Set(settleDate.Add(9 * time.Hour))

	advisorFlowsRunEvaluation(t, h)
	invoiceInsight := requireAdvisorFlowInsight(t, advisorFlowsInsights(t, h, advisor.SurfaceInvoices), "overdue_invoice")
	dashboardInsight := requireAdvisorFlowInsight(t, advisorFlowsInsights(t, h, advisor.SurfaceDashboard), "overdue_invoice")

	assertAdvisorFlowInsight(t, invoiceInsight, advisor.SeverityAmber, []advisor.Surface{advisor.SurfaceDashboard, advisor.SurfaceInvoices})
	if dashboardInsight.Key != invoiceInsight.Key {
		t.Fatalf("dashboard overdue key = %q, invoices key = %q", dashboardInsight.Key, invoiceInsight.Key)
	}
	if invoiceInsight.CTA.Action != "invoicing.sendReminder" || stringBinding(t, invoiceInsight.CTA.Params, "invoice_id") != overdue.ID {
		t.Fatalf("overdue CTA = %#v, want sendReminder invoice %s", invoiceInsight.CTA, overdue.ID)
	}
	if got := intBinding(t, invoiceInsight.Bindings, "days_overdue"); got != 3 {
		t.Fatalf("days_overdue binding = %d, want 3", got)
	}
	if !strings.Contains(invoiceInsight.RenderedText, "3 days overdue") {
		t.Fatalf("overdue text = %q, want day count", invoiceInsight.RenderedText)
	}
	expectedText := advisorFlowsExpectedText(t, "overdue_invoice", advisor.Facts{
		advisor.FactInvoiceClientName:  "Contoso GmbH",
		advisor.FactInvoiceCount:       1,
		advisor.FactInvoiceDaysOverdue: 3,
		advisor.FactInvoiceID:          overdue.ID,
		advisor.FactInvoiceNumber:      *overdue.Number,
	}, h.Clock.Now())
	if invoiceInsight.RenderedText != expectedText {
		t.Fatalf("overdue text = %q, want pack-rendered %q", invoiceInsight.RenderedText, expectedText)
	}

	settled := advisorFlowsMarkInvoiceSettled(t, h, overdue, "advisor-flows:clear-overdue", settleDate)
	if settled.Status != invoicing.InvoiceStatusPaid {
		t.Fatalf("settled status = %q, want paid", settled.Status)
	}
	advisorFlowsRunEvaluation(t, h)
	assertNoAdvisorFlowInsight(t, advisorFlowsInsights(t, h, advisor.SurfaceInvoices), "overdue_invoice")
	assertNoAdvisorFlowInsight(t, advisorFlowsInsights(t, h, advisor.SurfaceDashboard), "overdue_invoice")
	it.AssertLedgerBalanced(t, h)
}

func testAdvisorFlowsDLAOverdrawn(t *testing.T) {
	f := newDividendFlowFixture(t)
	f.postRetainedEarnings(t, day(2025, time.March, 31), 200_000)
	f.fileDLADrawing(t, "advisor-flows:dla-overdrawn", gbp(150_000))

	advisorFlowsRunEvaluation(t, f.h)
	insight := requireAdvisorFlowInsight(t, advisorFlowsInsights(t, f.h, advisor.SurfaceDLA), "dla_overdrawn_bik")
	assertAdvisorFlowInsight(t, insight, advisor.SeverityAmber, []advisor.Surface{advisor.SurfaceDashboard, advisor.SurfaceDLA})
	if insight.CTA.Action != "navigate:/dividends?amount=150000" {
		t.Fatalf("DLA CTA action = %q, want prefilled dividend route", insight.CTA.Action)
	}
	expectedText := advisorFlowsExpectedText(t, "dla_overdrawn_bik", advisor.Facts{
		advisor.FactRuleDLABalance:        gbp(-150_000),
		advisor.FactRuleDLAClearanceMinor: int64(150_000),
		advisor.FactRuleDLAStatus:         string(dla.StatusOverdrawn),
	}, f.h.Clock.Now())
	if insight.RenderedText != expectedText {
		t.Fatalf("DLA text = %q, want pack-rendered %q", insight.RenderedText, expectedText)
	}

	declaration, err := f.dividends().Declare(f.ctx, gbp(150_000))
	if err != nil {
		t.Fatalf("Declare(clear DLA) error = %v", err)
	}
	assertDividendFlowDLAEntry(t, f, dividendSourceRef(declaration.ID), 150_000, gbp(0), dla.BalanceSideZero)
	advisorFlowsRunEvaluation(t, f.h)
	assertNoAdvisorFlowInsight(t, advisorFlowsInsights(t, f.h, advisor.SurfaceDLA), "dla_overdrawn_bik")
	it.AssertLedgerBalanced(t, f.h)
}

func testAdvisorFlowsFilingDeadline(t *testing.T) {
	h := newAdvisorFlowsHarness(t, harness.Options{ClockStart: day(2026, time.July, 5).Add(9 * time.Hour)}, fixtures.RatesStep(map[time.Time]string{
		day(2026, time.July, 5): "0.8500",
	}))

	advisorFlowsRunEvaluation(t, h)
	warn := requireAdvisorFlowInsight(t, advisorFlowsInsights(t, h, advisor.SurfaceReports), "filing_deadline_window")
	assertAdvisorFlowInsight(t, warn, advisor.SeverityAmber, []advisor.Surface{advisor.SurfaceDashboard, advisor.SurfaceReports})
	assertFilingDeadlineBinding(t, warn, "2026-07-30", 25, "due-soon", 30)
	expectedWarn := advisorFlowsExpectedText(t, "filing_deadline_window", advisor.Facts{
		advisor.FactFilingAuthority:  "Isle of Man Customs & Excise",
		advisor.FactFilingDueDate:    day(2026, time.July, 30),
		advisor.FactFilingDaysUntil:  25,
		advisor.FactFilingName:       "VAT return",
		advisor.FactFilingStatus:     "due-soon",
		advisor.FactFilingWarnWindow: advisor.Days(30),
	}, h.Clock.Now())
	if warn.RenderedText != expectedWarn {
		t.Fatalf("filing text = %q, want pack-rendered %q", warn.RenderedText, expectedWarn)
	}

	h.Clock.Set(day(2026, time.July, 31).Add(9 * time.Hour))
	advisorFlowsRunEvaluation(t, h)
	overdue := requireAdvisorFlowInsight(t, advisorFlowsInsights(t, h, advisor.SurfaceReports), "filing_deadline_window")
	assertFilingDeadlineBinding(t, overdue, "2026-07-30", -1, "overdue", 30)
	if overdue.Key == warn.Key {
		t.Fatalf("overdue filing key = %q, want changed key after status/day-count change", overdue.Key)
	}

	h.Clock.Set(day(2026, time.December, 1).Add(9 * time.Hour))
	advisorFlowsRunEvaluation(t, h)
	assertNoAdvisorFlowInsight(t, advisorFlowsInsights(t, h, advisor.SurfaceReports), "filing_deadline_window")
}

func testAdvisorFlowsHeadroom(t *testing.T) {
	f := newDividendFlowFixture(t)
	f.postRetainedEarnings(t, day(2025, time.March, 31), 2_000_000)

	advisorFlowsRunEvaluation(t, f.h)
	insight := requireAdvisorFlowInsight(t, advisorFlowsInsights(t, f.h, advisor.SurfaceDividends), "dividend_set_aside")
	assertAdvisorFlowInsight(t, insight, advisor.SeverityTeal, []advisor.Surface{advisor.SurfaceDashboard, advisor.SurfaceDividends})
	if got := intBinding(t, insight.Bindings, "headroom_minor_units"); got != 2_000_000 {
		t.Fatalf("headroom_minor_units = %d, want 2000000", got)
	}
	// JUR-4 hand computation: GBP 20,000.00 gross - GBP 14,750.00 allowance
	// leaves GBP 5,250.00 taxable; all fits in the first 10% band = GBP 525.00.
	const wantMarginalEstimate = int64(52_500)
	if got := intBinding(t, insight.Bindings, "estimate_minor_units"); got != wantMarginalEstimate {
		t.Fatalf("estimate_minor_units = %d, want %d", got, wantMarginalEstimate)
	}
	expectedText := advisorFlowsExpectedText(t, "dividend_set_aside", advisor.Facts{
		advisor.FactDividendHeadroom:      gbp(2_000_000),
		advisor.FactDividendHeadroomMinor: int64(2_000_000),
		advisor.FactDividendEstimate:      gbp(wantMarginalEstimate),
		advisor.FactDividendEstimateMinor: wantMarginalEstimate,
	}, f.h.Clock.Now())
	if insight.RenderedText != expectedText {
		t.Fatalf("headroom text = %q, want pack-rendered %q", insight.RenderedText, expectedText)
	}
}

func testAdvisorFlowsRatesStale(t *testing.T) {
	now := day(2030, time.January, 7).Add(12 * time.Hour)
	h := newAdvisorFlowsHarness(t, harness.Options{ClockStart: now}, fixtures.RatesStep(map[time.Time]string{
		day(2030, time.January, 2): "0.8500",
	}))

	advisorFlowsRunEvaluation(t, h)
	insight := requireAdvisorFlowInsight(t, advisorFlowsInsights(t, h, advisor.SurfaceBanking), "rates_stale")
	assertAdvisorFlowInsight(t, insight, advisor.SeverityAmber, []advisor.Surface{advisor.SurfaceDashboard, advisor.SurfaceBanking, advisor.SurfaceInvoices})
	if insight.RenderedText != advisorFlowsExpectedText(t, "rates_stale", advisor.Facts{advisor.FactStaleDays: intBinding(t, insight.Bindings, "stale_days")}, h.Clock.Now()) {
		t.Fatalf("rates stale text = %q, want pack-rendered text", insight.RenderedText)
	}

	advisorFlowsFetchECBRates(t, h, day(2030, time.January, 7), "0.8600")
	advisorFlowsRunEvaluation(t, h)
	assertNoAdvisorFlowInsight(t, advisorFlowsInsights(t, h, advisor.SurfaceBanking), "rates_stale")
}

func testAdvisorFlowsDismissalSemantics(t *testing.T) {
	f := newDividendFlowFixture(t)
	f.postRetainedEarnings(t, day(2025, time.March, 31), 2_000_000)

	advisorFlowsRunEvaluation(t, f.h)
	original := requireAdvisorFlowInsight(t, advisorFlowsInsights(t, f.h, advisor.SurfaceDividends), "dividend_set_aside")
	advisorFlowsDismiss(t, f.h, original.Key)
	assertNoAdvisorFlowInsight(t, advisorFlowsInsights(t, f.h, advisor.SurfaceDividends), "dividend_set_aside")

	advisorFlowsRunEvaluation(t, f.h)
	assertNoAdvisorFlowInsight(t, advisorFlowsInsights(t, f.h, advisor.SurfaceDividends), "dividend_set_aside")

	f.postLedgerEntry(t, day(2025, time.March, 31), "advisor flows retained earnings change", "advisor-flows:headroom-change", []ledger.NewPosting{
		{AccountCode: dividendFlowCashAccount, Amount: gbp(100_000), AmountGBP: gbp(100_000)},
		{AccountCode: dividends.RetainedEarningsAccountCode, Amount: gbp(-100_000), AmountGBP: gbp(-100_000)},
	})
	advisorFlowsRunEvaluation(t, f.h)
	changed := requireAdvisorFlowInsight(t, advisorFlowsInsights(t, f.h, advisor.SurfaceDividends), "dividend_set_aside")
	if changed.Key == original.Key {
		t.Fatalf("changed headroom key = %q, want new undismissed key", changed.Key)
	}
	if got := intBinding(t, changed.Bindings, "headroom_minor_units"); got != 2_100_000 {
		t.Fatalf("changed headroom_minor_units = %d, want 2100000", got)
	}
}

func testAdvisorFlowsIdempotency(t *testing.T) {
	f := newDividendFlowFixture(t)
	f.postRetainedEarnings(t, day(2025, time.March, 31), 2_000_000)

	advisorFlowsRunEvaluation(t, f.h)
	wantRows := advisorFlowsInsightRowCount(t, f.h)
	wantActive := len(advisorFlowsInsights(t, f.h, advisor.SurfaceDashboard))
	for i := 0; i < 2; i++ {
		advisorFlowsRunEvaluation(t, f.h)
	}
	if got := advisorFlowsInsightRowCount(t, f.h); got != wantRows {
		t.Fatalf("advisor insight row count after stable evaluations = %d, want stable %d", got, wantRows)
	}
	if got := len(advisorFlowsInsights(t, f.h, advisor.SurfaceDashboard)); got != wantActive {
		t.Fatalf("dashboard active insight count after stable evaluations = %d, want stable %d", got, wantActive)
	}
}

func testAdvisorFlowsDashboardOrdering(t *testing.T) {
	h := newAdvisorFlowsHarness(t, harness.Options{ClockStart: day(2026, time.July, 5).Add(9 * time.Hour)}, fixtures.RatesStep(map[time.Time]string{
		day(2026, time.June, 30): "0.8500",
	}))
	advisorFlowsCreateSentEURInvoice(t, h, "2026-07-06", 100_000)
	h.Clock.Set(day(2026, time.July, 10).Add(9 * time.Hour))
	advisorFlowsFileDLADrawing(t, h, "advisor-flows:dashboard-dla", 150_000)
	advisorFlowsPostRetainedEarnings(t, h, "advisor-flows:dashboard-headroom", day(2026, time.March, 31), 2_000_000)

	advisorFlowsRunEvaluation(t, h)
	first := advisorFlowsInsights(t, h, advisor.SurfaceDashboard)
	second := advisorFlowsInsights(t, h, advisor.SurfaceDashboard)
	if len(first) != 4 {
		t.Fatalf("dashboard insights length = %d, want max 4; insights=%#v", len(first), first)
	}
	if got, want := advisorFlowRuleIDs(first), []string{"dla_overdrawn_bik", "filing_deadline_window", "overdue_invoice", "rates_stale"}; !slices.Equal(got, want) {
		t.Fatalf("dashboard rule order = %#v, want stable amber-first order %#v", got, want)
	}
	if got, want := advisorFlowRuleIDs(second), advisorFlowRuleIDs(first); !slices.Equal(got, want) {
		t.Fatalf("dashboard order changed between reads: first=%#v second=%#v", want, got)
	}
	for _, insight := range first {
		if insight.Severity != advisor.SeverityAmber {
			t.Fatalf("dashboard capped insight %s severity = %q, want amber before any teal", insight.RuleID, insight.Severity)
		}
	}
	requireAdvisorFlowInsight(t, advisorFlowsInsights(t, h, advisor.SurfaceDividends), "dividend_set_aside")
}

func testAdvisorFlowsPostCommitIsolation(t *testing.T) {
	forced := errors.New("forced advisor evaluation failure")
	issueDate := day(2025, time.May, 1)
	settleDate := day(2025, time.May, 2)
	h := newAdvisorFlowsHarness(t, harness.Options{
		ClockStart: issueDate.Add(9 * time.Hour),
		AdvisorOptions: []advisor.ServiceOption{
			advisor.WithBeforeEvaluate(func(string) error { return forced }),
		},
	}, fixtures.RatesStep(map[time.Time]string{
		issueDate:  "0.8500",
		settleDate: "0.8600",
	}))
	sent := advisorFlowsCreateSentEURInvoice(t, h, "2025-05-15", 450_000)
	lockID := mustLifecycleLockID(t, sent)
	if err := h.WaitAdvisorIdle(); err != nil {
		t.Fatalf("WaitAdvisorIdle(seed) error = %v", err)
	}
	baseline := advisorFlowsRunCount(t, h)

	settled := advisorFlowsMarkInvoiceSettled(t, h, sent, "advisor-flows:post-commit-settlement", settleDate)
	if settled.Status != invoicing.InvoiceStatusPaid {
		t.Fatalf("settled status = %q, want paid", settled.Status)
	}
	assertRealisedFXRows(t, context.Background(), h.DB, sent.ID, moneyfx.LockID(lockID), 1, 4_500)
	advisorFlowsWaitForRunCount(t, h, baseline+1)
	if err := h.WaitAdvisorIdle(); err != nil {
		t.Fatalf("WaitAdvisorIdle(settlement) error = %v", err)
	}
	if !strings.Contains(advisorFlowsLastRunError(t, h), forced.Error()) {
		t.Fatalf("last advisor run error = %q, want %q", advisorFlowsLastRunError(t, h), forced.Error())
	}
	it.AssertLedgerBalanced(t, h)
}

type advisorFlowInsight struct {
	Key          string            `json:"key"`
	RuleID       string            `json:"rule_id"`
	Severity     advisor.Severity  `json:"severity"`
	Surfaces     []advisor.Surface `json:"surfaces"`
	RenderedText string            `json:"rendered_text"`
	Bindings     map[string]any    `json:"bindings"`
	CTA          advisor.CTA       `json:"cta"`
	CreatedAt    string            `json:"created_at"`
}

type advisorFlowInsightsResponse struct {
	Insights []advisorFlowInsight `json:"insights"`
}

func newAdvisorFlowsHarness(t *testing.T, opts harness.Options, rates fixtures.RateTable) *harness.Harness {
	t.Helper()
	if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("LoadActive(%q) error = %v", jurisdiction.DefaultSelector, err)
	}
	t.Cleanup(func() {
		if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
			t.Fatalf("restore active jurisdiction pack: %v", err)
		}
	})
	h := harness.New(t, opts)
	fixtures.Company(t, h)
	fixtures.Rates(t, h, rates)
	return h
}

func advisorFlowsCreateSentEURInvoice(t testing.TB, h *harness.Harness, dueDate string, amount int64) invoicing.Invoice {
	t.Helper()
	contoso := fixtures.Contoso(t, h)
	draft := createLifecycleDraftViaHTTP(t, h, contoso.ID)
	patched := patchLifecycleDraftLinesViaHTTP(t, h, draft.ID, "advisor-flow-line", "Advisor flow retainer", amount, dueDate)
	sent := sendLifecycleInvoiceViaHTTP(t, h, patched.ID).Invoice
	if sent.Number == nil {
		t.Fatalf("sent invoice number = nil")
	}
	return sent
}

func advisorFlowsMarkInvoiceSettled(t *testing.T, h *harness.Harness, invoice invoicing.Invoice, txnRef string, date time.Time) invoicing.Invoice {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	service := newLifecycleInvoiceService(t, h, lifecycleIdentity(t, h))
	tx, err := h.BankingPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin banking settlement tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	settled, err := service.MarkSettled(ctx, tx, invoice.ID, txnRef, date, invoice.Totals.Total)
	if err != nil {
		t.Fatalf("MarkSettled(%s) error = %v", invoice.ID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit banking settlement tx: %v", err)
	}
	committed = true
	return settled
}

func advisorFlowsRunEvaluation(t testing.TB, h *harness.Harness) {
	t.Helper()
	response := advisorFlowsRequest(t, h, nethttp.MethodPost, "/api/advisor/refresh", nil)
	if response.StatusCode != nethttp.StatusOK {
		t.Fatalf("advisor refresh status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusOK, string(response.Body))
	}
	var body struct {
		Run struct {
			ID      int64  `json:"id"`
			Trigger string `json:"trigger"`
		} `json:"run"`
	}
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatalf("decode advisor refresh: %v; body=%s", err, string(response.Body))
	}
	if body.Run.ID == 0 || body.Run.Trigger != advisor.ManualRefreshTrigger {
		t.Fatalf("advisor refresh run = %#v, want persisted manual run", body.Run)
	}
}

func advisorFlowsInsights(t testing.TB, h *harness.Harness, surface advisor.Surface) []advisorFlowInsight {
	t.Helper()
	response := advisorFlowsRequest(t, h, nethttp.MethodGet, "/api/advisor/insights?surface="+string(surface), nil)
	if response.StatusCode != nethttp.StatusOK {
		t.Fatalf("advisor insights status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusOK, string(response.Body))
	}
	var body advisorFlowInsightsResponse
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatalf("decode advisor insights: %v; body=%s", err, string(response.Body))
	}
	return body.Insights
}

func advisorFlowsDismiss(t testing.TB, h *harness.Harness, key string) {
	t.Helper()
	response := advisorFlowsRequest(t, h, nethttp.MethodPost, "/api/advisor/insights/"+key+"/dismiss", nil)
	if response.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("dismiss advisor insight status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusNoContent, string(response.Body))
	}
}

type advisorFlowsHTTPResponse struct {
	StatusCode int
	Body       []byte
}

func advisorFlowsRequest(t testing.TB, h *harness.Harness, method string, path string, body io.Reader) advisorFlowsHTTPResponse {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = io.ReadAll(body)
		if err != nil {
			t.Fatalf("read request body for %s %s: %v", method, path, err)
		}
	}
	response := advisorFlowsRequestOnce(t, h, method, path, payload)
	if response.StatusCode == nethttp.StatusUnauthorized {
		advisorFlowsLogin(t, h)
		response = advisorFlowsRequestOnce(t, h, method, path, payload)
	}
	return response
}

func advisorFlowsRequestOnce(t testing.TB, h *harness.Harness, method string, path string, payload []byte) advisorFlowsHTTPResponse {
	t.Helper()
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	request, err := nethttp.NewRequestWithContext(context.Background(), method, path, body)
	if err != nil {
		t.Fatalf("create %s %s request: %v", method, path, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return advisorFlowsHTTPResponse{StatusCode: response.StatusCode, Body: responseBody}
}

func advisorFlowsLogin(t testing.TB, h *harness.Harness) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{
		"email":    "owner@example.test",
		"password": "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("marshal advisor flow login: %v", err)
	}
	response := advisorFlowsRequestOnce(t, h, nethttp.MethodPost, "/api/identity/login", payload)
	if response.StatusCode != nethttp.StatusOK {
		t.Fatalf("advisor flow re-login status = %d, want %d; body=%s", response.StatusCode, nethttp.StatusOK, string(response.Body))
	}
}

func requireAdvisorFlowInsight(t testing.TB, insights []advisorFlowInsight, ruleID string) advisorFlowInsight {
	t.Helper()
	for _, insight := range insights {
		if insight.RuleID == ruleID {
			return insight
		}
	}
	t.Fatalf("advisor insight rule %s not found in %#v", ruleID, insights)
	return advisorFlowInsight{}
}

func assertNoAdvisorFlowInsight(t testing.TB, insights []advisorFlowInsight, ruleID string) {
	t.Helper()
	for _, insight := range insights {
		if insight.RuleID == ruleID {
			t.Fatalf("advisor insight rule %s still active: %#v", ruleID, insight)
		}
	}
}

func assertAdvisorFlowInsight(t testing.TB, insight advisorFlowInsight, severity advisor.Severity, surfaces []advisor.Surface) {
	t.Helper()
	if insight.Severity != severity {
		t.Fatalf("%s severity = %q, want %q", insight.RuleID, insight.Severity, severity)
	}
	for _, surface := range surfaces {
		if !slices.Contains(insight.Surfaces, surface) {
			t.Fatalf("%s surfaces = %#v, want %s", insight.RuleID, insight.Surfaces, surface)
		}
	}
}

func assertFilingDeadlineBinding(t testing.TB, insight advisorFlowInsight, dueDate string, daysUntil int64, status string, warnWindow int64) {
	t.Helper()
	if got := stringBinding(t, insight.Bindings, "due_date"); got != dueDate {
		t.Fatalf("due_date binding = %q, want %q", got, dueDate)
	}
	if got := intBinding(t, insight.Bindings, "days_until"); got != daysUntil {
		t.Fatalf("days_until binding = %d, want %d", got, daysUntil)
	}
	if got := stringBinding(t, insight.Bindings, "filing_status"); got != status {
		t.Fatalf("filing_status binding = %q, want %q", got, status)
	}
	if got := intBinding(t, insight.Bindings, "warn_window_days"); got != warnWindow {
		t.Fatalf("warn_window_days binding = %d, want %d", got, warnWindow)
	}
}

func advisorFlowsExpectedText(t testing.TB, ruleID string, facts advisor.Facts, now time.Time) string {
	t.Helper()
	rules, err := advisor.CompileJurisdictionRules(jurisdiction.AdvisorRules())
	if err != nil {
		t.Fatalf("CompileJurisdictionRules() error = %v", err)
	}
	delta, err := advisor.Evaluate(rules, facts, now)
	if err != nil {
		t.Fatalf("Evaluate(pack text for %s) error = %v", ruleID, err)
	}
	for _, insight := range delta.Insights {
		if insight.RuleID == ruleID {
			return insight.RenderedText
		}
	}
	t.Fatalf("pack evaluation did not render %s; insights=%#v warnings=%#v facts=%#v", ruleID, delta.Insights, delta.Warnings, facts)
	return ""
}

func stringBinding(t testing.TB, values map[string]any, key string) string {
	t.Helper()
	value, ok := values[key]
	if !ok {
		t.Fatalf("binding %q missing from %#v", key, values)
	}
	got, ok := value.(string)
	if !ok {
		t.Fatalf("binding %q = %#v (%T), want string", key, value, value)
	}
	return got
}

func intBinding(t testing.TB, values map[string]any, key string) int64 {
	t.Helper()
	value, ok := values[key]
	if !ok {
		t.Fatalf("binding %q missing from %#v", key, values)
	}
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		got, err := typed.Int64()
		if err != nil {
			t.Fatalf("binding %q json number = %s: %v", key, typed, err)
		}
		return got
	default:
		t.Fatalf("binding %q = %#v (%T), want integer", key, value, value)
		return 0
	}
}

func advisorFlowsFetchECBRates(t *testing.T, h *harness.Harness, rateDate time.Time, rate string) {
	t.Helper()
	xml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
	<Cube>
		<Cube time="%s">
			<Cube currency="GBP" rate="%s"/>
		</Cube>
	</Cube>
</gesmes:Envelope>`, rateDate.Format(time.DateOnly), rate)
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(xml))
	}))
	t.Cleanup(server.Close)

	fetcher, err := moneyfx.NewECBFetcher(moneyfx.ECBFetcherConfig{
		Pool:         testdb.AsModule(t, moneyfx.ModuleName),
		Clock:        h.Clock,
		Location:     time.UTC,
		FeedURL:      server.URL,
		HTTPClient:   server.Client(),
		Retries:      -1,
		RetryBackoff: -1,
	})
	if err != nil {
		t.Fatalf("NewECBFetcher() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := fetcher.Run(ctx); err != nil {
		t.Fatalf("ECB fetcher Run() error = %v", err)
	}
}

func advisorFlowsFileDLADrawing(t *testing.T, h *harness.Harness, source string, amount int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	dlaService := dla.NewWithBusAndClock(h.DLAPool, h.Bus, h.Clock, ledgerService)
	tx, err := h.BankingPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin DLA drawing tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	ensureDividendFlowCashAccount(t, ctx, ledgerService, tx)
	if err := dlaService.FileDrawing(ctx, tx, dla.TxnRef{
		Ref:             source,
		Date:            h.Clock.Now(),
		Amount:          gbp(amount),
		CashAccountCode: dividendFlowCashAccount,
		Description:     "Advisor flow director drawing",
	}); err != nil {
		t.Fatalf("FileDrawing(%s) error = %v", source, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit DLA drawing tx: %v", err)
	}
	committed = true
}

func advisorFlowsPostRetainedEarnings(t *testing.T, h *harness.Harness, source string, date time.Time, amount int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	tx, err := h.LedgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin retained earnings tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	ensureDividendFlowCashAccount(t, ctx, ledgerService, tx)
	if _, err := ledgerService.Post(ctx, tx, ledger.NewJournalEntry{
		Date:         date,
		Description:  "Advisor flow retained earnings",
		SourceModule: "advisor-flows",
		SourceRef:    source,
		Postings: []ledger.NewPosting{
			{AccountCode: dividendFlowCashAccount, Amount: gbp(amount), AmountGBP: gbp(amount)},
			{AccountCode: dividends.RetainedEarningsAccountCode, Amount: gbp(-amount), AmountGBP: gbp(-amount)},
		},
	}); err != nil {
		t.Fatalf("post retained earnings fixture: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit retained earnings tx: %v", err)
	}
	committed = true
}

func advisorFlowsInsightRowCount(t testing.TB, h *harness.Harness) int {
	t.Helper()
	var count int
	if err := h.AdvisorPool.QueryRow(context.Background(), `SELECT count(*) FROM advisor.insights`).Scan(&count); err != nil {
		t.Fatalf("count advisor insights: %v", err)
	}
	return count
}

func advisorFlowsRunCount(t testing.TB, h *harness.Harness) int {
	t.Helper()
	var count int
	if err := h.AdvisorPool.QueryRow(context.Background(), `SELECT count(*) FROM advisor.evaluation_runs`).Scan(&count); err != nil {
		t.Fatalf("count advisor evaluation runs: %v", err)
	}
	return count
}

func advisorFlowsWaitForRunCount(t testing.TB, h *harness.Harness, wantAtLeast int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if advisorFlowsRunCount(t, h) >= wantAtLeast {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("advisor run count = %d, want at least %d", advisorFlowsRunCount(t, h), wantAtLeast)
}

func advisorFlowsLastRunError(t testing.TB, h *harness.Harness) string {
	t.Helper()
	var errorText string
	if err := h.AdvisorPool.QueryRow(context.Background(), `
SELECT COALESCE(error, '')
FROM advisor.evaluation_runs
ORDER BY id DESC
LIMIT 1`).Scan(&errorText); err != nil {
		t.Fatalf("last advisor run error: %v", err)
	}
	return errorText
}

func advisorFlowRuleIDs(insights []advisorFlowInsight) []string {
	out := make([]string, 0, len(insights))
	for _, insight := range insights {
		out = append(out, insight.RuleID)
	}
	return out
}
