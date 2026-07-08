package banking

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

const matchEngineCreatedByPrefix = "match-engine:"

func (s *Service) SubscribeEvents(eventBus *bus.Bus) {
	if s == nil || eventBus == nil {
		return
	}
	eventBus.Subscribe(invoicing.InvoiceSentName, func(ctx context.Context, tx db.Tx, evt bus.Event) error {
		sent, err := invoicing.InvoiceSentFromEvent(evt)
		if err != nil {
			return err
		}
		_, err = s.handleInvoiceSentTx(ctx, tx, sent)
		return err
	})
}

func (s *Service) HandleInvoiceSent(ctx context.Context, evt invoicing.InvoiceSent) (MatchEngineRun, error) {
	return s.RunMatchEngine(ctx, MatchEngineTriggerInvoiceSent, nil)
}

func (s *Service) handleInvoiceSentTx(ctx context.Context, tx db.Tx, evt invoicing.InvoiceSent) (_ MatchEngineRun, err error) {
	if tx == nil {
		return s.HandleInvoiceSent(ctx, evt)
	}
	restoreScope, err := db.ScopeTransactionToModule(ctx, tx, ModuleName)
	if err != nil {
		return MatchEngineRun{}, err
	}
	defer func() {
		if restoreErr := restoreScope(ctx); err == nil && restoreErr != nil {
			err = restoreErr
		}
	}()
	return s.runMatchEngineTx(ctx, tx, MatchEngineTriggerInvoiceSent, nil)
}

func (s *Service) ManualRefresh(ctx context.Context) (MatchEngineRun, error) {
	return s.RunMatchEngine(ctx, MatchEngineTriggerManualRefresh, nil)
}

func (s *Service) RefreshDirectorNameSuggestions(ctx context.Context, names []string) (MatchEngineRun, error) {
	return s.runMatchEngineWithDirectorNames(ctx, MatchEngineTriggerIdentityProfile, nil, directorNameSnapshot(names))
}

func (s *Service) RefreshDirectorSuggestions(ctx context.Context, directors []dla.Director) (MatchEngineRun, error) {
	return s.runMatchEngineWithDirectorNames(ctx, MatchEngineTriggerIdentityProfile, nil, directorSnapshot(directors))
}

func (s *Service) RefreshDirectorNameSuggestionsTx(ctx context.Context, tx db.Tx, names []string) (_ MatchEngineRun, err error) {
	if tx == nil {
		return s.RefreshDirectorNameSuggestions(ctx, names)
	}
	restoreScope, err := db.ScopeTransactionToModule(ctx, tx, ModuleName)
	if err != nil {
		return MatchEngineRun{}, err
	}
	defer func() {
		if restoreErr := restoreScope(ctx); err == nil && restoreErr != nil {
			err = restoreErr
		}
	}()
	return s.runMatchEngineTxWithDirectorNames(ctx, tx, MatchEngineTriggerIdentityProfile, nil, directorNameSnapshot(names))
}

func (s *Service) RefreshDirectorSuggestionsTx(ctx context.Context, tx db.Tx, directors []dla.Director) (_ MatchEngineRun, err error) {
	if tx == nil {
		return s.RefreshDirectorSuggestions(ctx, directors)
	}
	restoreScope, err := db.ScopeTransactionToModule(ctx, tx, ModuleName)
	if err != nil {
		return MatchEngineRun{}, err
	}
	defer func() {
		if restoreErr := restoreScope(ctx); err == nil && restoreErr != nil {
			err = restoreErr
		}
	}()
	return s.runMatchEngineTxWithDirectorNames(ctx, tx, MatchEngineTriggerIdentityProfile, nil, directorSnapshot(directors))
}

type directorSnapshot []dla.Director

