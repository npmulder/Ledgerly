package advisor

import (
	"context"
	"errors"
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

// NewReportsVATFactProvider maps VAT reports read models into advisor facts.
func NewReportsVATFactProvider(api ReportsReadAPI) FactProvider {
	return reportsVATFactProvider{api: api}
}

// NewReportsFilingFactProvider maps filing calendar read models into advisor facts.
func NewReportsFilingFactProvider(api ReportsReadAPI) FactProvider {
	return reportsFilingFactProvider{api: api}
}

func (p reportsFactProvider) Keys() []FactKey {
	return []FactKey{
		FactVATPosition,
		FactVATDueDate,
		FactFilings,
		FactFilingAuthority,
		FactFilingDueDate,
		FactFilingName,
	}
}

func (p reportsFactProvider) Gather(ctx context.Context) (map[FactKey]FactValue, error) {
	if p.api == nil {
		return nil, fmt.Errorf("advisor: reports read API is required")
	}
	values := map[FactKey]FactValue{}
	vatFacts, vatErr := gatherVATFacts(ctx, p.api)
	filingFacts, filingErr := gatherFilingFacts(ctx, p.api)
	if vatErr != nil && filingErr != nil {
		return nil, errors.Join(vatErr, filingErr)
	}
	for key, value := range vatFacts {
		values[key] = value
	}
	for key, value := range filingFacts {
		values[key] = value
	}
	return values, nil
}

type reportsVATFactProvider struct {
	api ReportsReadAPI
}

func (p reportsVATFactProvider) Keys() []FactKey {
	return []FactKey{FactVATPosition, FactVATDueDate}
}

func (p reportsVATFactProvider) Gather(ctx context.Context) (map[FactKey]FactValue, error) {
	if p.api == nil {
		return nil, fmt.Errorf("advisor: reports read API is required")
	}
	return gatherVATFacts(ctx, p.api)
}

type reportsFilingFactProvider struct {
	api ReportsReadAPI
}

func (p reportsFilingFactProvider) Keys() []FactKey {
	return []FactKey{FactFilings, FactFilingAuthority, FactFilingDueDate, FactFilingName}
}

func (p reportsFilingFactProvider) Gather(ctx context.Context) (map[FactKey]FactValue, error) {
	if p.api == nil {
		return nil, fmt.Errorf("advisor: reports read API is required")
	}
	return gatherFilingFacts(ctx, p.api)
}

func gatherVATFacts(ctx context.Context, api ReportsReadAPI) (map[FactKey]FactValue, error) {
	position, err := api.VATPosition(ctx)
	if err != nil {
		return nil, err
	}
	values := map[FactKey]FactValue{
		FactVATPosition: position,
	}
	if position.DueDate != nil {
		values[FactVATDueDate] = *position.DueDate
	} else {
		values[FactVATDueDate] = nil
	}
	return values, nil
}

func gatherFilingFacts(ctx context.Context, api ReportsReadAPI) (map[FactKey]FactValue, error) {
	calendar, err := api.FilingCalendarContext(ctx)
	if err != nil {
		return nil, err
	}
	filings := make([]FilingFact, 0, len(calendar))
	for _, filing := range calendar {
		filings = append(filings, FilingFact{
			Key:        filing.Key,
			Label:      filing.Label,
			Authority:  filing.Authority,
			DueDate:    filing.DueDate,
			DaysUntil:  filing.DaysUntil,
			Status:     string(filing.Status),
			WarnWindow: Days(filing.WarnWindow),
		})
	}
	values := map[FactKey]FactValue{FactFilings: filings}
	if len(filings) > 0 {
		first := filings[0]
		values[FactFilingAuthority] = first.Authority
		values[FactFilingDueDate] = first.DueDate
		values[FactFilingName] = first.Label
	}
	return values, nil
}
