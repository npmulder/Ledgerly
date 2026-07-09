package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
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
FROM ledger.accounts_list()`)
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

// CreateAccount inserts a new chart account and fails if the code already exists.
func (Store) CreateAccount(ctx context.Context, tx db.Tx, spec AccountSpec) (Account, error) {
	normalized, err := normalizeAccountSpec(spec)
	if err != nil {
		return Account{}, err
	}

	account, err := scanSingleAccount(tx.QueryRow(ctx, `
INSERT INTO ledger.accounts (code, name, type, currency)
VALUES ($1, $2, $3::ledger.account_type, $4)
RETURNING id, code, name, type::text, currency, created_at`,
		string(normalized.Code),
		normalized.Name,
		string(normalized.Type),
		nullableText(normalized.Currency),
	))
	if err != nil {
		if isAccountCodeUniqueViolation(err) {
			return Account{}, fmt.Errorf("ledger: account %s already exists: %w", normalized.Code, ErrAccountAlreadyExists)
		}
		return Account{}, fmt.Errorf("ledger: create account %s: %w", normalized.Code, err)
	}
	return account, nil
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
SELECT account_code, account_currency
FROM ledger.posting_account_currencies($1)`, unique)
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

	accountCodes := make([]string, len(entry.Postings))
	amounts := make([]int64, len(entry.Postings))
	currencies := make([]string, len(entry.Postings))
	amountGBPs := make([]int64, len(entry.Postings))
	for i, posting := range entry.Postings {
		accountCodes[i] = string(posting.AccountCode)
		amounts[i] = posting.Amount.Amount
		currencies[i] = posting.Amount.Currency
		amountGBPs[i] = posting.AmountGBP.Amount
	}

	var id int64
	if err := tx.QueryRow(ctx, `
SELECT ledger.insert_journal_entry($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		entry.Date,
		entry.Description,
		entry.SourceModule,
		entry.SourceRef,
		reversal,
		accountCodes,
		amounts,
		currencies,
		amountGBPs,
	).Scan(&id); err != nil {
		if reversalOf != nil && isReversalUniqueViolation(err) {
			return 0, fmt.Errorf("ledger: entry %d already has a reversal: %w", *reversalOf, ErrEntryAlreadyReversed)
		}
		return 0, fmt.Errorf("ledger: insert journal entry: %w", err)
	}

	return EntryID(id), nil
}

// JournalEntry loads an entry with postings for reversal construction.
func (Store) JournalEntry(ctx context.Context, tx db.Tx, id EntryID) (JournalEntry, error) {
	var (
		entry      JournalEntry
		reversalOf pgtype.Int8
	)
	if err := tx.QueryRow(ctx, `
SELECT id, entry_date, description, source_module, source_ref, reversal_of, created_at
FROM ledger.journal_entry($1)`, int64(id)).
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

// JournalEntryBySource loads the latest original entry with a source module/ref.
func (Store) JournalEntryBySource(ctx context.Context, tx db.Tx, sourceModule string, sourceRef string) (JournalEntry, error) {
	var (
		entry      JournalEntry
		reversalOf pgtype.Int8
	)
	if err := tx.QueryRow(ctx, `
SELECT id, entry_date, description, source_module, source_ref, reversal_of, created_at
FROM ledger.journal_entry_by_source($1, $2)`,
		sourceModule,
		sourceRef,
	).Scan(
		&entry.ID,
		&entry.Date,
		&entry.Description,
		&entry.SourceModule,
		&entry.SourceRef,
		&reversalOf,
		&entry.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return JournalEntry{}, fmt.Errorf("ledger: source %s/%s: %w", sourceModule, sourceRef, ErrEntryNotFound)
		}
		return JournalEntry{}, fmt.Errorf("ledger: load journal entry source %s/%s: %w", sourceModule, sourceRef, err)
	}
	if reversalOf.Valid {
		reversedID := EntryID(reversalOf.Int64)
		entry.ReversalOf = &reversedID
	}

	postings, err := loadPostings(ctx, tx, entry.ID)
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
SELECT ledger.has_reversal($1)`, int64(id)).Scan(&exists); err != nil {
		return false, fmt.Errorf("ledger: check reversal for entry %d: %w", id, err)
	}
	return exists, nil
}

