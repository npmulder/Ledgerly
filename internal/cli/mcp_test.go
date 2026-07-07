package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestMCPServerStdioReadTools(t *testing.T) {
	server := newReadFixtureServer(t, false)
	defer server.Close()
	configPath := writeTestConfig(t, server.URL, "lgy_read", configFileMode)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"list_invoices","arguments":{"status":["sent"],"search":"contoso","limit":1,"cursor":"0"}}}`,
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"get_invoice","arguments":{"id":"inv_1"}}}`,
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"advisor_insights","arguments":{"surface":"invoices"}}}`,
		`{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"dividend_headroom","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":14,"method":"tools/call","params":{"name":"dla_balance","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":15,"method":"tools/call","params":{"name":"dla_ledger","arguments":{"from":"2026-04-01","to":"2026-04-30","cursor":"dla-cursor"}}}`,
		`{"jsonrpc":"2.0","id":16,"method":"tools/call","params":{"name":"profit_and_loss","arguments":{"period":{"from":"2026-04-01","to":"2026-06-30"}}}}`,
		`{"jsonrpc":"2.0","id":17,"method":"tools/call","params":{"name":"vat_position","arguments":{"period":"2026-Q2"}}}`,
		`{"jsonrpc":"2.0","id":18,"method":"tools/call","params":{"name":"filing_calendar","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":19,"method":"tools/call","params":{"name":"bank_review_queue","arguments":{}}}`,
		"",
	}, "\n")

	responses := runMCPForTest(t, configPath, input, "test-sha")
	initialize := responses["1"]
	if initialize.Error != nil {
		t.Fatalf("initialize error = %+v", initialize.Error)
	}
	var initResult struct {
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Instructions string `json:"instructions"`
	}
	decodeResult(t, initialize, &initResult)
	if initResult.ServerInfo.Name != "ledgerly" || initResult.ServerInfo.Version != "test-sha" {
		t.Fatalf("serverInfo = %+v, want ledgerly/test-sha", initResult.ServerInfo)
	}
	for _, want := range []string{"integer minor units", "prepare with tools", "human confirms money movement", "advisor_insights", "deterministic rule outputs"} {
		if !strings.Contains(initResult.Instructions, want) {
			t.Fatalf("instructions missing %q: %s", want, initResult.Instructions)
		}
	}

	var toolsResult struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Properties map[string]struct {
					Description string `json:"description"`
				} `json:"properties"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	decodeResult(t, responses["2"], &toolsResult)
	gotNames := make([]string, 0, len(toolsResult.Tools))
	listInvoicesCursorDescription := ""
	for _, tool := range toolsResult.Tools {
		gotNames = append(gotNames, tool.Name)
		for _, want := range []string{"integer minor units", "currency", "ISO", "Prefer", "not"} {
			if !strings.Contains(tool.Description, want) {
				t.Fatalf("%s description missing %q: %s", tool.Name, want, tool.Description)
			}
		}
		if tool.Name == "list_invoices" {
			listInvoicesCursorDescription = tool.InputSchema.Properties["cursor"].Description
		}
	}
	wantNames := []string{
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
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("tool names = %#v, want %#v", gotNames, wantNames)
	}
	for _, want := range []string{"zero-based invoice row offset", "offset + limit", "total_count"} {
		if !strings.Contains(listInvoicesCursorDescription, want) {
			t.Fatalf("list_invoices cursor description missing %q: %s", want, listInvoicesCursorDescription)
		}
	}

	expectedPayloads := map[string]string{
		"10": invoiceListFixture,
		"11": invoiceDetailFixture,
		"12": advisorInsightsFixture,
		"13": dividendHeadroomFixture,
		"14": dlaBalanceFixture,
		"15": dlaLedgerFixture,
		"16": reportPLFixture,
		"17": reportVATFixture,
		"18": reportCalendarFixture,
		"19": bankReviewFixture,
	}
	for id, want := range expectedPayloads {
		assertMCPStructuredContent(t, responses[id], want)
	}
}

func TestMCPMalformedParamsReturnErrorsAndServerSurvives(t *testing.T) {
	server := newReadFixtureServer(t, false)
	defer server.Close()
	configPath := writeTestConfig(t, server.URL, "lgy_read", configFileMode)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"bad-date","method":"tools/call","params":{"name":"profit_and_loss","arguments":{"period":{"from":"not-a-date","to":"2026-06-30"}}}}`,
		`{"jsonrpc":"2.0","id":"extra-param","method":"tools/call","params":{"name":"filing_calendar","arguments":{"unexpected":true}}}`,
		`{"jsonrpc":"2.0","id":"after-error","method":"tools/call","params":{"name":"filing_calendar","arguments":{}}}`,
		"",
	}, "\n")

	responses := runMCPForTest(t, configPath, input, "dev")
	for _, id := range []string{`"bad-date"`, `"extra-param"`} {
		response := responses[id]
		if response.Error == nil {
			t.Fatalf("%s error = nil, want MCP error", id)
		}
		if response.Error.Code != -32602 {
			t.Fatalf("%s error code = %d, want -32602", id, response.Error.Code)
		}
		if strings.Contains(response.Error.Message, "goroutine") || strings.Contains(response.Error.Message, ".go:") {
			t.Fatalf("%s error leaked implementation detail: %s", id, response.Error.Message)
		}
	}
	if !strings.Contains(responses[`"bad-date"`].Error.Message, "YYYY-MM-DD") {
		t.Fatalf("bad-date error = %q, want actionable date format", responses[`"bad-date"`].Error.Message)
	}
	if !strings.Contains(responses[`"extra-param"`].Error.Message, "does not accept parameters") {
		t.Fatalf("extra-param error = %q, want actionable no-params message", responses[`"extra-param"`].Error.Message)
	}
	assertMCPStructuredContent(t, responses[`"after-error"`], reportCalendarFixture)
}

func TestMCPProblemDetailsBecomeToolErrorResult(t *testing.T) {
	server := newReadFixtureServer(t, true)
	defer server.Close()
	configPath := writeTestConfig(t, server.URL, "lgy_bad", configFileMode)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"filing_calendar","arguments":{}}}`,
		"",
	}, "\n")
	responses := runMCPForTest(t, configPath, input, "dev")
	response := responses["1"]
	if response.Error != nil {
		t.Fatalf("MCP protocol error = %+v, want tool error result", response.Error)
	}
	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent struct {
			Type   string `json:"type"`
			Title  string `json:"title"`
			Status int    `json:"status"`
			Detail string `json:"detail"`
		} `json:"structuredContent"`
	}
	decodeResult(t, response, &result)
	if !result.IsError {
		t.Fatal("isError = false, want true")
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("content = %+v, want one text item", result.Content)
	}
	if !strings.Contains(result.Content[0].Text, "Unauthorized") || !strings.Contains(result.Content[0].Text, "authentication required") {
		t.Fatalf("tool error text = %q, want problem detail", result.Content[0].Text)
	}
	if strings.Contains(result.Content[0].Text, "goroutine") || strings.Contains(result.Content[0].Text, ".go:") {
		t.Fatalf("tool error text leaked implementation detail: %s", result.Content[0].Text)
	}
	if result.StructuredContent.Title != "Unauthorized" || result.StructuredContent.Status != 401 || result.StructuredContent.Detail != "authentication required" {
		t.Fatalf("structuredContent = %+v, want problem detail", result.StructuredContent)
	}
}

