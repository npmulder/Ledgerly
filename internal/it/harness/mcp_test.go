//go:build integration

package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/banking"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

func TestMCPReadToolsAgainstHarness(t *testing.T) {
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
		ID:        "mcp-read-feed",
		Date:      time.Date(2025, 5, 2, 12, 0, 0, 0, time.UTC),
		Payee:     "Contoso GmbH",
		Reference: "INV-2025-01",
		Amount:    money.Money{Amount: 450_000, Currency: "EUR"},
	})
	mustRecordDashboardSuggestion(t, ctx, bankingService, txnID, banking.SuggestionKindInvoiceMatch, 0.98, sent.ID, "invoice match")

	readOnlyPAT := createReadOnlyPAT(t, h)
	configPath := writeCLIConfig(t, h.BaseURL, readOnlyPAT)
	repoRoot := findHarnessRepoRoot(t)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"harness","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"list_invoices","method":"tools/call","params":{"name":"list_invoices","arguments":{"status":["sent"]}}}`,
		`{"jsonrpc":"2.0","id":"get_invoice","method":"tools/call","params":{"name":"get_invoice","arguments":{"id":"` + sent.ID + `"}}}`,
		`{"jsonrpc":"2.0","id":"advisor_insights","method":"tools/call","params":{"name":"advisor_insights","arguments":{"surface":"invoices"}}}`,
		`{"jsonrpc":"2.0","id":"dividend_headroom","method":"tools/call","params":{"name":"dividend_headroom","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":"dla_balance","method":"tools/call","params":{"name":"dla_balance","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":"dla_ledger","method":"tools/call","params":{"name":"dla_ledger","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":"profit_and_loss","method":"tools/call","params":{"name":"profit_and_loss","arguments":{"period":{"from":"2025-04-01","to":"2025-06-30"}}}}`,
		`{"jsonrpc":"2.0","id":"vat_position","method":"tools/call","params":{"name":"vat_position","arguments":{"period":"2025-Q2"}}}`,
		`{"jsonrpc":"2.0","id":"filing_calendar","method":"tools/call","params":{"name":"filing_calendar","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":"bank_review_queue","method":"tools/call","params":{"name":"bank_review_queue","arguments":{}}}`,
		"",
	}, "\n")

	responses := runLedgerlyMCP(t, repoRoot, configPath, input)
	assertHarnessToolNames(t, responses["2"])

	expected := map[string]string{
		`"list_invoices"`:     h.BaseURL + "/api/invoicing/invoices?status=sent",
		`"get_invoice"`:       h.BaseURL + "/api/invoicing/invoices/" + url.PathEscape(sent.ID),
		`"advisor_insights"`:  h.BaseURL + "/api/advisor/insights?surface=invoices",
		`"dividend_headroom"`: h.BaseURL + "/api/dividends/headroom",
		`"dla_balance"`:       h.BaseURL + "/api/dla/balance",
		`"dla_ledger"`:        h.BaseURL + "/api/dla/ledger",
		`"profit_and_loss"`:   h.BaseURL + "/api/reports/pl?from=2025-04-01&to=2025-06-30",
		`"vat_position"`:      h.BaseURL + "/api/reports/vat?period=2025-Q2",
		`"filing_calendar"`:   h.BaseURL + "/api/reports/calendar",
		`"bank_review_queue"`: h.BaseURL + "/api/banking/review",
	}
	for id, endpoint := range expected {
		assertHarnessMCPMatchesHTTP(t, responses[id], h.Client, readOnlyPAT, endpoint)
	}
}

func runLedgerlyMCP(t *testing.T, repoRoot string, configPath string, input string) map[string]mcpHarnessResponse {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./cmd/ledgerly", "--config", configPath, "mcp")
	cmd.Dir = repoRoot
	cmd.Stdin = strings.NewReader(input)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run ./cmd/ledgerly mcp failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	responses := map[string]mcpHarnessResponse{}
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var response mcpHarnessResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("decode MCP response %q: %v", line, err)
		}
		responses[string(response.ID)] = response
	}
	return responses
}

func assertHarnessToolNames(t *testing.T, response mcpHarnessResponse) {
	t.Helper()
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	decodeHarnessResult(t, response, &result)
	got := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		got = append(got, tool.Name)
	}
	want := []string{
		"list_invoices",
		"get_invoice",
		"advisor_insights",
		"dividend_headroom",
		"dla_balance",
		"dla_ledger",
		"profit_and_loss",
		"vat_position",
		"filing_calendar",
		"bank_review_queue",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tool names = %#v, want %#v", got, want)
	}
}

func assertHarnessMCPMatchesHTTP(t *testing.T, response mcpHarnessResponse, client *http.Client, token string, endpoint string) {
	t.Helper()
	var result struct {
		StructuredContent json.RawMessage `json:"structuredContent"`
	}
	decodeHarnessResult(t, response, &result)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("new HTTP request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", endpoint, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", endpoint, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d body=%s", endpoint, resp.StatusCode, string(body))
	}

	var got any
	if err := json.Unmarshal(result.StructuredContent, &got); err != nil {
		t.Fatalf("decode MCP structuredContent: %v\n%s", err, string(result.StructuredContent))
	}
	var want any
	if err := json.Unmarshal(body, &want); err != nil {
		t.Fatalf("decode HTTP body: %v\n%s", err, string(body))
	}
	if !reflect.DeepEqual(got, want) {
		gotBytes, _ := json.MarshalIndent(got, "", "  ")
		wantBytes, _ := json.MarshalIndent(want, "", "  ")
		t.Fatalf("MCP structuredContent does not match HTTP %s\n--- got ---\n%s\n--- want ---\n%s", endpoint, string(gotBytes), string(wantBytes))
	}
}

func decodeHarnessResult(t *testing.T, response mcpHarnessResponse, target any) {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("MCP error = %+v", response.Error)
	}
	if err := json.Unmarshal(response.Result, target); err != nil {
		t.Fatalf("decode MCP result: %v\n%s", err, string(response.Result))
	}
}

type mcpHarnessResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