// CheckEntryInvariant performs the cheap per-entry balance check after insert.
func (Store) CheckEntryInvariant(ctx context.Context, tx db.Tx, id EntryID) error {
	if _, err := tx.Exec(ctx, `
SELECT ledger.check_entry_invariant($1)`, int64(id)); err != nil {
		if isCheckViolation(err) {
			return fmt.Errorf("ledger: check entry invariant for entry %d: %w", id, errors.Join(err, ErrInvariantViolation))
		}
		return fmt.Errorf("ledger: check entry invariant for entry %d: %w", id, err)
	}
	return nil
}

// AccountBalance sums postings for code on or before asOf.
func (Store) AccountBalance(ctx context.Context, tx db.Tx, code AccountCode, asOf time.Time) (AccountBalance, error) {
	account, err := loadAccount(ctx, tx, code)
	if err != nil {
		return AccountBalance{}, err
	}

	rows, err := tx.Query(ctx, `
SELECT currency, amount, amount_gbp
FROM ledger.account_balance_rows($1, $2)`, string(code), asOf)
	if err != nil {
		return AccountBalance{}, fmt.Errorf("ledger: account balance %s: %w", code, err)
	}
	defer rows.Close()

	balance := AccountBalance{
		AccountCode: account.Code,
		AccountName: account.Name,
		AccountType: account.Type,
		AmountGBP: money.Money{
			Currency: "GBP",
		},
	}
	for rows.Next() {
		var (
			currency  string
			amount    int64
			amountGBP int64
		)
		if err := rows.Scan(&currency, &amount, &amountGBP); err != nil {
			return AccountBalance{}, fmt.Errorf("ledger: scan account balance %s: %w", code, err)
		}
		balance.Native = append(balance.Native, money.Money{Amount: amount, Currency: currency})
		totalGBP, err := addMinorUnits(balance.AmountGBP.Amount, amountGBP)
		if err != nil {
			return AccountBalance{}, fmt.Errorf("ledger: account balance %s GBP overflow: %w", code, ErrInvalidMoney)
		}
		balance.AmountGBP.Amount = totalGBP
	}
	if err := rows.Err(); err != nil {
		return AccountBalance{}, fmt.Errorf("ledger: collect account balance %s: %w", code, err)
	}
	if len(balance.Native) == 0 && account.Currency != nil {
		balance.Native = append(balance.Native, money.Money{Currency: *account.Currency})
	}
	return balance, nil
}

// BalancesByType sums account types using P&L semantics for income/expense and
// balance-sheet semantics for asset/liability/equity.
func (Store) BalancesByType(ctx context.Context, tx db.Tx, from time.Time, to time.Time) ([]AccountBalance, error) {
	balances := initialTypeBalances()
	byType := make(map[AccountType]*AccountBalance, len(balances))
	for i := range balances {
		byType[balances[i].AccountType] = &balances[i]
	}

	rows, err := tx.Query(ctx, `
SELECT account_type, currency, amount, amount_gbp
FROM ledger.balances_by_type_rows($1, $2)`, from, to)
	if err != nil {
		return nil, fmt.Errorf("ledger: balances by type: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			typeValue string
			currency  pgtype.Text
			amount    int64
			amountGBP int64
		)
		if err := rows.Scan(&typeValue, &currency, &amount, &amountGBP); err != nil {
			return nil, fmt.Errorf("ledger: scan balances by type: %w", err)
		}
		accountType := AccountType(typeValue)
		balance, ok := byType[accountType]
		if !ok {
			continue
		}
		if currency.Valid {
			balance.Native = append(balance.Native, money.Money{Amount: amount, Currency: currency.String})
		}
		totalGBP, err := addMinorUnits(balance.AmountGBP.Amount, amountGBP)
		if err != nil {
			return nil, fmt.Errorf("ledger: balances by type %s GBP overflow: %w", accountType, ErrInvalidMoney)
		}
		balance.AmountGBP.Amount = totalGBP
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ledger: collect balances by type: %w", err)
	}
	return balances, nil
}

