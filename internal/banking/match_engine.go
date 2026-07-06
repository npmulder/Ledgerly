package banking

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

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

func (s *Service) handleInvoiceSentTx(ctx context.Context, tx db.Tx, evt invoicing.InvoiceSent) (_ MatchEngineRun, err error) {
	if tx == nil {
		return s.HandleInvoiceSent(ctx, evt)
	}
	restoreScope, err := scopeTransactionToModule(ctx, tx, ModuleName)
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
		canWrite, err := s.canWriteMatchEngineSuggestion(ctx, tx, txn.ID)
		if err != nil {
			return MatchEngineRun{}, err
		}
		if !canWrite {
			continue
		}
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

func (s *Service) canWriteMatchEngineSuggestion(ctx context.Context, tx db.Tx, txnID TransactionID) (bool, error) {
	if _, err := s.store.TransactionForUpdate(ctx, tx, txnID); err != nil {
		return false, err
	}
	active, err := s.store.ActiveSuggestion(ctx, tx, txnID)
	if err != nil {
		if errors.Is(err, ErrSuggestionNotFound) {
			return true, nil
		}
		return false, err
	}
	return strings.HasPrefix(active.CreatedBy, matchEngineCreatedByPrefix), nil
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
	return fmt.Sprintf("%s%d", matchEngineCreatedByPrefix, id)
}

func scopeTransactionToModule(ctx context.Context, tx db.Tx, module string) (func(context.Context) error, error) {
	role, err := db.RoleForModule(module)
	if err != nil {
		return nil, err
	}
	var previousRole string
	var previousSearchPath string
	if err := tx.QueryRow(ctx, "SELECT current_role, current_setting('search_path')").Scan(&previousRole, &previousSearchPath); err != nil {
		return nil, fmt.Errorf("banking: read transaction module scope: %w", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
		return nil, fmt.Errorf("banking: set transaction role %s: %w", role, err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('search_path', $1, true)", module); err != nil {
		return nil, fmt.Errorf("banking: set transaction search_path %s: %w", module, err)
	}
	return func(ctx context.Context) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{previousRole}.Sanitize()); err != nil {
			return fmt.Errorf("banking: restore transaction role %s: %w", previousRole, err)
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('search_path', $1, true)", previousSearchPath); err != nil {
			return fmt.Errorf("banking: restore transaction search_path %s: %w", previousSearchPath, err)
		}
		return nil
	}, nil
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
