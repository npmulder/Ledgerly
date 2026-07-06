package dividends

import "github.com/npmulder/ledgerly/internal/moneyfx/money"

// DeclaredName is the bus event name for dividend declarations.
const DeclaredName = "dividends.Declared"

// Declared is published after a dividend declaration and its ledger/DLA rows
// are appended in the same transaction.
type Declared struct {
	DeclarationID DeclarationID
	Amount        money.Money
}

// Name implements bus.Event.
func (Declared) Name() string {
	return DeclaredName
}