func (d directorSnapshot) DirectorNames(context.Context) ([]string, error) {
	names := make([]string, 0, len(d))
	for _, director := range d {
		if name := strings.TrimSpace(director.Name); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

func (d directorSnapshot) Directors(context.Context) ([]dla.Director, error) {
	return normalizeDirectorTargets([]dla.Director(d)), nil
}

func (s *Service) RunMatchEngine(ctx context.Context, trigger MatchEngineTrigger, txnIDs []TransactionID) (_ MatchEngineRun, err error) {
	return s.runMatchEngineWithDirectorNames(ctx, trigger, txnIDs, nil)
}

func (s *Service) runMatchEngineWithDirectorNames(ctx context.Context, trigger MatchEngineTrigger, txnIDs []TransactionID, directorNames DirectorNameSource) (_ MatchEngineRun, err error) {
	if s.pool == nil {
		return MatchEngineRun{}, fmt.Errorf("banking: match engine requires pool")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MatchEngineRun{}, fmt.Errorf("banking: begin match engine transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	run, err := s.runMatchEngineTxWithDirectorNames(ctx, tx, trigger, txnIDs, directorNames)
	if err != nil {
		return MatchEngineRun{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return MatchEngineRun{}, fmt.Errorf("banking: commit match engine transaction: %w", err)
	}
	return run, nil
}

func (s *Service) runMatchEngineTx(ctx context.Context, tx db.Tx, trigger MatchEngineTrigger, txnIDs []TransactionID) (MatchEngineRun, error) {
	return s.runMatchEngineTxWithDirectorNames(ctx, tx, trigger, txnIDs, nil)
}

func (s *Service) runMatchEngineTxWithDirectorNames(ctx context.Context, tx db.Tx, trigger MatchEngineTrigger, txnIDs []TransactionID, directorNames DirectorNameSource) (MatchEngineRun, error) {
	if err := ctx.Err(); err != nil {
		return MatchEngineRun{}, err
	}
	normalizedTrigger, err := normalizeMatchEngineTrigger(trigger)
	if err != nil {
		return MatchEngineRun{}, err
	}

	var txns []Transaction
	if txnIDs == nil {
		txns, err = s.store.MatchableTransactions(ctx, tx)
	} else {
		txnIDs = normalizeMatchEngineTxnIDs(txnIDs)
		txns, err = s.store.MatchableTransactionsByID(ctx, tx, txnIDs)
	}
	if err != nil {
		return MatchEngineRun{}, err
	}

	evaluated := make([]TransactionID, len(txns))
	for i, txn := range txns {
		evaluated[i] = txn.ID
	}
	run, err := s.store.InsertMatchEngineRun(ctx, tx, normalizedTrigger, evaluated)
	if err != nil {
		return MatchEngineRun{}, err
	}

	for _, txn := range txns {
		decision, err := s.evaluateTransactionWithDirectorNames(ctx, tx, txn, directorNames)
		if err != nil {
			return MatchEngineRun{}, err
		}
		if decision == nil {
			if err := s.store.ClearActiveSuggestion(ctx, tx, txn.ID, matchEngineCreatedBy(run.ID)); err != nil {
				return MatchEngineRun{}, err
			}
			continue
		}
		input := decision.input
		lockedTxn, canWrite, err := s.canWriteMatchEngineSuggestion(ctx, tx, txn.ID)
		if err != nil {
			return MatchEngineRun{}, err
		}
		if !canWrite {
			continue
		}
		if decision.payeeRule != nil {
			input, err = s.payeeRuleSuggestionInputAfterLock(ctx, tx, lockedTxn.ID, *decision.payeeRule)
			if err != nil {
				return MatchEngineRun{}, err
			}
		}
		if active, err := s.store.ActiveSuggestion(ctx, tx, lockedTxn.ID); err == nil {
			if sameMatchEngineSuggestion(active, input) {
				continue
			}
		} else if !errors.Is(err, ErrSuggestionNotFound) {
			return MatchEngineRun{}, err
		}
		input.CreatedBy = matchEngineCreatedBy(run.ID)
		suggestion, err := s.store.InsertSuggestion(ctx, tx, input)
		if err != nil {
			return MatchEngineRun{}, err
		}
		run.Suggestions = append(run.Suggestions, suggestion)
	}
	return run, nil
}

func sameMatchEngineSuggestion(active Suggestion, input SuggestionInput) bool {
	if !strings.HasPrefix(active.CreatedBy, matchEngineCreatedByPrefix) {
		return false
	}
	return active.Kind == input.Kind &&
		active.Confidence == input.Confidence &&
		active.Target == input.Target &&
		active.Explanation == input.Explanation &&
		active.AutoPostable == input.AutoPostable
}

type directorNameSnapshot []string

func (d directorNameSnapshot) DirectorNames(context.Context) ([]string, error) {
	names := make([]string, 0, len(d))
	for _, name := range d {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

func (d directorNameSnapshot) Directors(context.Context) ([]dla.Director, error) {
	directors := make([]dla.Director, 0, len(d))
	for index, name := range d {
		if name = strings.TrimSpace(name); name != "" {
			directors = append(directors, dla.Director{
				ID:   dla.DirectorIDForIndex(index),
				Name: name,
			})
		}
	}
	return directors, nil
}

func (s *Service) payeeRuleSuggestionInputAfterLock(ctx context.Context, tx db.Tx, txnID TransactionID, rule PayeeRule) (SuggestionInput, error) {
	recorded, err := s.store.PayeeRuleSuggestionRecorded(ctx, tx, txnID, rule.AccountCode)
	if err != nil {
		return SuggestionInput{}, err
	}
	if recorded {
		rule, err = s.store.PayeeRule(ctx, tx, rule.ID)
		if err != nil {
			return SuggestionInput{}, err
		}
	} else {
		rule, err = s.store.RecordPayeeRuleApplied(ctx, tx, rule.ID)
		if err != nil {
			return SuggestionInput{}, err
		}
	}
	return payeeRuleSuggestionInput(txnID, rule, s.payeeRuleAutoPostThreshold), nil
}

func (s *Service) canWriteMatchEngineSuggestion(ctx context.Context, tx db.Tx, txnID TransactionID) (Transaction, bool, error) {
	txn, err := s.store.TransactionForUpdate(ctx, tx, txnID)
	if err != nil {
		return Transaction{}, false, err
	}
	switch txn.State {
	case TransactionStateUnreconciled, TransactionStateSuggested:
	default:
		return txn, false, nil
	}
	active, err := s.store.ActiveSuggestion(ctx, tx, txnID)
	if err != nil {
		if errors.Is(err, ErrSuggestionNotFound) {
			return txn, true, nil
		}
		return Transaction{}, false, err
	}
	return txn, strings.HasPrefix(active.CreatedBy, matchEngineCreatedByPrefix), nil
}

func normalizeMatchEngineTrigger(trigger MatchEngineTrigger) (MatchEngineTrigger, error) {
	trigger = trimMatchEngineTrigger(trigger)
	switch trigger {
	case MatchEngineTriggerImportCompletion, MatchEngineTriggerInvoiceSent, MatchEngineTriggerIdentityProfile, MatchEngineTriggerManualRefresh:
		return trigger, nil
	default:
		return "", fmt.Errorf("banking: match engine trigger %q: %w", trigger, ErrInvalidSuggestion)
	}
}

func matchEngineCreatedBy(id MatchEngineRunID) string {
	return fmt.Sprintf("%s%d", matchEngineCreatedByPrefix, id)
}

func normalizeMatchEngineTxnIDs(ids []TransactionID) []TransactionID {
	normalized := make([]TransactionID, 0, len(ids))
	seen := make(map[TransactionID]bool, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		normalized = append(normalized, id)
	}
	return normalized
}

func trimMatchEngineTrigger(trigger MatchEngineTrigger) MatchEngineTrigger {
	return MatchEngineTrigger(strings.TrimSpace(string(trigger)))
}
