package advisor

import (
	"context"
	"fmt"

	"github.com/npmulder/ledgerly/internal/invoicing"
)

// InvoicingReadAPI is the public invoicing read surface used by advisor facts.
type InvoicingReadAPI interface {
	OverdueInvoices(context.Context) ([]invoicing.OverdueInvoiceFact, error)
}

type invoicingFactProvider struct {
	api InvoicingReadAPI
}

// NewInvoicingFactProvider maps invoicing read models into advisor facts.
func NewInvoicingFactProvider(api InvoicingReadAPI) FactProvider {
	return invoicingFactProvider{api: api}
}

func (p invoicingFactProvider) Keys() []FactKey {
	return []FactKey{FactInvoicesOverdue}
}

func (p invoicingFactProvider) Gather(ctx context.Context) (map[FactKey]FactValue, error) {
	if p.api == nil {
		return nil, fmt.Errorf("advisor: invoicing read API is required")
	}
	overdue, err := p.api.OverdueInvoices(ctx)
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
	return map[FactKey]FactValue{FactInvoicesOverdue: facts}, nil
}
