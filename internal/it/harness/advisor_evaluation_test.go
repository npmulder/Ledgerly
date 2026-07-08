//go:build integration

package harness_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/advisor"
	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/invoicing"
	it "github.com/npmulder/ledgerly/internal/it"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

func TestAdvisorRunEvaluationRulesEndToEnd(t *testing.T) {
	t.Run("overdue invoice creates and resolves", func(t *testing.T) {
		h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
		fixtures.Company(t, h)
		fixtures.Rates(t, h)
		service := newInvoiceService(t, h)

		sent, err := service.Send(context.Background(), createEURInvoiceDraft(t, h, service, 20_000).ID)
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		h.Clock.Set(sent.DueDate.AddDate(0, 0, 3))

		runAdvisorRefresh(t, h)
		insight := requireAdvisorInsight(t, h, "overdue_invoice")
		assertAdvisorInsight(t, insight, advisor.SeverityAmber, []advisor.Surface{advisor.SurfaceDashboard, advisor.SurfaceInvoices})
		if insight.CTA.Action != "invoicing.sendReminder" || insight.CTA.Params["invoice_id"] != sent.ID {
			t.Fatalf("overdue CTA = %#v, want sendReminder invoice %s", insight.CTA, sent.ID)
		}
		if !strings.Contains(insight.RenderedText, "is 3 days overdue") {
			t.Fatalf("overdue text = %q, want 3 days overdue", insight.RenderedText)
		}

		if _, err := markSettledFromBankingTx(t, h, service, sent.ID, "advisor-clear-overdue", h.Clock.Now(), sent.Totals.Total); err != nil {
			t.Fatalf("MarkSettled(clear overdue) error = %v", err)
		}
		runAdvisorRefresh(t, h)
		assertNoActiveAdvisorInsight(t, h, "overdue_invoice")
		it.AssertLedgerBalanced(t, h)
	})

	t.Run("DLA overdrawn creates and resolves", func(t *testing.T) {
		h := harness.New(t, harness.Options{ClockStart: time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)})
		fixtures.Company(t, h)
		fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{h.Clock.Now(): "0.8500"}))
		fixture := newDLAFixtureFromHarness(t, h)

		fixture.fileDrawingFromBanking(t, dla.TxnRef{
			Ref:             "advisor:dla-overdrawn",
			Date:            h.Clock.Now(),
			Amount:          gbp(150_000),
			CashAccountCode: dlaCashAccount,
		})
		runAdvisorRefresh(t, h)
		insight := requireAdvisorInsight(t, h, "dla_overdrawn_bik")
		assertAdvisorInsight(t, insight, advisor.SeverityAmber, []advisor.Surface{advisor.SurfaceDashboard, advisor.SurfaceDLA})
		if insight.CTA.Action != "navigate:/dla?director=director-1" {
			t.Fatalf("DLA CTA action = %q, want director DLA route", insight.CTA.Action)
		}
		if !strings.Contains(insight.RenderedText, "benefit in kind") {
			t.Fatalf("DLA text = %q, want BIK warning", insight.RenderedText)
		}

		if err := fixture.dla.AddEntry(fixture.ctx, dla.NewEntry{
			Date:            h.Clock.Now(),
			Kind:            dla.EntryKindRepayment,
			Description:     "Clear advisor test balance",
			Amount:          gbp(150_000),
			Source:          "advisor:dla-clear",
			CashAccountCode: dlaCashAccount,
		}); err != nil {
			t.Fatalf("AddEntry(clear DLA) error = %v", err)
		}
		runAdvisorRefresh(t, h)
		assertNoActiveAdvisorInsight(t, h, "dla_overdrawn_bik")
		it.AssertLedgerBalanced(t, h)
	})

	t.Run("filing deadline creates and resolves", func(t *testing.T) {
		h := harness.New(t, harness.Options{ClockStart: time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)})
		fixtures.Company(t, h, fixtures.CompanyVATRegistered(true))
		fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{h.Clock.Now(): "0.8500"}))

		runAdvisorRefresh(t, h)
		insight := requireAdvisorInsight(t, h, "filing_deadline_window")
		assertAdvisorInsight(t, insight, advisor.SeverityAmber, []advisor.Surface{advisor.SurfaceDashboard, advisor.SurfaceReports})
		if !strings.Contains(insight.RenderedText, "VAT return due 2026-07-30") {
			t.Fatalf("filing text = %q, want VAT due badge wording", insight.RenderedText)
		}

		h.Clock.Set(time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC))
		runAdvisorRefresh(t, h)
		assertNoActiveAdvisorInsight(t, h, "filing_deadline_window")
		it.AssertLedgerBalanced(t, h)
	})

	t.Run("dividend headroom creates and resolves", func(t *testing.T) {
		h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 7, 1, 9, 0, 0, 0, time.UTC)})
		fixtures.Company(t, h)
		fixtures.Rates(t, h)
		postRetainedEarnings(t, h, "2025-03-31", 2_000_000)

		runAdvisorRefresh(t, h)
		insight := requireAdvisorInsight(t, h, "dividend_set_aside")
		assertAdvisorInsight(t, insight, advisor.SeverityTeal, []advisor.Surface{advisor.SurfaceDashboard, advisor.SurfaceDividends})
		if !strings.Contains(insight.RenderedText, "Set aside") || !strings.Contains(insight.RenderedText, "personally") {
			t.Fatalf("dividend text = %q, want personal set-aside wording", insight.RenderedText)
		}

		postExpense(t, h, "2025-07-02", 3_000_000)
		runAdvisorRefresh(t, h)
		assertNoActiveAdvisorInsight(t, h, "dividend_set_aside")
		it.AssertLedgerBalanced(t, h)
	})

	t.Run("stale rates create and resolve", func(t *testing.T) {
		h := harness.New(t, harness.Options{ClockStart: time.Date(2030, 1, 7, 12, 0, 0, 0, time.UTC)})
		fixtures.Company(t, h)
		fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{
			time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC): "0.8500",
		}))

		runAdvisorRefresh(t, h)
		insight := requireAdvisorInsight(t, h, "rates_stale")
		assertAdvisorInsight(t, insight, advisor.SeverityAmber, []advisor.Surface{advisor.SurfaceDashboard, advisor.SurfaceBanking, advisor.SurfaceInvoices})
		if !strings.Contains(insight.RenderedText, "ECB rates are stale") {
			t.Fatalf("rates text = %q, want stale rates warning", insight.RenderedText)
		}

		store := moneyfx.NewStore(testdb.AsModule(t, moneyfx.ModuleName))
		if err := store.StoreECBRates(context.Background(), []moneyfx.ECBRate{{
			Date:     time.Date(2030, 1, 7, 0, 0, 0, 0, time.UTC),
			Currency: "GBP",
			Rate:     "0.8500",
		}}); err != nil {
			t.Fatalf("store current ECB rate: %v", err)
		}
		runAdvisorRefresh(t, h)
		assertNoActiveAdvisorInsight(t, h, "rates_stale")
		it.AssertLedgerBalanced(t, h)
	})
}

