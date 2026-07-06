package advisor

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"pgregory.net/rapid"
)

func TestConditionGrammarTable(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		condition string
		facts     Facts
	}{
		{
			name:      "equals",
			condition: "amount = threshold",
			facts:     Facts{"amount": 10, "threshold": 10},
		},
		{
			name:      "not equals",
			condition: "amount != threshold",
			facts:     Facts{"amount": 11, "threshold": 10},
		},
		{
			name:      "greater than",
			condition: "amount > threshold",
			facts:     Facts{"amount": 11, "threshold": 10},
		},
		{
			name:      "greater than or equal",
			condition: "amount >= threshold",
			facts:     Facts{"amount": 10, "threshold": 10},
		},
		{
			name:      "less than",
			condition: "amount < threshold",
			facts:     Facts{"amount": 9, "threshold": 10},
		},
		{
			name:      "less than or equal",
			condition: "amount <= threshold",
			facts:     Facts{"amount": 10, "threshold": 10},
		},
		{
			name:      "money comparison",
			condition: "balance > limit",
			facts: Facts{
				"balance": money.Money{Amount: 1500, Currency: "GBP"},
				"limit":   money.Money{Amount: 1000, Currency: "GBP"},
			},
		},
		{
			name:      "date comparison",
			condition: "due_date <= cutoff",
			facts: Facts{
				"due_date": time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC),
				"cutoff":   time.Date(2026, 7, 20, 23, 0, 0, 0, time.UTC),
			},
		},
		{
			name:      "exists and not",
			condition: "exists(optional) and not exists(missing)",
			facts:     Facts{"optional": "present"},
		},
		{
			name:      "and has higher precedence than or",
			condition: "true or false and false",
			facts:     Facts{"unused": 1},
		},
		{
			name:      "date arithmetic against today",
			condition: "due_date - today <= warn_window",
			facts: Facts{
				"due_date":    time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC),
				"warn_window": Days(20),
			},
		},
		{
			name:      "nested struct fact",
			condition: "filing.dueDate - today <= filing.warnWindow",
			facts: Facts{
				"filing": filingFact{
					DueDate:    time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC),
					WarnWindow: Days(20),
				},
			},
		},
		{
			name:      "nested named string map fact",
			condition: "group.flag = true",
			facts: Facts{
				"group": Facts{"flag": true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factQuery := make([]FactKey, 0, len(tt.facts))
			for key := range tt.facts {
				factQuery = append(factQuery, key)
			}
			rule := compileTestRule(t, RuleDef{
				ID:           "rule-" + strings.ReplaceAll(tt.name, " ", "-"),
				Severity:     SeverityAmber,
				Surfaces:     []Surface{SurfaceDashboard},
				FactQuery:    factQuery,
				Condition:    tt.condition,
				TextTemplate: "fires",
				CTA:          CTA{Label: "Open", Action: "test.open"},
			})

			delta, err := Evaluate([]RuleDef{rule}, tt.facts, now)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if len(delta.Warnings) != 0 {
				t.Fatalf("warnings = %#v, want none", delta.Warnings)
			}
			if len(delta.Insights) != 1 {
				t.Fatalf("insights length = %d, want 1", len(delta.Insights))
			}
		})
	}
}

func TestEvaluateUnknownFactSkipsRuleWithWarning(t *testing.T) {
	rule := compileTestRule(t, RuleDef{
		ID:           "unknown-fact",
		Severity:     SeverityAmber,
		Surfaces:     []Surface{SurfaceDashboard},
		FactQuery:    []FactKey{"known"},
		Condition:    "missing > 0",
		TextTemplate: "fires",
		CTA:          CTA{Label: "Open", Action: "test.open"},
	})

	delta, err := Evaluate([]RuleDef{rule}, Facts{"known": 1}, time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(delta.Insights) != 0 {
		t.Fatalf("insights length = %d, want 0", len(delta.Insights))
	}
	if len(delta.Warnings) != 1 || !strings.Contains(delta.Warnings[0].Message, `unknown fact "missing"`) {
		t.Fatalf("warnings = %#v, want unknown fact warning", delta.Warnings)
	}
}

func TestCompileRuleRejectsMalformedCondition(t *testing.T) {
	_, err := CompileRule(RuleDef{
		ID:           "bad-condition",
		Severity:     SeverityAmber,
		Surfaces:     []Surface{SurfaceDashboard},
		FactQuery:    []FactKey{"amount"},
		Condition:    "amount >",
		TextTemplate: "fires",
		CTA:          CTA{Label: "Open", Action: "test.open"},
	})
	if err == nil {
		t.Fatal("CompileRule() error = nil, want malformed condition error")
	}
	if !strings.Contains(err.Error(), "expected expression") {
		t.Fatalf("CompileRule() error = %v, want expected expression", err)
	}
}

func TestEvaluateDeterministicAcrossFactMapOrder(t *testing.T) {
	rule := compileTestRule(t, RuleDef{
		ID:           "deterministic",
		Severity:     SeverityTeal,
		Surfaces:     []Surface{SurfaceDashboard},
		FactQuery:    []FactKey{"amount", "threshold"},
		Condition:    "amount >= threshold",
		TextTemplate: "Amount {{ amount }} reached {{ threshold }}",
		CTA:          CTA{Label: "Open", Action: "test.open"},
	})
	now := time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)

	rapid.Check(t, func(t *rapid.T) {
		amount := rapid.IntRange(0, 1000).Draw(t, "amount")
		threshold := rapid.IntRange(0, 1000).Draw(t, "threshold")
		factsA := Facts{"amount": amount, "threshold": threshold}
		factsB := Facts{"threshold": threshold, "amount": amount}

		deltaA, err := Evaluate([]RuleDef{rule}, factsA, now)
		if err != nil {
			t.Fatalf("Evaluate(A) error = %v", err)
		}
		deltaB, err := Evaluate([]RuleDef{rule}, factsB, now)
		if err != nil {
			t.Fatalf("Evaluate(B) error = %v", err)
		}
		if !reflect.DeepEqual(deltaA, deltaB) {
			t.Fatalf("Evaluate deltas differ:\nA=%#v\nB=%#v", deltaA, deltaB)
		}
	})
}

type filingFact struct {
	DueDate    time.Time `json:"dueDate"`
	WarnWindow Days      `json:"warnWindow"`
}

func compileTestRule(t testing.TB, rule RuleDef) RuleDef {
	t.Helper()
	compiled, err := CompileRule(rule)
	if err != nil {
		t.Fatalf("CompileRule() error = %v", err)
	}
	return compiled
}
