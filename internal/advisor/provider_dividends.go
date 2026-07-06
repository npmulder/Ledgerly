package advisor

import (
	"context"
	"fmt"

	"github.com/npmulder/ledgerly/internal/dividends"
)

// DividendsReadAPI is the public dividends read surface used by advisor facts.
type DividendsReadAPI interface {
	Headroom(context.Context) (dividends.HeadroomBreakdown, error)
}

type dividendsFactProvider struct {
	api DividendsReadAPI
}

// NewDividendsFactProvider maps dividends read models into advisor facts.
func NewDividendsFactProvider(api DividendsReadAPI) FactProvider {
	return dividendsFactProvider{api: api}
}

func (p dividendsFactProvider) Keys() []FactKey {
	return []FactKey{FactDividendsHeadroom, FactDividendsDistributable}
}

func (p dividendsFactProvider) Gather(ctx context.Context) (map[FactKey]FactValue, error) {
	if p.api == nil {
		return nil, fmt.Errorf("advisor: dividends read API is required")
	}
	headroom, err := p.api.Headroom(ctx)
	if err != nil {
		return nil, err
	}
	return map[FactKey]FactValue{
		FactDividendsHeadroom:      headroom.Available,
		FactDividendsDistributable: headroom.Distributable,
	}, nil
}
