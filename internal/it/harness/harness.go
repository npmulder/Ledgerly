// Package harness boots Ledgerly in-process for integration suites.
package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/app"
	"github.com/npmulder/ledgerly/internal/banking"
	"github.com/npmulder/ledgerly/internal/dividends"
	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	"github.com/npmulder/ledgerly/internal/platform/config"
	"github.com/npmulder/ledgerly/internal/platform/db"
	"github.com/npmulder/ledgerly/internal/platform/mail"
)

const (
	defaultOwnerEmail     = "owner@example.test"
	defaultOwnerPassword  = "correct horse battery staple"
	defaultOwnerName      = "Owner"
	defaultJobTimeout     = 10 * time.Second
	defaultTxTimeout      = 10 * time.Second
	defaultBalanceTimeout = 10 * time.Second
)

var balanceCheckAsOf = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// Options customizes an integration harness. Cron autostart defaults to off.
type Options struct {
	ClockStart    time.Time
	CronAutostart bool
	BalanceCheck  BalanceCheckOption
	Logger        *slog.Logger

	ModuleBuilders map[string]app.ModuleBuilder
	Jobs           map[string]app.Job
	MailSender     mail.Sender
}

// BalanceCheckOption controls the default teardown trial-balance assertion.
type BalanceCheckOption struct {
	disabled      bool
	justification string
}

// WithoutBalanceCheck disables the default teardown trial-balance assertion.
// Call sites must include an in-code comment explaining why the suite is allowed
// to leave ledger postings unbalanced.
func WithoutBalanceCheck(justification string) BalanceCheckOption {
	return BalanceCheckOption{disabled: true, justification: justification}
}

// Harness is a running in-process Ledgerly app.
type Harness struct {
	BaseURL         string
	Client          *nethttp.Client
	DB              *pgxpool.Pool
	BankingPool     *pgxpool.Pool
	DLAPool         *pgxpool.Pool
	DividendsPool   *pgxpool.Pool
	LedgerPool      *pgxpool.Pool
	Clock           *clock.FakeClock
	Bus             *bus.Bus
	IdentityDataDir string

	BootDuration time.Duration

	t      testing.TB
	app    *app.App
	faults *busFaults
}

// New boots the monolith against an isolated IT0-1 database.
func New(t testing.TB, opts Options) *Harness {
	t.Helper()

	start := time.Now()
	rawPool := testdb.Raw(t)
	identityPool := testdb.AsModule(t, "identity")
	bankingPool := testdb.AsModule(t, banking.ModuleName)
	dlaPool := testdb.AsModule(t, dla.ModuleName)
	dividendsPool := testdb.AsModule(t, dividends.ModuleName)
	ledgerPool := testdb.AsModule(t, ledger.ModuleName)
	moneyFXPool := testdb.AsModule(t, moneyfx.ModuleName)
	invoicingPool := testdb.AsModule(t, "invoicing")

	startAt := opts.ClockStart
	if startAt.IsZero() {
		startAt = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	}
	fakeClock := clock.NewFake(startAt)
	faults := newBusFaults()
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	identityDataDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	built, err := app.Build(ctx, app.Config{
		Runtime: config.Config{
			Env:      config.EnvDev,
			DataDir:  identityDataDir,
			LogLevel: slog.LevelError,
		},
		Version: "test",
	}, app.Dependencies{
		Logger:              logger,
		Clock:               fakeClock,
		HealthDB:            pgxPinger{pool: rawPool},
		IdentityPool:        identityPool,
		BankingPool:         bankingPool,
		DLAPool:             dlaPool,
		DividendsPool:       dividendsPool,
		LedgerPool:          ledgerPool,
		MoneyFXPool:         moneyFXPool,
		InvoicingPool:       invoicingPool,
		InvoicingMailSender: opts.MailSender,
		BusOptions: []bus.Option{
			bus.WithMiddleware(faults.middleware),
		},
		IdentityServiceOptions: []identity.ServiceOption{
			identity.WithPasswordParams(fastPasswordParams()),
		},
		IdentityProfileOptions: []identity.ProfileOption{
			identity.WithDataDir(identityDataDir),
		},
		ModuleBuilders: opts.ModuleBuilders,
		Jobs:           opts.Jobs,
		CronAutostart:  opts.CronAutostart,
	})
	if err != nil {
		t.Fatalf("build app harness: %v", err)
	}

	server := httptest.NewServer(built.Handler)
	baseClient := server.Client()
	cookie := registerAndLoginOwner(t, baseClient, server.URL)

	h := &Harness{
		BaseURL:         server.URL,
		Client:          authenticatedClient(baseClient, cookie),
		DB:              rawPool,
		BankingPool:     bankingPool,
		DLAPool:         dlaPool,
		DividendsPool:   dividendsPool,
		LedgerPool:      ledgerPool,
		Clock:           fakeClock,
		Bus:             built.Bus,
		IdentityDataDir: identityDataDir,
		BootDuration:    time.Since(start),
		t:               t,
		app:             built,
		faults:          faults,
	}

	t.Cleanup(func() {
		server.Close()
		if err := built.Close(); err != nil {
			t.Fatalf("close app harness: %v", err)
		}
	})
	if opts.BalanceCheck.disabled {
		justification := strings.TrimSpace(opts.BalanceCheck.justification)
		if justification == "" {
			t.Fatalf("harness: WithoutBalanceCheck requires a justification")
		}
		t.Logf("harness ledger balance check disabled: %s", justification)
	} else {
		t.Cleanup(func() {
			AssertLedgerBalanced(t, h)
		})
	}

	return h
}

