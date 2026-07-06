package moneyfx

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/clock"
)

const (
	// ModuleName is the database schema and event namespace for moneyfx.
	ModuleName = "moneyfx"

	// ECBFetchJobName is the canonical platform cron job name.
	ECBFetchJobName = "moneyfx.ecb-fetch"

	// ECBFetchSchedule runs after the ECB daily reference rates are normally
	// published in Frankfurt on business days.
	ECBFetchSchedule = "CRON_TZ=Europe/Paris 15 16 * * MON-FRI"

	DefaultECBDailyURL     = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"
	DefaultECBHTTPTimeout  = 10 * time.Second
	DefaultECBRetries      = 3
	DefaultECBRetryBackoff = 250 * time.Millisecond
)

const (
	// RatesStaleName is the bus event name for stale ECB-rate facts.
	RatesStaleName = "moneyfx.RatesStale"

	ratesStaleEventName = RatesStaleName
)

// RatesStale is published after a failed ECB run when stored rates are more
// than three calendar days behind the current ECB date.
type RatesStale struct {
	LastDate time.Time
}

// Name implements bus.Event.
func (RatesStale) Name() string {
	return ratesStaleEventName
}

// ECBRate is one EUR-base reference rate published by the ECB.
type ECBRate struct {
	Date     time.Time
	Currency string
	Rate     string
}

// ECBDailyRates is the parsed result of one ECB XML document.
type ECBDailyRates struct {
	Rates []ECBRate
}

// SleepFunc waits between retry attempts.
type SleepFunc func(context.Context, time.Duration) error

type ecbRateStore interface {
	StoreECBRates(context.Context, []ECBRate) error
	LatestRateDate(context.Context) (time.Time, bool, error)
}

// ECBFetcherConfig controls ECB ingestion.
type ECBFetcherConfig struct {
	Pool *pgxpool.Pool
	Bus  *bus.Bus

	rateStore ecbRateStore

	Clock    clock.Clock
	Location *time.Location

	FeedURL     string
	HTTPClient  *nethttp.Client
	HTTPTimeout time.Duration

	Retries      int
	RetryBackoff time.Duration
	Sleep        SleepFunc
}

// ECBFetcher owns the daily ECB ingestion job.
type ECBFetcher struct {
	store ecbRateStore
	bus   *bus.Bus

	clock    clock.Clock
	location *time.Location

	feedURL string
	client  *nethttp.Client

	retries      int
	retryBackoff time.Duration
	sleep        SleepFunc
}

// NewECBFetcher creates a retrying ECB ingestion job.
func NewECBFetcher(cfg ECBFetcherConfig) (*ECBFetcher, error) {
	store := cfg.rateStore
	if store == nil && cfg.Pool != nil {
		store = NewStore(cfg.Pool)
	}
	if store == nil {
		return nil, fmt.Errorf("moneyfx: ECB fetcher requires pool")
	}

	clk := cfg.Clock
	if clk == nil {
		clk = clock.New()
	}
	location := cfg.Location
	if location == nil {
		location = ECBLocation()
	}

	feedURL := strings.TrimSpace(cfg.FeedURL)
	if feedURL == "" {
		feedURL = DefaultECBDailyURL
	}

	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = DefaultECBHTTPTimeout
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &nethttp.Client{Timeout: timeout}
	} else {
		copied := *client
		if copied.Timeout <= 0 {
			copied.Timeout = timeout
		}
		client = &copied
	}

	retries := cfg.Retries
	if retries == 0 {
		retries = DefaultECBRetries
	}
	if retries < 0 {
		retries = 0
	}

	backoff := cfg.RetryBackoff
	if backoff == 0 {
		backoff = DefaultECBRetryBackoff
	}
	if backoff < 0 {
		backoff = 0
	}

	sleep := cfg.Sleep
	if sleep == nil {
		sleep = sleepContext
	}

	return &ECBFetcher{
		store:        store,
		bus:          cfg.Bus,
		clock:        clk,
		location:     location,
		feedURL:      feedURL,
		client:       client,
		retries:      retries,
		retryBackoff: backoff,
		sleep:        sleep,
	}, nil
}

// ECBLocation returns the civil time zone used for ECB scheduling and stale
// date checks.
func ECBLocation() *time.Location {
	location, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		return time.FixedZone("CET", 3600)
	}
	return location
}

// Run fetches, parses, and stores the latest ECB XML feed.
func (f *ECBFetcher) Run(ctx context.Context) error {
	if f == nil {
		return fmt.Errorf("moneyfx: nil ECB fetcher")
	}

	daily, err := f.fetchWithRetry(ctx)
	if err == nil {
		err = f.store.StoreECBRates(ctx, daily.Rates)
	}
	if err == nil {
		return nil
	}

	if staleErr := f.publishStaleIfNeeded(ctx); staleErr != nil {
		return errors.Join(err, staleErr)
	}
	return err
}

