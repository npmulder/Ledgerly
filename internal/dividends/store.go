package dividends

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

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
	companySnapshot, shareholderSnapshot, headroomSnapshot, withholdingSnapshot, err := declarationSnapshotJSON(normalized)
	if err != nil {
		return Declaration{}, err
	}
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
	company_snapshot,
	shareholder_snapshot,
	headroom_snapshot,
	withholding_snapshot,
	voucher_asset,
	minutes_asset
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10::jsonb, $11::jsonb, $12::jsonb, $13, $14)
RETURNING id,
	declared_date,
	amount,
	amount_currency,
	per_share_amount,
	per_share_currency,
	shares,
	shareholder_name,
	company_snapshot,
	shareholder_snapshot,
	headroom_snapshot,
	withholding_snapshot,
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
		nullableJSONParam(companySnapshot),
		nullableJSONParam(shareholderSnapshot),
		nullableJSONParam(headroomSnapshot),
		nullableJSONParam(withholdingSnapshot),
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
		&companySnapshot,
		&shareholderSnapshot,
		&headroomSnapshot,
		&withholdingSnapshot,
		&voucherAsset,
		&minutesAsset,
		&stored.CreatedAt,
	); err != nil {
		return Declaration{}, fmt.Errorf("dividends: insert declaration: %w", err)
	}
	stored.Amount = money.Money{Amount: amount, Currency: amountCurrency}
	stored.PerShare = money.Money{Amount: perShare, Currency: perShareCurrency}
	if err := setDeclarationSnapshots(&stored, companySnapshot, shareholderSnapshot, headroomSnapshot, withholdingSnapshot); err != nil {
		return Declaration{}, err
	}
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
	company_snapshot,
	shareholder_snapshot,
	headroom_snapshot,
	withholding_snapshot,
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

// Declaration returns one declaration by id.
func (Store) Declaration(ctx context.Context, tx db.Tx, id DeclarationID) (Declaration, error) {
	declaration, err := scanDeclaration(tx.QueryRow(ctx, `
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
	company_snapshot,
	shareholder_snapshot,
	headroom_snapshot,
	withholding_snapshot,
	created_at
FROM dividends.declarations
WHERE id = $1`, strings.TrimSpace(string(id))))
	if err != nil {
		return Declaration{}, err
	}
	return declaration, nil
}

// SetDeclarationDocumentAssets stores immutable voucher/minutes asset ids. If
// both document assets are already present, the existing declaration is returned
// unchanged.
func (s Store) SetDeclarationDocumentAssets(
	ctx context.Context,
	tx db.Tx,
	id DeclarationID,
	voucherAsset identity.AssetID,
	minutesAsset identity.AssetID,
) (Declaration, error) {
	updated, err := scanDeclaration(tx.QueryRow(ctx, `
UPDATE dividends.declarations
SET voucher_asset = $2,
	minutes_asset = $3
WHERE id = $1
	AND (voucher_asset IS NULL OR btrim(voucher_asset) = '')
	AND (minutes_asset IS NULL OR btrim(minutes_asset) = '')
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
	company_snapshot,
	shareholder_snapshot,
	headroom_snapshot,
	withholding_snapshot,
	created_at`,
		strings.TrimSpace(string(id)),
		strings.TrimSpace(string(voucherAsset)),
		strings.TrimSpace(string(minutesAsset)),
	))
	if err == nil {
		return updated, nil
	}
	if errors.Is(err, ErrDeclarationNotFound) {
		existing, existingErr := s.Declaration(ctx, tx, id)
		if existingErr != nil {
			return Declaration{}, existingErr
		}
		if existing.VoucherAsset != nil && strings.TrimSpace(string(*existing.VoucherAsset)) != "" &&
			existing.MinutesAsset != nil && strings.TrimSpace(string(*existing.MinutesAsset)) != "" {
			return existing, nil
		}
	}
	return updated, err
}

type declarationScanner interface {
	Scan(dest ...any) error
}

