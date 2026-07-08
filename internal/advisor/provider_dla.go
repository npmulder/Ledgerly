package advisor

import (
	"context"
	"fmt"

	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

// DLAReadAPI is the public DLA read surface used by advisor facts.
type DLAReadAPI interface {
	Statuses(context.Context) ([]dla.StatusPayload, error)
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
		FactRuleDLAClearance,
		FactRuleDLAClearanceMinor,
		FactRuleDLADirectorID,
		FactRuleDLADirectorName,
		FactDLADirectorStatuses,
	}
}

func (p dlaFactProvider) Gather(ctx context.Context) (map[FactKey]FactValue, error) {
	if p.api == nil {
		return nil, fmt.Errorf("advisor: DLA read API is required")
	}
	statuses, err := p.api.Statuses(ctx)
	if err != nil {
		return nil, err
	}
	facts := make([]DLADirectorStatusFact, 0, len(statuses))
	for _, status := range statuses {
		clearance, err := clearanceAmountForBalance(status.Balance)
		if err != nil {
			return nil, err
		}
		name := status.DirectorName
		if name == "" {
			name = string(status.DirectorID)
		}
		facts = append(facts, DLADirectorStatusFact{
			DirectorID:     string(status.DirectorID),
			DirectorName:   name,
			Balance:        status.Balance,
			Status:         string(status.Status),
			Clearance:      clearance,
			ClearanceMinor: clearance.Amount,
		})
	}
	values := map[FactKey]FactValue{
		FactDLADirectorStatuses: facts,
	}
	if len(facts) > 0 {
		first := facts[0]
		values[FactDLABalance] = first.Balance
		values[FactDLAStatus] = first.Status
		values[FactDLASuggestedClearance] = first.Clearance
		values[FactRuleDLABalance] = first.Balance
		values[FactRuleDLAStatus] = first.Status
		values[FactRuleDLAClearance] = first.Clearance
		values[FactRuleDLAClearanceMinor] = first.ClearanceMinor
		values[FactRuleDLADirectorID] = first.DirectorID
		values[FactRuleDLADirectorName] = first.DirectorName
	}
	return values, nil
}

func clearanceAmountForBalance(balance money.Money) (money.Money, error) {
	if balance.Amount >= 0 {
		return money.Zero(balance.Currency), nil
	}
	return balance.Negate()
}