func (f *ECBFetcher) fetchWithRetry(ctx context.Context) (ECBDailyRates, error) {
	var lastErr error
	for attempt := 0; attempt <= f.retries; attempt++ {
		daily, err := FetchECBDaily(ctx, f.client, f.feedURL)
		if err == nil {
			return daily, nil
		}
		lastErr = err
		if attempt == f.retries {
			break
		}
		if err := f.sleep(ctx, f.retryBackoff); err != nil {
			return ECBDailyRates{}, errors.Join(lastErr, err)
		}
	}
	return ECBDailyRates{}, fmt.Errorf("moneyfx: fetch ECB daily rates after %d attempt(s): %w", f.retries+1, lastErr)
}

func (f *ECBFetcher) publishStaleIfNeeded(ctx context.Context) error {
	if f.bus == nil {
		return nil
	}

	lastDate, ok, err := f.store.LatestRateDate(ctx)
	if err != nil {
		return err
	}
	if !ok || !ratesAreStale(lastDate, f.clock.Now(), f.location) {
		return nil
	}
	return f.bus.Publish(ctx, nil, RatesStale{LastDate: normalizeRateDate(lastDate)})
}

func ratesAreStale(lastDate time.Time, now time.Time, location *time.Location) bool {
	if location == nil {
		location = time.UTC
	}

	today := civilDateIn(now, location)
	if today.Weekday() == time.Saturday || today.Weekday() == time.Sunday {
		return false
	}
	last := civilDateIn(lastDate, location)
	if !last.Before(today) {
		return false
	}
	return int(today.Sub(last).Hours()/24) > 3
}

func civilDateIn(value time.Time, location *time.Location) time.Time {
	year, month, day := value.In(location).Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// FetchECBDaily downloads and parses the ECB daily XML feed.
func FetchECBDaily(ctx context.Context, client *nethttp.Client, url string) (ECBDailyRates, error) {
	if client == nil {
		client = &nethttp.Client{Timeout: DefaultECBHTTPTimeout}
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return ECBDailyRates{}, fmt.Errorf("moneyfx: ECB feed URL is required")
	}

	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, url, nil)
	if err != nil {
		return ECBDailyRates{}, fmt.Errorf("moneyfx: create ECB request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return ECBDailyRates{}, fmt.Errorf("moneyfx: request ECB feed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ECBDailyRates{}, fmt.Errorf("moneyfx: ECB feed status %d", resp.StatusCode)
	}

	daily, err := ParseECBDailyXML(resp.Body)
	if err != nil {
		return ECBDailyRates{}, err
	}
	return daily, nil
}

// ParseECBDailyXML parses ECB eurofxref-daily XML. It does not use a currency
// allowlist, so newly published or unknown currency codes are stored normally.
func ParseECBDailyXML(r io.Reader) (ECBDailyRates, error) {
	decoder := xml.NewDecoder(r)
	var (
		currentDate time.Time
		dateStack   []time.Time
		rates       []ECBRate
	)

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ECBDailyRates{}, fmt.Errorf("moneyfx: parse ECB XML: %w", err)
		}

		switch token := token.(type) {
		case xml.StartElement:
			if token.Name.Local != "Cube" {
				continue
			}

			dateStack = append(dateStack, currentDate)
			if value, ok := attr(token, "time"); ok {
				date, err := time.Parse(time.DateOnly, strings.TrimSpace(value))
				if err != nil {
					return ECBDailyRates{}, fmt.Errorf("moneyfx: parse ECB rate date %q: %w", value, err)
				}
				currentDate = normalizeRateDate(date)
			}

			currency, hasCurrency := attr(token, "currency")
			rate, hasRate := attr(token, "rate")
			if hasCurrency || hasRate {
				if !hasCurrency || !hasRate {
					return ECBDailyRates{}, fmt.Errorf("moneyfx: ECB rate cube requires currency and rate")
				}
				if currentDate.IsZero() {
					return ECBDailyRates{}, fmt.Errorf("moneyfx: ECB rate cube missing date")
				}

				normalizedCurrency, err := normalizeCurrency(currency)
				if err != nil {
					return ECBDailyRates{}, err
				}
				normalizedRate, err := canonicalRateDecimal(rate)
				if err != nil {
					return ECBDailyRates{}, err
				}
				rates = append(rates, ECBRate{
					Date:     currentDate,
					Currency: normalizedCurrency,
					Rate:     normalizedRate,
				})
			}
		case xml.EndElement:
			if token.Name.Local != "Cube" || len(dateStack) == 0 {
				continue
			}
			currentDate = dateStack[len(dateStack)-1]
			dateStack = dateStack[:len(dateStack)-1]
		}
	}

	if len(rates) == 0 {
		return ECBDailyRates{}, fmt.Errorf("moneyfx: ECB XML contains no rates")
	}
	return ECBDailyRates{Rates: rates}, nil
}

func attr(element xml.StartElement, name string) (string, bool) {
	for _, attr := range element.Attr {
		if attr.Name.Local == name {
			return attr.Value, true
		}
	}
	return "", false
}
