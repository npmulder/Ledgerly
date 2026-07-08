package advisor

import (
	"context"
	"fmt"

	"github.com/npmulder/ledgerly/internal/invoicing"
)

// InvoicingReadAPI is the public invoicing read surface used by advisor facts.
type InvoicingReadAPI interface {
	OverdueInvoices(context.Context) ([]invoicing.OverdueInvoiceFact, error)
	RecurringDraftInvoices(context.Context) ([]invoicing.RecurringDraftInvoiceFact, error)
}

type invoicingFactProvider struct {
	api InvoicingReadAPI
}

// NewInvoicingFactProvider maps invoicing read models into advisor facts.
func NewInvoicingFactProvider(api InvoicingReadAPI) FactProvider {
	return invoicingFactProvider{api: api}
}

func (p invoicingFactProvider) Keys() []FactKey {
	return []FactKey{
		FactInvoicesOverdue,
		FactInvoiceClientName,
		FactInvoiceCount,
		FactInvoiceDaysOverdue,
		FactInvoiceID,
		FactInvoiceNumber,
		FactRecurringDrafts,
		FactRecurringDraftClientName,
		FactRecurringDraftCount,
		FactRecurringDraftInvoiceID,
		FactRecurringDraftRunDate,
	}
}

func (p invoicingFactProvider) Gather(ctx context.Context) (map[FactKey]FactValue, error) {
	if p.api == nil {
		return nil, fmt.Errorf("advisor: invoicing read API is required")
	}
	overdue, err := p.api.OverdueInvoices(ctx)
	if err != nil {
		return nil, err
	}
	recurringDrafts, err := p.api.RecurringDraftInvoices(ctx)
	if err != nil {
		return nil, err
	}
	facts := make([]OverdueInvoiceFact, 0, len(overdue))
	for _, invoice := range overdue {
		facts = append(facts, OverdueInvoiceFact{
			ID:          invoice.InvoiceID,
			Number:      invoice.InvoiceNumber,
			Client:      invoice.ClientName,
			Amount:      invoice.Amount,
			DaysOverdue: invoice.DaysOverdue,
		})
	}
	recurring := make([]RecurringDraftFact, 0, len(recurringDrafts))
	for _, invoice := range recurringDrafts {
		recurring = append(recurring, RecurringDraftFact{
			ID:      invoice.InvoiceID,
			Client:  invoice.ClientName,
			RunDate: invoice.RunDate,
			Amount:  invoice.Amount,
		})
	}
	values := map[FactKey]FactValue{
		FactInvoicesOverdue:     facts,
		FactInvoiceCount:        len(facts),
		FactRecurringDrafts:     recurring,
		FactRecurringDraftCount: len(recurring),
	}
	if len(facts) > 0 {
		first := facts[0]
		values[FactInvoiceClientName] = first.Client
		values[FactInvoiceDaysOverdue] = first.DaysOverdue
		values[FactInvoiceID] = first.ID
		values[FactInvoiceNumber] = first.Number
	}
	if len(recurring) > 0 {
		first := recurring[0]
		values[FactRecurringDraftClientName] = first.Client
		values[FactRecurringDraftInvoiceID] = first.ID
		values[FactRecurringDraftRunDate] = first.RunDate
	}
	return values, nil
}
