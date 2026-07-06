package advisor

import (
	"context"
	"fmt"

	"github.com/npmulder/ledgerly/internal/identity"
)

// IdentityReadAPI is the public identity read surface used by advisor facts.
type IdentityReadAPI interface {
	CompanyFacts(context.Context) (identity.CompanyFacts, error)
}

type identityFactProvider struct {
	api IdentityReadAPI
}

// NewIdentityFactProvider maps company identity facts into advisor facts.
func NewIdentityFactProvider(api IdentityReadAPI) FactProvider {
	return identityFactProvider{api: api}
}

func (p identityFactProvider) Keys() []FactKey {
	return []FactKey{
		FactCompanyIncorporationDate,
		FactCompanyYearEnd,
		FactCompanyYearEndMonth,
		FactCompanyYearEndDay,
	}
}

func (p identityFactProvider) Gather(ctx context.Context) (map[FactKey]FactValue, error) {
	if p.api == nil {
		return nil, fmt.Errorf("advisor: identity read API is required")
	}
	facts, err := p.api.CompanyFacts(ctx)
	if err != nil {
		return nil, err
	}
	yearEnd := CompanyYearEndFact{Month: int(facts.YearEnd.Month), Day: facts.YearEnd.Day}
	return map[FactKey]FactValue{
		FactCompanyIncorporationDate: facts.IncorporationDate,
		FactCompanyYearEnd:           yearEnd,
		FactCompanyYearEndMonth:      yearEnd.Month,
		FactCompanyYearEndDay:        yearEnd.Day,
	}, nil
}
