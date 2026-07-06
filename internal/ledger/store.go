package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

// Store owns ledger persistence. SQL qualifies ledger objects so callers can
// share transactions from other module pools.
type Store struct{}

// EnsureAccount creates spec.Code or returns the existing account code when it is consistent.
func (Store) EnsureAccount(ctx context.Context, tx db.Tx, spec AccountSpec) (AccountCode, error) {
	normalized, err := normalizeAccountSpec(spec)
	if err != nil {
		return "", err
	}

	existing, err := ensureAccount(ctx, tx, normalized)
	if err != nil {
		return "", err
	}
	if existing.Name != normalized.Name {
		return "", &AccountConflictError{
			Code:      normalized.Code,
			Field:     "name",
			Existing:  existing.Name,
			Requested: normalized.Name,
		}
	}
	if existing.Type != normalized.Type {
		return "", &AccountConflictError{
			Code:      normalized.Code,
			Field:     "type",
			Existing:  string(existing.Type),
			Requested: string(normalized.Type),
		}
	}
	if currencyLabel(existing.Currency) != currencyLabel(normalized.Currency) {
		return "", &AccountConflictError{
			Code:      normalized.Code,
			Field:     "currency",
			Existing:  currencyLabel(existing.Currency),
			Requested: currencyLabel(normalized.Currency),
		}
	}

	return existing.Code, nil
}

// ListAccounts lists the chart of accounts ordered by code.
func (Store) ListAccounts(ctx context.Context, tx db.Tx) ([]Account, error) {
	rows, err := tx.Query(ctx, `
SELECT id, code, name, type, currency, created_at
FROM ledger.accounts
ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("ledger: list accounts: %w", err)
	}
	defer rows.Close()

	accounts, err := pgx.CollectRows(rows, scanAccount)
	if err != nil {
		return nil, fmt.Errorf("ledger: collect accounts: %w", err)
	}
	return accounts, nil
}

// PostingAccountCurrencies loads fixed account currencies and verifies every posting account exists.
func (Store) PostingAccountCurrencies(ctx context.Context, tx db.Tx, codes []AccountCode) (map[AccountCode]*string, error) {
	unique := make([]string, 0, len(codes))
	seen := make(map[AccountCode]struct{}, len(codes))
	for _, code := range codes {
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		unique = append(unique, string(code))
	}

	rows, err := tx.Query(ctx, `
SELECT code, currency
FROM ledger.accounts
WHERE code = ANY($1)`, unique)
	if err != nil {
		return nil, fmt.Errorf("ledger: check posting accounts: %w", err)
	}
	defer rows.Close()

	found := make(map[AccountCode]*string, len(unique))
	for rows.Next() {
		var (
			code     string
			currency pgtype.Text
		)
		if err := rows.Scan(&code, &currency); err != nil {
			return nil, fmt.Errorf("ledger: scan posting account: %w", err)
		}
		if currency.Valid {
			value := currency.String
			found[AccountCode(code)] = &value
		} else {
			found[AccountCode(code)] = nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ledger: collect posting accounts: %w", err)
	}

	for _, code := range codes {
		if _, ok := found[code]; !ok {
			return nil, &AccountNotFoundError{Code: code}
		}
	}
	return found, nil
}

// InsertJournalEntry appends entry and all postings. It never updates existing rows.
func (Store) InsertJournalEntry(ctx context.Context, tx db.Tx, entry NewJournalEntry, reversalOf *EntryID) (EntryID, error) {
	var reversal any
	if reversalOf != nil {
		reversal = int64(*reversalOf)
	}

	var id int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ledger.journal_entries (date, description, source_module, source_ref, reversal_of)
VALUES ($1, $2, $3, $4, $5)
RETURNING id`,
		entry.Date,
		entry.Description,
		entry.SourceModule,
		entry.SourceRef,
		reversal,
	).Scan(&id); err != nil {
		if reversalOf != nil && isReversalUniqueViolation(err) {
			return 0, fmt.Errorf("ledger: entry %d already has a reversal: %w", *reversalOf, ErrEntryAlreadyReversed)
		}
		return 0, fmt.Errorf("ledger: insert journal entry: %w", err)
	}

	entryID := EntryID(id)
	for i, posting := range entry.Postings {
		if _, err := tx.Exec(ctx, `
INSERT INTO ledger.postings (entry_id, account_code, amount, currency, amount_gbp)
VALUES ($1, $2, $3, $4, $5)`,
			int64(entryID),
			string(posting.AccountCode),
			posting.Amount.Amount,
			posting.Amount.Currency,
			posting.AmountGBP.Amount,
		); err != nil {
			return 0, fmt.Errorf("ledger: insert posting %d: %w", i, err)
		}
	}

	return entryID, nil
}

