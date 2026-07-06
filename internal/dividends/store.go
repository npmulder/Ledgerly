package dividends

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

// Store owns dividends persistence. SQL qualifies dividends objects so callers
// can share transactions from other module pools where permissions allow it.
type Store struct{}

// InsertDeclaration appends one immutable dividend declaration. It does not
// post ledger entries, publish events, or render documents.
func (Store) InsertDeclaration(ctx context.Context, tx db.Tx, declaration Declaration) (Declaration, error) {
	normalized, err := normalizeDeclaration(declaration)
	if err != nil {
		return Declaration{}, err
	}

	var stored Declaration
	var amount, perShare int64
	var amountCurrency, perShareCurrency string
	var voucherAsset, minutesAsset *string
	if err := tx.QueryRow(ctx, `
INSERT INTO dividends.declarations (
	id,
	declared_date,
	amount,
	amount_currency,
	per_share_amount,
	per_share_currency,
	shares,
	shareholder_name,
	voucher_asset,
	minutes_asset
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id,
	declared_date,
	amount,
	amount_currency,
	per_share_amount,
	per_share_currency,
	shares,
	shareholder_name,
	voucher_asset,
	minutes_asset,
	created_at`,
		string(normalized.ID),
		normalized.DeclaredDate,
		normalized.Amount.Amount,
		normalized.Amount.Currency,
		normalized.PerShare.Amount,
		normalized.PerShare.Currency,
		normalized.Shares,
		normalized.ShareholderName,
		assetIDString(normalized.VoucherAsset),
		assetIDString(normalized.MinutesAsset),
	).Scan(
		&stored.ID,
		&stored.DeclaredDate,
		&amount,
		&amountCurrency,
		&perShare,
		&perShareCurrency,
		&stored.Shares,
		&stored.ShareholderName,
		&voucherAsset,
		&minutesAsset,
		&stored.CreatedAt,
	); err != nil {
		return Declaration{}, fmt.Errorf("dividends: insert declaration: %w", err)
	}
	stored.Amount = money.Money{Amount: amount, Currency: amountCurrency}
	stored.PerShare = money.Money{Amount: perShare, Currency: perShareCurrency}
	stored.VoucherAsset = identityAssetID(voucherAsset)
	stored.MinutesAsset = identityAssetID(minutesAsset)
	return stored, nil
}

// DeclaredInPeriod sums declarations whose declared date is inside the
// inclusive financial-year window.
func (Store) DeclaredInPeriod(ctx context.Context, tx db.Tx, from, to time.Time) (money.Money, error) {
	fromDate, err := normalizeDate(from)
	if err != nil {
		return money.Money{}, err
	}
	toDate, err := normalizeDate(to)
	if err != nil {
		return money.Money{}, err
	}
	if fromDate.After(toDate) {
		return money.Money{}, fmt.Errorf("dividends: declaration period from %s is after to %s: %w",
			fromDate.Format(time.DateOnly),
			toDate.Format(time.DateOnly),
			ErrInvalidFinancialYear,
		)
	}

	var amount int64
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(sum(amount), 0)::bigint
FROM dividends.declarations
WHERE declared_date >= $1
	AND declared_date <= $2`, fromDate, toDate).Scan(&amount); err != nil {
		return money.Money{}, fmt.Errorf("dividends: declared in period: %w", err)
	}
	return money.Money{Amount: amount, Currency: gbpCurrency}, nil
}

// Declarations returns declaration history newest first.
func (Store) Declarations(ctx context.Context, tx db.Tx) ([]Declaration, error) {
	rows, err := tx.Query(ctx, `
SELECT id,
	declared_date,
	amount,
	amount_currency,
	per_share_amount,
	per_share_currency,
	shares,
	shareholder_name,
	voucher_asset,
	minutes_asset,
	created_at
FROM dividends.declarations
ORDER BY declared_date DESC, created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("dividends: list declarations: %w", err)
	}
	defer rows.Close()

	declarations := []Declaration{}
	for rows.Next() {
		declaration, err := scanDeclaration(rows)
		if err != nil {
			return nil, err
		}
		declarations = append(declarations, declaration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dividends: collect declarations: %w", err)
	}
	return declarations, nil
}

