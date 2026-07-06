package banking

import (
	"context"
	"fmt"
	"strings"

	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

const reconciliationActor = "reconciliation-command"

// ReconciliationCommandHooks are deterministic fault-injection points for
// command rollback tests. Production wiring leaves them empty.
type ReconciliationCommandHooks struct {
	AfterConfirmLedgerPost      func(context.Context) error
	AfterConfirmInvoiceSettled  func(context.Context) error
	AfterConfirmStateTransition func(context.Context) error
}

func (s *Service) ConfirmMatch(ctx context.Context, txnID TransactionID) (_ ConfirmMatchResult, err error) {
	if err := s.requireReconciliationDeps(true, true, false); err != nil {
		return ConfirmMatchResult{}, err
	}

	tx, err := s.beginReconciliationTx(ctx, "confirm match")
	if err != nil {
		return ConfirmMatchResult{}, err
	}
	defer rollbackOnError(ctx, tx, &err)

	txn, account, suggestion, err := s.lockSuggestedCommandTransaction(ctx, tx, txnID, SuggestionKindInvoiceMatch)
	if err != nil {
		return ConfirmMatchResult{}, err
	}
	if txn.Amount.Amount <= 0 {
		return ConfirmMatchResult{}, fmt.Errorf("banking: confirm match transaction %d amount must be inbound: %w", txn.ID, ErrInvalidReconciliation)
	}

	cashGBP, err := s.moneyFX.ToGBP(ctx, txn.Amount, txn.Date)
	if err != nil {
		return ConfirmMatchResult{}, fmt.Errorf("banking: confirm match GBP conversion: %w", err)
	}
	debtorsNative, err := txn.Amount.Negate()
	if err != nil {
		return ConfirmMatchResult{}, fmt.Errorf("banking: confirm match debtors native amount: %w", err)
	}
	debtorsGBP, err := cashGBP.Negate()
	if err != nil {
		return ConfirmMatchResult{}, fmt.Errorf("banking: confirm match debtors GBP amount: %w", err)
	}
	if _, err = s.ledgerJournal.Post(ctx, tx, ledger.NewJournalEntry{
		Date:         txn.Date,
		Description:  fmt.Sprintf("Banking invoice match %s", transactionDisplayRef(txn)),
		SourceModule: ModuleName,
		SourceRef:    commandSourceRef(txn.ID, "confirm-match"),
		Postings: []ledger.NewPosting{
			{AccountCode: account.LedgerAccountCode, Amount: txn.Amount, AmountGBP: cashGBP},
			{AccountCode: tradeDebtorsAccountForCurrency(txn.Amount.Currency), Amount: debtorsNative, AmountGBP: debtorsGBP},
		},
	}); err != nil {
		return ConfirmMatchResult{}, fmt.Errorf("banking: confirm match ledger post: %w", err)
	}
	if err = callReconciliationHook(ctx, s.reconciliationHooks.AfterConfirmLedgerPost); err != nil {
		return ConfirmMatchResult{}, err
	}

	invoiceID := strings.TrimSpace(suggestion.Target)
	if err = withTransactionSearchPath(ctx, tx, invoicing.ModuleName, func() error {
		_, settleErr := s.invoices.MarkSettled(ctx, tx, invoiceID, bankingTxnRef(txn.ID), txn.Date, invoicing.Money{
			Amount:   txn.Amount.Amount,
			Currency: txn.Amount.Currency,
		})
		return settleErr
	}); err != nil {
		return ConfirmMatchResult{}, fmt.Errorf("banking: confirm match settle invoice %s: %w", invoiceID, err)
	}
	if err = callReconciliationHook(ctx, s.reconciliationHooks.AfterConfirmInvoiceSettled); err != nil {
		return ConfirmMatchResult{}, err
	}

	realisedFX, err := s.moneyFX.RealisedFXAmount(ctx, tx, invoiceID)
	if err != nil {
		return ConfirmMatchResult{}, fmt.Errorf("banking: confirm match realised FX lookup: %w", err)
	}
	if err = s.store.SupersedeActiveSuggestion(ctx, tx, txn.ID); err != nil {
		return ConfirmMatchResult{}, err
	}
	if _, err = s.store.transitionTransactionStateLocked(ctx, tx, txn, TransactionStateReconciled, reconciliationActor); err != nil {
		return ConfirmMatchResult{}, err
	}
	if err = callReconciliationHook(ctx, s.reconciliationHooks.AfterConfirmStateTransition); err != nil {
		return ConfirmMatchResult{}, err
	}
	if err = s.publishTransactionReconciled(ctx, tx, txn.ID, SuggestionKindInvoiceMatch); err != nil {
		return ConfirmMatchResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ConfirmMatchResult{}, fmt.Errorf("banking: commit confirm match transaction: %w", err)
	}
	txn.State = TransactionStateReconciled
	return ConfirmMatchResult{
		Transaction:   txn,
		Kind:          SuggestionKindInvoiceMatch,
		InvoiceID:     invoiceID,
		RealisedFXGBP: realisedFX,
	}, nil
}

func (s *Service) FileToDLA(ctx context.Context, txnID TransactionID) (_ FileToDLAResult, err error) {
	if err := s.requireReconciliationDeps(false, true, true); err != nil {
		return FileToDLAResult{}, err
	}

	tx, err := s.beginReconciliationTx(ctx, "file to DLA")
	if err != nil {
		return FileToDLAResult{}, err
	}
	defer rollbackOnError(ctx, tx, &err)

	txn, account, _, err := s.lockSuggestedCommandTransaction(ctx, tx, txnID, SuggestionKindDLA)
	if err != nil {
		return FileToDLAResult{}, err
	}
	if txn.Amount.Amount >= 0 {
		return FileToDLAResult{}, fmt.Errorf("banking: DLA transaction %d amount must be outbound: %w", txn.ID, ErrInvalidReconciliation)
	}

	drawingNative, err := txn.Amount.Negate()
	if err != nil {
		return FileToDLAResult{}, fmt.Errorf("banking: DLA drawing native amount: %w", err)
	}
	drawingGBP, err := s.moneyFX.ToGBP(ctx, drawingNative, txn.Date)
	if err != nil {
		return FileToDLAResult{}, fmt.Errorf("banking: DLA GBP conversion: %w", err)
	}
	if err = s.dla.FileDrawing(ctx, tx, dla.TxnRef{
		Ref:             bankingTxnRef(txn.ID),
		Date:            txn.Date,
		Amount:          drawingGBP,
		CashAccountCode: account.LedgerAccountCode,
		Description:     fmt.Sprintf("Banking DLA drawing %s", transactionDisplayRef(txn)),
	}); err != nil {
		return FileToDLAResult{}, fmt.Errorf("banking: file DLA drawing: %w", err)
	}
	if err = s.store.SupersedeActiveSuggestion(ctx, tx, txn.ID); err != nil {
		return FileToDLAResult{}, err
	}
	if _, err = s.store.transitionTransactionStateLocked(ctx, tx, txn, TransactionStateReconciled, reconciliationActor); err != nil {
		return FileToDLAResult{}, err
	}
	if err = s.publishTransactionReconciled(ctx, tx, txn.ID, SuggestionKindDLA); err != nil {
		return FileToDLAResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return FileToDLAResult{}, fmt.Errorf("banking: commit file to DLA transaction: %w", err)
	}
	txn.State = TransactionStateReconciled
	return FileToDLAResult{Transaction: txn, Kind: SuggestionKindDLA, AmountGBP: drawingGBP}, nil
}

func (s *Service) Recode(ctx context.Context, txnID TransactionID, accountCode ledger.AccountCode) (_ RecodeResult, err error) {
	if err := s.requireReconciliationDeps(false, true, false); err != nil {
		return RecodeResult{}, err
	}

	tx, err := s.beginReconciliationTx(ctx, "recode")
	if err != nil {
		return RecodeResult{}, err
	}
	defer rollbackOnError(ctx, tx, &err)

	txn, account, err := s.lockCommandTransaction(ctx, tx, txnID)
	if err != nil {
		return RecodeResult{}, err
	}
	if txn.State != TransactionStateSuggested {
		return RecodeResult{}, invalidReconciliationState(txn, TransactionStateReconciled)
	}
	if txn.Amount.Amount >= 0 {
		return RecodeResult{}, fmt.Errorf("banking: recode transaction %d amount must be outbound: %w", txn.ID, ErrInvalidReconciliation)
	}
	expenseNative, err := txn.Amount.Negate()
	if err != nil {
		return RecodeResult{}, fmt.Errorf("banking: recode expense native amount: %w", err)
	}
	cashGBP, err := s.moneyFX.ToGBP(ctx, txn.Amount, txn.Date)
	if err != nil {
		return RecodeResult{}, fmt.Errorf("banking: recode GBP conversion: %w", err)
	}
	expenseGBP, err := cashGBP.Negate()
	if err != nil {
		return RecodeResult{}, fmt.Errorf("banking: recode expense GBP amount: %w", err)
	}
	if _, err = s.ledgerJournal.Post(ctx, tx, ledger.NewJournalEntry{
		Date:         txn.Date,
		Description:  fmt.Sprintf("Banking recode %s", transactionDisplayRef(txn)),
		SourceModule: ModuleName,
		SourceRef:    commandSourceRef(txn.ID, "recode"),
		Postings: []ledger.NewPosting{
			{AccountCode: accountCode, Amount: expenseNative, AmountGBP: expenseGBP},
			{AccountCode: account.LedgerAccountCode, Amount: txn.Amount, AmountGBP: cashGBP},
		},
	}); err != nil {
		return RecodeResult{}, fmt.Errorf("banking: recode ledger post: %w", err)
	}
	rule, err := s.store.LearnPayeeRuleFromRecode(ctx, tx, txn, accountCode)
	if err != nil {
		return RecodeResult{}, err
	}
	if err = s.store.SupersedeActiveSuggestion(ctx, tx, txn.ID); err != nil {
		return RecodeResult{}, err
	}
	if _, err = s.store.transitionTransactionStateLocked(ctx, tx, txn, TransactionStateReconciled, reconciliationActor); err != nil {
		return RecodeResult{}, err
	}
	if err = s.publishTransactionReconciled(ctx, tx, txn.ID, SuggestionKindPayeeRule); err != nil {
		return RecodeResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return RecodeResult{}, fmt.Errorf("banking: commit recode transaction: %w", err)
	}
	txn.State = TransactionStateReconciled
	return RecodeResult{Transaction: txn, Kind: SuggestionKindPayeeRule, Rule: rule}, nil
}

func (s *Service) Exclude(ctx context.Context, txnID TransactionID, reason string) (_ TransactionStateChange, err error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return TransactionStateChange{}, fmt.Errorf("banking: exclude reason is required: %w", ErrInvalidReconciliation)
	}
	tx, err := s.beginReconciliationTx(ctx, "exclude")
	if err != nil {
		return TransactionStateChange{}, err
	}
	defer rollbackOnError(ctx, tx, &err)

	txn, err := s.store.TransactionForUpdate(ctx, tx, txnID)
	if err != nil {
		return TransactionStateChange{}, err
	}
	switch txn.State {
	case TransactionStateReconciled, TransactionStateExcluded:
		return TransactionStateChange{}, alreadyReconciled(txn)
	}
	change, err := s.store.transitionTransactionStateLocked(ctx, tx, txn, TransactionStateExcluded, stateReasonActor("exclude", reason))
	if err != nil {
		return TransactionStateChange{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return TransactionStateChange{}, fmt.Errorf("banking: commit exclude transaction: %w", err)
	}
	return change, nil
}

func (s *Service) Unexclude(ctx context.Context, txnID TransactionID, reason string) (_ TransactionStateChange, err error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return TransactionStateChange{}, fmt.Errorf("banking: unexclude reason is required: %w", ErrInvalidReconciliation)
	}
	tx, err := s.beginReconciliationTx(ctx, "unexclude")
	if err != nil {
		return TransactionStateChange{}, err
	}
	defer rollbackOnError(ctx, tx, &err)

	txn, err := s.store.TransactionForUpdate(ctx, tx, txnID)
	if err != nil {
		return TransactionStateChange{}, err
	}
	if txn.State == TransactionStateReconciled {
		return TransactionStateChange{}, alreadyReconciled(txn)
	}
	change, err := s.store.transitionTransactionStateLocked(ctx, tx, txn, TransactionStateUnreconciled, stateReasonActor("unexclude", reason))
	if err != nil {
		return TransactionStateChange{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return TransactionStateChange{}, fmt.Errorf("banking: commit unexclude transaction: %w", err)
	}
	return change, nil
}

func (s *Service) requireReconciliationDeps(needInvoices bool, needLedger bool, needDLA bool) error {
	if s.pool == nil {
		return fmt.Errorf("banking: reconciliation requires pool")
	}
	if needLedger && s.ledgerJournal == nil {
		return fmt.Errorf("banking: reconciliation requires ledger")
	}
	if s.moneyFX == nil {
		return fmt.Errorf("banking: reconciliation requires moneyfx")
	}
	if needInvoices && s.invoices == nil {
		return fmt.Errorf("banking: reconciliation requires invoicing")
	}
	if needDLA && s.dla == nil {
		return fmt.Errorf("banking: reconciliation requires DLA")
	}
	return nil
}

func (s *Service) beginReconciliationTx(ctx context.Context, command string) (dbTx, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("banking: %s requires pool", command)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("banking: begin %s transaction: %w", command, err)
	}
	return tx, nil
}

