package harness_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/npmulder/ledgerly/internal/advisor"
	"github.com/npmulder/ledgerly/internal/it/testdb"
)

func TestAdvisorStoreApplyIdempotencyDismissalsAndResolution(t *testing.T) {
	pool := testdb.AsModule(t, advisor.ModuleName)
	store := advisor.Store{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rule, err := advisor.CompileRule(advisor.RuleDef{
		ID:           "count-rule",
		Severity:     advisor.SeverityAmber,
		Surfaces:     []advisor.Surface{advisor.SurfaceDashboard},
		FactQuery:    []advisor.FactKey{"count"},
		Condition:    "count > 0",
		TextTemplate: "Count {{ count }}",
		CTA:          advisor.CTA{Label: "Open", Action: "test.open"},
	})
	if err != nil {
		t.Fatalf("CompileRule() error = %v", err)
	}

	first := evaluateForStore(t, rule, 1, time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC))
	if err := store.Apply(ctx, pool, first); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	if err := store.Apply(ctx, pool, first); err != nil {
		t.Fatalf("Apply(first again) error = %v", err)
	}
	assertInsightRowCount(t, ctx, pool, 1)

	second := evaluateForStore(t, rule, 2, time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC))
	if first.Insights[0].Key == second.Insights[0].Key {
		t.Fatalf("changed fact produced same key %q", first.Insights[0].Key)
	}
	if err := store.Apply(ctx, pool, second); err != nil {
		t.Fatalf("Apply(second) error = %v", err)
	}
	assertInsightRowCount(t, ctx, pool, 2)
	assertResolution(t, ctx, pool, first.Insights[0].Key, advisor.ResolutionSuperseded, second.Insights[0].Key)

	if err := store.Dismiss(ctx, pool, second.Insights[0].Key, time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Dismiss(second) error = %v", err)
	}
	if err := store.Apply(ctx, pool, second); err != nil {
		t.Fatalf("Apply(second dismissed) error = %v", err)
	}
	active, err := store.ActiveInsights(ctx, pool, advisor.SurfaceDashboard)
	if err != nil {
		t.Fatalf("ActiveInsights() error = %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active after dismissal = %#v, want none", active)
	}
	assertDismissalRowCount(t, ctx, pool, 1)

	third := evaluateForStore(t, rule, 3, time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC))
	if err := store.Apply(ctx, pool, third); err != nil {
		t.Fatalf("Apply(third) error = %v", err)
	}
	active, err = store.ActiveInsights(ctx, pool, advisor.SurfaceDashboard)
	if err != nil {
		t.Fatalf("ActiveInsights() error = %v", err)
	}
	if len(active) != 1 || active[0].Key != third.Insights[0].Key {
		t.Fatalf("active after fact change = %#v, want third insight", active)
	}
	assertDismissalRowCount(t, ctx, pool, 1)

	noLongerFiring, err := advisor.Evaluate([]advisor.RuleDef{rule}, advisor.Facts{"count": 0}, time.Date(2026, 7, 9, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Evaluate(no longer firing) error = %v", err)
	}
	if len(noLongerFiring.Insights) != 0 || len(noLongerFiring.EvaluatedRuleIDs) != 1 {
		t.Fatalf("no-longer-firing delta = %#v", noLongerFiring)
	}
	if err := store.Apply(ctx, pool, noLongerFiring); err != nil {
		t.Fatalf("Apply(no longer firing) error = %v", err)
	}
	active, err = store.ActiveInsights(ctx, pool, advisor.SurfaceDashboard)
	if err != nil {
		t.Fatalf("ActiveInsights() error = %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active after no-longer-firing = %#v, want none", active)
	}
	assertResolution(t, ctx, pool, third.Insights[0].Key, advisor.ResolutionNoLongerFiring, "")
}

type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func evaluateForStore(t testing.TB, rule advisor.RuleDef, count int, now time.Time) advisor.Delta {
	t.Helper()
	delta, err := advisor.Evaluate([]advisor.RuleDef{rule}, advisor.Facts{"count": count}, now)
	if err != nil {
		t.Fatalf("Evaluate(%d) error = %v", count, err)
	}
	if len(delta.Warnings) != 0 {
		t.Fatalf("Evaluate(%d) warnings = %#v", count, delta.Warnings)
	}
	if len(delta.Insights) != 1 {
		t.Fatalf("Evaluate(%d) insights length = %d, want 1", count, len(delta.Insights))
	}
	return delta
}

func assertInsightRowCount(t testing.TB, ctx context.Context, tx queryer, want int) {
	t.Helper()
	var got int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM advisor.insights`).Scan(&got); err != nil {
		t.Fatalf("count advisor.insights: %v", err)
	}
	if got != want {
		t.Fatalf("advisor.insights count = %d, want %d", got, want)
	}
}

func assertDismissalRowCount(t testing.TB, ctx context.Context, tx queryer, want int) {
	t.Helper()
	var got int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM advisor.dismissals`).Scan(&got); err != nil {
		t.Fatalf("count advisor.dismissals: %v", err)
	}
	if got != want {
		t.Fatalf("advisor.dismissals count = %d, want %d", got, want)
	}
}

func assertResolution(t testing.TB, ctx context.Context, tx queryer, key advisor.InsightKey, wantResolution string, wantSupersededBy advisor.InsightKey) {
	t.Helper()
	var (
		resolvedAt   sql.NullTime
		resolution   sql.NullString
		supersededBy sql.NullString
	)
	if err := tx.QueryRow(ctx, `
SELECT resolved_at, resolution, superseded_by
FROM advisor.insights
WHERE key = $1`,
		string(key),
	).Scan(&resolvedAt, &resolution, &supersededBy); err != nil {
		t.Fatalf("resolution for %s: %v", key, err)
	}
	if !resolvedAt.Valid {
		t.Fatalf("resolved_at for %s is NULL, want resolved", key)
	}
	if !resolution.Valid || resolution.String != wantResolution {
		t.Fatalf("resolution for %s = %q valid=%v, want %q", key, resolution.String, resolution.Valid, wantResolution)
	}
	if wantSupersededBy == "" {
		if supersededBy.Valid {
			t.Fatalf("superseded_by for %s = %q, want NULL", key, supersededBy.String)
		}
		return
	}
	if !supersededBy.Valid || supersededBy.String != string(wantSupersededBy) {
		t.Fatalf("superseded_by for %s = %q valid=%v, want %s", key, supersededBy.String, supersededBy.Valid, wantSupersededBy)
	}
}
