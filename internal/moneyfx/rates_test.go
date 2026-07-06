package moneyfx

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/clock"
)

func TestRateRatParsesExactValue(t *testing.T) {
	t.Parallel()

	rat, err := (Rate{Value: "4/5"}).Rat()
	if err != nil {
		t.Fatalf("Rat() error = %v", err)
	}
	if rat.Num().Int64() != 4 || rat.Denom().Int64() != 5 {
		t.Fatalf("Rat() = %s, want 4/5", rat.String())
	}
}

func TestTodayRateIdentityAndUnavailable(t *testing.T) {
	t.Parallel()

	rate, _, err := TodayRate(context.Background(), "gbp", "GBP")
	if err != nil {
		t.Fatalf("TodayRate(identity) error = %v", err)
	}
	if rate.From != "GBP" || rate.To != "GBP" || rate.Value != "1" {
		t.Fatalf("identity rate = %+v, want GBP->GBP value 1", rate)
	}

	_, _, err = TodayRate(context.Background(), "EUR", "GBP")
	if !errors.Is(err, ErrRateUnavailable) {
		t.Fatalf("TodayRate(EUR,GBP) error = %v, want ErrRateUnavailable", err)
	}
}

func TestServiceRateOnCrossRatesAndWeekendFallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	friday := time.Date(2030, 1, 4, 0, 0, 0, 0, time.UTC)
	monday := friday.AddDate(0, 0, 3)
	service := newTestRateService(t, friday)

	tests := []struct {
		name  string
		from  string
		to    string
		want  string
		value string
	}{
		{name: "EUR to GBP direct", from: "EUR", to: "GBP", want: "4/5", value: "0.8"},
		{name: "GBP to EUR inverse", from: "GBP", to: "EUR", want: "5/4", value: "1.25"},
		{name: "USD to GBP cross", from: "USD", to: "GBP", want: "16/25", value: "0.64"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate, err := service.RateOn(ctx, monday, tt.from, tt.to)
			if err != nil {
				t.Fatalf("RateOn() error = %v", err)
			}
			if rate.From != tt.from || rate.To != tt.to {
				t.Fatalf("RateOn() pair = %s->%s, want %s->%s", rate.From, rate.To, tt.from, tt.to)
			}
			if !rate.RateDate.Equal(friday) {
				t.Fatalf("RateOn() date = %s, want Friday %s", rate.RateDate.Format(time.DateOnly), friday.Format(time.DateOnly))
			}
			if rate.Source != rateSourceECB {
				t.Fatalf("RateOn() source = %q, want %q", rate.Source, rateSourceECB)
			}
			if rate.Value != tt.value {
				t.Fatalf("RateOn() value = %q, want %q", rate.Value, tt.value)
			}
			assertRat(t, rate, tt.want)
		})
	}
}

func TestServiceRateOnSkipsPartialFallbackDates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	friday := time.Date(2030, 1, 4, 0, 0, 0, 0, time.UTC)
	monday := friday.AddDate(0, 0, 3)
	store := newMemoryRateReader()
	store.seed(t, friday, "GBP", "0.8")
	store.seed(t, friday, "USD", "1.25")
	store.seed(t, monday.AddDate(0, 0, -1), "CHF", "0.95")
	store.seed(t, monday, "USD", "1.3")
	service := NewService(store, clock.NewFake(monday.Add(12*time.Hour)))

	rate, err := service.RateOn(ctx, monday, "EUR", "GBP")
	if err != nil {
		t.Fatalf("RateOn(EUR,GBP) error = %v", err)
	}
	if !rate.RateDate.Equal(friday) {
		t.Fatalf("RateOn(EUR,GBP) date = %s, want %s", rate.RateDate.Format(time.DateOnly), friday.Format(time.DateOnly))
	}
	assertRat(t, rate, "4/5")

	rate, err = service.RateOn(ctx, monday, "USD", "GBP")
	if err != nil {
		t.Fatalf("RateOn(USD,GBP) error = %v", err)
	}
	if !rate.RateDate.Equal(friday) {
		t.Fatalf("RateOn(USD,GBP) date = %s, want %s", rate.RateDate.Format(time.DateOnly), friday.Format(time.DateOnly))
	}
	assertRat(t, rate, "16/25")
}