func TestAdvisorDebouncesLedgerEntryStorm(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Company(t, h)
	fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{h.Clock.Now(): "0.8500"}))
	if err := h.WaitAdvisorIdle(); err != nil {
		t.Fatalf("WaitAdvisorIdle(seed) error = %v", err)
	}
	baseline := advisorRunCount(t, h)

	ctx := context.Background()
	service := ledger.New(h.LedgerPool, h.Bus)
	tx, err := h.LedgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin ledger storm tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	ensureCashAccount(t, ctx, service, tx)
	currency := "GBP"
	if _, err := service.EnsureAccount(ctx, tx, ledger.AccountSpec{
		Code:     "4999-advisor-storm",
		Name:     "Advisor storm income",
		Type:     ledger.AccountTypeIncome,
		Currency: &currency,
	}); err != nil {
		t.Fatalf("ensure storm account: %v", err)
	}
	for i := 0; i < 50; i++ {
		if _, err := service.Post(ctx, tx, ledger.NewJournalEntry{
			Date:         h.Clock.Now(),
			Description:  "Advisor debounce storm",
			SourceModule: "advisor-test",
			SourceRef:    fmt.Sprintf("storm-entry-%02d", i),
			Postings: []ledger.NewPosting{
				{AccountCode: "1000-cash-gbp", Amount: money.Money{Amount: 100, Currency: "GBP"}, AmountGBP: money.Money{Amount: 100, Currency: "GBP"}},
				{AccountCode: "4999-advisor-storm", Amount: money.Money{Amount: -100, Currency: "GBP"}, AmountGBP: money.Money{Amount: -100, Currency: "GBP"}},
			},
		}); err != nil {
			t.Fatalf("post storm entry %d: %v", i, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit ledger storm tx: %v", err)
	}
	committed = true

	waitForAdvisorRunCount(t, h, baseline+1)
	if err := h.WaitAdvisorIdle(); err != nil {
		t.Fatalf("WaitAdvisorIdle(storm) error = %v", err)
	}
	if got := advisorRunCount(t, h); got != baseline+1 {
		t.Fatalf("advisor run count after 50 ledger entries = %d, want %d", got, baseline+1)
	}
	it.AssertLedgerBalanced(t, h)
}

func TestAdvisorConcurrentManualStormIsSerializedAndIdempotent(t *testing.T) {
	var current atomic.Int32
	var maxConcurrent atomic.Int32
	h := harness.New(t, harness.Options{
		ClockStart: time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC),
		AdvisorOptions: []advisor.ServiceOption{
			advisor.WithBeforeEvaluate(func(string) error {
				now := current.Add(1)
				for {
					maxSeen := maxConcurrent.Load()
					if now <= maxSeen || maxConcurrent.CompareAndSwap(maxSeen, now) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				current.Add(-1)
				return nil
			}),
		},
	})
	fixtures.Company(t, h)
	fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{h.Clock.Now(): "0.8500"}))
	fixture := newDLAFixtureFromHarness(t, h)
	fixture.fileDrawingFromBanking(t, dla.TxnRef{
		Ref:             "advisor:race-overdrawn",
		Date:            h.Clock.Now(),
		Amount:          gbp(100_000),
		CashAccountCode: dlaCashAccount,
	})
	if err := h.WaitAdvisorIdle(); err != nil {
		t.Fatalf("WaitAdvisorIdle(seed) error = %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.RefreshAdvisorNow()
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("RefreshAdvisorNow concurrent error = %v", err)
		}
	}
	if got := maxConcurrent.Load(); got != 1 {
		t.Fatalf("max concurrent advisor evaluations = %d, want 1", got)
	}
	assertActiveAdvisorInsightCount(t, h, "dla_overdrawn_bik", 1)
	it.AssertLedgerBalanced(t, h)
}

