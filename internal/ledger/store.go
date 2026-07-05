package ledger

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

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

type ensuredAccount struct {
	Code     AccountCode
	Name     string
	Type     AccountType
	Currency *string
}

func ensureAccount(ctx context.Context, tx db.Tx, spec AccountSpec) (ensuredAccount, error) {
	const query = `
SELECT code, name, account_type, currency
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
