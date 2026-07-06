package moneyfx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

func TestParseECBDailyXMLReadsRealSnapshot(t *testing.T) {
	t.Parallel()

	file, err := os.Open("testdata/eurofxref-daily-real.xml")
	if err != nil {
		t.Fatalf("open real fixture: %v", err)
	}
	defer func() {
		_ = file.Close()
	}()

	daily, err := ParseECBDailyXML(file)
	if err != nil {
		t.Fatalf("ParseECBDailyXML() error = %v", err)
	}
	if len(daily.Rates) == 0 {
		t.Fatal("ParseECBDailyXML() returned no rates")
	}
	if _, ok := findRate(daily.Rates, "GBP"); !ok {
		t.Fatalf("real fixture rates missing GBP: %+v", daily.Rates)
	}
}

func TestECBFetcherHappyPathStoresRatesIncludingUnknownCurrency(t *testing.T) {
	t.Parallel()

	store := newMemoryECBStore()
	eventBus, events := ratesUpdatedCaptureBus(t)
	server := fixtureServer(t, http.StatusOK, "testdata/eurofxref-daily-unknown.xml")
	defer server.Close()

	fetcher := newTestFetcher(t, store, server, eventBus, clock.NewFake(time.Date(2030, 1, 2, 16, 20, 0, 0, time.UTC)))
	if err := fetcher.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got := store.count(); got != 2 {
		t.Fatalf("stored rates = %d, want 2", got)
	}
	gbp, ok := store.rate(time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC), "GBP")
	if !ok {
		t.Fatal("GBP rate was not stored")
	}
	if gbp.Rate != "0.85423" {
		t.Fatalf("GBP rate = %q, want 0.85423", gbp.Rate)
	}
	if _, ok := store.rate(time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC), "XBT"); !ok {
		t.Fatal("unknown XBT rate was not stored")
	}
	if len(*events) != 1 {
		t.Fatalf("rates updated events = %d, want 1", len(*events))
	}
	if got := (*events)[0].RateDate.Format(time.DateOnly); got != "2030-01-02" {
		t.Fatalf("RatesUpdated.RateDate = %s, want 2030-01-02", got)
	}
}