func TestAdvisorPostCommitEvaluationFailureDoesNotRollbackSettlement(t *testing.T) {
	forced := errors.New("forced advisor evaluation failure")
	h := harness.New(t, harness.Options{
		ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC),
		AdvisorOptions: []advisor.ServiceOption{
			advisor.WithBeforeEvaluate(func(string) error { return forced }),
		},
	})
	fixtures.Company(t, h)
	fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{
		time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC): "0.8500",
		time.Date(2025, 5, 2, 0, 0, 0, 0, time.UTC): "0.8600",
	}))
	service := newInvoiceService(t, h)
	sent, err := service.Send(context.Background(), createEURInvoiceDraft(t, h, service, 450_000).ID)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	lockID := mustInvoiceLockID(t, sent)
	if err := h.WaitAdvisorIdle(); err != nil {
		t.Fatalf("WaitAdvisorIdle(seed) error = %v", err)
	}
	baseline := advisorRunCount(t, h)

	settled, err := markSettledFromBankingTx(t, h, service, sent.ID, "advisor-post-commit-settlement", time.Date(2025, 5, 2, 0, 0, 0, 0, time.UTC), invoicing.Money{Amount: 450_000, Currency: "EUR"})
	if err != nil {
		t.Fatalf("MarkSettled() error = %v; advisor failure must not roll back settlement", err)
	}
	if settled.Status != invoicing.InvoiceStatusPaid {
		t.Fatalf("settled Status = %q, want paid", settled.Status)
	}
	assertRealisedFXRow(t, h, sent.ID, lockID, 1, 4_500)
	waitForAdvisorRunCount(t, h, baseline+1)
	if err := h.WaitAdvisorIdle(); err != nil {
		t.Fatalf("WaitAdvisorIdle(settlement) error = %v", err)
	}
	if !strings.Contains(lastAdvisorRunError(t, h), forced.Error()) {
		t.Fatalf("last advisor run error = %q, want forced failure", lastAdvisorRunError(t, h))
	}
	it.AssertLedgerBalanced(t, h)
}