type dbTx interface {
	db.Tx
	Commit(context.Context) error
	Rollback(context.Context) error
}

func rollbackOnError(ctx context.Context, tx dbTx, err *error) {
	if err != nil && *err != nil {
		_ = tx.Rollback(ctx)
	}
}

func (s *Service) lockSuggestedCommandTransaction(ctx context.Context, tx db.Tx, txnID TransactionID, kind SuggestionKind) (Transaction, BankAccount, Suggestion, error) {
	txn, account, err := s.lockCommandTransaction(ctx, tx, txnID)
	if err != nil {
		return Transaction{}, BankAccount{}, Suggestion{}, err
	}
	if txn.State != TransactionStateSuggested {
		return Transaction{}, BankAccount{}, Suggestion{}, invalidReconciliationState(txn, TransactionStateReconciled)
	}
	suggestion, err := s.store.ActiveSuggestion(ctx, tx, txn.ID)
	if err != nil {
		return Transaction{}, BankAccount{}, Suggestion{}, err
	}
	if suggestion.Kind != kind {
		return Transaction{}, BankAccount{}, Suggestion{}, fmt.Errorf("banking: transaction %d active suggestion is %s, want %s: %w", txn.ID, suggestion.Kind, kind, ErrInvalidSuggestion)
	}
	return txn, account, suggestion, nil
}

