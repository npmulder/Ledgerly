package banking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func (Store) InsertTransaction(ctx context.Context, tx db.Tx, txn newTransaction) (bool, error) {
	meta, err := marshalProviderMeta(txn.ProviderMeta)
	if err != nil {
		return false, err
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
		return true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("banking: insert transaction: %w", err)
}

func accountSelectSQL() string {
	return `SELECT id, name, provider, currency, ledger_account_code, created_at
FROM bank_accounts
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
