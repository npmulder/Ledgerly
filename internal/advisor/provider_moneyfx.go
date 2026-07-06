package advisor

import (
	"context"
	"fmt"

	"github.com/npmulder/ledgerly/internal/moneyfx"
)

// MoneyFXReadAPI is the public moneyfx read surface used by advisor facts.
type MoneyFXReadAPI interface {
	RateStaleness(context.Context) (moneyfx.RateStaleness, error)
}

type moneyFXFactProvider struct {
	api MoneyFXReadAPI
}

// NewMoneyFXFactProvider maps moneyfx staleness into advisor facts.
func NewMoneyFXFactProvider(api MoneyFXReadAPI) FactProvider {
	return moneyFXFactProvider{api: api}
}

func (p moneyFXFactProvider) Keys() []FactKey {
	return []FactKey{FactRatesLastDate, FactRatesStale}
}

func (p moneyFXFactProvider) Gather(ctx context.Context) (map[FactKey]FactValue, error) {
	if p.api == nil {
		return nil, fmt.Errorf("advisor: moneyfx read API is required")
	}
	staleness, err := p.api.RateStaleness(ctx)
	if err != nil {
		return nil, err
	}
	var lastDate FactValue
	if staleness.LastDate != nil {
		lastDate = *staleness.LastDate
	}
	return map[FactKey]FactValue{
		FactRatesLastDate: lastDate,
		FactRatesStale:    staleness.Stale,
	}, nil
}