func TestServiceTodayRateReturnsLatestRateAndFetchTimestamp(t *testing.T) {
	t.Parallel()

	friday := time.Date(2030, 1, 4, 0, 0, 0, 0, time.UTC)
	now := time.Date(2030, 1, 7, 9, 30, 0, 0, time.UTC)
	service := newTestRateService(t, friday)
	service.clock = clock.NewFake(now)

	rate, fetchedAt, err := service.TodayRate(context.Background(), "EUR", "GBP")
	if err != nil {
		t.Fatalf("TodayRate() error = %v", err)
	}
	if !rate.RateDate.Equal(friday) {
		t.Fatalf("TodayRate() rate date = %s, want %s", rate.RateDate.Format(time.DateOnly), friday.Format(time.DateOnly))
	}
	if !fetchedAt.Equal(now) {
		t.Fatalf("TodayRate() fetchedAt = %s, want %s", fetchedAt, now)
	}
	assertRat(t, rate, "4/5")
}

func TestServiceRateStalenessReturnsStaleLatestDate(t *testing.T) {
	t.Parallel()

	lastDate := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	service := newTestRateService(t, lastDate)
	service.clock = clock.NewFake(time.Date(2030, 1, 7, 12, 0, 0, 0, time.UTC))

	staleness, err := service.RateStaleness(context.Background())
	if err != nil {
		t.Fatalf("RateStaleness() error = %v", err)
	}
	if staleness.LastDate == nil || !staleness.LastDate.Equal(lastDate) {
		t.Fatalf("LastDate = %v, want %s", staleness.LastDate, lastDate.Format(time.DateOnly))
	}
	if !staleness.Stale {
		t.Fatal("Stale = false, want true")
	}
	if staleness.StaleDays != 5 {
		t.Fatalf("StaleDays = %d, want 5", staleness.StaleDays)
	}
}

func TestServiceRateOnErrNoRateBeyondLookback(t *testing.T) {
	t.Parallel()

	friday := time.Date(2030, 1, 4, 0, 0, 0, 0, time.UTC)
	service := newTestRateService(t, friday)

	_, err := service.RateOn(context.Background(), friday.AddDate(0, 0, 8), "EUR", "GBP")
	if !errors.Is(err, ErrNoRate) {
		t.Fatalf("RateOn() error = %v, want ErrNoRate", err)
	}
}

func TestServiceToGBPRoundsWithMulRatAndPreservesGBP(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	friday := time.Date(2030, 1, 4, 0, 0, 0, 0, time.UTC)
	service := newTestRateService(t, friday)

	original := money.Money{Amount: 12345, Currency: "GBP"}
	unchanged, err := service.ToGBP(ctx, original, time.Time{})
	if err != nil {
		t.Fatalf("ToGBP(GBP) error = %v", err)
	}
	if unchanged != original {
		t.Fatalf("ToGBP(GBP) = %+v, want unchanged %+v", unchanged, original)
	}

	gbpToEUR, err := service.RateOn(ctx, friday, "GBP", "EUR")
	if err != nil {
		t.Fatalf("RateOn(GBP,EUR) error = %v", err)
	}
	gbpToEURRat, err := gbpToEUR.Rat()
	if err != nil {
		t.Fatalf("GBP->EUR Rat() error = %v", err)
	}

	for _, amount := range []int64{-12345, -101, -1, 0, 1, 2, 3, 99, 101, 12345} {
		gbp := money.Money{Amount: amount, Currency: "GBP"}
		eur := gbp.MulRat(gbpToEURRat)
		eur.Currency = "EUR"
		roundTrip, err := service.ToGBP(ctx, eur, friday)
		if err != nil {
			t.Fatalf("ToGBP(%+v) error = %v", eur, err)
		}
		if roundTrip.Currency != "GBP" {
			t.Fatalf("ToGBP(%+v) currency = %q, want GBP", eur, roundTrip.Currency)
		}
		if diff := absInt64ForTest(roundTrip.Amount - amount); diff > 1 {
			t.Fatalf("ToGBP(ToEUR-equivalent %d) = %d, diff = %d minor units, want <= 1", amount, roundTrip.Amount, diff)
		}
	}
}

