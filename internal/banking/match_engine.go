package banking

import (
	"context"
	"fmt"
	"strings"

	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

func (s *Service) SubscribeEvents(eventBus *bus.Bus) {
	if s == nil || eventBus == nil {
		return
	}
	eventBus.Subscribe(invoicing.InvoiceSentName, func(ctx context.Context, tx db.Tx, evt bus.Event) error {
		sent, err := invoiceSentEvent(evt)
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

func (s *Service) handleInvoiceSentTx(ctx context.Context, tx db.Tx, evt invoicing.InvoiceSent) (MatchEngineRun, error) {
	if tx == nil {
		return s.HandleInvoiceSent(ctx, evt)
	}
	return s.runMatchEngineTx(ctx, tx, MatchEngineTriggerInvoiceSent, nil)
}

func (s *Service) ManualRefresh(ctx context.Context) (MatchEngineRun, error) {
	return s.RunMatchEngine(ctx, MatchEngineTriggerManualRefresh, nil)
}

func (s *Service) RunMatchEngine(ctx context.Context, trigger MatchEngineTrigger, txnIDs []TransactionID) (_ MatchEngineRun, err error) {
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

	run, err := s.runMatchEngineTx(ctx, tx, trigger, txnIDs)
	if err != nil {
		return MatchEngineRun{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return MatchEngineRun{}, fmt.Errorf("banking: commit match engine transaction: %w", err)
	}
	return run, nil
}

func (s *Service) runMatchEngineTx(ctx context.Context, tx db.Tx, trigger MatchEngineTrigger, txnIDs []TransactionID) (MatchEngineRun, error) {
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
		decision, err := s.evaluateTransaction(ctx, tx, txn)
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
		if decision.payeeRule != nil {
			rule := *decision.payeeRule
			recorded, err := s.store.PayeeRuleSuggestionRecorded(ctx, tx, txn.ID, rule.AccountCode)
			if err != nil {
				return MatchEngineRun{}, err
			}
			if !recorded {
				rule, err = s.store.RecordPayeeRuleApplied(ctx, tx, rule.ID)
				if err != nil {
					return MatchEngineRun{}, err
				}
			}
			input = payeeRuleSuggestionInput(txn.ID, rule, s.payeeRuleAutoPostThreshold)
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

func normalizeMatchEngineTrigger(trigger MatchEngineTrigger) (MatchEngineTrigger, error) {
	trigger = trimMatchEngineTrigger(trigger)
	switch trigger {
	case MatchEngineTriggerImportCompletion, MatchEngineTriggerInvoiceSent, MatchEngineTriggerManualRefresh:
		return trigger, nil
	default:
		return "", fmt.Errorf("banking: match engine trigger %q: %w", trigger, ErrInvalidSuggestion)
	}
}

func matchEngineCreatedBy(id MatchEngineRunID) string {
	return fmt.Sprintf("match-engine:%d", id)
}

func invoiceSentEvent(evt bus.Event) (invoicing.InvoiceSent, error) {
	switch e := evt.(type) {
	case invoicing.InvoiceSent:
		return e, nil
	case *invoicing.InvoiceSent:
		if e == nil {
			return invoicing.InvoiceSent{}, fmt.Errorf("banking: nil InvoiceSent event")
		}
		return *e, nil
	default:
		return invoicing.InvoiceSent{}, fmt.Errorf("banking: got %T, want invoicing.InvoiceSent", evt)
	}
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
