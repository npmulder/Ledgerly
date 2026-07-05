package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

// Store owns ledger persistence. All SQL relies on the module search_path.
type Store struct{}

// EnsureAccount creates spec.Code or returns the existing account code when it is consistent.
func (Store) EnsureAccount(ctx context.Context, tx db.Tx, spec AccountSpec) (AccountCode, error) {
	normalized, err := normalizeAccountSpec(spec)
	if err != nil {
		return "", err
	}

	const insertQuery = `
INSERT INTO accounts (code, name, type, currency)
VALUES ($1, $2, $3, $4)
ON CONFLICT (code) DO NOTHING
RETURNING code`

	var createdCode string
	err = tx.QueryRow(
		ctx,
		insertQuery,
		string(normalized.Code),
		normalized.Name,
		string(normalized.Type),
		nullableText(normalized.Currency),
	).Scan(&createdCode)
	if err == nil {
		return AccountCode(createdCode), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("ledger: insert account %s: %w", normalized.Code, err)
	}

	existing, err := accountByCode(ctx, tx, normalized.Code)
	if err != nil {
		return "", err
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
FROM accounts
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

func accountByCode(ctx context.Context, tx db.Tx, code AccountCode) (Account, error) {
	const query = `
SELECT id, code, name, type, currency, created_at
FROM accounts
WHERE code = $1`

	account, err := scanSingleAccount(tx.QueryRow(ctx, query, string(code)))
	if err != nil {
		return Account{}, fmt.Errorf("ledger: read account %s: %w", code, err)
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
