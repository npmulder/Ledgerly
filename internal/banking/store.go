package banking

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

type Store struct{}

type newTransaction struct {
	AccountID     AccountID
	Date          time.Time
	Amount        money.Money
	Payee         string
	Reference     string
	ProviderMeta  map[string]string
	ImportBatchID ImportBatchID
	DedupeHash    string
}

func (Store) AccountByNaturalKey(ctx context.Context, tx db.Tx, input AccountInput) (BankAccount, bool, error) {
	account, err := scanAccountRow(tx.QueryRow(ctx, accountSelectSQL()+`
WHERE provider = $1
	AND name = $2
	AND currency = $3`,
		string(input.Provider),
		input.Name,
		input.Currency,
	))
	if err == nil {
		return account, true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return BankAccount{}, false, nil
	}
	return BankAccount{}, false, fmt.Errorf("banking: load account by natural key: %w", err)
}

func (Store) Account(ctx context.Context, tx db.Tx, id AccountID) (BankAccount, error) {
	account, err := scanAccountRow(tx.QueryRow(ctx, accountSelectSQL()+`
WHERE id = $1`, int64(id)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BankAccount{}, &AccountNotFoundError{ID: id}
		}
		return BankAccount{}, fmt.Errorf("banking: load account %d: %w", id, err)
	}
	return account, nil
}

func (Store) ListAccounts(ctx context.Context, tx db.Tx) ([]BankAccount, error) {
	rows, err := tx.Query(ctx, accountSelectSQL()+`
ORDER BY lower(name), currency, id`)
	if err != nil {
		return nil, fmt.Errorf("banking: list accounts: %w", err)
	}
	defer rows.Close()

	accounts, err := pgx.CollectRows(rows, scanAccount)
	if err != nil {
		return nil, fmt.Errorf("banking: collect accounts: %w", err)
	}
	return accounts, nil
}

func (s Store) InsertAccount(ctx context.Context, tx db.Tx, input AccountInput, code ledger.AccountCode) (BankAccount, error) {
	account, err := scanAccountRow(tx.QueryRow(ctx, `
INSERT INTO bank_accounts (name, provider, currency, ledger_account_code)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING
RETURNING id, name, provider, currency, ledger_account_code, created_at`,
		input.Name,
		string(input.Provider),
		input.Currency,
		string(code),
	))
	if err == nil {
		return account, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		account, found, loadErr := s.AccountByNaturalKey(ctx, tx, input)
		if loadErr != nil {
			return BankAccount{}, loadErr
		}
		if found {
			return account, nil
		}
		return BankAccount{}, fmt.Errorf("banking: insert account conflict without matching natural key: %w", ErrInvalidAccount)
	}
	return BankAccount{}, fmt.Errorf("banking: insert account: %w", err)
}

func (Store) InsertImportBatch(ctx context.Context, tx db.Tx, accountID AccountID, filename string, totalRows int) (BatchSummary, error) {
	var summary BatchSummary
	var (
		batchID          int64
		scannedAccountID int64
	)
	if err := tx.QueryRow(ctx, `
INSERT INTO import_batches (account_id, filename)
VALUES ($1, $2)
RETURNING id, account_id, filename, imported_at, total_rows, new_rows, duplicate_rows`,
		int64(accountID),
		filename,
	).Scan(
		&batchID,
		&scannedAccountID,
		&summary.Filename,
		&summary.ImportedAt,
		&summary.TotalRows,
		&summary.NewRows,
		&summary.DuplicateRows,
	); err != nil {
		return BatchSummary{}, fmt.Errorf("banking: insert import batch: %w", err)
	}
	summary.BatchID = ImportBatchID(batchID)
	summary.AccountID = AccountID(scannedAccountID)
	summary.TotalRows = totalRows
	return summary, nil
}

func (Store) UpdateImportBatchCounts(ctx context.Context, tx db.Tx, summary BatchSummary) (BatchSummary, error) {
	var (
		batchID          int64
		scannedAccountID int64
	)
	if err := tx.QueryRow(ctx, `
UPDATE import_batches
SET total_rows = $2,
	new_rows = $3,
	duplicate_rows = $4
WHERE id = $1
RETURNING id, account_id, filename, imported_at, total_rows, new_rows, duplicate_rows`,
		int64(summary.BatchID),
		summary.TotalRows,
		summary.NewRows,
		summary.DuplicateRows,
	).Scan(
		&batchID,
		&scannedAccountID,
		&summary.Filename,
		&summary.ImportedAt,
		&summary.TotalRows,
		&summary.NewRows,
		&summary.DuplicateRows,
	); err != nil {
		return BatchSummary{}, fmt.Errorf("banking: update import batch counts: %w", err)
	}
	summary.BatchID = ImportBatchID(batchID)
	summary.AccountID = AccountID(scannedAccountID)
	return summary, nil
}