// JournalEntry loads an entry with postings for reversal construction.
func (Store) JournalEntry(ctx context.Context, tx db.Tx, id EntryID) (JournalEntry, error) {
	var (
		entry      JournalEntry
		reversalOf pgtype.Int8
	)
	if err := tx.QueryRow(ctx, `
SELECT id, date, description, source_module, source_ref, reversal_of, created_at
FROM ledger.journal_entries
WHERE id = $1`, int64(id)).
		Scan(
			&entry.ID,
			&entry.Date,
			&entry.Description,
			&entry.SourceModule,
			&entry.SourceRef,
			&reversalOf,
			&entry.CreatedAt,
		); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return JournalEntry{}, &EntryNotFoundError{ID: id}
		}
		return JournalEntry{}, fmt.Errorf("ledger: load journal entry %d: %w", id, err)
	}
	if reversalOf.Valid {
		reversedID := EntryID(reversalOf.Int64)
		entry.ReversalOf = &reversedID
	}

	postings, err := loadPostings(ctx, tx, id)
	if err != nil {
		return JournalEntry{}, err
	}
	entry.Postings = postings
	return entry, nil
}

// HasReversal reports whether id already has a reversing entry.
func (Store) HasReversal(ctx context.Context, tx db.Tx, id EntryID) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
	SELECT 1
	FROM ledger.journal_entries
	WHERE reversal_of = $1
)`, int64(id)).Scan(&exists); err != nil {
		return false, fmt.Errorf("ledger: check reversal for entry %d: %w", id, err)
	}
	return exists, nil
}

// CheckEntryInvariant performs the cheap per-entry balance check after insert.
func (Store) CheckEntryInvariant(ctx context.Context, tx db.Tx, id EntryID) error {
	var (
		postingCount int
		gbpTotal     int64
	)
	if err := tx.QueryRow(ctx, `
SELECT count(*), COALESCE(sum(amount_gbp), 0)::bigint
FROM ledger.postings
WHERE entry_id = $1`, int64(id)).Scan(&postingCount, &gbpTotal); err != nil {
		return fmt.Errorf("ledger: check GBP invariant for entry %d: %w", id, err)
	}
	if postingCount < 2 {
		return fmt.Errorf("ledger: entry %d has %d postings after insert: %w", id, postingCount, ErrInvariantViolation)
	}
	if gbpTotal != 0 {
		return fmt.Errorf("ledger: entry %d stored GBP total is %d: %w", id, gbpTotal, ErrInvariantViolation)
	}

	rows, err := tx.Query(ctx, `
SELECT currency, sum(amount)::bigint
FROM ledger.postings
WHERE entry_id = $1
GROUP BY currency
HAVING sum(amount) <> 0`, int64(id))
	if err != nil {
		return fmt.Errorf("ledger: check native invariant for entry %d: %w", id, err)
	}
	defer rows.Close()

	if rows.Next() {
		var (
			currency string
			total    int64
		)
		if err := rows.Scan(&currency, &total); err != nil {
			return fmt.Errorf("ledger: scan native invariant for entry %d: %w", id, err)
		}
		return fmt.Errorf("ledger: entry %d stored %s total is %d: %w", id, currency, total, ErrInvariantViolation)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("ledger: collect native invariant for entry %d: %w", id, err)
	}
	return nil
}

type ensuredAccount struct {
	Code     AccountCode
	Name     string
	Type     AccountType
	Currency *string
}

func ensureAccount(ctx context.Context, tx db.Tx, spec AccountSpec) (ensuredAccount, error) {
	const query = `
