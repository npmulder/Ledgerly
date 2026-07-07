//go:build integration

package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/banking"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

func TestCLIReadCommandsAgainstHarness(t *testing.T) {
	ctx := context.Background()
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 12, 0, 0, 0, time.UTC)})
	fixtures.Company(t, h)
	fixtures.Rates(t, h)

	invoiceService := newInvoiceService(t, h)
	sent, err := invoiceService.Send(ctx, createEURInvoiceDraft(t, h, invoiceService, 450_000).ID)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	bankingService := newBankingCommandService(t, h, invoiceService)
	account := mustCreateBankingAccount(t, ctx, bankingService, "Revolut EUR", "EUR")
	txnID := importDashboardBankTxn(t, ctx, h, bankingService, account.ID, dashboardBankTxn{
		ID:        "cli-read-feed",
		Date:      time.Date(2025, 5, 2, 12, 0, 0, 0, time.UTC),
		Payee:     "Contoso GmbH",
		Reference: "INV-2025-01",
		Amount:    money.Money{Amount: 450_000, Currency: "EUR"},
	})
	mustRecordDashboardSuggestion(t, ctx, bankingService, txnID, banking.SuggestionKindInvoiceMatch, 0.98, sent.ID, "invoice match")

	configPath := writeCLIConfig(t, h.BaseURL, createReadOnlyPAT(t, h))
	repoRoot := findHarnessRepoRoot(t)

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "invoice list table", args: []string{"invoice", "list", "--status", "sent"}, want: []string{"NUMBER", "INV-2025-01", "Contoso GmbH", "Totals:"}},
		{name: "invoice list json", args: []string{"--json", "invoice", "list", "--status", "sent"}, want: []string{`"invoices": [`, `"number": "INV-2025-01"`}},
		{name: "invoice show", args: []string{"invoice", "show", "INV-2025-01"}, want: []string{"NUMBER", "INV-2025-01", "Monthly retainer", "GBP APPROX"}},
		{name: "client list", args: []string{"client", "list"}, want: []string{"Contoso GmbH", "reverse-charge-eu-b2b"}},
		{name: "bank accounts", args: []string{"bank", "accounts"}, want: []string{"Revolut EUR", "UNRECONCILED"}},
		{name: "bank review", args: []string{"bank", "review"}, want: []string{"match", "invoice match", "0.98"}},
		{name: "bank feed", args: []string{"bank", "feed", "--account", stringInt64(int64(account.ID))}, want: []string{"INV-2025-01", "suggested"}},
		{name: "dla ledger empty", args: []string{"dla", "ledger"}, want: []string{"No DLA ledger entries match the current filters."}},
		{name: "dla balance", args: []string{"dla", "balance"}, want: []string{"STATUS", "BALANCE"}},
		{name: "dividend headroom", args: []string{"dividend", "headroom"}, want: []string{"Available", "Financial year:"}},
		{name: "dividend history empty", args: []string{"dividend", "history"}, want: []string{"No dividend declarations found."}},
		{name: "report pl", args: []string{"report", "pl", "--from", "2025-04-01", "--to", "2025-06-30"}, want: []string{"net profit", "Tax year:"}},
		{name: "report vat", args: []string{"report", "vat", "--period", "2025-Q2"}, want: []string{"box1", "Net position"}},
		{name: "report calendar", args: []string{"report", "calendar"}, want: []string{"STATUS", "vat_return"}},
		{name: "report profit-ytd", args: []string{"report", "profit-ytd", "--tax-year", "2025-26"}, want: []string{"TAX YEAR", "PROFIT"}},
		{name: "advisor insights", args: []string{"advisor", "insights", "--surface", "invoices"}, want: []string{"No advisor insights"}},
		{name: "rates today", args: []string{"rates", "today", "--from", "EUR", "--to", "GBP"}, want: []string{"RATE", "RATE DATE"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := runLedgerlyCLI(t, repoRoot, configPath, tt.args...)
			for _, want := range tt.want {
				if !strings.Contains(output, want) {
					t.Fatalf("output missing %q\n%s", want, output)
				}
			}
		})
	}
}

func createReadOnlyPAT(t *testing.T, h *harness.Harness) string {
	t.Helper()
	body := strings.NewReader(`{"name":"CLI read integration","scope":"read-only","expires_at":"2030-01-01T00:00:00Z"}`)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, h.BaseURL+"/api/identity/pats", body)
	if err != nil {
		t.Fatalf("create PAT request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.Client.Do(req)
	if err != nil {
		t.Fatalf("create PAT: %v", err)
	}
	defer resp.Body.Close()
	var decoded struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode PAT response: %v", err)
	}
	if resp.StatusCode != http.StatusCreated || decoded.Token == "" {
		t.Fatalf("create PAT status = %d token empty=%t", resp.StatusCode, decoded.Token == "")
	}
	return decoded.Token
}

func writeCLIConfig(t *testing.T, baseURL string, token string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "url = " + strconvQuote(baseURL) + "\ntoken = " + strconvQuote(token) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write CLI config: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod CLI config: %v", err)
	}
	return path
}

func runLedgerlyCLI(t *testing.T, repoRoot string, configPath string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fullArgs := append([]string{"run", "./cmd/ledgerly", "--config", configPath}, args...)
	cmd := exec.CommandContext(ctx, "go", fullArgs...)
	cmd.Dir = repoRoot
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("go %s failed: %v\n%s", strings.Join(fullArgs, " "), err, output.String())
	}
	return output.String()
}

func findHarnessRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root with go.mod not found")
		}
		dir = parent
	}
}

func stringInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