// AssertLedgerBalanced fails t when any stored ledger entry leaves the ledger
// out of trial balance. Harnesses register this in cleanup by default.
func AssertLedgerBalanced(t testing.TB, h *Harness) {
	t.Helper()

	if h == nil {
		t.Fatalf("ledger balance check: nil harness")
		return
	}
	if h.LedgerPool == nil {
		t.Fatalf("ledger balance check: nil ledger pool")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultBalanceTimeout)
	defer cancel()

	report, err := ledger.New(h.LedgerPool).TrialBalance(ctx, balanceCheckAsOf)
	if err != nil {
		t.Fatalf("ledger balance check failed: %v; report=%+v", err, report)
	}
	if !report.Balanced {
		t.Fatalf("ledger balance check failed: report=%+v", report)
	}
}

// Do executes req with the harness' authenticated client. Relative request URLs
// are resolved against BaseURL.
func (h *Harness) Do(req *nethttp.Request) (*nethttp.Response, error) {
	h.t.Helper()

	if req.URL == nil {
		return nil, errors.New("harness: request URL is nil")
	}
	if !req.URL.IsAbs() {
		base, err := url.Parse(h.BaseURL)
		if err != nil {
			return nil, err
		}
		cloned := req.Clone(req.Context())
		cloned.URL = base.ResolveReference(req.URL)
		cloned.Host = cloned.URL.Host
		req = cloned
	}
	return h.Client.Do(req)
}

// Tx runs fn in a raw database transaction and fails the test on error.
func (h *Harness) Tx(fn func(context.Context, db.Tx) error) {
	h.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTxTimeout)
	defer cancel()

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		h.t.Fatalf("begin harness transaction: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	if err := fn(ctx, tx); err != nil {
		h.t.Fatalf("harness transaction step: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		h.t.Fatalf("commit harness transaction: %v", err)
	}
	committed = true
}

// RunJob runs a named deterministic app job.
func (h *Harness) RunJob(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultJobTimeout)
	defer cancel()
	return h.app.RunJob(ctx, name)
}

// FailNextBusSubscriber makes the next subscriber for eventName return err after
// its normal handler runs.
func (h *Harness) FailNextBusSubscriber(eventName string, err error) {
	h.t.Helper()
	h.faults.failNext(eventName, err)
}