func TestMCPUsesLedgerlyTokenEnv(t *testing.T) {
	server := newReadFixtureServer(t, false)
	defer server.Close()
	configPath := writeTestConfig(t, server.URL, "", configFileMode)
	t.Setenv("LEDGERLY_TOKEN", "lgy_read")

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"filing_calendar","arguments":{}}}`,
		"",
	}, "\n")
	responses := runMCPForTest(t, configPath, input, "dev")
	assertMCPStructuredContent(t, responses["1"], reportCalendarFixture)
}

func TestMCPAuditLogsToolCallsToStderr(t *testing.T) {
	server := newReadFixtureServer(t, false)
	defer server.Close()
	configPath := writeTestConfig(t, server.URL, "lgy_read", configFileMode)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"filing_calendar","arguments":{}}}`,
		"",
	}, "\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := Execute(
		context.Background(),
		[]string{"--config", configPath, "mcp"},
		&stdout,
		&stderr,
		WithStdin(strings.NewReader(input)),
		WithVersion("dev"),
	)
	if err != nil {
		t.Fatalf("Execute(mcp) error = %v", err)
	}
	if !json.Valid(bytes.TrimSpace(stdout.Bytes())) {
		t.Fatalf("stdout is not a single JSON-RPC response: %s", stdout.String())
	}
	audit := stderr.String()
	for _, want := range []string{`msg="mcp tool call"`, "tool=filing_calendar", `pat_name="CLI read integration"`, "duration_ms="} {
		if !strings.Contains(audit, want) {
			t.Fatalf("audit log missing %q: %s", want, audit)
		}
	}
}

func runMCPForTest(t *testing.T, configPath string, input string, version string) map[string]mcpTestResponse {
	t.Helper()

	var stdout bytes.Buffer
	err := Execute(
		context.Background(),
		[]string{"--config", configPath, "mcp"},
		&stdout,
		ioDiscard{},
		WithStdin(strings.NewReader(input)),
		WithVersion(version),
	)
	if err != nil {
		t.Fatalf("Execute(mcp) error = %v", err)
	}

	responses := map[string]mcpTestResponse{}
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var response mcpTestResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("decode MCP response %q: %v", line, err)
		}
		responses[string(response.ID)] = response
	}
	return responses
}

func decodeResult(t *testing.T, response mcpTestResponse, target any) {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("MCP error = %+v", response.Error)
	}
	if err := json.Unmarshal(response.Result, target); err != nil {
		t.Fatalf("decode MCP result: %v\n%s", err, string(response.Result))
	}
}

func assertMCPStructuredContent(t *testing.T, response mcpTestResponse, wantRaw string) {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("MCP error = %+v", response.Error)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
	}
	decodeResult(t, response, &result)
	if len(result.Content) != 1 || result.Content[0].Type != "text" || !json.Valid([]byte(result.Content[0].Text)) {
		t.Fatalf("content = %+v, want one JSON text item", result.Content)
	}
	var got any
	if err := json.Unmarshal(result.StructuredContent, &got); err != nil {
		t.Fatalf("decode structuredContent: %v\n%s", err, string(result.StructuredContent))
	}
	var want any
	if err := json.Unmarshal([]byte(wantRaw), &want); err != nil {
		t.Fatalf("decode expected fixture: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		gotBytes, _ := json.MarshalIndent(got, "", "  ")
		wantBytes, _ := json.MarshalIndent(want, "", "  ")
		t.Fatalf("structuredContent mismatch\n--- got ---\n%s\n--- want ---\n%s", string(gotBytes), string(wantBytes))
	}
}

type mcpTestResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