func (Store) InsertTransaction(ctx context.Context, tx db.Tx, txn newTransaction) (TransactionID, bool, error) {
	meta, err := marshalProviderMeta(txn.ProviderMeta)
	if err != nil {
		return 0, false, err
	}
	var id int64
	err = tx.QueryRow(ctx, `
INSERT INTO transactions (
	account_id,
	date,
	amount,
	currency,
	payee,
	reference,
	provider_meta,
	import_batch_id,
	dedupe_hash
) VALUES (
	$1,
	$2,
	$3,
	$4,
	$5,
	$6,
	$7::jsonb,
	$8,
	$9
)
ON CONFLICT (dedupe_hash) DO NOTHING
RETURNING id`,
		int64(txn.AccountID),
		txn.Date,
		txn.Amount.Amount,
		txn.Amount.Currency,
		txn.Payee,
		txn.Reference,
		string(meta),
		int64(txn.ImportBatchID),
		txn.DedupeHash,
	).Scan(&id)
	if err == nil {
		return TransactionID(id), true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return 0, false, fmt.Errorf("banking: insert transaction: %w", err)
}

func (Store) Transaction(ctx context.Context, tx db.Tx, id TransactionID) (Transaction, error) {
	txn, err := scanTransactionRow(tx.QueryRow(ctx, transactionSelectSQL()+`
WHERE id = $1`, int64(id)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Transaction{}, &TransactionNotFoundError{ID: id}
		}
		return Transaction{}, fmt.Errorf("banking: load transaction %d: %w", id, err)
	}
	return txn, nil
}

func (Store) TransactionForUpdate(ctx context.Context, tx db.Tx, id TransactionID) (Transaction, error) {
	txn, err := scanTransactionRow(tx.QueryRow(ctx, transactionSelectSQL()+`
WHERE id = $1
FOR UPDATE`, int64(id)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Transaction{}, &TransactionNotFoundError{ID: id}
		}
		return Transaction{}, fmt.Errorf("banking: lock transaction %d: %w", id, err)
	}
	return txn, nil
}

func (Store) MatchableTransactions(ctx context.Context, tx db.Tx) ([]Transaction, error) {
	rows, err := tx.Query(ctx, transactionSelectSQL()+`
WHERE state IN ('unreconciled', 'suggested')
ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("banking: matchable transactions: %w", err)
	}
	defer rows.Close()
	txns, err := pgx.CollectRows(rows, scanTransaction)
	if err != nil {
		return nil, fmt.Errorf("banking: collect matchable transactions: %w", err)
	}
	return txns, nil
}

func (Store) MatchableTransactionsByID(ctx context.Context, tx db.Tx, ids []TransactionID) ([]Transaction, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	intIDs := transactionIDsToInt64(ids)
	rows, err := tx.Query(ctx, transactionSelectSQL()+`
WHERE id = ANY($1::bigint[])
	AND state IN ('unreconciled', 'suggested')
ORDER BY id`, intIDs)
	if err != nil {
		return nil, fmt.Errorf("banking: matchable transactions by id: %w", err)
	}
	defer rows.Close()
	txns, err := pgx.CollectRows(rows, scanTransaction)
	if err != nil {
		return nil, fmt.Errorf("banking: collect matchable transactions by id: %w", err)
	}
	return txns, nil
}

func (Store) InsertMatchEngineRun(ctx context.Context, tx db.Tx, trigger MatchEngineTrigger, txnIDs []TransactionID) (MatchEngineRun, error) {
	var (
		run            MatchEngineRun
		id             int64
		scannedTrigger string
	)
	if err := tx.QueryRow(ctx, `
INSERT INTO match_engine_runs (trigger, txns_evaluated)
VALUES ($1, $2::bigint[])
RETURNING id, trigger, created_at`,
		string(trigger),
		transactionIDsToInt64(txnIDs),
	).Scan(
		&id,
		&scannedTrigger,
		&run.CreatedAt,
	); err != nil {
		return MatchEngineRun{}, fmt.Errorf("banking: insert match engine run: %w", err)
	}
	run.ID = MatchEngineRunID(id)
	run.Trigger = MatchEngineTrigger(scannedTrigger)
	run.TxnsEvaluated = append([]TransactionID{}, txnIDs...)
	run.CreatedAt = run.CreatedAt.UTC()
	return run, nil
}

func (s Store) TransitionTransactionState(ctx context.Context, tx db.Tx, id TransactionID, to TransactionState, actor string) (TransactionStateChange, error) {
	if !validTransactionState(to) {
		return TransactionStateChange{}, fmt.Errorf("banking: state %q: %w", to, ErrInvalidStateTransition)
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return TransactionStateChange{}, fmt.Errorf("banking: state transition actor is required: %w", ErrInvalidStateTransition)
	}
	txn, err := s.TransactionForUpdate(ctx, tx, id)
	if err != nil {
		return TransactionStateChange{}, err
	}
	return s.transitionTransactionStateLocked(ctx, tx, txn, to, actor)
}

func (Store) transitionTransactionStateLocked(ctx context.Context, tx db.Tx, txn Transaction, to TransactionState, actor string) (TransactionStateChange, error) {
	from := txn.State
	if !legalTransactionStateTransition(from, to) {
		return TransactionStateChange{}, &InvalidStateTransitionError{
			TransactionID: txn.ID,
			From:          from,
			To:            to,
		}
	}
	if _, err := tx.Exec(ctx, `
UPDATE transactions
SET state = $2
WHERE id = $1`,
		int64(txn.ID),
		string(to),
	); err != nil {
		return TransactionStateChange{}, fmt.Errorf("banking: update transaction %d state: %w", txn.ID, err)
	}
	return scanStateChangeRow(tx.QueryRow(ctx, `
INSERT INTO transaction_state_changes (txn_id, from_state, to_state, actor)
VALUES ($1, $2, $3, $4)
RETURNING id, txn_id, from_state, to_state, changed_at, actor`,
		int64(txn.ID),
		string(from),
		string(to),
		actor,
	))
}

func (s Store) InsertSuggestion(ctx context.Context, tx db.Tx, input SuggestionInput) (Suggestion, error) {
	normalized, err := normalizeSuggestionInput(input)
	if err != nil {
		return Suggestion{}, err
	}
	txn, err := s.TransactionForUpdate(ctx, tx, normalized.TransactionID)
	if err != nil {
		return Suggestion{}, err
	}
	switch txn.State {
	case TransactionStateUnreconciled:
		if _, err := s.transitionTransactionStateLocked(ctx, tx, txn, TransactionStateSuggested, normalized.CreatedBy); err != nil {
			return Suggestion{}, err
		}
	case TransactionStateSuggested:
	default:
		return Suggestion{}, &InvalidStateTransitionError{
			TransactionID: txn.ID,
			From:          txn.State,
			To:            TransactionStateSuggested,
		}
	}
	if _, err := tx.Exec(ctx, `
UPDATE suggestions
SET superseded_at = now()
WHERE txn_id = $1
	AND superseded_at IS NULL`,
		int64(normalized.TransactionID),
	); err != nil {
		return Suggestion{}, fmt.Errorf("banking: supersede active suggestion for transaction %d: %w", normalized.TransactionID, err)
	}
	return scanSuggestionRow(tx.QueryRow(ctx, `
INSERT INTO suggestions (txn_id, kind, confidence, target, explanation, auto_postable, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, txn_id, kind, confidence::float8, target, explanation, auto_postable, created_by, created_at, superseded_at`,
		int64(normalized.TransactionID),
		string(normalized.Kind),
		normalized.Confidence,
		normalized.Target,
		normalized.Explanation,
		normalized.AutoPostable,
		normalized.CreatedBy,
	))
}

func (Store) ActiveSuggestion(ctx context.Context, tx db.Tx, txnID TransactionID) (Suggestion, error) {
	suggestion, err := scanSuggestionRow(tx.QueryRow(ctx, suggestionSelectSQL()+`
WHERE txn_id = $1
	AND superseded_at IS NULL`, int64(txnID)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Suggestion{}, ErrSuggestionNotFound
		}
		return Suggestion{}, fmt.Errorf("banking: load active suggestion for transaction %d: %w", txnID, err)
	}
	return suggestion, nil
}

func (Store) SupersedeActiveSuggestion(ctx context.Context, tx db.Tx, txnID TransactionID) error {
	if txnID <= 0 {
		return fmt.Errorf("banking: suggestion transaction id is required: %w", ErrInvalidSuggestion)
	}
	if _, err := tx.Exec(ctx, `
UPDATE suggestions
SET superseded_at = now()
WHERE txn_id = $1
	AND superseded_at IS NULL`,
		int64(txnID),
	); err != nil {
		return fmt.Errorf("banking: supersede active suggestion for transaction %d: %w", txnID, err)
	}
	return nil
}

func (s Store) ClearActiveSuggestion(ctx context.Context, tx db.Tx, txnID TransactionID, actor string) error {
	actor = strings.TrimSpace(actor)
	if txnID <= 0 {
		return fmt.Errorf("banking: suggestion transaction id is required: %w", ErrInvalidSuggestion)
	}
	if actor == "" {
		return fmt.Errorf("banking: suggestion clear actor is required: %w", ErrInvalidSuggestion)
	}
	txn, err := s.TransactionForUpdate(ctx, tx, txnID)
	if err != nil {
		return err
	}
	active, err := s.ActiveSuggestion(ctx, tx, txnID)
	if err != nil {
		if errors.Is(err, ErrSuggestionNotFound) {
			return nil
		}
		return err
	}
	if !strings.HasPrefix(active.CreatedBy, matchEngineCreatedByPrefix) {
		return nil
	}
	if _, err := tx.Exec(ctx, `
UPDATE suggestions
SET superseded_at = now()
WHERE id = $1`,
		int64(active.ID),
	); err != nil {
		return fmt.Errorf("banking: clear active suggestion for transaction %d: %w", txnID, err)
	}
	if txn.State != TransactionStateSuggested {
		return nil
	}
	if _, err := tx.Exec(ctx, `
UPDATE transactions
SET state = $2
WHERE id = $1`,
		int64(txn.ID),
		string(TransactionStateUnreconciled),
	); err != nil {
		return fmt.Errorf("banking: return transaction %d to unreconciled: %w", txn.ID, err)
	}
	if _, err := scanStateChangeRow(tx.QueryRow(ctx, `
INSERT INTO transaction_state_changes (txn_id, from_state, to_state, actor)
VALUES ($1, $2, $3, $4)
RETURNING id, txn_id, from_state, to_state, changed_at, actor`,
		int64(txn.ID),
		string(TransactionStateSuggested),
		string(TransactionStateUnreconciled),
		actor,
	)); err != nil {
		return fmt.Errorf("banking: record suggestion clear state change for transaction %d: %w", txn.ID, err)
	}
	return nil
}

func (Store) SuggestionsForTransaction(ctx context.Context, tx db.Tx, txnID TransactionID) ([]Suggestion, error) {
	rows, err := tx.Query(ctx, suggestionSelectSQL()+`
WHERE txn_id = $1
ORDER BY created_at DESC, id DESC`, int64(txnID))
	if err != nil {
		return nil, fmt.Errorf("banking: list suggestions for transaction %d: %w", txnID, err)
	}
	defer rows.Close()
	suggestions, err := pgx.CollectRows(rows, scanSuggestion)
	if err != nil {
		return nil, fmt.Errorf("banking: collect suggestions for transaction %d: %w", txnID, err)
	}
	return suggestions, nil
}

func (Store) InsertPayeeRule(ctx context.Context, tx db.Tx, input PayeeRuleInput) (PayeeRule, error) {
	normalized, err := normalizePayeeRuleInput(input)
	if err != nil {
		return PayeeRule{}, err
	}
	return scanPayeeRuleRow(tx.QueryRow(ctx, `
INSERT INTO payee_rules (matcher, match_mode, account_code, created_from)
VALUES ($1, $2, $3, $4)
RETURNING id, matcher, match_mode, account_code, times_applied, last_applied_at, created_from, created_at`,
		normalized.Matcher,
		string(normalized.MatchMode),
		string(normalized.AccountCode),
		string(normalized.CreatedFrom),
	))
}

func (Store) PayeeRule(ctx context.Context, tx db.Tx, id PayeeRuleID) (PayeeRule, error) {
	rule, err := scanPayeeRuleRow(tx.QueryRow(ctx, `
SELECT id, matcher, match_mode, account_code, times_applied, last_applied_at, created_from, created_at
FROM payee_rules
WHERE id = $1`, int64(id)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PayeeRule{}, ErrPayeeRuleNotFound
		}
		return PayeeRule{}, fmt.Errorf("banking: load payee rule %d: %w", id, err)
	}
	return rule, nil
}

func (Store) RecordPayeeRuleApplied(ctx context.Context, tx db.Tx, id PayeeRuleID) (PayeeRule, error) {
	rule, err := scanPayeeRuleRow(tx.QueryRow(ctx, `
UPDATE payee_rules
SET times_applied = times_applied + 1,
	last_applied_at = now()
WHERE id = $1
RETURNING id, matcher, match_mode, account_code, times_applied, last_applied_at, created_from, created_at`,
		int64(id),
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PayeeRule{}, ErrPayeeRuleNotFound
		}
		return PayeeRule{}, fmt.Errorf("banking: record payee rule %d applied: %w", id, err)
	}
	return rule, nil
}

func (Store) MatchingPayeeRules(ctx context.Context, tx db.Tx, payee string) ([]PayeeRule, error) {
	normalized := NormalizePayee(payee)
	if normalized == "" {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
SELECT id, matcher, match_mode, account_code, times_applied, last_applied_at, created_from, created_at
FROM payee_rules
WHERE (match_mode = 'exact' AND matcher = $1)
	OR (match_mode = 'contains' AND $1 LIKE '%' || matcher || '%')
ORDER BY times_applied DESC, last_applied_at DESC NULLS LAST, id`, normalized)
	if err != nil {
		return nil, fmt.Errorf("banking: match payee rules for %q: %w", normalized, err)
	}
	defer rows.Close()
	rules, err := pgx.CollectRows(rows, scanPayeeRule)
	if err != nil {
		return nil, fmt.Errorf("banking: collect payee rule matches for %q: %w", normalized, err)
	}
	return rules, nil
}

func (Store) PayeeRuleSuggestionRecorded(ctx context.Context, tx db.Tx, txnID TransactionID, accountCode ledger.AccountCode) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
	SELECT 1
	FROM suggestions
	WHERE txn_id = $1
		AND kind = 'payee-rule'
		AND target = $2
)`,
		int64(txnID),
		string(accountCode),
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("banking: check payee rule suggestion for transaction %d: %w", txnID, err)
	}
	return exists, nil
}

func (Store) LearnPayeeRuleFromRecode(ctx context.Context, tx db.Tx, txn Transaction, accountCode ledger.AccountCode) (PayeeRule, error) {
	matcher := NormalizePayee(txn.Payee)
	code := ledger.AccountCode(strings.TrimSpace(string(accountCode)))
	if matcher == "" {
		return PayeeRule{}, fmt.Errorf("banking: recode payee is required: %w", ErrInvalidPayeeRule)
	}
	if code == "" {
		return PayeeRule{}, fmt.Errorf("banking: recode account code is required: %w", ErrInvalidPayeeRule)
	}
	return scanPayeeRuleRow(tx.QueryRow(ctx, `
INSERT INTO payee_rules (matcher, match_mode, account_code, times_applied, last_applied_at, created_from)
VALUES ($1, 'exact', $2, 1, now(), 'recode')
ON CONFLICT (matcher, match_mode, account_code)
DO UPDATE SET times_applied = payee_rules.times_applied + 1,
	last_applied_at = now()
RETURNING id, matcher, match_mode, account_code, times_applied, last_applied_at, created_from, created_at`,
		matcher,
		string(code),
	))
}

func (Store) Feed(ctx context.Context, tx db.Tx, filter FeedFilter) ([]Transaction, error) {
	var (
		fromDate any
		toDate   any
		after    any
		afterID  int64
	)
	if filter.From != nil {
		fromDate = *filter.From
	}
	if filter.To != nil {
		toDate = *filter.To
	}
	if filter.After != nil {
		after = filter.After.Date
		afterID = int64(filter.After.ID)
	}
	rows, err := tx.Query(ctx, transactionSelectSQL()+`
WHERE ($1::bigint = 0 OR account_id = $1)
	AND (NULLIF($2::text, '') IS NULL OR state = NULLIF($2::text, '')::transaction_state)
	AND ($3::date IS NULL OR date >= $3::date)
	AND ($4::date IS NULL OR date <= $4::date)
	AND ($5::date IS NULL OR date < $5::date OR (date = $5::date AND id < $6))
ORDER BY date DESC, id DESC
LIMIT $7`,
		int64(filter.AccountID),
		string(filter.State),
		fromDate,
		toDate,
		after,
		afterID,
		filter.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("banking: transaction feed: %w", err)
	}
	defer rows.Close()
	txns, err := pgx.CollectRows(rows, scanTransaction)
	if err != nil {
		return nil, fmt.Errorf("banking: collect transaction feed: %w", err)
	}
	return txns, nil
}

func (Store) ReviewQueue(ctx context.Context, tx db.Tx) (ReviewQueue, error) {
	rows, err := tx.Query(ctx, `
SELECT `+transactionColumns("t")+`,
	s.id,
	s.txn_id,
	s.kind,
	s.confidence::float8,
	s.target,
	s.explanation,
	s.auto_postable,
	s.created_by,
	s.created_at,
	s.superseded_at
FROM transactions t
JOIN suggestions s ON s.txn_id = t.id
	AND s.superseded_at IS NULL
WHERE t.state = 'suggested'
ORDER BY s.kind, s.confidence DESC, t.date DESC, t.id DESC`)
	if err != nil {
		return ReviewQueue{}, fmt.Errorf("banking: review queue: %w", err)
	}
	defer rows.Close()

	var queue ReviewQueue
	for rows.Next() {
		item, err := scanReviewQueueItem(rows)
		if err != nil {
			return ReviewQueue{}, err
		}
		switch item.Suggestion.Kind {
		case SuggestionKindInvoiceMatch:
			queue.InvoiceMatches = append(queue.InvoiceMatches, item)
		case SuggestionKindDLA:
			queue.DLA = append(queue.DLA, item)
		case SuggestionKindPayeeRule:
			queue.PayeeRules = append(queue.PayeeRules, item)
		default:
			return ReviewQueue{}, fmt.Errorf("banking: review queue suggestion kind %q: %w", item.Suggestion.Kind, ErrInvalidSuggestion)
		}
	}
	if err := rows.Err(); err != nil {
		return ReviewQueue{}, fmt.Errorf("banking: collect review queue: %w", err)
	}
	return queue, nil
}

func (Store) RecentlyReconciled(ctx context.Context, tx db.Tx, accountID AccountID, limit int) ([]ReconciledTransaction, error) {
	rows, err := tx.Query(ctx, `
	SELECT `+transactionColumns("t")+`,
		c.changed_at,
		c.actor
	FROM transaction_state_changes c
	JOIN transactions t ON t.id = c.txn_id
	WHERE c.to_state = 'reconciled'
		AND t.state = 'reconciled'
		AND ($1::bigint = 0 OR t.account_id = $1)
	ORDER BY c.changed_at DESC, c.id DESC
	LIMIT $2`, int64(accountID), limit)
	if err != nil {
		return nil, fmt.Errorf("banking: recently reconciled: %w", err)
	}
	defer rows.Close()

	var items []ReconciledTransaction
	for rows.Next() {
		item, err := scanReconciledTransaction(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("banking: collect recently reconciled: %w", err)
	}
	return items, nil
}

func (Store) UnreconciledCount(ctx context.Context, tx db.Tx, accountID AccountID) (int, error) {
	var count int
	if err := tx.QueryRow(ctx, `
SELECT count(*)::integer
FROM transactions
WHERE account_id = $1
	AND state = 'unreconciled'`, int64(accountID)).Scan(&count); err != nil {
		return 0, fmt.Errorf("banking: unreconciled count for account %d: %w", accountID, err)
	}
	return count, nil
}

func accountSelectSQL() string {
	return `SELECT id, name, provider, currency, ledger_account_code, created_at
FROM bank_accounts
`
}

func scanAccount(row pgx.CollectableRow) (BankAccount, error) {
	return scanAccountRow(row)
}

func transactionSelectSQL() string {
	return "SELECT " + transactionColumns("") + "\nFROM transactions\n"
}

func transactionColumns(alias string) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return prefix + `id,
	` + prefix + `account_id,
	` + prefix + `date,
	` + prefix + `amount,
	` + prefix + `currency,
	` + prefix + `payee,
	` + prefix + `reference,
	` + prefix + `provider_meta,
	` + prefix + `import_batch_id,
	` + prefix + `state,
	` + prefix + `created_at`
}

func suggestionSelectSQL() string {
	return `SELECT id,
	txn_id,
	kind,
	confidence::float8,
	target,
	explanation,
	auto_postable,
	created_by,
	created_at,
	superseded_at
FROM suggestions
`
}

type accountRow interface {
	Scan(dest ...any) error
}

func scanAccountRow(row accountRow) (BankAccount, error) {
	var (
		account    BankAccount
		id         int64
		provider   string
		ledgerCode string
	)
	if err := row.Scan(
		&id,
		&account.Name,
		&provider,
		&account.Currency,
		&ledgerCode,
		&account.CreatedAt,
	); err != nil {
		return BankAccount{}, err
	}
	account.ID = AccountID(id)
	account.Provider = Provider(provider)
	account.LedgerAccountCode = ledger.AccountCode(ledgerCode)
	return account, nil
}

func scanTransaction(row pgx.CollectableRow) (Transaction, error) {
	return scanTransactionRow(row)
}

type transactionRow interface {
	Scan(dest ...any) error
}

func scanTransactionRow(row transactionRow) (Transaction, error) {
	var (
		txn       Transaction
		id        int64
		accountID int64
		amount    int64
		currency  string
		meta      []byte
		batchID   int64
		state     string
	)
	if err := row.Scan(
		&id,
		&accountID,
		&txn.Date,
		&amount,
		&currency,
		&txn.Payee,
		&txn.Reference,
		&meta,
		&batchID,
		&state,
		&txn.CreatedAt,
	); err != nil {
		return Transaction{}, err
	}
	if err := json.Unmarshal(meta, &txn.ProviderMeta); err != nil {
		return Transaction{}, fmt.Errorf("banking: unmarshal transaction provider metadata: %w", err)
	}
	txn.ID = TransactionID(id)
	txn.AccountID = AccountID(accountID)
	txn.Amount = money.Money{Amount: amount, Currency: currency}
	txn.ImportBatchID = ImportBatchID(batchID)
	txn.State = TransactionState(state)
	txn.Date = txn.Date.UTC()
	txn.CreatedAt = txn.CreatedAt.UTC()
	return txn, nil
}

type stateChangeRow interface {
	Scan(dest ...any) error
}

func scanStateChangeRow(row stateChangeRow) (TransactionStateChange, error) {
	var (
		change TransactionStateChange
		id     int64
		txnID  int64
		from   string
		to     string
	)
	if err := row.Scan(
		&id,
		&txnID,
		&from,
		&to,
		&change.ChangedAt,
		&change.Actor,
	); err != nil {
		return TransactionStateChange{}, fmt.Errorf("banking: scan state change: %w", err)
	}
	change.ID = TransactionStateChangeID(id)
	change.TransactionID = TransactionID(txnID)
	change.From = TransactionState(from)
	change.To = TransactionState(to)
	change.ChangedAt = change.ChangedAt.UTC()
	return change, nil
}

func scanSuggestion(row pgx.CollectableRow) (Suggestion, error) {
	return scanSuggestionRow(row)
}

type suggestionRow interface {
	Scan(dest ...any) error
}

func scanSuggestionRow(row suggestionRow) (Suggestion, error) {
	var (
		suggestion   Suggestion
		id           int64
		txnID        int64
		kind         string
		supersededAt sql.NullTime
	)
	if err := row.Scan(
		&id,
		&txnID,
		&kind,
		&suggestion.Confidence,
		&suggestion.Target,
		&suggestion.Explanation,
		&suggestion.AutoPostable,
		&suggestion.CreatedBy,
		&suggestion.CreatedAt,
		&supersededAt,
	); err != nil {
		return Suggestion{}, fmt.Errorf("banking: scan suggestion: %w", err)
	}
	suggestion.ID = SuggestionID(id)
	suggestion.TransactionID = TransactionID(txnID)
	suggestion.Kind = SuggestionKind(kind)
	suggestion.CreatedAt = suggestion.CreatedAt.UTC()
	if supersededAt.Valid {
		value := supersededAt.Time.UTC()
		suggestion.SupersededAt = &value
	}
	return suggestion, nil
}

func scanPayeeRule(row pgx.CollectableRow) (PayeeRule, error) {
	return scanPayeeRuleRow(row)
}

type payeeRuleRow interface {
	Scan(dest ...any) error
}

func scanPayeeRuleRow(row payeeRuleRow) (PayeeRule, error) {
	var (
		rule          PayeeRule
		id            int64
		matchMode     string
		accountCode   string
		lastAppliedAt sql.NullTime
		createdFrom   string
	)
	if err := row.Scan(
		&id,
		&rule.Matcher,
		&matchMode,
		&accountCode,
		&rule.TimesApplied,
		&lastAppliedAt,
		&createdFrom,
		&rule.CreatedAt,
	); err != nil {
		return PayeeRule{}, fmt.Errorf("banking: scan payee rule: %w", err)
	}
	rule.ID = PayeeRuleID(id)
	rule.MatchMode = PayeeRuleMatchMode(matchMode)
	rule.AccountCode = ledger.AccountCode(accountCode)
	rule.CreatedFrom = PayeeRuleCreatedFrom(createdFrom)
	rule.CreatedAt = rule.CreatedAt.UTC()
	if lastAppliedAt.Valid {
		value := lastAppliedAt.Time.UTC()
		rule.LastAppliedAt = &value
	}
	return rule, nil
}

func scanReviewQueueItem(row transactionRow) (ReviewQueueItem, error) {
	var (
		item          ReviewQueueItem
		txnID         int64
		accountID     int64
		amount        int64
		currency      string
		meta          []byte
		batchID       int64
		state         string
		suggestionID  int64
		suggestionTxn int64
		kind          string
		supersededAt  sql.NullTime
	)
	if err := row.Scan(
		&txnID,
		&accountID,
		&item.Transaction.Date,
		&amount,
		&currency,
		&item.Transaction.Payee,
		&item.Transaction.Reference,
		&meta,
		&batchID,
		&state,
		&item.Transaction.CreatedAt,
		&suggestionID,
		&suggestionTxn,
		&kind,
		&item.Suggestion.Confidence,
		&item.Suggestion.Target,
		&item.Suggestion.Explanation,
		&item.Suggestion.AutoPostable,
		&item.Suggestion.CreatedBy,
		&item.Suggestion.CreatedAt,
		&supersededAt,
	); err != nil {
		return ReviewQueueItem{}, fmt.Errorf("banking: scan review queue item: %w", err)
	}
	if err := json.Unmarshal(meta, &item.Transaction.ProviderMeta); err != nil {
		return ReviewQueueItem{}, fmt.Errorf("banking: unmarshal review queue transaction provider metadata: %w", err)
	}
	item.Transaction.ID = TransactionID(txnID)
	item.Transaction.AccountID = AccountID(accountID)
	item.Transaction.Amount = money.Money{Amount: amount, Currency: currency}
	item.Transaction.ImportBatchID = ImportBatchID(batchID)
	item.Transaction.State = TransactionState(state)
	item.Transaction.Date = item.Transaction.Date.UTC()
	item.Transaction.CreatedAt = item.Transaction.CreatedAt.UTC()
	item.Suggestion.ID = SuggestionID(suggestionID)
	item.Suggestion.TransactionID = TransactionID(suggestionTxn)
	item.Suggestion.Kind = SuggestionKind(kind)
	item.Suggestion.CreatedAt = item.Suggestion.CreatedAt.UTC()
	if supersededAt.Valid {
		value := supersededAt.Time.UTC()
		item.Suggestion.SupersededAt = &value
	}
	return item, nil
}

func scanReconciledTransaction(row transactionRow) (ReconciledTransaction, error) {
	var (
		item      ReconciledTransaction
		txnID     int64
		accountID int64
		amount    int64
		currency  string
		meta      []byte
		batchID   int64
		state     string
	)
	if err := row.Scan(
		&txnID,
		&accountID,
		&item.Transaction.Date,
		&amount,
		&currency,
		&item.Transaction.Payee,
		&item.Transaction.Reference,
		&meta,
		&batchID,
		&state,
		&item.Transaction.CreatedAt,
		&item.ReconciledAt,
		&item.Actor,
	); err != nil {
		return ReconciledTransaction{}, fmt.Errorf("banking: scan reconciled transaction: %w", err)
	}
	if err := json.Unmarshal(meta, &item.Transaction.ProviderMeta); err != nil {
		return ReconciledTransaction{}, fmt.Errorf("banking: unmarshal reconciled transaction provider metadata: %w", err)
	}
	item.Transaction.ID = TransactionID(txnID)
	item.Transaction.AccountID = AccountID(accountID)
	item.Transaction.Amount = money.Money{Amount: amount, Currency: currency}
	item.Transaction.ImportBatchID = ImportBatchID(batchID)
	item.Transaction.State = TransactionState(state)
	item.Transaction.Date = item.Transaction.Date.UTC()
	item.Transaction.CreatedAt = item.Transaction.CreatedAt.UTC()
	item.ReconciledAt = item.ReconciledAt.UTC()
	return item, nil
}

func marshalProviderMeta(meta map[string]string) ([]byte, error) {
	if meta == nil {
		meta = map[string]string{}
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("banking: marshal provider metadata: %w", err)
	}
	return data, nil
}

func transactionIDsToInt64(ids []TransactionID) []int64 {
	intIDs := make([]int64, len(ids))
	for i, id := range ids {
		intIDs[i] = int64(id)
	}
	return intIDs
}

func validTransactionState(state TransactionState) bool {
	switch state {
	case TransactionStateUnreconciled,
		TransactionStateSuggested,
		TransactionStateReconciled,
		TransactionStateExcluded:
		return true
	default:
		return false
	}
}

func legalTransactionStateTransition(from TransactionState, to TransactionState) bool {
	switch from {
	case TransactionStateUnreconciled:
		return to == TransactionStateSuggested || to == TransactionStateExcluded
	case TransactionStateSuggested:
		return to == TransactionStateReconciled || to == TransactionStateExcluded
	case TransactionStateExcluded:
		return to == TransactionStateUnreconciled
	case TransactionStateReconciled:
		return false
	default:
		return false
	}
}

func normalizeSuggestionInput(input SuggestionInput) (SuggestionInput, error) {
	normalized := SuggestionInput{
		TransactionID: input.TransactionID,
		Kind:          input.Kind,
		Confidence:    input.Confidence,
		Target:        strings.TrimSpace(input.Target),
		Explanation:   strings.TrimSpace(input.Explanation),
		AutoPostable:  input.AutoPostable,
		CreatedBy:     strings.TrimSpace(input.CreatedBy),
	}
	if normalized.TransactionID <= 0 {
		return SuggestionInput{}, fmt.Errorf("banking: suggestion transaction id is required: %w", ErrInvalidSuggestion)
	}
	switch normalized.Kind {
	case SuggestionKindInvoiceMatch, SuggestionKindDLA, SuggestionKindPayeeRule:
	default:
		return SuggestionInput{}, fmt.Errorf("banking: suggestion kind %q: %w", input.Kind, ErrInvalidSuggestion)
	}
	if math.IsNaN(normalized.Confidence) || math.IsInf(normalized.Confidence, 0) || normalized.Confidence < 0 || normalized.Confidence > 1 {
		return SuggestionInput{}, fmt.Errorf("banking: suggestion confidence %.3f: %w", normalized.Confidence, ErrInvalidSuggestion)
	}
	if normalized.Target == "" {
		return SuggestionInput{}, fmt.Errorf("banking: suggestion target is required: %w", ErrInvalidSuggestion)
	}
	if normalized.Explanation == "" {
		return SuggestionInput{}, fmt.Errorf("banking: suggestion explanation is required: %w", ErrInvalidSuggestion)
	}
	if normalized.CreatedBy == "" {
		return SuggestionInput{}, fmt.Errorf("banking: suggestion created_by is required: %w", ErrInvalidSuggestion)
	}
	return normalized, nil
}

func normalizePayeeRuleInput(input PayeeRuleInput) (PayeeRuleInput, error) {
	matchMode := input.MatchMode
	if matchMode == "" {
		matchMode = PayeeRuleMatchExact
	}
	createdFrom := input.CreatedFrom
	normalized := PayeeRuleInput{
		Matcher:     NormalizePayee(input.Matcher),
		MatchMode:   matchMode,
		AccountCode: ledger.AccountCode(strings.TrimSpace(string(input.AccountCode))),
		CreatedFrom: createdFrom,
	}
	if normalized.Matcher == "" {
		return PayeeRuleInput{}, fmt.Errorf("banking: payee rule matcher is required: %w", ErrInvalidPayeeRule)
	}
	switch normalized.MatchMode {
	case PayeeRuleMatchExact, PayeeRuleMatchContains:
	default:
		return PayeeRuleInput{}, fmt.Errorf("banking: payee rule match mode %q: %w", input.MatchMode, ErrInvalidPayeeRule)
	}
	if normalized.AccountCode == "" {
		return PayeeRuleInput{}, fmt.Errorf("banking: payee rule account code is required: %w", ErrInvalidPayeeRule)
	}
	switch normalized.CreatedFrom {
	case PayeeRuleCreatedFromRecode, PayeeRuleCreatedFromManual:
	default:
		return PayeeRuleInput{}, fmt.Errorf("banking: payee rule created_from %q: %w", input.CreatedFrom, ErrInvalidPayeeRule)
	}
	return normalized, nil
}