func fastPasswordParams() identity.PasswordParams {
	return identity.PasswordParams{
		MemoryKiB: 1024,
		Time:      1,
		Threads:   1,
		SaltLen:   16,
		KeyLen:    32,
	}
}

func registerAndLoginOwner(t testing.TB, client *nethttp.Client, baseURL string) *nethttp.Cookie {
	t.Helper()

	postJSON(t, client, baseURL+"/api/identity/register", map[string]string{
		"email":    defaultOwnerEmail,
		"password": defaultOwnerPassword,
		"name":     defaultOwnerName,
	}, nethttp.StatusCreated)

	_, cookies := postJSON(t, client, baseURL+"/api/identity/login", map[string]string{
		"email":    defaultOwnerEmail,
		"password": defaultOwnerPassword,
	}, nethttp.StatusOK)

	for _, cookie := range cookies {
		if cookie.Name == identity.SessionCookieName {
			return cookie
		}
	}
	t.Fatalf("login response did not set %s cookie", identity.SessionCookieName)
	return nil
}

func postJSON(t testing.TB, client *nethttp.Client, url string, requestBody any, wantStatus int) ([]byte, []*nethttp.Cookie) {
	t.Helper()

	payload, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST response: %v", err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST %s status = %d, want %d; body=%s", url, resp.StatusCode, wantStatus, string(bodyBytes))
	}
	return bodyBytes, resp.Cookies()
}

func authenticatedClient(base *nethttp.Client, cookie *nethttp.Cookie) *nethttp.Client {
	transport := base.Transport
	if transport == nil {
		transport = nethttp.DefaultTransport
	}
	return &nethttp.Client{
		Transport: &sessionTransport{
			base:   transport,
			cookie: cloneCookie(cookie),
		},
		CheckRedirect: base.CheckRedirect,
		Jar:           base.Jar,
		Timeout:       base.Timeout,
	}
}

type sessionTransport struct {
	base nethttp.RoundTripper

	mu     sync.Mutex
	cookie *nethttp.Cookie
}

func (t *sessionTransport) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) {
	if cookie := t.currentCookie(); cookie != nil {
		cloned := req.Clone(req.Context())
		cloned.Header = req.Header.Clone()
		cloned.AddCookie(cookie)
		req = cloned
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == identity.SessionCookieName {
			t.setCookie(cookie)
		}
	}
	return resp, nil
}

func (t *sessionTransport) currentCookie() *nethttp.Cookie {
	t.mu.Lock()
	defer t.mu.Unlock()
	return cloneCookie(t.cookie)
}

func (t *sessionTransport) setCookie(cookie *nethttp.Cookie) {
	t.mu.Lock()
	t.cookie = cloneCookie(cookie)
	t.mu.Unlock()
}

func cloneCookie(cookie *nethttp.Cookie) *nethttp.Cookie {
	if cookie == nil {
		return nil
	}
	clone := *cookie
	return &clone
}

type busFaults struct {
	mu   sync.Mutex
	next map[string]error
}

type pgxPinger struct {
	pool *pgxpool.Pool
}

func (p pgxPinger) PingContext(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

func newBusFaults() *busFaults {
	return &busFaults{next: make(map[string]error)}
}

func (f *busFaults) failNext(eventName string, err error) {
	if err == nil {
		err = fmt.Errorf("forced subscriber failure for %s", eventName)
	}

	f.mu.Lock()
	f.next[eventName] = err
	f.mu.Unlock()
}

func (f *busFaults) middleware(eventName string, next bus.Handler) bus.Handler {
	return func(ctx context.Context, tx db.Tx, evt bus.Event) error {
		if err := next(ctx, tx, evt); err != nil {
			return err
		}

		f.mu.Lock()
		err := f.next[eventName]
		delete(f.next, eventName)
		f.mu.Unlock()
		return err
	}
}
