package advisor

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

func TestIsleOfManRenderedInsightTextSnapshots(t *testing.T) {
	if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("LoadActive() error = %v", err)
	}
	rules, err := CompileJurisdictionRules(jurisdiction.AdvisorRules())
	if err != nil {
		t.Fatalf("CompileJurisdictionRules() error = %v", err)
	}
	facts := Facts{
		FactInvoiceClientName:        "Contoso GmbH",
		FactInvoiceCount:             1,
		FactInvoiceDaysOverdue:       9,
		FactInvoiceID:                "inv-123",
		FactInvoiceNumber:            "INV-2026-01",
		FactRecurringDraftClientName: "Fabrikam Ltd",
		FactRecurringDraftCount:      1,
		FactRecurringDraftInvoiceID:  "draft-456",
		FactRecurringDraftRunDate:    time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC),
		FactRuleDLABalance:           money.Money{Amount: -150000, Currency: "GBP"},
		FactRuleDLAClearanceMinor:    int64(150000),
		FactRuleDLAStatus:            "overdrawn",
		FactFilingAuthority:          "Isle of Man Customs & Excise",
		FactFilingDueDate:            time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC),
		FactFilingDaysUntil:          24,
		FactFilingName:               "VAT return",
		FactFilingStatus:             "due-soon",
		FactFilingWarnWindow:         Days(30),
		FactDividendHeadroom:         money.Money{Amount: 150000, Currency: "GBP"},
		FactDividendHeadroomMinor:    int64(150000),
		FactDividendEstimate:         money.Money{Amount: 15000, Currency: "GBP"},
		FactDividendEstimateMinor:    int64(15000),
		FactStaleDays:                5,
	}
	delta, err := Evaluate(rules, facts, time.Date(2026, time.July, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(delta.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", delta.Warnings)
	}

	byRule := map[string]Insight{}
	for _, insight := range delta.Insights {
		byRule[insight.RuleID] = insight
	}
	var lines []string
	for _, rule := range rules {
		insight, ok := byRule[rule.ID]
		if !ok {
			t.Fatalf("rule %s did not render an insight; insights=%#v", rule.ID, delta.Insights)
		}
		lines = append(lines,
			"rule: "+insight.RuleID,
			"severity: "+string(insight.Severity),
			"surfaces: "+strings.Join(surfaceStrings(insight.Surfaces), ","),
			"text: "+insight.RenderedText,
			"cta: "+insight.CTA.Label+" | "+insight.CTA.Action+" | "+formatSnapshotParams(insight.CTA.Params),
			"",
		)
	}
	got := strings.Join(lines, "\n")
	wantBytes, err := os.ReadFile("testdata/isle_of_man_rule_texts.golden")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(wantBytes) {
		t.Fatalf("rendered insight snapshot mismatch\n--- got ---\n%s\n--- want ---\n%s", got, string(wantBytes))
	}
}

func formatSnapshotParams(params map[string]any) string {
	if len(params) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(params))
	for _, key := range sortedStringKeys(params) {
		parts = append(parts, key+"="+strings.TrimSpace(toSnapshotString(params[key])))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func sortedStringKeys(params map[string]any) []string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func toSnapshotString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return strings.TrimSpace(strings.ReplaceAll(fmt.Sprint(typed), "\n", " "))
	}
}
