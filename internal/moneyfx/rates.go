package moneyfx

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/clock"
)

const (
	rateSourceECB      = "ECB"
	rateSourceIdentity = "identity"
	maxRateLookback    = 7
)

// ErrNoRate is returned when no ECB rate can be found for a requested pair and
// date within the supported lookback window.
var ErrNoRate = errors.New("moneyfx: no rate")

// ErrRateUnavailable is retained for older callers that treated missing rates
// as a soft presentation failure.
var ErrRateUnavailable = ErrNoRate

// Rate is an exact FX multiplier from one currency into another.
type Rate struct {
	From     string    `json:"from"`
	To       string    `json:"to"`
	Value    string    `json:"value"`
	RateDate time.Time `json:"rate_date"`
	Source   string    `json:"source"`
}

// Rat parses the exact rate value for use with money.MulRat.
func (r Rate) Rat() (*big.Rat, error) {
	value := strings.TrimSpace(r.Value)
	if value == "" {
		return nil, fmt.Errorf("moneyfx: rate value is required")
	}
	rat, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, fmt.Errorf("moneyfx: parse rate %q", r.Value)
	}
	return rat, nil
}

type rateReader interface {
	ECBRate(ctx context.Context, date time.Time, currency string) (StoredECBRate, error)
	ECBRateDateOnOrBefore(ctx context.Context, date time.Time, minDate time.Time) (time.Time, bool, error)
	LatestRateDate(ctx context.Context) (time.Time, bool, error)
}

// Service provides DB-backed money-fx queries and conversions.
type Service struct {
	store rateReader
	clock clock.Clock
}

// NewService creates the money-fx service used by modules and HTTP handlers.
func NewService(store rateReader, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.New()
	}
	return &Service{
		store: store,
		clock: clk,
	}
}

// RateOn returns the exact FX multiplier for date, falling back to the most
// recent prior ECB rate date within seven calendar days.
func (s *Service) RateOn(ctx context.Context, date time.Time, from string, to string) (Rate, error) {
	if err := ctx.Err(); err != nil {
		return Rate{}, err
	}
	from, to, err := normalizeRatePair(from, to)
	if err != nil {
		return Rate{}, err
	}
	rateDate := normalizeRateDate(date)
	if rateDate.IsZero() {
		return Rate{}, fmt.Errorf("moneyfx: rate date is required")
	}
	if from == to {
		return Rate{
			From:     from,
			To:       to,
			Value:    "1",
			RateDate: rateDate,
			Source:   rateSourceIdentity,
		}, nil
	}
	if s == nil || s.store == nil {
		return Rate{}, fmt.Errorf("moneyfx: rate store is required")
	}

	actualDate, ok, err := s.store.ECBRateDateOnOrBefore(ctx, rateDate, rateDate.AddDate(0, 0, -maxRateLookback))
	if err != nil {
		return Rate{}, err
	}
	if !ok {
		return Rate{}, fmt.Errorf("moneyfx: no ECB rates on or before %s within %d days: %w", rateDate.Format(time.DateOnly), maxRateLookback, ErrNoRate)
	}

	fromEUR, err := s.eurBaseRate(ctx, actualDate, from)
	if err != nil {
		return Rate{}, err
	}
	toEUR, err := s.eurBaseRate(ctx, actualDate, to)
	if err != nil {
		return Rate{}, err
	}
	value := new(big.Rat).Quo(toEUR, fromEUR)
	return Rate{
		From:     from,
		To:       to,
		Value:    exactRateValue(value),
		RateDate: actualDate,
		Source:   rateSourceECB,
	}, nil
}

// TodayRate returns the latest stored ECB rate and the timestamp at which the
// lookup was served.
func (s *Service) TodayRate(ctx context.Context, from string, to string) (Rate, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return Rate{}, time.Time{}, err
	}
	from, to, err := normalizeRatePair(from, to)
	if err != nil {
		return Rate{}, time.Time{}, err
	}
	fetchedAt := s.nowUTC()
	if from == to {
		return Rate{
			From:     from,
			To:       to,
			Value:    "1",
			RateDate: normalizeRateDate(fetchedAt),
			Source:   rateSourceIdentity,
		}, fetchedAt, nil
	}
	if s == nil || s.store == nil {
		return Rate{}, time.Time{}, fmt.Errorf("moneyfx: rate store is required")
	}
	rateDate, ok, err := s.store.LatestRateDate(ctx)
	if err != nil {
		return Rate{}, time.Time{}, err
	}
	if !ok {
		return Rate{}, time.Time{}, fmt.Errorf("moneyfx: no ECB rates stored: %w", ErrNoRate)
	}
	rate, err := s.RateOn(ctx, rateDate, from, to)
	if err != nil {
		return Rate{}, time.Time{}, err
	}
	return rate, fetchedAt, nil
}

