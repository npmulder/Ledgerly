package advisor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

const (
	ResolutionSuperseded     = "superseded"
	ResolutionNoLongerFiring = "no_longer_firing"
)

// Store owns advisor persistence. SQL qualifies advisor objects so callers can
// share transactions from any module pool.
type Store struct{}

// Apply persists an Evaluate delta idempotently and resolves active insights
// for evaluated rules that no longer fire.
func (Store) Apply(ctx context.Context, tx db.Tx, delta Delta) error {
	_, err := (Store{}).ApplyWithSummary(ctx, tx, delta)
	return err
}

// ApplyWithSummary persists an Evaluate delta and returns audit counts for the
// evaluation run log.
func (Store) ApplyWithSummary(ctx context.Context, tx db.Tx, delta Delta) (ApplySummary, error) {
	now := delta.GeneratedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	var summary ApplySummary
	firedByRule := make(map[string][]string)
	for _, insight := range delta.Insights {
		exists, err := insightExists(ctx, tx, insight.Key)
		if err != nil {
			return ApplySummary{}, err
		}
		if err := upsertInsight(ctx, tx, insight); err != nil {
			return ApplySummary{}, err
		}
		if !exists {
			summary.InsightsCreated++
		}
		firedByRule[insight.RuleID] = append(firedByRule[insight.RuleID], string(insight.Key))
	}

	for _, ruleID := range delta.EvaluatedRuleIDs {
		firedKeys := firedByRule[ruleID]
		if len(firedKeys) == 0 {
			tag, err := tx.Exec(ctx, `
UPDATE advisor.insights
SET resolved_at = $2,
	resolution = $3,
	superseded_by = NULL
WHERE rule_id = $1
	AND resolved_at IS NULL`,
				ruleID,
				now,
				ResolutionNoLongerFiring,
			)
			if err != nil {
				return ApplySummary{}, fmt.Errorf("advisor: resolve inactive rule %s: %w", ruleID, err)
			}
			summary.InsightsResolved += int(tag.RowsAffected())
			continue
		}

		supersededBy := firedKeys[0]
		tag, err := tx.Exec(ctx, `
UPDATE advisor.insights
SET resolved_at = $2,
	resolution = $3,
	superseded_by = $4
WHERE rule_id = $1
	AND resolved_at IS NULL
	AND NOT (key = ANY($5::text[]))`,
			ruleID,
			now,
			ResolutionSuperseded,
			supersededBy,
			firedKeys,
		)
		if err != nil {
			return ApplySummary{}, fmt.Errorf("advisor: resolve superseded rule %s: %w", ruleID, err)
		}
		summary.InsightsSuperseded += int(tag.RowsAffected())
	}

	return summary, nil
}

// Dismiss suppresses one active insight key until its facts change and produce
// a different key.
func (Store) Dismiss(ctx context.Context, tx db.Tx, key InsightKey, dismissedAt time.Time) error {
	if dismissedAt.IsZero() {
		dismissedAt = time.Now().UTC()
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO advisor.dismissals (insight_key, dismissed_at)
VALUES ($1, $2)
ON CONFLICT (insight_key) DO UPDATE
SET dismissed_at = EXCLUDED.dismissed_at`,
		string(key),
		dismissedAt.UTC(),
	); err != nil {
		return fmt.Errorf("advisor: dismiss insight %s: %w", key, err)
	}
	return nil
}

// ActiveInsights returns unresolved, undismissed insights for a surface.
func (Store) ActiveInsights(ctx context.Context, tx db.Tx, surface Surface) ([]Insight, error) {
	rows, err := tx.Query(ctx, `
SELECT i.key,
	i.rule_id,
	i.fact_hash,
	i.severity,
	i.surfaces,
	i.rendered_text,
	i.bindings,
	i.cta,
	i.created_at,
	i.resolved_at,
	i.resolution,
	i.superseded_by,
	d.dismissed_at
FROM advisor.insights i
LEFT JOIN advisor.dismissals d ON d.insight_key = i.key
WHERE i.resolved_at IS NULL
	AND d.insight_key IS NULL
	AND ($1 = '' OR $1 = ANY(i.surfaces))
ORDER BY i.created_at, i.key`,
		string(surface),
	)
	if err != nil {
		return nil, fmt.Errorf("advisor: active insights: %w", err)
	}
	defer rows.Close()

	insights := []Insight{}
	for rows.Next() {
		insight, err := scanInsight(rows)
		if err != nil {
			return nil, err
		}
		insights = append(insights, insight)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("advisor: collect active insights: %w", err)
	}
	return insights, nil
}

type insightScanner interface {
	Scan(dest ...any) error
}

func upsertInsight(ctx context.Context, tx db.Tx, insight Insight) error {
	bindings, err := json.Marshal(insight.Bindings)
	if err != nil {
		return fmt.Errorf("advisor: marshal bindings for %s: %w", insight.Key, err)
	}
	cta, err := json.Marshal(insight.CTA)
	if err != nil {
		return fmt.Errorf("advisor: marshal cta for %s: %w", insight.Key, err)
	}
	surfaces := surfaceStrings(insight.Surfaces)
	createdAt := insight.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO advisor.insights (
	key,
	rule_id,
	fact_hash,
	severity,
	surfaces,
	rendered_text,
	bindings,
	cta,
	created_at
) VALUES (
	$1,
	$2,
	$3,
	$4,
	$5,
	$6,
	$7::jsonb,
	$8::jsonb,
	$9
)
ON CONFLICT (key) DO UPDATE
SET severity = EXCLUDED.severity,
	surfaces = EXCLUDED.surfaces,
	rendered_text = EXCLUDED.rendered_text,
	bindings = EXCLUDED.bindings,
	cta = EXCLUDED.cta,
	resolved_at = NULL,
	resolution = NULL,
	superseded_by = NULL
WHERE advisor.insights.resolved_at IS NOT NULL
	OR advisor.insights.resolution IS NOT NULL
	OR advisor.insights.superseded_by IS NOT NULL
	OR advisor.insights.severity IS DISTINCT FROM EXCLUDED.severity
	OR advisor.insights.surfaces IS DISTINCT FROM EXCLUDED.surfaces
	OR advisor.insights.rendered_text IS DISTINCT FROM EXCLUDED.rendered_text
	OR advisor.insights.bindings IS DISTINCT FROM EXCLUDED.bindings
	OR advisor.insights.cta IS DISTINCT FROM EXCLUDED.cta`,
		string(insight.Key),
		insight.RuleID,
		insight.FactHash,
		string(insight.Severity),
		surfaces,
		insight.RenderedText,
		string(bindings),
		string(cta),
		createdAt,
	); err != nil {
		return fmt.Errorf("advisor: upsert insight %s: %w", insight.Key, err)
	}
	return nil
}

