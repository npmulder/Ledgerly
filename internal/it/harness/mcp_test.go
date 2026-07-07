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
	"github.com/npmulder/ledgerly/internal/platform/mail"
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

func TestMCPWriteToolsAgainstHarness(t *testing.T) {
	fakeMailer := mail.NewMemorySender()
	h := harness.New(t, harness.Options{
		ClockStart: time.Date(2025, 5, 11, 9, 0, 0, 0, time.UTC),
		MailSender: fakeMailer,
	})
	fixtures.Company(t, h)
	fixtures.Rates(t, h)
	fabrikam := fixtures.Fabrikam(t, h)

	overdueDraft := createDraftInvoiceViaHTTP(t, h, fabrikam.ID)
	patched := performInvoiceRequest(t, h, http.MethodPatch, "/api/invoicing/invoices/"+overdueDraft.ID, mustInvoiceJSON(t, map[string]any{
		"issue_date": "2025-05-01",
		"due_date":   "2025-05-02",
		"lines": []map[string]any{
			{
				"id":          "line-mcp-reminder",
				"description": "Overdue support",
				"qty":         "1",
				"unit_price": map[string]any{
					"amount":   int64(60_000),
					"currency": "GBP",
				},
			},
		},
	}), true)
	if patched.StatusCode != http.StatusOK {
		t.Fatalf("patch overdue invoice status = %d, want %d; body=%s", patched.StatusCode, http.StatusOK, patched.BodyString())
	}
	send := performInvoiceRequest(t, h, http.MethodPost, "/api/invoicing/invoices/"+overdueDraft.ID+"/send", nil, true)
	if send.StatusCode != http.StatusOK {
		t.Fatalf("send overdue invoice status = %d, want %d; body=%s", send.StatusCode, http.StatusOK, send.BodyString())
	}
	sent := decodeSendInvoiceResponse(t, send)
	storeInvoicePDFAssetForReminderTest(t, h, sent.Invoice.ID, []byte("%PDF-1.4\n% mcp reminder fixture\n"))

	fullPAT := createFullPAT(t, h)
	configPath := writeCLIConfig(t, h.BaseURL, fullPAT)
	repoRoot := findHarnessRepoRoot(t)
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"harness","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"create","method":"tools/call","params":{"name":"create_draft_invoice","arguments":{"clientName":"Fabrikam Ltd","lines":[{"description":"Next month retainer","qty":"1","unitPriceMinor":125000,"currency":"GBP"}]}}}`,
		`{"jsonrpc":"2.0","id":"remind","method":"tools/call","params":{"name":"send_invoice_reminder","arguments":{"invoiceId":"` + sent.Invoice.ID + `"}}}`,
		"",
	}, "\n")
	responses, stderr := runLedgerlyMCPWithStderr(t, repoRoot, configPath, input)
	assertHarnessInstructions(t, responses["1"])
	assertHarnessToolNames(t, responses["2"])
	assertAuditLogContains(t, stderr, "create_draft_invoice", "CLI write integration")
	assertAuditLogContains(t, stderr, "send_invoice_reminder", "CLI write integration")

	var createResult struct {
		StructuredContent struct {
			DraftID   string `json:"draft_id"`
			EditorURL string `json:"editor_url"`
			Invoice   struct {
				ID     string     `json:"id"`
				Status string     `json:"status"`
				Number *string    `json:"number"`
				SentAt *time.Time `json:"sent_at"`
				Lines  []struct {
					Description string `json:"description"`
					Qty         string `json:"qty"`
					UnitPrice   struct {
						Amount   int64  `json:"amount"`
						Currency string `json:"currency"`
					} `json:"unit_price"`
				} `json:"lines"`
			} `json:"invoice"`
		} `json:"structuredContent"`
	}
	decodeHarnessResult(t, responses[`"create"`], &createResult)
	if createResult.StructuredContent.DraftID == "" || createResult.StructuredContent.Invoice.ID != createResult.StructuredContent.DraftID {
		t.Fatalf("create draft ids = structured %q invoice %q, want matching non-empty ids", createResult.StructuredContent.DraftID, createResult.StructuredContent.Invoice.ID)
	}
	wantEditorURL := h.BaseURL + "/invoices/" + url.PathEscape(createResult.StructuredContent.DraftID)
	if createResult.StructuredContent.EditorURL != wantEditorURL {
		t.Fatalf("editor_url = %q, want %q", createResult.StructuredContent.EditorURL, wantEditorURL)
	}
	if createResult.StructuredContent.Invoice.Status != "draft" || createResult.StructuredContent.Invoice.SentAt != nil || createResult.StructuredContent.Invoice.Number != nil {
		t.Fatalf("created invoice status=%q sent_at=%v number=%v, want unsent draft", createResult.StructuredContent.Invoice.Status, createResult.StructuredContent.Invoice.SentAt, createResult.StructuredContent.Invoice.Number)
	}
	if len(createResult.StructuredContent.Invoice.Lines) != 1 || createResult.StructuredContent.Invoice.Lines[0].Description != "Next month retainer" || createResult.StructuredContent.Invoice.Lines[0].UnitPrice.Amount != 125000 {
		t.Fatalf("created invoice lines = %+v, want one retainer line", createResult.StructuredContent.Invoice.Lines)
	}
	assertDraftVisibleViaAPI(t, h.BaseURL, fullPAT, createResult.StructuredContent.DraftID)

	var reminderResult struct {
		StructuredContent struct {
			Confirmation string `json:"confirmation"`
			InvoiceID    string `json:"invoice_id"`
			Reminder     struct {
				InvoiceID string    `json:"invoice_id"`
				SentAt    time.Time `json:"sent_at"`
			} `json:"reminder"`
		} `json:"structuredContent"`
	}
	decodeHarnessResult(t, responses[`"remind"`], &reminderResult)
	if reminderResult.StructuredContent.Confirmation == "" || reminderResult.StructuredContent.InvoiceID != sent.Invoice.ID || reminderResult.StructuredContent.Reminder.InvoiceID != sent.Invoice.ID || reminderResult.StructuredContent.Reminder.SentAt.IsZero() {
		t.Fatalf("reminder result = %+v, want confirmation and reminder for %s", reminderResult.StructuredContent, sent.Invoice.ID)
	}
	if got := reminderRowCount(t, sent.Invoice.ID); got != 1 {
		t.Fatalf("reminder row count = %d, want 1", got)
	}
	if got := len(fakeMailer.Messages()); got != 1 {
		t.Fatalf("mail count = %d, want 1", got)
	}

	readOnlyConfigPath := writeCLIConfig(t, h.BaseURL, createReadOnlyPAT(t, h))
	readOnlyInput := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"create-readonly","method":"tools/call","params":{"name":"create_draft_invoice","arguments":{"clientId":"` + fabrikam.ID + `","lines":[{"description":"Blocked draft","qty":1,"unitPriceMinor":1000,"currency":"GBP"}]}}}`,
		`{"jsonrpc":"2.0","id":"remind-readonly","method":"tools/call","params":{"name":"send_invoice_reminder","arguments":{"invoiceId":"` + sent.Invoice.ID + `"}}}`,
		"",
	}, "\n")
	readOnlyResponses, readOnlyStderr := runLedgerlyMCPWithStderr(t, repoRoot, readOnlyConfigPath, readOnlyInput)
	assertHarnessToolErrorContains(t, readOnlyResponses[`"create-readonly"`], "requires a full-scope personal access token")
	assertHarnessToolErrorContains(t, readOnlyResponses[`"remind-readonly"`], "requires a full-scope personal access token")
	assertAuditLogContains(t, readOnlyStderr, "create_draft_invoice", "CLI read integration")
	assertAuditLogContains(t, readOnlyStderr, "send_invoice_reminder", "CLI read integration")
	if got := reminderRowCount(t, sent.Invoice.ID); got != 1 {
		t.Fatalf("reminder row count after read-only rejection = %d, want 1", got)
	}
	if got := len(fakeMailer.Messages()); got != 1 {
		t.Fatalf("mail count after read-only rejection = %d, want 1", got)
	}
}

func runLedgerlyMCP(t *testing.T, repoRoot string, configPath string, input string) map[string]mcpHarnessResponse {
	t.Helper()
	responses, _ := runLedgerlyMCPWithStderr(t, repoRoot, configPath, input)
	return responses
}

func runLedgerlyMCPWithStderr(t *testing.T, repoRoot string, configPath string, input string) (map[string]mcpHarnessResponse, string) {
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
	return responses, stderr.String()
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
		"create_draft_invoice",
		"send_invoice_reminder",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tool names = %#v, want %#v", got, want)
	}
}

func assertHarnessInstructions(t *testing.T, response mcpHarnessResponse) {
	t.Helper()
	var result struct {
		Instructions string `json:"instructions"`
	}
	decodeHarnessResult(t, response, &result)
	for _, want := range []string{"integer minor units", "human confirms money movement", "advisor_insights", "deterministic rule outputs"} {
		if !strings.Contains(result.Instructions, want) {
			t.Fatalf("instructions missing %q: %s", want, result.Instructions)
		}
	}
}

func assertDraftVisibleViaAPI(t *testing.T, baseURL string, token string, invoiceID string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, baseURL+"/api/invoicing/invoices/"+url.PathEscape(invoiceID), nil)
	if err != nil {
		t.Fatalf("new invoice GET request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET draft invoice: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read draft invoice: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET draft invoice status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, string(body))
	}
	var invoice struct {
		Status string     `json:"status"`
		Number *string    `json:"number"`
		SentAt *time.Time `json:"sent_at"`
		Lines  []struct {
			Description string `json:"description"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(body, &invoice); err != nil {
		t.Fatalf("decode draft invoice: %v; body=%s", err, string(body))
	}
	if invoice.Status != "draft" || invoice.Number != nil || invoice.SentAt != nil || len(invoice.Lines) != 1 {
		t.Fatalf("API draft invoice = %+v, want one-line unsent draft", invoice)
	}
}

func assertHarnessToolErrorContains(t *testing.T, response mcpHarnessResponse, want string) {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("MCP protocol error = %+v, want tool error result", response.Error)
	}
	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	decodeHarnessResult(t, response, &result)
	if !result.IsError {
		t.Fatalf("isError = false, want true")
	}
	if len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, want) {
		t.Fatalf("tool error content = %+v, want %q", result.Content, want)
	}
}

func assertAuditLogContains(t *testing.T, stderr string, toolName string, patName string) {
	t.Helper()
	for _, want := range []string{"mcp tool call", "tool=" + toolName, "pat_name=\"" + patName + "\"", "duration_ms="} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("audit stderr missing %q:\n%s", want, stderr)
		}
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