func TestECBFetcherMalformedXMLReturnsErrorWithoutStore(t *testing.T) {
	t.Parallel()

	store := newMemoryECBStore()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<gesmes:Envelope><Cube><Cube time="2030-01-02"><Cube currency="GBP" rate="0.85423">`))
	}))
	defer server.Close()

	fetcher := newTestFetcher(t, store, server, nil, clock.NewFake(time.Date(2030, 1, 2, 16, 20, 0, 0, time.UTC)))
	err := fetcher.Run(context.Background())
	if err == nil {
		t.Fatal("Run() error = nil, want malformed XML error")
	}
	if store.storeCalls() != 0 {
		t.Fatalf("store calls = %d, want 0", store.storeCalls())
	}
	if got := store.count(); got != 0 {
		t.Fatalf("stored rates after malformed XML = %d, want 0", got)
	}
}

func TestECBFetcherRetriesThenStores(t *testing.T) {
	t.Parallel()

	store := newMemoryECBStore()
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(w, "try again", http.StatusBadGateway)
			return
		}
		http.ServeFile(w, r, "testdata/eurofxref-daily-unknown.xml")
	}))
	defer server.Close()

	fetcher := newTestFetcher(t, store, server, nil, clock.NewFake(time.Date(2030, 1, 2, 16, 20, 0, 0, time.UTC)))
	var sleeps []time.Duration
	fetcher.retries = 3
	fetcher.retryBackoff = 25 * time.Millisecond
	fetcher.sleep = func(_ context.Context, duration time.Duration) error {
		sleeps = append(sleeps, duration)
		return nil
	}
	if err := fetcher.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if len(sleeps) != 2 || sleeps[0] != 25*time.Millisecond || sleeps[1] != 25*time.Millisecond {
		t.Fatalf("retry sleeps = %v, want two 25ms backoffs", sleeps)
	}
	if got := store.count(); got != 2 {
		t.Fatalf("stored rates = %d, want 2", got)
	}
}

func TestECBFetcherIdempotentUpsert(t *testing.T) {
	t.Parallel()

	store := newMemoryECBStore()
	server := fixtureServer(t, http.StatusOK, "testdata/eurofxref-daily-unknown.xml")
	defer server.Close()

	fetcher := newTestFetcher(t, store, server, nil, clock.NewFake(time.Date(2030, 1, 2, 16, 20, 0, 0, time.UTC)))
	if err := fetcher.Run(context.Background()); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if err := fetcher.Run(context.Background()); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if got := store.count(); got != 2 {
		t.Fatalf("stored rates after repeat fetch = %d, want 2", got)
	}
	if got := store.storeCalls(); got != 2 {
		t.Fatalf("store calls = %d, want 2", got)
	}
}

func TestECBFetcherPublishesRatesStaleAfterFailedRun(t *testing.T) {
	t.Parallel()

	store := newMemoryECBStore()
	store.seed(ECBRate{
		Date:     time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC),
		Currency: "GBP",
		Rate:     "0.85423",
	})
	eventBus, events := staleCaptureBus(t)
	server := statusServer(http.StatusBadGateway)
	defer server.Close()

	fetcher := newTestFetcher(t, store, server, eventBus, clock.NewFake(time.Date(2030, 1, 7, 12, 0, 0, 0, time.UTC)))
	err := fetcher.Run(context.Background())
	if err == nil {
		t.Fatal("Run() error = nil, want failed fetch error")
	}
	if len(*events) != 1 {
		t.Fatalf("stale events = %d, want 1", len(*events))
	}
	if got := (*events)[0].LastDate.Format(time.DateOnly); got != "2030-01-02" {
		t.Fatalf("RatesStale.LastDate = %s, want 2030-01-02", got)
	}
}

func TestECBFetcherDoesNotPublishRatesStaleOnWeekend(t *testing.T) {
	t.Parallel()

	store := newMemoryECBStore()
	store.seed(ECBRate{
		Date:     time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC),
		Currency: "GBP",
		Rate:     "0.85423",
	})
	eventBus, events := staleCaptureBus(t)
	server := statusServer(http.StatusBadGateway)
	defer server.Close()

	fetcher := newTestFetcher(t, store, server, eventBus, clock.NewFake(time.Date(2030, 1, 6, 12, 0, 0, 0, time.UTC)))
	err := fetcher.Run(context.Background())
	if err == nil {
		t.Fatal("Run() error = nil, want failed fetch error")
	}
	if len(*events) != 0 {
		t.Fatalf("stale events = %d, want 0", len(*events))
	}
}

func TestECBFetcherRecoveryRunPublishesNoStaleEvent(t *testing.T) {
	t.Parallel()

	store := newMemoryECBStore()
	store.seed(ECBRate{
		Date:     time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC),
		Currency: "GBP",
		Rate:     "0.85423",
	})
	eventBus, events := staleCaptureBus(t)
	server := fixtureServer(t, http.StatusOK, "testdata/eurofxref-daily-unknown.xml")
	defer server.Close()

	fetcher := newTestFetcher(t, store, server, eventBus, clock.NewFake(time.Date(2030, 1, 7, 12, 0, 0, 0, time.UTC)))
	if err := fetcher.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(*events) != 0 {
		t.Fatalf("stale events = %d, want 0", len(*events))
	}
}

func TestCanonicalRateDecimalPreservesExactDecimalString(t *testing.T) {
	t.Parallel()

	got, err := canonicalRateDecimal("0.85423000")
	if err != nil {
		t.Fatalf("canonicalRateDecimal() error = %v", err)
	}
	if got != "0.85423" {
		t.Fatalf("canonicalRateDecimal() = %q, want 0.85423", got)
	}

	rat, err := (StoredECBRate{Rate: got}).Rat()
	if err != nil {
		t.Fatalf("StoredECBRate.Rat() error = %v", err)
	}
	if rat.FloatString(5) != "0.85423" {
		t.Fatalf("StoredECBRate.Rat() = %s, want 0.85423", rat.FloatString(5))
	}
}

func newTestFetcher(t *testing.T, store *memoryECBStore, server *httptest.Server, eventBus *bus.Bus, clk clock.Clock) *ECBFetcher {
	t.Helper()

	fetcher, err := NewECBFetcher(ECBFetcherConfig{
		Bus:          eventBus,
		rateStore:    store,
		Clock:        clk,
		Location:     time.UTC,
		FeedURL:      server.URL,
		HTTPClient:   server.Client(),
		Retries:      -1,
		RetryBackoff: -1,
	})
	if err != nil {
		t.Fatalf("NewECBFetcher() error = %v", err)
	}
	return fetcher
}

func fixtureServer(t *testing.T, status int, path string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			http.Error(w, http.StatusText(status), status)
			return
		}
		http.ServeFile(w, r, path)
	}))
}

func statusServer(status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, http.StatusText(status), status)
	}))
}

func staleCaptureBus(t *testing.T) (*bus.Bus, *[]RatesStale) {
	t.Helper()

	var events []RatesStale
	eventBus := bus.New()
	eventBus.Subscribe(ratesStaleEventName, func(_ context.Context, _ db.Tx, evt bus.Event) error {
		stale, ok := evt.(RatesStale)
		if !ok {
			return errors.New("unexpected event")
		}
		events = append(events, stale)
		return nil
	})
	return eventBus, &events
}

func ratesUpdatedCaptureBus(t *testing.T) (*bus.Bus, *[]RatesUpdated) {
	t.Helper()

	var events []RatesUpdated
	eventBus := bus.New()
	eventBus.Subscribe(RatesUpdatedName, func(_ context.Context, _ db.Tx, evt bus.Event) error {
		updated, ok := evt.(RatesUpdated)
		if !ok {
			return errors.New("unexpected event")
		}
		events = append(events, updated)
		return nil
	})
	return eventBus, &events
}

func findRate(rates []ECBRate, currency string) (ECBRate, bool) {
	for _, rate := range rates {
		if rate.Currency == currency {
			return rate, true
		}
	}
	return ECBRate{}, false
}

type memoryECBStore struct {
	mu          sync.Mutex
	rates       map[string]ECBRate
	latest      time.Time
	stores      int
	storeErr    error
	latestError error
}

func newMemoryECBStore() *memoryECBStore {
	return &memoryECBStore{rates: make(map[string]ECBRate)}
}

func (s *memoryECBStore) StoreECBRates(_ context.Context, rates []ECBRate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.stores++
	if s.storeErr != nil {
		return s.storeErr
	}
	normalized, err := normalizeECBRates(rates)
	if err != nil {
		return err
	}
	for _, rate := range normalized {
		s.rates[rateKey(rate.Date, rate.Currency)] = rate
		if s.latest.IsZero() || rate.Date.After(s.latest) {
			s.latest = rate.Date
		}
	}
	return nil
}

func (s *memoryECBStore) LatestRateDate(context.Context) (time.Time, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.latestError != nil {
		return time.Time{}, false, s.latestError
	}
	if s.latest.IsZero() {
		return time.Time{}, false, nil
	}
	return s.latest, true, nil
}

func (s *memoryECBStore) seed(rate ECBRate) {
	s.mu.Lock()
	defer s.mu.Unlock()

	normalized, err := normalizeECBRates([]ECBRate{rate})
	if err != nil {
		panic(err)
	}
	rate = normalized[0]
	s.rates[rateKey(rate.Date, rate.Currency)] = rate
	if s.latest.IsZero() || rate.Date.After(s.latest) {
		s.latest = rate.Date
	}
}

func (s *memoryECBStore) rate(date time.Time, currency string) (ECBRate, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rate, ok := s.rates[rateKey(normalizeRateDate(date), currency)]
	return rate, ok
}

func (s *memoryECBStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.rates)
}

func (s *memoryECBStore) storeCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.stores
}

func rateKey(date time.Time, currency string) string {
	return normalizeRateDate(date).Format(time.DateOnly) + "/" + currency
}
