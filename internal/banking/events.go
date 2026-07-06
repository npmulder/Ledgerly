package banking

// TransactionReconciledName is the canonical bus event name for completed
// banking reconciliation commands.
const TransactionReconciledName = "banking.TransactionReconciled"

// TransactionReconciled reports a bank transaction reconciled by a user-
// confirmed command.
type TransactionReconciled struct {
	TransactionID TransactionID
	Kind          SuggestionKind
}

// Name implements bus.Event.
func (TransactionReconciled) Name() string {
	return TransactionReconciledName
}
