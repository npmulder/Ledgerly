package advisor

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

// ModuleName is the database schema and module boundary name.
const ModuleName = "advisor"

// FactKey names one typed fact supplied by a provider. The advisor engine never
// fetches facts; callers inject a complete fact set for evaluation.
type FactKey string

// FactValue is a scalar, money value, date, day count, or small struct/map.
type FactValue any

// Facts is the complete input to one pure advisor evaluation run.
type Facts map[FactKey]FactValue

// Days is a whole-day duration used by date arithmetic in conditions.
type Days int

// Severity is the closed set of advisor insight severities.
type Severity string

const (
	SeverityTeal  Severity = "teal"
	SeverityAmber Severity = "amber"
)

// Surface identifies where an insight may be shown.
type Surface string

const (
	SurfaceDashboard Surface = "dashboard"
	SurfaceInvoices  Surface = "invoices"
	SurfaceBanking   Surface = "banking"
	SurfaceDLA       Surface = "dla"
	SurfaceDividends Surface = "dividends"
	SurfaceReports   Surface = "reports"
)

// CTA is a declarative frontend action. The advisor records intent only; it
// never performs side effects.
type CTA struct {
	Label  string         `json:"label" yaml:"label"`
	Action string         `json:"action" yaml:"action"`
	Params map[string]any `json:"params,omitempty" yaml:"params,omitempty"`
}

// RuleDef is a compiled deterministic advisor rule.
type RuleDef struct {
	ID           string
	Severity     Severity
	Surfaces     []Surface
	FactQuery    []FactKey
	Condition    string
	TextTemplate string
	CTA          CTA

	condition conditionExpr
}

// InsightKey is the idempotency key for one rule firing and fact binding hash.
type InsightKey string

// Insight is the persisted advisor insight model.
type Insight struct {
	Key          InsightKey
	RuleID       string
	FactHash     string
	Severity     Severity
	Surfaces     []Surface
	RenderedText string
	Bindings     map[string]any
	CTA          CTA
	CreatedAt    time.Time
	ResolvedAt   *time.Time
	Resolution   string
	SupersededBy *InsightKey
	DismissedAt  *time.Time
}

// Warning records a skipped rule and the reason. Warnings are part of Delta so
// callers can log them without turning a bad rule/fact into a failed run.
type Warning struct {
	RuleID  string
	Message string
}

// Delta is the pure result of Evaluate. Apply compares this desired active set
// against durable advisor tables.
type Delta struct {
	Insights         []Insight
	EvaluatedRuleIDs []string
	Warnings         []Warning
	GeneratedAt      time.Time
}

var templateIdentPattern = regexp.MustCompile(`\A[A-Za-z_][A-Za-z0-9_]*\z`)

// CompileRule validates and compiles a rule definition before evaluation.
func CompileRule(rule RuleDef) (RuleDef, error) {
	if strings.TrimSpace(rule.ID) == "" {
		return RuleDef{}, fmt.Errorf("advisor: rule id must not be empty")
	}
	severity, err := normalizeSeverity(string(rule.Severity))
	if err != nil {
		return RuleDef{}, fmt.Errorf("advisor: rule %s severity: %w", rule.ID, err)
	}
	surfaces, err := normalizeSurfaces(rule.Surfaces)
	if err != nil {
		return RuleDef{}, fmt.Errorf("advisor: rule %s surfaces: %w", rule.ID, err)
	}
	factQuery, err := normalizeFactQuery(rule.FactQuery)
	if err != nil {
		return RuleDef{}, fmt.Errorf("advisor: rule %s fact query: %w", rule.ID, err)
	}
	if strings.TrimSpace(rule.TextTemplate) == "" {
		return RuleDef{}, fmt.Errorf("advisor: rule %s text template must not be empty", rule.ID)
	}
	cta, err := normalizeCTA(rule.CTA)
	if err != nil {
		return RuleDef{}, fmt.Errorf("advisor: rule %s cta: %w", rule.ID, err)
	}
	condition, err := parseCondition(rule.Condition)
	if err != nil {
		return RuleDef{}, fmt.Errorf("advisor: rule %s condition: %w", rule.ID, err)
	}

	rule.ID = strings.TrimSpace(rule.ID)
	rule.Severity = severity
	rule.Surfaces = surfaces
	rule.FactQuery = factQuery
	rule.Condition = strings.TrimSpace(rule.Condition)
	rule.CTA = cta
	rule.condition = condition
	return rule, nil
}

