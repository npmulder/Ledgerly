package advisor

import (
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

func TestTemplateRenderingFormatsMoneyAndDates(t *testing.T) {
	rule := compileTestRule(t, RuleDef{
		ID:           "template-format",
		Severity:     SeverityAmber,
		Surfaces:     []Surface{SurfaceDashboard},
		FactQuery:    []FactKey{"amount", "due_date", "invoice_id", "zero"},
		Condition:    "amount > zero",
		TextTemplate: "Pay {{ amount }} by {{ due_date }}",
		CTA: CTA{
			Label:  "Open",
			Action: "test.open",
			Params: map[string]any{"invoice_id": "{{ invoice_id }}"},
		},
	})
	facts := Facts{
		"amount":     money.Money{Amount: 123456, Currency: "GBP"},
		"zero":       money.Money{Amount: 0, Currency: "GBP"},
		"due_date":   time.Date(2026, 7, 20, 15, 30, 0, 0, time.FixedZone("BST", 3600)),
		"invoice_id": "inv-123",
	}

	delta, err := Evaluate([]RuleDef{rule}, facts, time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(delta.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", delta.Warnings)
	}
	if len(delta.Insights) != 1 {
		t.Fatalf("insights length = %d, want 1", len(delta.Insights))
	}
	if delta.Insights[0].RenderedText != "Pay £1,234.56 by 2026-07-20" {
		t.Fatalf("rendered text = %q", delta.Insights[0].RenderedText)
	}
	if delta.Insights[0].CTA.Params["invoice_id"] != "inv-123" {
		t.Fatalf("cta params = %#v, want rendered invoice_id", delta.Insights[0].CTA.Params)
	}
}

func TestTemplateErrorSkipsOnlyBadRule(t *testing.T) {
	bad := compileTestRule(t, RuleDef{
		ID:           "bad-template",
		Severity:     SeverityAmber,
		Surfaces:     []Surface{SurfaceDashboard},
		FactQuery:    []FactKey{"amount"},
		Condition:    "amount > 0",
		TextTemplate: "Broken {{ missing_func }}",
		CTA:          CTA{Label: "Open", Action: "test.open"},
	})
	good := compileTestRule(t, RuleDef{
		ID:           "good-template",
		Severity:     SeverityTeal,
		Surfaces:     []Surface{SurfaceDashboard},
		FactQuery:    []FactKey{"amount"},
		Condition:    "amount > 0",
		TextTemplate: "Amount {{ amount }}",
		CTA:          CTA{Label: "Open", Action: "test.open"},
	})

	delta, err := Evaluate([]RuleDef{bad, good}, Facts{"amount": 10}, time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(delta.Insights) != 1 || delta.Insights[0].RuleID != "good-template" {
		t.Fatalf("insights = %#v, want only good-template", delta.Insights)
	}
	if delta.Insights[0].RenderedText != "Amount 10" {
		t.Fatalf("rendered text = %q", delta.Insights[0].RenderedText)
	}
	if len(delta.Warnings) != 1 || delta.Warnings[0].RuleID != "bad-template" || !strings.Contains(delta.Warnings[0].Message, "template skipped") {
		t.Fatalf("warnings = %#v, want bad template warning", delta.Warnings)
	}
}

func TestCompileJurisdictionRules(t *testing.T) {
	if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("LoadActive() error = %v", err)
	}
	rules, err := CompileJurisdictionRules(jurisdiction.AdvisorRules())
	if err != nil {
		t.Fatalf("CompileJurisdictionRules() error = %v", err)
	}
	if len(rules) != 5 {
		t.Fatalf("compiled rules length = %d, want 5", len(rules))
	}
}
