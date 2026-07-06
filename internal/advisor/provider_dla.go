package advisor

import (
	"context"
	"fmt"

	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

// DLAReadAPI is the public DLA read surface used by advisor facts.
type DLAReadAPI interface {
	CurrentBalance(context.Context) (money.Money, dla.Status, error)
}

type dlaFactProvider struct {
	api DLAReadAPI
}

// NewDLAFactProvider maps DLA read models into advisor facts.
func NewDLAFactProvider(api DLAReadAPI) FactProvider {
	return dlaFactProvider{api: api}
}

func (p dlaFactProvider) Keys() []FactKey {
	return []FactKey{
		FactDLABalance,
		FactDLAStatus,
		FactDLASuggestedClearance,
		FactRuleDLABalance,
		FactRuleDLAStatus,
	}
}

func (p dlaFactProvider) Gather(ctx context.Context) (map[FactKey]FactValue, error) {
	if p.api == nil {
		return nil, fmt.Errorf("advisor: DLA read API is required")
	}
	balance, status, err := p.api.CurrentBalance(ctx)
	if err != nil {
		return nil, err
	}
	clearance, err := clearanceAmountForBalance(balance)
	if err != nil {
		return nil, err
	}
	return map[FactKey]FactValue{
		FactDLABalance:            balance,
		FactDLAStatus:             string(status),
		FactDLASuggestedClearance: clearance,
		FactRuleDLABalance:        balance,
		FactRuleDLAStatus:         string(status),
	}, nil
}

func clearanceAmountForBalance(balance money.Money) (money.Money, error) {
	if balance.Amount >= 0 {
		return money.Zero(balance.Currency), nil
	}
	return balance.Negate()
}
