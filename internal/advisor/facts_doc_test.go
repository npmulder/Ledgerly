package advisor

import (
	"os"
	"regexp"
	"testing"

	"github.com/npmulder/ledgerly/internal/jurisdiction"
)

func TestFactsDocCoversProviderAndActiveRuleKeys(t *testing.T) {
	body, err := os.ReadFile("../../docs/design/modules/facts.md")
	if err != nil {
		t.Fatalf("read facts.md: %v", err)
	}
	documented := documentedFactKeys(string(body))

	for _, key := range []FactKey{
		FactInvoicesOverdue,
		FactInvoiceClientName,
		FactInvoiceCount,
		FactInvoiceDaysOverdue,
		FactInvoiceID,
		FactInvoiceNumber,
		FactDLABalance,
		FactDLAStatus,
		FactDLASuggestedClearance,
		FactRuleDLABalance,
		FactRuleDLAStatus,
		FactDividendsHeadroom,
		FactDividendsDistributable,
		FactDividendsYTD,
		FactDividendEstimate,
		FactDividendEstimateMinor,
		FactVATPosition,
		FactVATDueDate,
		FactFilings,
		FactFilingAuthority,
		FactFilingDueDate,
		FactFilingName,
		FactRatesLastDate,
		FactRatesStale,
		FactStaleDays,
		FactCompanyIncorporationDate,
		FactCompanyYearEnd,
		FactCompanyYearEndMonth,
		FactCompanyYearEndDay,
	} {
		if !documented[string(key)] {
			t.Fatalf("facts.md missing provider fact key %q", key)
		}
	}

	if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("LoadActive() error = %v", err)
	}
	for _, rule := range jurisdiction.AdvisorRules() {
		for _, key := range rule.FactQuery {
			if !documented[key] {
				t.Fatalf("facts.md missing active rule fact key %q for rule %s", key, rule.ID)
			}
		}
	}
}

func documentedFactKeys(body string) map[string]bool {
	matches := regexp.MustCompile("(?m)^\\|\\s*`([^`]+)`\\s*\\|").FindAllStringSubmatch(body, -1)
	keys := make(map[string]bool, len(matches))
	for _, match := range matches {
		keys[match[1]] = true
	}
	return keys
}
