package advisor

import (
	"context"
	"fmt"

	"github.com/npmulder/ledgerly/internal/dividends"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

// DividendsReadAPI is the public dividends read surface used by advisor facts.
type DividendsReadAPI interface {
	Headroom(context.Context) (dividends.HeadroomBreakdown, error)
	DeclaredInYear(context.Context, string) (money.Money, error)
}

type dividendsFactProvider struct {
	api DividendsReadAPI
}

// NewDividendsFactProvider maps dividends read models into advisor facts.
func NewDividendsFactProvider(api DividendsReadAPI) FactProvider {
	return dividendsFactProvider{api: api}
}

func (p dividendsFactProvider) Keys() []FactKey {
	return []FactKey{
		FactDividendsHeadroom,
		FactDividendsDistributable,
		FactDividendsYTD,
		FactDividendEstimate,
		FactDividendEstimateMinor,
	}
}

func (p dividendsFactProvider) Gather(ctx context.Context) (map[FactKey]FactValue, error) {
	if p.api == nil {
		return nil, fmt.Errorf("advisor: dividends read API is required")
	}
	headroom, err := p.api.Headroom(ctx)
	if err != nil {
		return nil, err
	}
	taxYear, err := jurisdiction.TaxYearForDate(headroom.AsOf)
	if err != nil {
		return nil, err
	}
	declaredYear := headroom.FinancialYear
	if declaredYear == "" {
		declaredYear = taxYear
	}
	declared, err := p.api.DeclaredInYear(ctx, declaredYear)
	if err != nil {
		return nil, err
	}
	estimate, err := jurisdiction.PersonalTaxEstimate(taxYear, declared)
	if err != nil {
		return nil, err
	}
	return map[FactKey]FactValue{
		FactDividendsHeadroom:      headroom.Available,
		FactDividendsDistributable: headroom.Distributable,
		FactDividendsYTD:           declared,
		FactDividendEstimate:       estimate.Total,
		FactDividendEstimateMinor:  estimate.Total.Amount,
	}, nil
}