// Entries loads journal entries matching filter and attaches all postings for
// each matched entry.
func (Store) Entries(ctx context.Context, tx db.Tx, filter EntryFilter) ([]JournalEntry, error) {
	var from any
	if filter.From != nil {
		from = *filter.From
	}
	var to any
	if filter.To != nil {
		to = *filter.To
	}
	var sourceModule any
	if filter.SourceModule != "" {
		sourceModule = filter.SourceModule
	}
	var accountCode any
	if filter.AccountCode != "" {
		accountCode = string(filter.AccountCode)
	}
	var afterDate any
	var afterID any
	if filter.After != nil {
		afterDate = filter.After.Date
		afterID = int64(filter.After.ID)
	}

	rows, err := tx.Query(ctx, `
SELECT id, entry_date, description, source_module, source_ref, reversal_of, created_at
FROM ledger.entries($1, $2, $3, $4, $5, $6, $7)`,
		from,
		to,
		sourceModule,
		accountCode,
		afterDate,
		afterID,
		filter.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("ledger: list entries: %w", err)
	}
	defer rows.Close()

	entries := []JournalEntry{}
	entryIDs := []int64{}
	for rows.Next() {
		entry, err := scanJournalEntryWithoutPostings(rows)
		if err != nil {
			return nil, fmt.Errorf("ledger: scan entry: %w", err)
		}
		entries = append(entries, entry)
		entryIDs = append(entryIDs, int64(entry.ID))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ledger: collect entries: %w", err)
	}
	if len(entries) == 0 {
		return entries, nil
	}

	postingsByEntry, err := loadPostingsForEntries(ctx, tx, entryIDs)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		entries[i].Postings = postingsByEntry[entries[i].ID]
	}
	return entries, nil
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

func loadAccount(ctx context.Context, tx db.Tx, code AccountCode) (Account, error) {
	account, err := scanSingleAccount(tx.QueryRow(ctx, `
SELECT id, code, name, type, currency, created_at
FROM ledger.account_by_code($1)`, string(code)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Account{}, &AccountNotFoundError{Code: code}
		}
		return Account{}, fmt.Errorf("ledger: load account %s: %w", code, err)
	}
	return account, nil
}

func initialTypeBalances() []AccountBalance {
	return []AccountBalance{
		typeBalance(AccountTypeAsset),
		typeBalance(AccountTypeLiability),
		typeBalance(AccountTypeEquity),
		typeBalance(AccountTypeIncome),
		typeBalance(AccountTypeExpense),
	}
}

func typeBalance(accountType AccountType) AccountBalance {
	return AccountBalance{
		AccountName: string(accountType),
		AccountType: accountType,
		AmountGBP:   money.Money{Currency: "GBP"},
	}
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

func scanJournalEntryWithoutPostings(row accountRow) (JournalEntry, error) {
	var (
		entry      JournalEntry
		reversalOf pgtype.Int8
	)
	if err := row.Scan(
		&entry.ID,
		&entry.Date,
		&entry.Description,
		&entry.SourceModule,
		&entry.SourceRef,
		&reversalOf,
		&entry.CreatedAt,
	); err != nil {
		return JournalEntry{}, err
	}
	if reversalOf.Valid {
		reversedID := EntryID(reversalOf.Int64)
		entry.ReversalOf = &reversedID
	}
	return entry, nil
}

func loadPostings(ctx context.Context, tx db.Tx, entryID EntryID) ([]Posting, error) {
	rows, err := tx.Query(ctx, `
SELECT account_code, amount, currency, amount_gbp
FROM ledger.entry_postings($1)`, int64(entryID))
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

func loadPostingsForEntries(ctx context.Context, tx db.Tx, entryIDs []int64) (map[EntryID][]Posting, error) {
	rows, err := tx.Query(ctx, `
SELECT entry_id, account_code, amount, currency, amount_gbp
FROM ledger.entry_postings_for_entries($1)`, entryIDs)
	if err != nil {
		return nil, fmt.Errorf("ledger: load postings for entries: %w", err)
	}
	defer rows.Close()

	postingsByEntry := make(map[EntryID][]Posting, len(entryIDs))
	for rows.Next() {
		var (
			entryID     int64
			accountCode string
			amount      int64
			currency    string
			amountGBP   int64
		)
		if err := rows.Scan(&entryID, &accountCode, &amount, &currency, &amountGBP); err != nil {
			return nil, fmt.Errorf("ledger: scan posting for entries: %w", err)
		}
		id := EntryID(entryID)
		postingsByEntry[id] = append(postingsByEntry[id], Posting{
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
		return nil, fmt.Errorf("ledger: collect postings for entries: %w", err)
	}
	return postingsByEntry, nil
}

func isReversalUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "journal_entries_reversal_of_unique_idx"
}

func isAccountCodeUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "accounts_code_key"
}

func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}