SELECT account_code, account_name, account_type, account_currency
FROM ledger.ensure_account($1, $2, $3, $4)`

	var (
		code      string
		name      string
		typeValue string
		currency  pgtype.Text
	)
	if err := tx.QueryRow(
		ctx,
		query,
		string(spec.Code),
		spec.Name,
		string(spec.Type),
		nullableText(spec.Currency),
	).Scan(&code, &name, &typeValue, &currency); err != nil {
		return ensuredAccount{}, fmt.Errorf("ledger: ensure account %s: %w", spec.Code, err)
	}

	account := ensuredAccount{
		Code: AccountCode(code),
		Name: name,
		Type: AccountType(typeValue),
	}
	if currency.Valid {
		value := currency.String
		account.Currency = &value
	}
	return account, nil
}

func normalizeAccountSpec(spec AccountSpec) (AccountSpec, error) {
	code := strings.ToLower(strings.TrimSpace(string(spec.Code)))
	name := strings.TrimSpace(spec.Name)
	accountType := AccountType(strings.ToLower(strings.TrimSpace(string(spec.Type))))
	currency, err := normalizeCurrency(spec.Currency)
	if err != nil {
		return AccountSpec{}, err
	}

	if code == "" {
		return AccountSpec{}, fmt.Errorf("account code is required: %w", ErrInvalidAccountSpec)
	}
	if name == "" {
		return AccountSpec{}, fmt.Errorf("account name is required: %w", ErrInvalidAccountSpec)
	}
	if !validAccountType(accountType) {
		return AccountSpec{}, fmt.Errorf("account type %q is invalid: %w", accountType, ErrInvalidAccountSpec)
	}

	return AccountSpec{
		Code:     AccountCode(code),
		Name:     name,
		Type:     accountType,
		Currency: currency,
	}, nil
}

func validAccountType(accountType AccountType) bool {
	switch accountType {
	case AccountTypeAsset,
		AccountTypeLiability,
		AccountTypeEquity,
		AccountTypeIncome,
		AccountTypeExpense:
		return true
	default:
		return false
	}
}

func normalizeCurrency(currency *string) (*string, error) {
	if currency == nil {
		return nil, nil
	}

	normalized := strings.ToUpper(strings.TrimSpace(*currency))
	if normalized == "" {
		return nil, nil
	}
	if len(normalized) != 3 {
		return nil, fmt.Errorf("account currency %q is invalid: %w", normalized, ErrInvalidAccountSpec)
	}
	for _, char := range normalized {
		if !unicode.IsUpper(char) || !unicode.IsLetter(char) {
			return nil, fmt.Errorf("account currency %q is invalid: %w", normalized, ErrInvalidAccountSpec)
		}
	}
	return &normalized, nil
}

func nullableText(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func currencyLabel(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func scanAccount(row pgx.CollectableRow) (Account, error) {
	return scanSingleAccount(row)
}

type accountRow interface {
	Scan(dest ...any) error
}

func scanSingleAccount(row accountRow) (Account, error) {
	var (
		account   Account
		code      string
		typeValue string
		currency  pgtype.Text
	)
	if err := row.Scan(&account.ID, &code, &account.Name, &typeValue, &currency, &account.CreatedAt); err != nil {
		return Account{}, err
	}

	account.Code = AccountCode(code)
	account.Type = AccountType(typeValue)
	if currency.Valid {
		value := currency.String
		account.Currency = &value
	}
	return account, nil
}

func loadPostings(ctx context.Context, tx db.Tx, entryID EntryID) ([]Posting, error) {
	rows, err := tx.Query(ctx, `
SELECT account_code, amount, currency, amount_gbp
FROM ledger.postings
WHERE entry_id = $1
ORDER BY id`, int64(entryID))
	if err != nil {
		return nil, fmt.Errorf("ledger: load postings for entry %d: %w", entryID, err)
	}
	defer rows.Close()

	postings := []Posting{}
	for rows.Next() {
		var (
			accountCode string
			amount      int64
			currency    string
			amountGBP   int64
		)
		if err := rows.Scan(&accountCode, &amount, &currency, &amountGBP); err != nil {
			return nil, fmt.Errorf("ledger: scan posting for entry %d: %w", entryID, err)
		}
		postings = append(postings, Posting{
			AccountCode: AccountCode(accountCode),
			Amount: money.Money{
				Amount:   amount,
				Currency: currency,
			},
			AmountGBP: money.Money{
				Amount:   amountGBP,
				Currency: "GBP",
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ledger: collect postings for entry %d: %w", entryID, err)
	}
	return postings, nil
}

func isReversalUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "journal_entries_reversal_of_unique_idx"
}
