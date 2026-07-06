package invoicing

import (
	"strings"
	"testing"
	"time"
)

func TestBuildInvoiceListCountQueryBindsTodayOnlyWhenStatusFilterUsesIt(t *testing.T) {
	today := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		filter   normalizedInvoiceListFilter
		wantArgs int
		wantDate bool
	}{
		{
			name: "no status filter",
		},
		{
			name: "persisted-only status filter",
			filter: normalizedInvoiceListFilter{
				statuses: []InvoiceStatus{InvoiceStatusDraft, InvoiceStatusPaid},
			},
		},
		{
			name: "persisted-only status filter with search",
			filter: normalizedInvoiceListFilter{
				statuses: []InvoiceStatus{InvoiceStatusDraft},
				search:   "contoso",
			},
			wantArgs: 1,
		},
		{
			name: "sent status filter",
			filter: normalizedInvoiceListFilter{
				statuses: []InvoiceStatus{InvoiceStatusSent},
			},
			wantArgs: 1,
			wantDate: true,
		},
		{
			name: "overdue status filter",
			filter: normalizedInvoiceListFilter{
				statuses: []InvoiceStatus{InvoiceStatusOverdue},
			},
			wantArgs: 1,
			wantDate: true,
		},
		{
			name: "mixed persisted and virtual statuses",
			filter: normalizedInvoiceListFilter{
				statuses: []InvoiceStatus{InvoiceStatusDraft, InvoiceStatusOverdue},
			},
			wantArgs: 1,
			wantDate: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query, args := buildInvoiceListCountQuery(test.filter, today)
			if len(args) != test.wantArgs {
				t.Fatalf("args length = %d, want %d; query:\n%s", len(args), test.wantArgs, query)
			}
			hasDatePredicate := strings.Contains(query, "::date")
			if hasDatePredicate != test.wantDate {
				t.Fatalf("date predicate present = %v, want %v; query:\n%s", hasDatePredicate, test.wantDate, query)
			}
		})
	}
}
