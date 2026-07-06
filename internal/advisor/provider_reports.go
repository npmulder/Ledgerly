package advisor

import (
	"context"
	"fmt"

	"github.com/npmulder/ledgerly/internal/reports"
)

// ReportsReadAPI is the public reports read surface used by advisor facts.
type ReportsReadAPI interface {
	VATPosition(context.Context) (reports.VATPosition, error)
	FilingCalendarContext(context.Context) ([]reports.Filing, error)
}

type reportsFactProvider struct {
	api ReportsReadAPI
}

// NewReportsFactProvider maps reports read models into advisor facts.
func NewReportsFactProvider(api ReportsReadAPI) FactProvider {
	return reportsFactProvider{api: api}
}

func (p reportsFactProvider) Keys() []FactKey {
	return []FactKey{FactVATPosition, FactVATDueDate, FactFilings}
}

func (p reportsFactProvider) Gather(ctx context.Context) (map[FactKey]FactValue, error) {
	if p.api == nil {
		return nil, fmt.Errorf("advisor: reports read API is required")
	}
	position, err := p.api.VATPosition(ctx)
	if err != nil {
		return nil, err
	}
	calendar, err := p.api.FilingCalendarContext(ctx)
	if err != nil {
		return nil, err
	}
	filings := make([]FilingFact, 0, len(calendar))
	for _, filing := range calendar {
		filings = append(filings, FilingFact{
			Key:        filing.Key,
			Label:      filing.Label,
			DueDate:    filing.DueDate,
			DaysUntil:  filing.DaysUntil,
			Status:     string(filing.Status),
			WarnWindow: Days(filing.WarnWindow),
		})
	}
	values := map[FactKey]FactValue{
		FactVATPosition: position,
		FactFilings:     filings,
	}
	if position.DueDate != nil {
		values[FactVATDueDate] = *position.DueDate
	} else {
		values[FactVATDueDate] = nil
	}
	return values, nil
}