// CompileRules validates and compiles multiple rule definitions.
func CompileRules(rules []RuleDef) ([]RuleDef, error) {
	compiled := make([]RuleDef, len(rules))
	seen := make(map[string]struct{}, len(rules))
	for index, rule := range rules {
		next, err := CompileRule(rule)
		if err != nil {
			return nil, fmt.Errorf("compile rule %d: %w", index, err)
		}
		if _, ok := seen[next.ID]; ok {
			return nil, fmt.Errorf("compile rule %d: duplicate rule id %q", index, next.ID)
		}
		seen[next.ID] = struct{}{}
		compiled[index] = next
	}
	return compiled, nil
}

// RuleFromJurisdiction converts a jurisdiction pack rule into an advisor rule.
func RuleFromJurisdiction(rule jurisdiction.AdvisorRule) RuleDef {
	surfaces := make([]Surface, 0, len(rule.Surfaces))
	for _, surface := range rule.Surfaces {
		surfaces = append(surfaces, Surface(surface))
	}
	facts := make([]FactKey, 0, len(rule.FactQuery))
	for _, fact := range rule.FactQuery {
		facts = append(facts, FactKey(fact))
	}
	return RuleDef{
		ID:           rule.ID,
		Severity:     Severity(rule.Severity),
		Surfaces:     surfaces,
		FactQuery:    facts,
		Condition:    rule.Condition,
		TextTemplate: rule.TextTemplate,
		CTA: CTA{
			Label:  rule.CTA.Label,
			Action: rule.CTA.Action,
			Params: cloneAnyMap(rule.CTA.Params),
		},
	}
}

// CompileJurisdictionRules validates and compiles rules from the active pack.
func CompileJurisdictionRules(rules []jurisdiction.AdvisorRule) ([]RuleDef, error) {
	defs := make([]RuleDef, len(rules))
	for index, rule := range rules {
		defs[index] = RuleFromJurisdiction(rule)
	}
	return CompileRules(defs)
}

func normalizeSeverity(value string) (Severity, error) {
	switch Severity(strings.TrimSpace(value)) {
	case SeverityTeal:
		return SeverityTeal, nil
	case SeverityAmber:
		return SeverityAmber, nil
	default:
		return "", fmt.Errorf("must be teal or amber")
	}
}

func normalizeSurfaces(values []Surface) ([]Surface, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("must contain at least one surface")
	}
	seen := make(map[Surface]struct{}, len(values))
	out := make([]Surface, 0, len(values))
	for _, value := range values {
		surface, err := normalizeSurface(string(value))
		if err != nil {
			return nil, err
		}
		if _, ok := seen[surface]; ok {
			continue
		}
		seen[surface] = struct{}{}
		out = append(out, surface)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func normalizeSurface(value string) (Surface, error) {
	switch Surface(strings.TrimSpace(value)) {
	case SurfaceDashboard:
		return SurfaceDashboard, nil
	case SurfaceInvoices:
		return SurfaceInvoices, nil
	case SurfaceBanking:
		return SurfaceBanking, nil
	case SurfaceDLA:
		return SurfaceDLA, nil
	case SurfaceDividends:
		return SurfaceDividends, nil
	case SurfaceReports:
		return SurfaceReports, nil
	default:
		return "", fmt.Errorf("unknown surface %q", value)
	}
}

func normalizeFactQuery(values []FactKey) ([]FactKey, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("must contain at least one fact key")
	}
	seen := make(map[FactKey]struct{}, len(values))
	out := make([]FactKey, 0, len(values))
	for _, value := range values {
		key := FactKey(strings.TrimSpace(string(value)))
		if key == "" {
			return nil, fmt.Errorf("fact key must not be empty")
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func normalizeCTA(value CTA) (CTA, error) {
	value.Label = strings.TrimSpace(value.Label)
	value.Action = strings.TrimSpace(value.Action)
	if value.Label == "" {
		return CTA{}, fmt.Errorf("label must not be empty")
	}
	if value.Action == "" {
		return CTA{}, fmt.Errorf("action must not be empty")
	}
	value.Params = cloneAnyMap(value.Params)
	return value, nil
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneAnyValue(value)
	}
	return out
}

func cloneAnyValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMap(typed)
	case []any:
		out := make([]any, len(typed))
		for index, nested := range typed {
			out[index] = cloneAnyValue(nested)
		}
		return out
	default:
		return value
	}
}

func formatFactValue(value any) any {
	switch v := value.(type) {
	case money.Money:
		return v.Format()
	case time.Time:
		return dateOnly(v).Format(time.DateOnly)
	case Days:
		return int(v)
	default:
		return value
	}
}