type declarationScanner interface {
	Scan(dest ...any) error
}

func scanDeclaration(row declarationScanner) (Declaration, error) {
	var declaration Declaration
	var amount, perShare int64
	var amountCurrency, perShareCurrency string
	var voucherAsset, minutesAsset *string
	if err := row.Scan(
		&declaration.ID,
		&declaration.DeclaredDate,
		&amount,
		&amountCurrency,
		&perShare,
		&perShareCurrency,
		&declaration.Shares,
		&declaration.ShareholderName,
		&voucherAsset,
		&minutesAsset,
		&declaration.CreatedAt,
	); err != nil {
		return Declaration{}, fmt.Errorf("dividends: scan declaration: %w", err)
	}
	declaration.Amount = money.Money{Amount: amount, Currency: amountCurrency}
	declaration.PerShare = money.Money{Amount: perShare, Currency: perShareCurrency}
	declaration.VoucherAsset = identityAssetID(voucherAsset)
	declaration.MinutesAsset = identityAssetID(minutesAsset)
	return declaration, nil
}

func normalizeDeclaration(declaration Declaration) (Declaration, error) {
	id := strings.TrimSpace(string(declaration.ID))
	if id == "" {
		return Declaration{}, fmt.Errorf("dividends: declaration id is required: %w", ErrInvalidDeclaration)
	}
	declaredDate, err := normalizeDate(declaration.DeclaredDate)
	if err != nil {
		return Declaration{}, err
	}
	amount, err := normalizePositiveGBP("amount", declaration.Amount)
	if err != nil {
		return Declaration{}, err
	}
	perShare, err := normalizePositiveGBP("per share", declaration.PerShare)
	if err != nil {
		return Declaration{}, err
	}
	if declaration.Shares <= 0 {
		return Declaration{}, fmt.Errorf("dividends: shares must be positive: %w", ErrInvalidDeclaration)
	}
	shareholderName := strings.TrimSpace(declaration.ShareholderName)
	if shareholderName == "" {
		return Declaration{}, fmt.Errorf("dividends: shareholder name is required: %w", ErrInvalidDeclaration)
	}
	voucherAsset, err := normalizeAssetID("voucher asset", declaration.VoucherAsset)
	if err != nil {
		return Declaration{}, err
	}
	minutesAsset, err := normalizeAssetID("minutes asset", declaration.MinutesAsset)
	if err != nil {
		return Declaration{}, err
	}

	return Declaration{
		ID:              DeclarationID(id),
		DeclaredDate:    declaredDate,
		Amount:          amount,
		PerShare:        perShare,
		Shares:          declaration.Shares,
		ShareholderName: shareholderName,
		VoucherAsset:    voucherAsset,
		MinutesAsset:    minutesAsset,
	}, nil
}

func normalizePositiveGBP(label string, amount money.Money) (money.Money, error) {
	currency := strings.ToUpper(strings.TrimSpace(amount.Currency))
	if currency != gbpCurrency {
		return money.Money{}, fmt.Errorf("dividends: %s currency %q must be GBP: %w", label, amount.Currency, ErrInvalidDeclaration)
	}
	if amount.Amount <= 0 {
		return money.Money{}, fmt.Errorf("dividends: %s must be positive: %w", label, ErrInvalidDeclaration)
	}
	return money.Money{Amount: amount.Amount, Currency: gbpCurrency}, nil
}

func normalizeAssetID(label string, assetID *identity.AssetID) (*identity.AssetID, error) {
	if assetID == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(string(*assetID))
	if trimmed == "" {
		return nil, fmt.Errorf("dividends: %s is blank: %w", label, ErrInvalidDeclaration)
	}
	normalized := identity.AssetID(trimmed)
	return &normalized, nil
}

func assetIDString(assetID *identity.AssetID) *string {
	if assetID == nil {
		return nil
	}
	value := string(*assetID)
	return &value
}

func identityAssetID(value *string) *identity.AssetID {
	if value == nil {
		return nil
	}
	assetID := identity.AssetID(*value)
	return &assetID
}