func scanDeclaration(row declarationScanner) (Declaration, error) {
	var declaration Declaration
	var amount, perShare int64
	var amountCurrency, perShareCurrency string
	var voucherAsset, minutesAsset *string
	var companySnapshot, shareholderSnapshot, headroomSnapshot, withholdingSnapshot []byte
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
		&companySnapshot,
		&shareholderSnapshot,
		&headroomSnapshot,
		&withholdingSnapshot,
		&declaration.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Declaration{}, ErrDeclarationNotFound
		}
		return Declaration{}, fmt.Errorf("dividends: scan declaration: %w", err)
	}
	declaration.Amount = money.Money{Amount: amount, Currency: amountCurrency}
	declaration.PerShare = money.Money{Amount: perShare, Currency: perShareCurrency}
	if err := setDeclarationSnapshots(&declaration, companySnapshot, shareholderSnapshot, headroomSnapshot, withholdingSnapshot); err != nil {
		return Declaration{}, err
	}
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
	if amount.Amount%perShare.Amount != 0 || amount.Amount/perShare.Amount != declaration.Shares {
		return Declaration{}, fmt.Errorf("dividends: amount must equal per share times shares: %w", ErrInvalidDeclaration)
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
	companySnapshot, err := normalizeCompanySnapshot(declaration.CompanySnapshot)
	if err != nil {
		return Declaration{}, err
	}
	shareholderSnapshot, err := normalizeShareholderSnapshot(declaration.ShareholderSnapshot)
	if err != nil {
		return Declaration{}, err
	}
	headroomSnapshot := normalizeHeadroomSnapshot(declaration.HeadroomSnapshot)
	withholdingSnapshot, err := normalizeWithholdingSnapshot(declaration.WithholdingSnapshot)
	if err != nil {
		return Declaration{}, err
	}

	return Declaration{
		ID:                  DeclarationID(id),
		DeclaredDate:        declaredDate,
		Amount:              amount,
		PerShare:            perShare,
		Shares:              declaration.Shares,
		ShareholderName:     shareholderName,
		CompanySnapshot:     companySnapshot,
		ShareholderSnapshot: shareholderSnapshot,
		HeadroomSnapshot:    headroomSnapshot,
		WithholdingSnapshot: withholdingSnapshot,
		VoucherAsset:        voucherAsset,
		MinutesAsset:        minutesAsset,
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

func declarationSnapshotJSON(declaration Declaration) ([]byte, []byte, []byte, []byte, error) {
	companySnapshot, err := nullableJSON(declaration.CompanySnapshot)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("dividends: marshal company snapshot: %w", err)
	}
	shareholderSnapshot, err := nullableJSON(declaration.ShareholderSnapshot)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("dividends: marshal shareholder snapshot: %w", err)
	}
	headroomSnapshot, err := nullableJSON(declaration.HeadroomSnapshot)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("dividends: marshal headroom snapshot: %w", err)
	}
	withholdingSnapshot, err := nullableJSON(declaration.WithholdingSnapshot)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("dividends: marshal withholding snapshot: %w", err)
	}
	return companySnapshot, shareholderSnapshot, headroomSnapshot, withholdingSnapshot, nil
}

func nullableJSON(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	switch typed := value.(type) {
	case *CompanySnapshot:
		if typed == nil {
			return nil, nil
		}
	case *ShareholderSnapshot:
		if typed == nil {
			return nil, nil
		}
	case *HeadroomBreakdown:
		if typed == nil {
			return nil, nil
		}
	case *WithholdingSnapshot:
		if typed == nil {
			return nil, nil
		}
	}
	return json.Marshal(value)
}

func nullableJSONParam(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	return string(data)
}

func setDeclarationSnapshots(declaration *Declaration, companySnapshot, shareholderSnapshot, headroomSnapshot, withholdingSnapshot []byte) error {
	if len(companySnapshot) > 0 {
		var snapshot CompanySnapshot
		if err := json.Unmarshal(companySnapshot, &snapshot); err != nil {
			return fmt.Errorf("dividends: unmarshal company snapshot: %w", err)
		}
		declaration.CompanySnapshot = &snapshot
	}
	if len(shareholderSnapshot) > 0 {
		var snapshot ShareholderSnapshot
		if err := json.Unmarshal(shareholderSnapshot, &snapshot); err != nil {
			return fmt.Errorf("dividends: unmarshal shareholder snapshot: %w", err)
		}
		declaration.ShareholderSnapshot = &snapshot
	}
	if len(headroomSnapshot) > 0 {
		var snapshot HeadroomBreakdown
		if err := json.Unmarshal(headroomSnapshot, &snapshot); err != nil {
			return fmt.Errorf("dividends: unmarshal headroom snapshot: %w", err)
		}
		declaration.HeadroomSnapshot = &snapshot
	}
	if len(withholdingSnapshot) > 0 {
		var snapshot WithholdingSnapshot
		if err := json.Unmarshal(withholdingSnapshot, &snapshot); err != nil {
			return fmt.Errorf("dividends: unmarshal withholding snapshot: %w", err)
		}
		declaration.WithholdingSnapshot = &snapshot
	}
	return nil
}

