package moneyfx

import (
	"context"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

var rateDecimalPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// Store owns moneyfx persistence.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a moneyfx store.
func NewStore(pool *pgxpool.Pool) Store {
	return Store{pool: pool}
}

// StoredECBRate is an exact EUR-base ECB rate loaded from Postgres.
type StoredECBRate struct {
	Date     time.Time
	Currency string
	Rate     string
}

// Rat parses the exact stored decimal for calculation code.
func (r StoredECBRate) Rat() (*big.Rat, error) {
	rat, ok := new(big.Rat).SetString(r.Rate)
	if !ok {
		return nil, fmt.Errorf("moneyfx: parse stored ECB rate %q", r.Rate)
	}
	return rat, nil
}

// StoreECBRates atomically upserts all rates.
func (s Store) StoreECBRates(ctx context.Context, rates []ECBRate) (err error) {
	if s.pool == nil {
		return fmt.Errorf("moneyfx: store rates requires pool")
	}
	normalized, err := normalizeECBRates(rates)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("moneyfx: begin ECB rate transaction: %w", err)
	}
	defer func() {
		if err != nil {
			err = errorsJoin(err, tx.Rollback(ctx))
		}
	}()

	if err = StoreECBRatesTx(ctx, tx, normalized); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("moneyfx: commit ECB rates: %w", err)
	}
	return nil
}

// StoreECBRatesTx upserts all rates using the caller's transaction.
func StoreECBRatesTx(ctx context.Context, tx db.Tx, rates []ECBRate) error {
	if tx == nil {
		return fmt.Errorf("moneyfx: store rates requires transaction")
	}
	normalized, err := normalizeECBRates(rates)
	if err != nil {
		return err
	}

	for _, rate := range normalized {
		if _, err := tx.Exec(ctx, `
INSERT INTO moneyfx.ecb_rates (date, currency, rate)
VALUES ($1, $2, $3::numeric)
ON CONFLICT (date, currency) DO UPDATE
SET rate = EXCLUDED.rate`,
			rate.Date,
			rate.Currency,
			rate.Rate,
		); err != nil {
			return fmt.Errorf("moneyfx: upsert ECB rate %s %s: %w", rate.Date.Format(time.DateOnly), rate.Currency, err)
		}
	}
	return nil
}

// ECBRate returns one stored ECB rate.
func (s Store) ECBRate(ctx context.Context, date time.Time, currency string) (StoredECBRate, error) {
	if s.pool == nil {
		return StoredECBRate{}, fmt.Errorf("moneyfx: load rate requires pool")
	}
	normalizedDate := normalizeRateDate(date)
	normalizedCurrency, err := normalizeCurrency(currency)
	if err != nil {
		return StoredECBRate{}, err
	}

	var row StoredECBRate
	var decimalText string
	if err := s.pool.QueryRow(ctx, `
SELECT date, currency, rate::text
FROM moneyfx.ecb_rates
WHERE date = $1 AND currency = $2`,
		normalizedDate,
		normalizedCurrency,
	).Scan(&row.Date, &row.Currency, &decimalText); err != nil {
		if err == pgx.ErrNoRows {
			return StoredECBRate{}, fmt.Errorf("moneyfx: ECB rate %s %s: %w", normalizedDate.Format(time.DateOnly), normalizedCurrency, ErrRateUnavailable)
		}
		return StoredECBRate{}, fmt.Errorf("moneyfx: load ECB rate: %w", err)
	}

	row.Date = normalizeRateDate(row.Date)
	row.Rate, err = canonicalRateDecimal(decimalText)
	if err != nil {
		return StoredECBRate{}, err
	}
	return row, nil
}

// CountECBRates returns the number of stored ECB rate rows.
func (s Store) CountECBRates(ctx context.Context) (int, error) {
	if s.pool == nil {
		return 0, fmt.Errorf("moneyfx: count rates requires pool")
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM moneyfx.ecb_rates`).Scan(&count); err != nil {
		return 0, fmt.Errorf("moneyfx: count ECB rates: %w", err)
	}
	return count, nil
}

// LatestRateDate returns the newest stored ECB rate date.
func (s Store) LatestRateDate(ctx context.Context) (time.Time, bool, error) {
	if s.pool == nil {
		return time.Time{}, false, fmt.Errorf("moneyfx: latest rate date requires pool")
	}

	var date time.Time
	err := s.pool.QueryRow(ctx, `
SELECT date
FROM moneyfx.ecb_rates
ORDER BY date DESC
LIMIT 1`).Scan(&date)
	if err == pgx.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("moneyfx: latest ECB rate date: %w", err)
	}
	return normalizeRateDate(date), true, nil
}

func normalizeECBRates(rates []ECBRate) ([]ECBRate, error) {
	if len(rates) == 0 {
		return nil, fmt.Errorf("moneyfx: at least one ECB rate is required")
	}

	normalized := make([]ECBRate, len(rates))
	for i, rate := range rates {
		date := normalizeRateDate(rate.Date)
		if date.IsZero() {
			return nil, fmt.Errorf("moneyfx: ECB rate %d date is required", i)
		}
		currency, err := normalizeCurrency(rate.Currency)
		if err != nil {
			return nil, fmt.Errorf("moneyfx: ECB rate %d currency: %w", i, err)
		}
		decimal, err := canonicalRateDecimal(rate.Rate)
		if err != nil {
			return nil, fmt.Errorf("moneyfx: ECB rate %d decimal: %w", i, err)
		}
		normalized[i] = ECBRate{
			Date:     date,
			Currency: currency,
			Rate:     decimal,
		}
	}
	return normalized, nil
}

func normalizeRateDate(date time.Time) time.Time {
	if date.IsZero() {
		return time.Time{}
	}
	year, month, day := date.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func normalizeCurrency(currency string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(currency))
	if normalized == "" {
		return "", fmt.Errorf("currency is required")
	}
	if len(normalized) > 12 {
		return "", fmt.Errorf("currency %q is too long", normalized)
	}
	for _, r := range normalized {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return "", fmt.Errorf("currency %q contains invalid character %q", normalized, r)
		}
	}
	return normalized, nil
}

func canonicalRateDecimal(value string) (string, error) {
	decimal := strings.TrimSpace(value)
	if decimal == "" {
		return "", fmt.Errorf("rate is required")
	}
	if !rateDecimalPattern.MatchString(decimal) {
		return "", fmt.Errorf("rate %q is not a plain decimal", value)
	}
	if dot := strings.IndexByte(decimal, '.'); dot >= 0 && len(decimal)-dot-1 > 8 {
		return "", fmt.Errorf("rate %q exceeds numeric(18,8)", value)
	}

	rat, ok := new(big.Rat).SetString(decimal)
	if !ok {
		return "", fmt.Errorf("parse rate %q", value)
	}
	if rat.Sign() <= 0 {
		return "", fmt.Errorf("rate %q must be positive", value)
	}

	if strings.Contains(decimal, ".") {
		decimal = strings.TrimRight(decimal, "0")
		decimal = strings.TrimRight(decimal, ".")
	}
	for len(decimal) > 1 && decimal[0] == '0' && decimal[1] != '.' {
		decimal = decimal[1:]
	}
	return decimal, nil
}

func errorsJoin(err error, other error) error {
	if other == nil {
		return err
	}
	return fmt.Errorf("%w; %w", err, other)
}
