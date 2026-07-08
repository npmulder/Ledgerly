package advisor

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
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

func TestTemplateRenderingFormatsCTAAction(t *testing.T) {
	rule := compileTestRule(t, RuleDef{
		ID:           "cta-action-format",
		Severity:     SeverityAmber,
		Surfaces:     []Surface{SurfaceDLA},
		FactQuery:    []FactKey{"amount_minor_units"},
		Condition:    "amount_minor_units > 0",
		TextTemplate: "Clear the balance",
		CTA: CTA{
			Label:  "Clear with dividend",
			Action: "navigate:/dividends?amount={{ amount_minor_units }}",
		},
	})

	delta, err := Evaluate([]RuleDef{rule}, Facts{"amount_minor_units": int64(150000)}, time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(delta.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", delta.Warnings)
	}
	if len(delta.Insights) != 1 {
		t.Fatalf("insights length = %d, want 1", len(delta.Insights))
	}
	if delta.Insights[0].CTA.Action != "navigate:/dividends?amount=150000" {
		t.Fatalf("cta action = %q", delta.Insights[0].CTA.Action)
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

func TestEvaluateFalseRuleWithMissingRenderBindingStillEvaluatesRule(t *testing.T) {
	rule := compileTestRule(t, RuleDef{
		ID:           "false-with-missing-render-binding",
		Severity:     SeverityAmber,
		Surfaces:     []Surface{SurfaceInvoices},
		FactQuery:    []FactKey{"client_name", "count"},
		Condition:    "count > 0",
		TextTemplate: "{{ client_name }} has overdue invoices",
		CTA:          CTA{Label: "Open", Action: "test.open"},
	})

	delta, err := Evaluate([]RuleDef{rule}, Facts{"count": 0}, time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(delta.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", delta.Warnings)
	}
	if len(delta.Insights) != 0 {
		t.Fatalf("insights length = %d, want 0", len(delta.Insights))
	}
	if len(delta.EvaluatedRuleIDs) != 1 || delta.EvaluatedRuleIDs[0] != "false-with-missing-render-binding" {
		t.Fatalf("EvaluatedRuleIDs = %#v, want false rule marked evaluated", delta.EvaluatedRuleIDs)
	}
}

func TestCompileRulesRejectsDuplicateIDs(t *testing.T) {
	rule := RuleDef{
		ID:           "duplicate",
		Severity:     SeverityAmber,
		Surfaces:     []Surface{SurfaceDashboard},
		FactQuery:    []FactKey{"amount"},
		Condition:    "amount > 0",
		TextTemplate: "Amount {{ amount }}",
		CTA:          CTA{Label: "Open", Action: "test.open"},
	}

	_, err := CompileRules([]RuleDef{rule, rule})
	if err == nil {
		t.Fatal("CompileRules() error = nil, want duplicate rule id error")
	}
	if !strings.Contains(err.Error(), `duplicate rule id "duplicate"`) {
		t.Fatalf("CompileRules() error = %v, want duplicate rule id", err)
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
	if len(rules) != 7 {
		t.Fatalf("compiled rules length = %d, want 7", len(rules))
	}
}

func TestCompanyMinimumDirectorsRuleUsesPackActDefinition(t *testing.T) {
	if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("LoadActive() error = %v", err)
	}
	rule := compileActiveRuleByID(t, "company_minimum_directors")

	tests := []struct {
		name      string
		actType   string
		directors int
		wantWarn  bool
	}{
		{name: "1931 one director warns", actType: "companies-act-1931", directors: 1, wantWarn: true},
		{name: "1931 two directors clears", actType: "companies-act-1931", directors: 2},
		{name: "2006 one director clears", actType: "companies-act-2006", directors: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facts, err := NewIdentityFactProvider(fakeIdentityReadAPI{
				facts: identityCompanyFacts(tt.actType, tt.directors),
			}).Gather(t.Context())
			if err != nil {
				t.Fatalf("Gather() error = %v", err)
			}

			delta, err := Evaluate([]RuleDef{rule}, facts, time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if len(delta.Warnings) != 0 {
				t.Fatalf("warnings = %#v, want none", delta.Warnings)
			}
			if tt.wantWarn {
				if len(delta.Insights) != 1 {
					t.Fatalf("insights length = %d, want 1", len(delta.Insights))
				}
				insight := delta.Insights[0]
				if insight.RenderedText != "Your company is registered under the Companies Act 1931 and must have at least 2 directors - profile lists 1." {
					t.Fatalf("RenderedText = %q", insight.RenderedText)
				}
				if insight.Severity != SeverityAmber {
					t.Fatalf("Severity = %q, want amber", insight.Severity)
				}
				if !slices.Equal(insight.Surfaces, []Surface{SurfaceDashboard, SurfaceSettings}) {
					t.Fatalf("Surfaces = %#v, want dashboard/settings", insight.Surfaces)
				}
				if insight.CTA.Action != "navigate:/settings/company" {
					t.Fatalf("CTA.Action = %q, want settings navigation", insight.CTA.Action)
				}
				return
			}
			if len(delta.Insights) != 0 {
				t.Fatalf("insights = %#v, want none", delta.Insights)
			}
		})
	}
}

func compileActiveRuleByID(t *testing.T, id string) RuleDef {
	t.Helper()

	rules, err := CompileJurisdictionRules(jurisdiction.AdvisorRules())
	if err != nil {
		t.Fatalf("CompileJurisdictionRules() error = %v", err)
	}
	for _, rule := range rules {
		if rule.ID == id {
			return rule
		}
	}
	t.Fatalf("active rule %q not found", id)
	return RuleDef{}
}

func identityCompanyFacts(actType string, directorCount int) identity.CompanyFacts {
	directors := make([]identity.Director, directorCount)
	for index := range directors {
		directors[index] = identity.Director{Name: "Director"}
	}
	return identity.CompanyFacts{
		ActType:   actType,
		Directors: directors,
	}
}