func runAdvisorRefresh(t testing.TB, h *harness.Harness) advisor.EvaluationRun {
	t.Helper()
	run, err := h.RefreshAdvisorNow()
	if err != nil {
		t.Fatalf("RefreshAdvisorNow() error = %v", err)
	}
	if run.Trigger != advisor.ManualRefreshTrigger {
		t.Fatalf("advisor run trigger = %q, want %q", run.Trigger, advisor.ManualRefreshTrigger)
	}
	return run
}

func requireAdvisorInsight(t testing.TB, h *harness.Harness, ruleID string) advisor.Insight {
	t.Helper()
	active := activeAdvisorInsights(t, h)
	for _, insight := range active {
		if insight.RuleID == ruleID {
			return insight
		}
	}
	t.Fatalf("active advisor insight for rule %s not found; active=%#v", ruleID, active)
	return advisor.Insight{}
}

func assertNoActiveAdvisorInsight(t testing.TB, h *harness.Harness, ruleID string) {
	t.Helper()
	for _, insight := range activeAdvisorInsights(t, h) {
		if insight.RuleID == ruleID {
			t.Fatalf("active advisor insight for rule %s still present: %#v", ruleID, insight)
		}
	}
}

func activeAdvisorInsights(t testing.TB, h *harness.Harness) []advisor.Insight {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	insights, err := (advisor.Store{}).ActiveInsights(ctx, h.AdvisorPool, "")
	if err != nil {
		t.Fatalf("ActiveInsights() error = %v", err)
	}
	return insights
}

func assertAdvisorInsight(t testing.TB, insight advisor.Insight, severity advisor.Severity, surfaces []advisor.Surface) {
	t.Helper()
	if insight.Severity != severity {
		t.Fatalf("%s severity = %q, want %q", insight.RuleID, insight.Severity, severity)
	}
	for _, surface := range surfaces {
		if !advisorInsightHasSurface(insight, surface) {
			t.Fatalf("%s surfaces = %#v, want %s", insight.RuleID, insight.Surfaces, surface)
		}
	}
}

func advisorInsightHasSurface(insight advisor.Insight, surface advisor.Surface) bool {
	for _, got := range insight.Surfaces {
		if got == surface {
			return true
		}
	}
	return false
}

func assertActiveAdvisorInsightCount(t testing.TB, h *harness.Harness, ruleID string, want int) {
	t.Helper()
	var got int
	if err := h.AdvisorPool.QueryRow(context.Background(), `
SELECT count(*)
FROM advisor.insights
WHERE rule_id = $1
	AND resolved_at IS NULL`, ruleID).Scan(&got); err != nil {
		t.Fatalf("count active advisor insights for %s: %v", ruleID, err)
	}
	if got != want {
		t.Fatalf("active advisor insight count for %s = %d, want %d", ruleID, got, want)
	}
}

func advisorRunCount(t testing.TB, h *harness.Harness) int {
	t.Helper()
	var count int
	if err := h.AdvisorPool.QueryRow(context.Background(), `SELECT count(*) FROM advisor.evaluation_runs`).Scan(&count); err != nil {
		t.Fatalf("count advisor evaluation runs: %v", err)
	}
	return count
}

func waitForAdvisorRunCount(t testing.TB, h *harness.Harness, wantAtLeast int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if advisorRunCount(t, h) >= wantAtLeast {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("advisor run count = %d, want at least %d", advisorRunCount(t, h), wantAtLeast)
}

func lastAdvisorRunError(t testing.TB, h *harness.Harness) string {
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
