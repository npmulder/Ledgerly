//go:build integration

package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"sync"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/app"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

func TestLedgerReadSurfaceE2E(t *testing.T) {
	t.Parallel()

	h := harness.New(t, harness.Options{})

	accounts := getLedgerAccounts(t, h)
	assertLedgerAccount(t, accounts, "4000-sales")

	var txAccountCount int
	h.Tx(func(ctx context.Context, tx db.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM ledger.accounts").Scan(&txAccountCount)
	})
	if txAccountCount < len(accounts) {
		t.Fatalf("h.Tx account count = %d, want at least listed account count %d", txAccountCount, len(accounts))
	}

	trialBalance := getLedgerTrialBalance(t, h)
	if trialBalance.Status != "balanced" {
		t.Fatalf("trial balance status = %q, want balanced: %+v", trialBalance.Status, trialBalance)
	}
	if trialBalance.AsOf == "" {
		t.Fatalf("trial balance as_of is empty: %+v", trialBalance)
	}
}

func TestHarnessBootsUnderTwoSecondsAfterTemplateDB(t *testing.T) {
	t.Parallel()

	testdb.Raw(t)

	start := time.Now()
	h := harness.New(t, harness.Options{})
	elapsed := time.Since(start)
	if elapsed >= 2*time.Second {
		t.Fatalf("harness boot duration = %s, want < 2s", elapsed)
	}
	if h.BootDuration >= 2*time.Second {
		t.Fatalf("reported harness boot duration = %s, want < 2s", h.BootDuration)
	}
}

func TestParallelHarnessesDoNotInterfere(t *testing.T) {
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	queried := make(chan struct{}, 2)
	releaseAssert := make(chan struct{})
	var wait sync.Once

	waitForBoth := func() {
		wait.Do(func() {
			go func() {
				<-ready
				<-ready
				close(release)
				<-queried
				<-queried
				close(releaseAssert)
			}()
		})
	}

	for _, name := range []string{"suite_one", "suite_two"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			waitForBoth()

			h := harness.New(t, harness.Options{})
			ready <- struct{}{}
			<-release

			accounts := getLedgerAccounts(t, h)
			queried <- struct{}{}
			<-releaseAssert

			assertLedgerAccount(t, accounts, "4000-sales")
		})
	}
}

func TestClockAdvanceChangesHealthResponse(t *testing.T) {
	t.Parallel()

	start := time.Date(2040, 6, 7, 8, 9, 10, 0, time.UTC)
	h := harness.New(t, harness.Options{ClockStart: start})

	first := getHealth(t, h)
	if first.CheckedAt != start.Format(time.RFC3339Nano) {
		t.Fatalf("first checked_at = %q, want %q", first.CheckedAt, start.Format(time.RFC3339Nano))
	}

	advanced := h.Clock.Advance(3 * time.Hour)
	second := getHealth(t, h)
	if second.CheckedAt != advanced.Format(time.RFC3339Nano) {
		t.Fatalf("second checked_at = %q, want %q", second.CheckedAt, advanced.Format(time.RFC3339Nano))
	}
}

func TestRunJobExecutesRegisteredJob(t *testing.T) {
	t.Parallel()

	var ran bool
	h := harness.New(t, harness.Options{
		Jobs: map[string]app.Job{
			"probe": func(context.Context) error {
				ran = true
				return nil
			},
		},
	})

	if err := h.RunJob("probe"); err != nil {
		t.Fatalf("RunJob() error = %v", err)
	}
	if !ran {
		t.Fatal("registered job did not run")
	}
}

type ledgerAccountsResponse struct {
	Accounts []ledgerAccount `json:"accounts"`
}

type ledgerAccount struct {
	Code string `json:"code"`
}

func getLedgerAccounts(t *testing.T, h *harness.Harness) []ledgerAccount {
	t.Helper()

	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, "/api/ledger/accounts", nil)
	if err != nil {
		t.Fatalf("create GET /api/ledger/accounts request: %v", err)
	}
	bodyBytes := doRequest(t, h, req, nethttp.StatusOK)

	var response ledgerAccountsResponse
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		t.Fatalf("decode ledger accounts: %v; body=%s", err, string(bodyBytes))
	}
	return response.Accounts
}

func assertLedgerAccount(t *testing.T, accounts []ledgerAccount, code string) {
	t.Helper()

	for _, account := range accounts {
		if account.Code == code {
			return
		}
	}
	t.Fatalf("ledger account %q missing from %+v", code, accounts)
}

type ledgerTrialBalanceResponse struct {
	AsOf   string `json:"as_of"`
	Status string `json:"status"`
}

func getLedgerTrialBalance(t *testing.T, h *harness.Harness) ledgerTrialBalanceResponse {
	t.Helper()

	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, "/api/ledger/trial-balance", nil)
	if err != nil {
		t.Fatalf("create GET /api/ledger/trial-balance request: %v", err)
	}
	bodyBytes := doRequest(t, h, req, nethttp.StatusOK)

	var response ledgerTrialBalanceResponse
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		t.Fatalf("decode ledger trial balance: %v; body=%s", err, string(bodyBytes))
	}
	return response
}

type healthBody struct {
	CheckedAt string `json:"checked_at"`
}

func getHealth(t *testing.T, h *harness.Harness) healthBody {
	t.Helper()

	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, "/healthz", nil)
	if err != nil {
		t.Fatalf("create GET /healthz request: %v", err)
	}
	bodyBytes := doRequest(t, h, req, nethttp.StatusOK)

	var body healthBody
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("decode health response: %v; body=%s", err, string(bodyBytes))
	}
	return body
}

func doRequest(t *testing.T, h *harness.Harness, req *nethttp.Request, wantStatus int) []byte {
	t.Helper()

	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", req.Method, req.URL, err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; body=%s", req.Method, req.URL, resp.StatusCode, wantStatus, string(bodyBytes))
	}
	return bodyBytes
}

func doJSON(t *testing.T, h *harness.Harness, method string, path string, requestBody any) ([]byte, int) {
	t.Helper()

	payload, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req, err := nethttp.NewRequestWithContext(context.Background(), method, path, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create %s request: %v", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return bodyBytes, resp.StatusCode
}