func insightExists(ctx context.Context, tx db.Tx, key InsightKey) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
	SELECT 1
	FROM advisor.insights
	WHERE key = $1
)`, string(key)).Scan(&exists); err != nil {
		return false, fmt.Errorf("advisor: check insight %s existence: %w", key, err)
	}
	return exists, nil
}

// InsertEvaluationRun records one whole-set evaluation attempt.
func (Store) InsertEvaluationRun(ctx context.Context, tx db.Tx, run EvaluationRun) (EvaluationRun, error) {
	trigger := strings.TrimSpace(run.Trigger)
	if trigger == "" {
		trigger = "unknown"
	}
	startedAt := run.StartedAt.UTC()
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	finishedAt := run.FinishedAt.UTC()
	if finishedAt.IsZero() {
		finishedAt = startedAt.Add(run.Duration)
	}
	duration := run.Duration
	if duration < 0 {
		duration = 0
	}
	if run.Warnings == nil {
		run.Warnings = []Warning{}
	}
	warnings, err := json.Marshal(run.Warnings)
	if err != nil {
		return EvaluationRun{}, fmt.Errorf("advisor: marshal evaluation run warnings: %w", err)
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO advisor.evaluation_runs (
	trigger,
	started_at,
	finished_at,
	duration_ms,
	insights_created,
	insights_superseded,
	insights_resolved,
	error,
	warnings
) VALUES (
	$1,
	$2,
	$3,
	$4,
	$5,
	$6,
	$7,
	NULLIF($8, ''),
	$9::jsonb
)
RETURNING id`,
		trigger,
		startedAt,
		finishedAt,
		duration.Milliseconds(),
		run.InsightsCreated,
		run.InsightsSuperseded,
		run.InsightsResolved,
		strings.TrimSpace(run.Error),
		string(warnings),
	).Scan(&run.ID); err != nil {
		return EvaluationRun{}, fmt.Errorf("advisor: insert evaluation run: %w", err)
	}
	run.Trigger = trigger
	run.StartedAt = startedAt
	run.FinishedAt = finishedAt
	run.Duration = duration
	return run, nil
}

func scanInsight(row insightScanner) (Insight, error) {
	var (
		insight      Insight
		key          string
		severity     string
		surfaces     []string
		bindings     []byte
		cta          []byte
		resolvedAt   sql.NullTime
		resolution   sql.NullString
		supersededBy sql.NullString
		dismissedAt  sql.NullTime
	)
	if err := row.Scan(
		&key,
		&insight.RuleID,
		&insight.FactHash,
		&severity,
		&surfaces,
		&insight.RenderedText,
		&bindings,
		&cta,
		&insight.CreatedAt,
		&resolvedAt,
		&resolution,
		&supersededBy,
		&dismissedAt,
	); err != nil {
		return Insight{}, fmt.Errorf("advisor: scan insight: %w", err)
	}
	if err := json.Unmarshal(bindings, &insight.Bindings); err != nil {
		return Insight{}, fmt.Errorf("advisor: unmarshal bindings for %s: %w", key, err)
	}
	if err := json.Unmarshal(cta, &insight.CTA); err != nil {
		return Insight{}, fmt.Errorf("advisor: unmarshal cta for %s: %w", key, err)
	}
	insight.Key = InsightKey(key)
	insight.Severity = Severity(severity)
	insight.Surfaces = surfacesFromStrings(surfaces)
	if resolvedAt.Valid {
		value := resolvedAt.Time
		insight.ResolvedAt = &value
	}
	if resolution.Valid {
		insight.Resolution = resolution.String
	}
	if supersededBy.Valid {
		value := InsightKey(supersededBy.String)
		insight.SupersededBy = &value
	}
	if dismissedAt.Valid {
		value := dismissedAt.Time
		insight.DismissedAt = &value
	}
	return insight, nil
}

func surfaceStrings(surfaces []Surface) []string {
	out := make([]string, len(surfaces))
	for index, surface := range surfaces {
		out[index] = string(surface)
	}
	return out
}

func surfacesFromStrings(surfaces []string) []Surface {
	out := make([]Surface, len(surfaces))
	for index, surface := range surfaces {
		out[index] = Surface(surface)
	}
	return out
}