func (s *Service) lockCommandTransaction(ctx context.Context, tx db.Tx, txnID TransactionID) (Transaction, BankAccount, error) {
	txn, err := s.store.TransactionForUpdate(ctx, tx, txnID)
	if err != nil {
		return Transaction{}, BankAccount{}, err
	}
	if txn.State == TransactionStateReconciled {
		return Transaction{}, BankAccount{}, alreadyReconciled(txn)
	}
	account, err := s.store.Account(ctx, tx, txn.AccountID)
	if err != nil {
		return Transaction{}, BankAccount{}, err
	}
	return txn, account, nil
}

func invalidReconciliationState(txn Transaction, to TransactionState) error {
	if txn.State == TransactionStateReconciled {
		return alreadyReconciled(txn)
	}
	return &InvalidStateTransitionError{
		TransactionID: txn.ID,
		From:          txn.State,
		To:            to,
	}
}

func alreadyReconciled(txn Transaction) error {
	return &AlreadyReconciledError{TransactionID: txn.ID, State: txn.State}
}

func (s *Service) publishTransactionReconciled(ctx context.Context, tx db.Tx, txnID TransactionID, kind SuggestionKind) error {
	if s.eventBus == nil {
		return nil
	}
	if err := s.eventBus.Publish(ctx, tx, TransactionReconciled{
		TransactionID: txnID,
		Kind:          kind,
	}); err != nil {
		return fmt.Errorf("banking: publish transaction reconciled: %w", err)
	}
	return nil
}