func normalizeCompanySnapshot(snapshot *CompanySnapshot) (*CompanySnapshot, error) {
	if snapshot == nil {
		return nil, nil
	}
	normalized := *snapshot
	normalized.TradingName = strings.TrimSpace(normalized.TradingName)
	normalized.LegalName = strings.TrimSpace(normalized.LegalName)
	normalized.CompanyNumber = strings.TrimSpace(normalized.CompanyNumber)
	normalized.DirectorName = strings.TrimSpace(normalized.DirectorName)
	normalized.RegisteredOffice.Line1 = strings.TrimSpace(normalized.RegisteredOffice.Line1)
	normalized.RegisteredOffice.Line2 = strings.TrimSpace(normalized.RegisteredOffice.Line2)
	normalized.RegisteredOffice.Locality = strings.TrimSpace(normalized.RegisteredOffice.Locality)
	normalized.RegisteredOffice.Region = strings.TrimSpace(normalized.RegisteredOffice.Region)
	normalized.RegisteredOffice.PostalCode = strings.TrimSpace(normalized.RegisteredOffice.PostalCode)
	normalized.RegisteredOffice.Country = strings.TrimSpace(normalized.RegisteredOffice.Country)
	if normalized.LogoAssetID != nil {
		logoID := identity.AssetID(strings.TrimSpace(string(*normalized.LogoAssetID)))
		if logoID == "" {
			normalized.LogoAssetID = nil
		} else {
			normalized.LogoAssetID = &logoID
		}
	}
	if normalized.LogoAssetURL != nil {
		logoURL := strings.TrimSpace(*normalized.LogoAssetURL)
		if logoURL == "" {
			normalized.LogoAssetURL = nil
		} else {
			normalized.LogoAssetURL = &logoURL
		}
	}
	if normalized.LegalName == "" {
		return nil, fmt.Errorf("dividends: company snapshot legal name is required: %w", ErrInvalidDeclaration)
	}
	if normalized.CompanyNumber == "" {
		return nil, fmt.Errorf("dividends: company snapshot company number is required: %w", ErrInvalidDeclaration)
	}
	if normalized.DirectorName == "" {
		return nil, fmt.Errorf("dividends: company snapshot director name is required: %w", ErrInvalidDeclaration)
	}
	return &normalized, nil
}

func normalizeShareholderSnapshot(snapshot *ShareholderSnapshot) (*ShareholderSnapshot, error) {
	if snapshot == nil {
		return nil, nil
	}
	normalized := *snapshot
	normalized.Name = strings.TrimSpace(normalized.Name)
	normalized.Class = strings.TrimSpace(normalized.Class)
	if normalized.Name == "" {
		return nil, fmt.Errorf("dividends: shareholder snapshot name is required: %w", ErrInvalidDeclaration)
	}
	if normalized.Shares <= 0 {
		return nil, fmt.Errorf("dividends: shareholder snapshot shares must be positive: %w", ErrInvalidDeclaration)
	}
	if normalized.Class == "" {
		return nil, fmt.Errorf("dividends: shareholder snapshot class is required: %w", ErrInvalidDeclaration)
	}
	return &normalized, nil
}

func normalizeHeadroomSnapshot(snapshot *HeadroomBreakdown) *HeadroomBreakdown {
	if snapshot == nil {
		return nil
	}
	normalized := *snapshot
	normalized.Lines = append([]MoneyLine{}, snapshot.Lines...)
	return &normalized
}

func normalizeWithholdingSnapshot(snapshot *WithholdingSnapshot) (*WithholdingSnapshot, error) {
	if snapshot == nil {
		return nil, nil
	}
	normalized := *snapshot
	normalized.TaxYear = strings.TrimSpace(normalized.TaxYear)
	normalized.Policy = strings.TrimSpace(normalized.Policy)
	normalized.Note = strings.TrimSpace(normalized.Note)
	if normalized.TaxYear == "" {
		return nil, fmt.Errorf("dividends: withholding snapshot tax year is required: %w", ErrInvalidDeclaration)
	}
	if normalized.Policy == "" {
		return nil, fmt.Errorf("dividends: withholding snapshot policy is required: %w", ErrInvalidDeclaration)
	}
	if normalized.Note == "" {
		return nil, fmt.Errorf("dividends: withholding snapshot note is required: %w", ErrInvalidDeclaration)
	}
	return &normalized, nil
}

func identityAssetID(value *string) *identity.AssetID {
	if value == nil {
		return nil
	}
	assetID := identity.AssetID(*value)
	return &assetID
}
