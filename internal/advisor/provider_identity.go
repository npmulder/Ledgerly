package advisor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
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
		FactCompanyVATRegistered,
		FactCompanyActType,
		FactCompanyActLabel,
		FactCompanyActMinimumDirectors,
		FactCompanyDirectorCount,
		FactRuleCompanyActName,
		FactRuleDirectorCount,
		FactRuleMinimumDirectors,
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
	actType := strings.TrimSpace(facts.ActType)
	actLabel := ""
	minimumDirectors := 0
	if actType != "" {
		act, err := jurisdiction.CompanyActDefinition(actType)
		var unknownAct jurisdiction.UnknownCompanyActError
		if err != nil && !errors.As(err, &unknownAct) {
			return nil, err
		}
		if err == nil {
			actLabel = act.Label
			minimumDirectors = act.MinimumDirectors
		}
	}
	directorCount := len(facts.Directors)
	return map[FactKey]FactValue{
		FactCompanyIncorporationDate:   facts.IncorporationDate,
		FactCompanyYearEnd:             yearEnd,
		FactCompanyYearEndMonth:        yearEnd.Month,
		FactCompanyYearEndDay:          yearEnd.Day,
		FactCompanyVATRegistered:       facts.IsVATRegistered,
		FactCompanyActType:             actType,
		FactCompanyActLabel:            actLabel,
		FactCompanyActMinimumDirectors: minimumDirectors,
		FactCompanyDirectorCount:       directorCount,
		FactRuleCompanyActName:         actLabel,
		FactRuleDirectorCount:          directorCount,
		FactRuleMinimumDirectors:       minimumDirectors,
	}, nil
}