func withTransactionSearchPath(ctx context.Context, tx db.Tx, module string, fn func() error) (err error) {
	if err := db.ValidateModule(module); err != nil {
		return err
	}
	var previousSearchPath string
	if err := tx.QueryRow(ctx, "SELECT current_setting('search_path')").Scan(&previousSearchPath); err != nil {
		return fmt.Errorf("banking: read transaction search_path: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('search_path', $1, true)", module); err != nil {
		return fmt.Errorf("banking: set transaction search_path %s: %w", module, err)
	}
	defer func() {
		if restoreErr := restoreTransactionSearchPath(ctx, tx, previousSearchPath); err == nil && restoreErr != nil {
			err = restoreErr
		}
	}()
	return fn()
}

func restoreTransactionSearchPath(ctx context.Context, tx db.Tx, previousSearchPath string) error {
	if _, err := tx.Exec(ctx, "SELECT set_config('search_path', $1, true)", previousSearchPath); err != nil {
		return fmt.Errorf("banking: restore transaction search_path %s: %w", previousSearchPath, err)
	}
	return nil
}

func callReconciliationHook(ctx context.Context, hook func(context.Context) error) error {
	if hook == nil {
		return nil
	}
	return hook(ctx)
}

func bankingTxnRef(txnID TransactionID) string {
	return fmt.Sprintf("banking:%d", txnID)
}

func commandSourceRef(txnID TransactionID, command string) string {
	return fmt.Sprintf("%s:%s", bankingTxnRef(txnID), command)
}

func transactionDisplayRef(txn Transaction) string {
	reference := strings.TrimSpace(txn.Reference)
	if reference != "" {
		return reference
	}
	payee := strings.TrimSpace(txn.Payee)
	if payee != "" {
		return payee
	}
	return bankingTxnRef(txn.ID)
}

func tradeDebtorsAccountForCurrency(currency string) ledger.AccountCode {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "EUR":
		return "1100-debtors-eur"
	case "GBP":
		return "1101-debtors-gbp"
	default:
		return ledger.AccountCode("1100-debtors-" + strings.ToLower(strings.TrimSpace(currency)))
	}
}

func stateReasonActor(action string, reason string) string {
	return strings.TrimSpace(action) + ": " + strings.TrimSpace(reason)
}
