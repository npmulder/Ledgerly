package dla

import "github.com/npmulder/ledgerly/internal/moneyfx/money"

const (
	// WentOverdrawnName is published when the DLA crosses from credit to debit.
	WentOverdrawnName = "dla.WentOverdrawn"

	// BackInCreditName is published when the DLA crosses from debit to credit.
	BackInCreditName = "dla.BackInCredit"
)

// WentOverdrawn is published inside the balance-changing transaction when the
// current DLA balance first becomes overdrawn.
type WentOverdrawn struct {
	Director DirectorID
	Balance  money.Money
}

// Name implements bus.Event.
func (WentOverdrawn) Name() string {
	return WentOverdrawnName
}

// BackInCredit is published inside the balance-changing transaction when the
// current DLA balance returns to zero or credit.
type BackInCredit struct {
	Director DirectorID
	Balance  money.Money
}

// Name implements bus.Event.
func (BackInCredit) Name() string {
	return BackInCreditName
}