// ToGBP converts m into GBP using the ECB rate for date. GBP inputs are already
// presentational GBP and are returned unchanged.
func (s *Service) ToGBP(ctx context.Context, m money.Money, date time.Time) (money.Money, error) {
	currency, err := normalizeCurrency(m.Currency)
	if err != nil {
		return money.Money{}, err
	}
	if currency == "GBP" {
		return m, nil
	}
	rate, err := s.RateOn(ctx, date, currency, "GBP")
	if err != nil {
		return money.Money{}, err
	}
	rat, err := rate.Rat()
	if err != nil {
		return money.Money{}, err
	}
	converted := m.MulRat(rat)
	converted.Currency = "GBP"
	return converted, nil
}

func (s *Service) eurBaseRate(ctx context.Context, date time.Time, currency string) (*big.Rat, error) {
	if currency == "EUR" {
		return big.NewRat(1, 1), nil
	}
	stored, err := s.store.ECBRate(ctx, date, currency)
	if err != nil {
		if errors.Is(err, ErrNoRate) {
			return nil, fmt.Errorf("moneyfx: no ECB rate %s %s: %w", date.Format(time.DateOnly), currency, ErrNoRate)
		}
		return nil, err
	}
	return stored.Rat()
}

func (s *Service) nowUTC() time.Time {
	if s == nil || s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}

// TodayRate is the dependency-free compatibility lookup used when no moneyfx
// service is wired. Production callers should use Service.TodayRate.
func TodayRate(ctx context.Context, from string, to string) (Rate, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return Rate{}, time.Time{}, err
	}
	from, to, err := normalizeRatePair(from, to)
	if err != nil {
		return Rate{}, time.Time{}, err
	}
	if from == to {
		now := time.Now().UTC()
		return Rate{
			From:     from,
			To:       to,
			Value:    "1",
			RateDate: normalizeRateDate(now),
			Source:   rateSourceIdentity,
		}, now, nil
	}
	return Rate{}, time.Time{}, ErrNoRate
}

func normalizeRatePair(from string, to string) (string, string, error) {
	normalizedFrom, err := normalizeCurrency(from)
	if err != nil {
		return "", "", fmt.Errorf("moneyfx: from currency: %w", err)
	}
	normalizedTo, err := normalizeCurrency(to)
	if err != nil {
		return "", "", fmt.Errorf("moneyfx: to currency: %w", err)
	}
	return normalizedFrom, normalizedTo, nil
}

func exactRateValue(r *big.Rat) string {
	if r == nil {
		return ""
	}
	if decimal, ok := finiteDecimalString(r); ok {
		return decimal
	}
	return r.String()
}

func finiteDecimalString(r *big.Rat) (string, bool) {
	if r == nil {
		return "", false
	}
	numerator := new(big.Int).Set(r.Num())
	denominator := new(big.Int).Set(r.Denom())
	sign := ""
	if numerator.Sign() < 0 {
		sign = "-"
		numerator.Abs(numerator)
	}

	twos := factorCount(denominator, 2)
	fives := factorCount(denominator, 5)
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return "", false
	}

	scale := twos
	if fives > scale {
		scale = fives
	}
	if scale > twos {
		numerator.Mul(numerator, new(big.Int).Exp(big.NewInt(2), big.NewInt(int64(scale-twos)), nil))
	}
	if scale > fives {
		numerator.Mul(numerator, new(big.Int).Exp(big.NewInt(5), big.NewInt(int64(scale-fives)), nil))
	}

	digits := numerator.String()
	if scale == 0 {
		return sign + digits, true
	}
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	split := len(digits) - scale
	whole := digits[:split]
	frac := strings.TrimRight(digits[split:], "0")
	if frac == "" {
		return sign + whole, true
	}
	return sign + whole + "." + frac, true
}

func factorCount(value *big.Int, factor int64) int {
	count := 0
	divisor := big.NewInt(factor)
	remainder := new(big.Int)
	for value.Sign() != 0 {
		quotient := new(big.Int)
		quotient.QuoRem(value, divisor, remainder)
		if remainder.Sign() != 0 {
			return count
		}
		value.Set(quotient)
		count++
	}
	return count
}
