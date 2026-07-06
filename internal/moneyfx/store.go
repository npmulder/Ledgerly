package moneyfx

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

var rateDecimalPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

const (
	ecbRateDecimalScale  = 8
	lockRateDecimalScale = 18
)

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
	return loadECBRate(ctx, s.pool, date, currency)
}

func loadECBRate(ctx context.Context, queryer rateQueryer, date time.Time, currency string) (StoredECBRate, error) {
	if queryer == nil {
		return StoredECBRate{}, fmt.Errorf("moneyfx: load rate requires queryer")
	}
	normalizedDate := normalizeRateDate(date)
	normalizedCurrency, err := normalizeCurrency(currency)
	if err != nil {
		return StoredECBRate{}, err
	}

	var row StoredECBRate
	var decimalText string
	if err := queryer.QueryRow(ctx, `
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

// ECBRateDateOnOrBefore returns the newest stored ECB rate date in the inclusive
// window that has every requested non-EUR currency. Rate lookups issue direct
// indexed queries; request-scope caching is unnecessary for the small
// per-request read surface.
func (s Store) ECBRateDateOnOrBefore(ctx context.Context, date time.Time, minDate time.Time, currencies []string) (time.Time, bool, error) {
	if s.pool == nil {
		return time.Time{}, false, fmt.Errorf("moneyfx: load rate date requires pool")
	}
	return loadECBRateDateOnOrBefore(ctx, s.pool, date, minDate, currencies)
}

func loadECBRateDateOnOrBefore(ctx context.Context, queryer rateQueryer, date time.Time, minDate time.Time, currencies []string) (time.Time, bool, error) {
	if queryer == nil {
		return time.Time{}, false, fmt.Errorf("moneyfx: load rate date requires queryer")
	}
	normalizedDate := normalizeRateDate(date)
	normalizedMinDate := normalizeRateDate(minDate)
	if normalizedDate.IsZero() || normalizedMinDate.IsZero() {
		return time.Time{}, false, fmt.Errorf("moneyfx: rate date window is required")
	}
	normalizedCurrencies, err := normalizeCurrencyFilter(currencies)
	if err != nil {
		return time.Time{}, false, err
	}

	var rateDate time.Time
	if len(normalizedCurrencies) == 0 {
		err := queryer.QueryRow(ctx, `
SELECT date
FROM moneyfx.ecb_rates
WHERE date <= $1 AND date >= $2
GROUP BY date
ORDER BY date DESC
LIMIT 1`,
			normalizedDate,
			normalizedMinDate,
		).Scan(&rateDate)
		if err == pgx.ErrNoRows {
			return time.Time{}, false, nil
		}
		if err != nil {
			return time.Time{}, false, fmt.Errorf("moneyfx: load ECB rate date: %w", err)
		}
		return normalizeRateDate(rateDate), true, nil
	}

	err = queryer.QueryRow(ctx, `
SELECT date
FROM moneyfx.ecb_rates
WHERE date <= $1 AND date >= $2 AND currency = ANY($3::text[])
GROUP BY date
HAVING count(DISTINCT currency) = $4
ORDER BY date DESC
LIMIT 1`,
		normalizedDate,
		normalizedMinDate,
		normalizedCurrencies,
		len(normalizedCurrencies),
	).Scan(&rateDate)
	if err == pgx.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("moneyfx: load ECB rate date: %w", err)
	}
	return normalizeRateDate(rateDate), true, nil
}

func normalizeCurrencyFilter(currencies []string) ([]string, error) {
	seen := make(map[string]struct{}, len(currencies))
	normalized := make([]string, 0, len(currencies))
	for _, currency := range currencies {
		currency, err := normalizeCurrency(currency)
		if err != nil {
			return nil, err
		}
		if currency == "EUR" {
			continue
		}
		if _, ok := seen[currency]; ok {
			continue
		}
		seen[currency] = struct{}{}
		normalized = append(normalized, currency)
	}
	return normalized, nil
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
	return loadLatestRateDate(ctx, s.pool)
}

func loadLatestRateDate(ctx context.Context, queryer rateQueryer) (time.Time, bool, error) {
	if queryer == nil {
		return time.Time{}, false, fmt.Errorf("moneyfx: latest rate date requires queryer")
	}

	var date time.Time
	err := queryer.QueryRow(ctx, `
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

type rateQueryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// InsertRateLock appends a new immutable rate lock using the caller's
// transaction.
func (s Store) InsertRateLock(ctx context.Context, tx db.Tx, lock newRateLock) (RateLock, error) {
	if tx == nil {
		return RateLock{}, fmt.Errorf("moneyfx: insert rate lock requires transaction")
	}
	normalized, err := normalizeNewRateLock(lock)
	if err != nil {
		return RateLock{}, err
	}

	return scanRateLock(tx.QueryRow(ctx, `
INSERT INTO moneyfx.rate_locks (ref, from_currency, to_currency, rate, rate_date, locked_at, source)
VALUES ($1, $2, $3, $4::numeric, $5, $6, $7)
RETURNING id, ref, from_currency, to_currency, rate::text, rate_date, locked_at, source`,
		normalized.RefText,
		normalized.From,
		normalized.To,
		normalized.Rate,
		normalized.RateDate,
		normalized.LockedAt,
		normalized.Source,
	))
}

// RateLockByID returns a stored rate lock by id.
func (s Store) RateLockByID(ctx context.Context, id LockID) (RateLock, error) {
	if s.pool == nil {
		return RateLock{}, fmt.Errorf("moneyfx: load rate lock requires pool")
	}
	return loadRateLockByID(ctx, s.pool, id)
}

func loadRateLockByID(ctx context.Context, queryer rateQueryer, id LockID) (RateLock, error) {
	if queryer == nil {
		return RateLock{}, fmt.Errorf("moneyfx: load rate lock requires queryer")
	}
	if id <= 0 {
		return RateLock{}, fmt.Errorf("moneyfx: rate lock id %d: %w", id, ErrLockNotFound)
	}
	lock, err := scanRateLock(queryer.QueryRow(ctx, `
SELECT id, ref, from_currency, to_currency, rate::text, rate_date, locked_at, source
FROM moneyfx.rate_locks
WHERE id = $1`,
		id,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RateLock{}, fmt.Errorf("moneyfx: rate lock %d: %w", id, ErrLockNotFound)
		}
		return RateLock{}, fmt.Errorf("moneyfx: load rate lock: %w", err)
	}
	return lock, nil
}

type realisedFXStore interface {
	InsertRealisedFX(ctx context.Context, tx db.Tx, record newRealisedFX) (bool, error)
}

type newRealisedFX struct {
	InvoiceID      string
	LockID         LockID
	SettlementDate time.Time
	AmountGBP      money.Money
	SourceRef      string
}

// InsertRealisedFX records the settlement key and amount inside tx.
//
// The returned bool is false when the `(invoice_id, lock_id)` pair was already
// processed. Callers insert before ledger posting so replayed settlement events
// no-op without a second journal entry.
func (s Store) InsertRealisedFX(ctx context.Context, tx db.Tx, record newRealisedFX) (bool, error) {
	if tx == nil {
		return false, fmt.Errorf("moneyfx: insert realised FX requires transaction")
	}
	normalized, err := normalizeNewRealisedFX(record)
	if err != nil {
		return false, err
	}

	var id int64
	if err := tx.QueryRow(ctx, `
INSERT INTO moneyfx.realised_fx (invoice_id, lock_id, settlement_date, amount_gbp, source_ref)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (invoice_id, lock_id) DO NOTHING
RETURNING id`,
		normalized.InvoiceID,
		int64(normalized.LockID),
		normalized.SettlementDate,
		normalized.AmountGBP.Amount,
		normalized.SourceRef,
	).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("moneyfx: insert realised FX %s/%d: %w", normalized.InvoiceID, normalized.LockID, err)
	}
	return true, nil
}

func normalizeNewRealisedFX(record newRealisedFX) (newRealisedFX, error) {
	normalized := newRealisedFX{
		InvoiceID:      strings.TrimSpace(record.InvoiceID),
		LockID:         record.LockID,
		SettlementDate: normalizeRateDate(record.SettlementDate),
		AmountGBP: money.Money{
			Amount:   record.AmountGBP.Amount,
			Currency: strings.ToUpper(strings.TrimSpace(record.AmountGBP.Currency)),
		},
		SourceRef: strings.TrimSpace(record.SourceRef),
	}
	if normalized.InvoiceID == "" {
		return newRealisedFX{}, fmt.Errorf("moneyfx: realised FX invoice id is required")
	}
	if normalized.LockID <= 0 {
		return newRealisedFX{}, fmt.Errorf("moneyfx: realised FX lock id %d: %w", normalized.LockID, ErrLockNotFound)
	}
	if normalized.SettlementDate.IsZero() {
		return newRealisedFX{}, fmt.Errorf("moneyfx: realised FX settlement date is required")
	}
	if normalized.AmountGBP.Currency != "GBP" {
		return newRealisedFX{}, fmt.Errorf("moneyfx: realised FX amount currency is %q, want GBP", normalized.AmountGBP.Currency)
	}
	if normalized.SourceRef == "" {
		return newRealisedFX{}, fmt.Errorf("moneyfx: realised FX source ref is required")
	}
	return normalized, nil
}

// ActiveRateLockFor returns the newest rate lock for ref.
func (s Store) ActiveRateLockFor(ctx context.Context, ref LockRef) (RateLock, error) {
	if s.pool == nil {
		return RateLock{}, fmt.Errorf("moneyfx: active rate lock requires pool")
	}
	_, refText, err := normalizeLockRef(ref)
	if err != nil {
		return RateLock{}, err
	}
	lock, err := scanRateLock(s.pool.QueryRow(ctx, `
SELECT id, ref, from_currency, to_currency, rate::text, rate_date, locked_at, source
FROM moneyfx.rate_locks
WHERE ref = $1
ORDER BY id DESC
LIMIT 1`,
		refText,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RateLock{}, fmt.Errorf("moneyfx: active rate lock for %s: %w", refText, ErrLockNotFound)
		}
		return RateLock{}, fmt.Errorf("moneyfx: load active rate lock: %w", err)
	}
	return lock, nil
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
	return canonicalPositiveDecimal(value, ecbRateDecimalScale)
}

func canonicalRateLockDecimal(value string) (string, error) {
	return canonicalPositiveDecimal(value, lockRateDecimalScale)
}

func canonicalPositiveDecimal(value string, maxScale int) (string, error) {
	decimal := strings.TrimSpace(value)
	if decimal == "" {
		return "", fmt.Errorf("rate is required")
	}
	if !rateDecimalPattern.MatchString(decimal) {
		return "", fmt.Errorf("rate %q is not a plain decimal", value)
	}
	if dot := strings.IndexByte(decimal, '.'); dot >= 0 && len(decimal)-dot-1 > maxScale {
		return "", fmt.Errorf("rate %q exceeds scale %d", value, maxScale)
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

func normalizeNewRateLock(lock newRateLock) (newRateLock, error) {
	ref, refText, err := normalizeLockRef(lock.Ref)
	if err != nil {
		return newRateLock{}, err
	}
	if strings.TrimSpace(lock.RefText) != "" && strings.TrimSpace(lock.RefText) != refText {
		return newRateLock{}, fmt.Errorf("moneyfx: lock ref text %q does not match %q", lock.RefText, refText)
	}
	from, to, err := normalizeRatePair(lock.From, lock.To)
	if err != nil {
		return newRateLock{}, err
	}
	rate, err := canonicalRateLockDecimal(lock.Rate)
	if err != nil {
		return newRateLock{}, err
	}
	rateDate := normalizeRateDate(lock.RateDate)
	if rateDate.IsZero() {
		return newRateLock{}, fmt.Errorf("moneyfx: lock rate date is required")
	}
	lockedAt := lock.LockedAt.UTC()
	if lockedAt.IsZero() {
		return newRateLock{}, fmt.Errorf("moneyfx: lock timestamp is required")
	}
	source := strings.TrimSpace(lock.Source)
	if source == "" {
		source = rateSourceECB
	}
	if source != rateSourceECB {
		return newRateLock{}, fmt.Errorf("moneyfx: lock source %q is not supported", source)
	}
	return newRateLock{
		Ref:      ref,
		RefText:  refText,
		From:     from,
		To:       to,
		Rate:     rate,
		RateDate: rateDate,
		LockedAt: lockedAt,
		Source:   source,
	}, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRateLock(row scanner) (RateLock, error) {
	var (
		id       int64
		refText  string
		rateText string
		lock     RateLock
	)
	if err := row.Scan(
		&id,
		&refText,
		&lock.From,
		&lock.To,
		&rateText,
		&lock.RateDate,
		&lock.LockedAt,
		&lock.Source,
	); err != nil {
		return RateLock{}, err
	}

	ref, err := parseLockRef(refText)
	if err != nil {
		return RateLock{}, err
	}
	rate, err := canonicalRateLockDecimal(rateText)
	if err != nil {
		return RateLock{}, err
	}
	lock.ID = LockID(id)
	lock.Ref = ref
	lock.Rate = rate
	lock.RateDate = normalizeRateDate(lock.RateDate)
	lock.LockedAt = lock.LockedAt.UTC()
	return lock, nil
}

func errorsJoin(err error, other error) error {
	if other == nil {
		return err
	}
	return fmt.Errorf("%w; %w", err, other)
}