func assertRat(t *testing.T, rate Rate, want string) {
	t.Helper()

	got, err := rate.Rat()
	if err != nil {
		t.Fatalf("Rate.Rat() error = %v", err)
	}
	wantRat, ok := new(big.Rat).SetString(want)
	if !ok {
		t.Fatalf("invalid wanted rat %q", want)
	}
	if got.Cmp(wantRat) != 0 {
		t.Fatalf("Rate.Rat() = %s, want %s", got.String(), wantRat.String())
	}
}

func newTestRateService(t *testing.T, date time.Time) *Service {
	t.Helper()

	store := newMemoryRateReader()
	store.seed(t, date, "GBP", "0.8")
	store.seed(t, date, "USD", "1.25")
	return NewService(store, clock.NewFake(date.Add(12*time.Hour)))
}

type memoryRateReader struct {
	rates map[string]StoredECBRate
}

func newMemoryRateReader() *memoryRateReader {
	return &memoryRateReader{rates: make(map[string]StoredECBRate)}
}

func (s *memoryRateReader) seed(t *testing.T, date time.Time, currency string, rate string) {
	t.Helper()

	normalizedCurrency, err := normalizeCurrency(currency)
	if err != nil {
		t.Fatalf("normalize currency: %v", err)
	}
	decimal, err := canonicalRateDecimal(rate)
	if err != nil {
		t.Fatalf("canonical decimal: %v", err)
	}
	stored := StoredECBRate{
		Date:     normalizeRateDate(date),
		Currency: normalizedCurrency,
		Rate:     decimal,
	}
	s.rates[memoryRateKey(stored.Date, stored.Currency)] = stored
}

func (s *memoryRateReader) ECBRate(_ context.Context, date time.Time, currency string) (StoredECBRate, error) {
	normalizedCurrency, err := normalizeCurrency(currency)
	if err != nil {
		return StoredECBRate{}, err
	}
	normalizedDate := normalizeRateDate(date)
	rate, ok := s.rates[memoryRateKey(normalizedDate, normalizedCurrency)]
	if !ok {
		return StoredECBRate{}, fmt.Errorf("moneyfx: ECB rate %s %s: %w", normalizedDate.Format(time.DateOnly), normalizedCurrency, ErrNoRate)
	}
	return rate, nil
}

func (s *memoryRateReader) ECBRateDateOnOrBefore(_ context.Context, date time.Time, minDate time.Time, currencies []string) (time.Time, bool, error) {
	normalizedDate := normalizeRateDate(date)
	normalizedMinDate := normalizeRateDate(minDate)
	required, err := normalizeCurrencyFilter(currencies)
	if err != nil {
		return time.Time{}, false, err
	}
	for _, rateDate := range s.rateDates() {
		if !rateDate.After(normalizedDate) && !rateDate.Before(normalizedMinDate) && s.hasRates(rateDate, required) {
			return rateDate, true, nil
		}
	}
	return time.Time{}, false, nil
}

func (s *memoryRateReader) LatestRateDate(context.Context) (time.Time, bool, error) {
	dates := s.rateDates()
	if len(dates) == 0 {
		return time.Time{}, false, nil
	}
	return dates[0], true, nil
}

func (s *memoryRateReader) rateDates() []time.Time {
	seen := make(map[time.Time]struct{})
	for _, rate := range s.rates {
		seen[rate.Date] = struct{}{}
	}
	dates := make([]time.Time, 0, len(seen))
	for date := range seen {
		dates = append(dates, date)
	}
	sort.Slice(dates, func(i, j int) bool {
		return dates[i].After(dates[j])
	})
	return dates
}

func (s *memoryRateReader) hasRates(date time.Time, currencies []string) bool {
	for _, currency := range currencies {
		if _, ok := s.rates[memoryRateKey(date, currency)]; !ok {
			return false
		}
	}
	return true
}

func memoryRateKey(date time.Time, currency string) string {
	return normalizeRateDate(date).Format(time.DateOnly) + "/" + currency
}

func absInt64ForTest(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
