package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/npmulder/ledgerly/internal/cli/gen"
)

const protocolVersion = "2025-06-18"

const serverInstructions = "Ledgerly MCP uses integer minor units for money with explicit currency codes and ISO dates/timestamps; prepare with tools, but a human confirms money movement in the CLI or web UI, so sending invoices, settling payments, confirming bank matches, and declaring dividends are not exposed as MCP tools; advisor_insights are deterministic rule outputs suitable for explanation, not model-generated advice."

type Config struct {
	BaseURL    string
	Token      string
	Version    string
	HTTPClient *http.Client
	Logger     *slog.Logger
}

type Server struct {
	baseURL string
	client  *gen.ClientWithResponses
	logger  *slog.Logger
	version string
}

func New(cfg Config) (*Server, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("url is required")
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}
	version := strings.TrimSpace(cfg.Version)
	if version == "" {
		version = "dev"
	}

	options := []gen.ClientOption{
		gen.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+token)
			return nil
		}),
	}
	if cfg.HTTPClient != nil {
		options = append(options, gen.WithHTTPClient(cfg.HTTPClient))
	}
	client, err := gen.NewClientWithResponses(baseURL, options...)
	if err != nil {
		return nil, err
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Server{baseURL: baseURL, client: client, logger: logger, version: version}, nil
}

func (s *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	writer := bufio.NewWriter(output)
	encoder := json.NewEncoder(writer)
	messages := make(chan scanResult, 1)

	go func() {
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case messages <- scanResult{line: line}:
			case <-ctx.Done():
				return
			}
		}
		result := scanResult{err: scanner.Err(), done: true}
		select {
		case messages <- result:
		case <-ctx.Done():
		}
	}()

	for {
		var scanned scanResult
		select {
		case <-ctx.Done():
			return writer.Flush()
		case scanned = <-messages:
		}
		if scanned.done {
			if scanned.err != nil {
				return fmt.Errorf("read MCP message: %w", scanned.err)
			}
			return writer.Flush()
		}
		line := bytes.TrimSpace(scanned.line)
		if len(line) == 0 {
			continue
		}
		response := s.handle(ctx, line)
		if response == nil {
			continue
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
}

func (s *Server) handle(ctx context.Context, line []byte) *rpcResponse {
	var request rpcRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return errorResponse(json.RawMessage("null"), -32700, "invalid JSON-RPC message")
	}
	hasID := len(bytes.TrimSpace(request.ID)) > 0
	if request.JSONRPC != "2.0" || strings.TrimSpace(request.Method) == "" {
		if !hasID {
			return errorResponse(json.RawMessage("null"), -32600, "invalid JSON-RPC request")
		}
		return errorResponse(request.ID, -32600, "invalid JSON-RPC request")
	}

	switch request.Method {
	case "initialize":
		if !hasID {
			return nil
		}
		return resultResponse(request.ID, initializeResult{
			ProtocolVersion: protocolVersion,
			Capabilities: serverCapabilities{
				Tools: toolsCapability{ListChanged: false},
			},
			ServerInfo: implementationInfo{
				Name:    "ledgerly",
				Version: s.version,
			},
			Instructions: serverInstructions,
		})
	case "notifications/initialized", "initialized":
		if !hasID {
			return nil
		}
		return resultResponse(request.ID, map[string]any{})
	case "ping":
		if !hasID {
			return nil
		}
		return resultResponse(request.ID, map[string]any{})
	case "tools/list":
		if !hasID {
			return nil
		}
		return resultResponse(request.ID, listToolsResult{Tools: toolDefinitions()})
	case "tools/call":
		if !hasID {
			return nil
		}
		result, err := s.callTool(ctx, request.Params)
		if err != nil {
			return errorFromTool(request.ID, err)
		}
		return resultResponse(request.ID, result)
	default:
		if !hasID {
			return nil
		}
		return errorResponse(request.ID, -32601, fmt.Sprintf("method %q is not supported", request.Method))
	}
}

func (s *Server) callTool(ctx context.Context, rawParams json.RawMessage) (toolResult, error) {
	var params callToolParams
	if len(bytes.TrimSpace(rawParams)) == 0 {
		return toolResult{}, invalidParams("tools/call params must include name and arguments")
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return toolResult{}, invalidParams("tools/call params must be an object with name and arguments")
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return toolResult{}, invalidParams("tools/call params must include a non-empty tool name")
	}
	arguments := params.Arguments
	if len(bytes.TrimSpace(arguments)) == 0 || bytes.Equal(bytes.TrimSpace(arguments), []byte("null")) {
		arguments = json.RawMessage("{}")
	}

	start := time.Now()
	caller, err := s.callerInfo(ctx)
	if err != nil {
		s.logToolCall(name, caller.PATName, start)
		var toolErr *toolExecutionError
		if errors.As(err, &toolErr) {
			return toolErrorResult(toolErr), nil
		}
		return toolResult{}, err
	}
	defer s.logToolCall(name, caller.PATName, start)

	var payload any
	switch name {
	case "list_invoices":
		payload, err = s.listInvoices(ctx, arguments)
	case "get_invoice":
		payload, err = s.getInvoice(ctx, arguments)
	case "advisor_insights":
		payload, err = s.advisorInsights(ctx, arguments)
	case "dividend_headroom":
		payload, err = s.dividendHeadroom(ctx, arguments)
	case "dla_balance":
		payload, err = s.dlaBalance(ctx, arguments)
	case "dla_ledger":
		payload, err = s.dlaLedger(ctx, arguments)
	case "profit_and_loss":
		payload, err = s.profitAndLoss(ctx, arguments)
	case "vat_position":
		payload, err = s.vatPosition(ctx, arguments)
	case "filing_calendar":
		payload, err = s.filingCalendar(ctx, arguments)
	case "bank_review_queue":
		payload, err = s.bankReviewQueue(ctx, arguments)
	case "create_draft_invoice":
		payload, err = s.createDraftInvoice(ctx, caller, arguments)
	case "send_invoice_reminder":
		payload, err = s.sendInvoiceReminder(ctx, caller, arguments)
	default:
		return toolResult{}, invalidParams(fmt.Sprintf("unknown Ledgerly MCP tool %q", name))
	}
	if err != nil {
		var toolErr *toolExecutionError
		if errors.As(err, &toolErr) {
			return toolErrorResult(toolErr), nil
		}
		return toolResult{}, err
	}
	return structuredToolResult(payload)
}

func (s *Server) createDraftInvoice(ctx context.Context, caller callerInfo, raw json.RawMessage) (any, error) {
	if err := requireFullScope("create_draft_invoice", caller); err != nil {
		return nil, err
	}
	var args createDraftInvoiceArgs
	if err := decodeArgs("create_draft_invoice", raw, &args); err != nil {
		return nil, err
	}
	clientID, err := s.resolveInvoiceClientID(ctx, args)
	if err != nil {
		return nil, err
	}
	lines, currency, err := invoiceLinesFromArgs(args.Lines)
	if err != nil {
		return nil, err
	}

	createResponse, err := s.client.InvoicingCreateDraftInvoiceWithResponse(ctx, gen.InvoicingCreateDraftInvoiceRequest{
		ClientId: clientID,
	})
	if err != nil {
		return nil, apiUnavailable(err)
	}
	if createResponse.JSON201 == nil {
		return nil, apiProblem(createResponse.StatusCode(), createResponse.Status(), createResponse.Body, createResponse.ApplicationproblemJSON400, createResponse.ApplicationproblemJSON401, createResponse.ApplicationproblemJSON404, createResponse.ApplicationproblemJSON413, problemFromValidation(createResponse.ApplicationproblemJSON422))
	}

	invoice := createResponse.JSON201
	patchCurrency := gen.InvoicingInvoicePatchCurrency(currency)
	patchResponse, err := s.client.InvoicingPatchInvoiceWithResponse(ctx, invoice.Id, gen.InvoicingInvoicePatch{
		Currency: &patchCurrency,
		Lines:    &lines,
	})
	if err != nil {
		return nil, apiUnavailable(err)
	}
	if patchResponse.JSON200 == nil {
		return nil, apiProblem(patchResponse.StatusCode(), patchResponse.Status(), patchResponse.Body, patchResponse.ApplicationproblemJSON400, patchResponse.ApplicationproblemJSON401, patchResponse.ApplicationproblemJSON404, problemFromValidation(patchResponse.ApplicationproblemJSON409), patchResponse.ApplicationproblemJSON413, problemFromValidation(patchResponse.ApplicationproblemJSON422))
	}
	invoice = patchResponse.JSON200

	return map[string]any{
		"draft_id":   invoice.Id,
		"editor_url": s.editorURL(invoice.Id),
		"invoice":    responsePayload(patchResponse.Body, invoice),
		"policy":     "Draft prepared only; this tool does not send, settle, confirm payment, or declare dividends.",
	}, nil
}

func (s *Server) sendInvoiceReminder(ctx context.Context, caller callerInfo, raw json.RawMessage) (any, error) {
	if err := requireFullScope("send_invoice_reminder", caller); err != nil {
		return nil, err
	}
	var args sendInvoiceReminderArgs
	if err := decodeArgs("send_invoice_reminder", raw, &args); err != nil {
		return nil, err
	}
	invoiceID := strings.TrimSpace(args.InvoiceID)
	if invoiceID == "" {
		return nil, invalidParams("send_invoice_reminder.invoiceId is required")
	}
	response, err := s.client.InvoicingSendInvoiceReminderWithResponse(ctx, invoiceID)
	if err != nil {
		return nil, apiUnavailable(err)
	}
	if response.JSON200 == nil {
		return nil, apiProblem(response.StatusCode(), response.Status(), response.Body, response.ApplicationproblemJSON401, response.ApplicationproblemJSON404, problemFromValidation(response.ApplicationproblemJSON409))
	}
	payload := responsePayload(response.Body, response.JSON200)
	return map[string]any{
		"confirmation": "invoice reminder recorded and sent",
		"invoice_id":   response.JSON200.Reminder.InvoiceId,
		"reminder":     response.JSON200.Reminder,
		"result":       payload,
		"policy":       "Reminder prepared only; this tool does not send the invoice, settle it, confirm payment, or declare dividends.",
	}, nil
}

func (s *Server) listInvoices(ctx context.Context, raw json.RawMessage) (any, error) {
	var args listInvoicesArgs
	if err := decodeArgs("list_invoices", raw, &args); err != nil {
		return nil, err
	}
	params := &gen.InvoicingListInvoicesParams{}
	if len(args.Status) > 0 {
		statuses := make([]gen.InvoicingListInvoicesParamsStatus, 0, len(args.Status))
		for _, status := range args.Status {
			status = strings.TrimSpace(status)
			if status == "" || status == "all" {
				continue
			}
			statuses = append(statuses, gen.InvoicingListInvoicesParamsStatus(status))
		}
		if len(statuses) > 0 {
			params.Status = &statuses
		}
	}
	if strings.TrimSpace(args.Search) != "" {
		search := strings.TrimSpace(args.Search)
		params.Search = &search
	}
	if args.Limit != nil {
		if *args.Limit <= 0 {
			return nil, invalidParams("list_invoices.limit must be a positive integer when provided")
		}
		params.Limit = args.Limit
	}
	if strings.TrimSpace(args.Cursor) != "" {
		offset, err := strconv.Atoi(strings.TrimSpace(args.Cursor))
		if err != nil || offset < 0 {
			return nil, invalidParams("list_invoices.cursor must be a non-negative invoice offset encoded as a string")
		}
		params.Offset = &offset
	}

	response, err := s.client.InvoicingListInvoicesWithResponse(ctx, params)
	if err != nil {
		return nil, apiUnavailable(err)
	}
	if response.JSON200 != nil {
		return responsePayload(response.Body, response.JSON200), nil
	}
	return nil, apiProblem(response.StatusCode(), response.Status(), response.Body, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401)
}

func (s *Server) getInvoice(ctx context.Context, raw json.RawMessage) (any, error) {
	var args getInvoiceArgs
	if err := decodeArgs("get_invoice", raw, &args); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(args.ID)
	if id == "" {
		return nil, invalidParams("get_invoice.id is required")
	}
	response, err := s.client.InvoicingGetInvoiceWithResponse(ctx, id)
	if err != nil {
		return nil, apiUnavailable(err)
	}
	if response.JSON200 != nil {
		return responsePayload(response.Body, response.JSON200), nil
	}
	return nil, apiProblem(response.StatusCode(), response.Status(), response.Body, response.ApplicationproblemJSON401, response.ApplicationproblemJSON404)
}

func (s *Server) advisorInsights(ctx context.Context, raw json.RawMessage) (any, error) {
	var args advisorInsightsArgs
	if err := decodeArgs("advisor_insights", raw, &args); err != nil {
		return nil, err
	}
	params := &gen.AdvisorListInsightsParams{}
	if strings.TrimSpace(args.Surface) != "" {
		surface := gen.AdvisorListInsightsParamsSurface(strings.TrimSpace(args.Surface))
		params.Surface = &surface
	}
	response, err := s.client.AdvisorListInsightsWithResponse(ctx, params)
	if err != nil {
		return nil, apiUnavailable(err)
	}
	if response.JSON200 != nil {
		return responsePayload(response.Body, response.JSON200), nil
	}
	return nil, apiProblem(response.StatusCode(), response.Status(), response.Body, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401)
}

func (s *Server) dividendHeadroom(ctx context.Context, raw json.RawMessage) (any, error) {
	if err := decodeNoArgs("dividend_headroom", raw); err != nil {
		return nil, err
	}
	response, err := s.client.DividendsGetHeadroomWithResponse(ctx)
	if err != nil {
		return nil, apiUnavailable(err)
	}
	if response.JSON200 != nil {
		return responsePayload(response.Body, response.JSON200), nil
	}
	return nil, apiProblem(response.StatusCode(), response.Status(), response.Body, response.ApplicationproblemJSON401)
}

func (s *Server) dlaBalance(ctx context.Context, raw json.RawMessage) (any, error) {
	if err := decodeNoArgs("dla_balance", raw); err != nil {
		return nil, err
	}
	response, err := s.client.DlaGetBalanceWithResponse(ctx, nil)
	if err != nil {
		return nil, apiUnavailable(err)
	}
	if response.JSON200 != nil {
		return responsePayload(response.Body, response.JSON200), nil
	}
	return nil, apiProblem(response.StatusCode(), response.Status(), response.Body, response.ApplicationproblemJSON401)
}

func (s *Server) dlaLedger(ctx context.Context, raw json.RawMessage) (any, error) {
	var args dateCursorArgs
	if err := decodeArgs("dla_ledger", raw, &args); err != nil {
		return nil, err
	}
	params := &gen.DlaListLedgerParams{}
	if strings.TrimSpace(args.From) != "" {
		parsed, err := parseDateArg("dla_ledger.from", args.From)
		if err != nil {
			return nil, err
		}
		params.From = &parsed
	}
	if strings.TrimSpace(args.To) != "" {
		parsed, err := parseDateArg("dla_ledger.to", args.To)
		if err != nil {
			return nil, err
		}
		params.To = &parsed
	}
	if strings.TrimSpace(args.Cursor) != "" {
		cursor := strings.TrimSpace(args.Cursor)
		params.Cursor = &cursor
	}
	response, err := s.client.DlaListLedgerWithResponse(ctx, params)
	if err != nil {
		return nil, apiUnavailable(err)
	}
	if response.JSON200 != nil {
		return responsePayload(response.Body, response.JSON200), nil
	}
	return nil, apiProblem(response.StatusCode(), response.Status(), response.Body, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401)
}

func (s *Server) profitAndLoss(ctx context.Context, raw json.RawMessage) (any, error) {
	var args profitAndLossArgs
	if err := decodeArgs("profit_and_loss", raw, &args); err != nil {
		return nil, err
	}
	from := strings.TrimSpace(args.From)
	to := strings.TrimSpace(args.To)
	if args.Period != nil {
		if from != "" || to != "" {
			return nil, invalidParams("profit_and_loss accepts either period.from/period.to or top-level from/to, not both")
		}
		from = strings.TrimSpace(args.Period.From)
		to = strings.TrimSpace(args.Period.To)
	}
	if from == "" || to == "" {
		return nil, invalidParams("profit_and_loss requires an ISO date period with from and to in YYYY-MM-DD form")
	}
	parsedFrom, err := parseDateArg("profit_and_loss.period.from", from)
	if err != nil {
		return nil, err
	}
	parsedTo, err := parseDateArg("profit_and_loss.period.to", to)
	if err != nil {
		return nil, err
	}
	response, err := s.client.ReportsGetProfitAndLossWithResponse(ctx, &gen.ReportsGetProfitAndLossParams{From: parsedFrom, To: parsedTo})
	if err != nil {
		return nil, apiUnavailable(err)
	}
	if response.JSON200 != nil {
		return responsePayload(response.Body, response.JSON200), nil
	}
	return nil, apiProblem(response.StatusCode(), response.Status(), response.Body, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401)
}

func (s *Server) vatPosition(ctx context.Context, raw json.RawMessage) (any, error) {
	var args vatPositionArgs
	if err := decodeArgs("vat_position", raw, &args); err != nil {
		return nil, err
	}
	period := strings.TrimSpace(args.Period)
	if period == "" {
		return nil, invalidParams("vat_position.period is required in YYYY-QN form, for example 2026-Q2")
	}
	response, err := s.client.ReportsGetVATReturnWithResponse(ctx, &gen.ReportsGetVATReturnParams{Period: period})
	if err != nil {
		return nil, apiUnavailable(err)
	}
	if response.JSON200 != nil {
		return responsePayload(response.Body, response.JSON200), nil
	}
	return nil, apiProblem(response.StatusCode(), response.Status(), response.Body, response.ApplicationproblemJSON400, response.ApplicationproblemJSON401)
}

func (s *Server) filingCalendar(ctx context.Context, raw json.RawMessage) (any, error) {
	if err := decodeNoArgs("filing_calendar", raw); err != nil {
		return nil, err
	}
	response, err := s.client.ReportsGetFilingCalendarWithResponse(ctx)
	if err != nil {
		return nil, apiUnavailable(err)
	}
	if response.JSON200 != nil {
		return responsePayload(response.Body, response.JSON200), nil
	}
	return nil, apiProblem(response.StatusCode(), response.Status(), response.Body, response.ApplicationproblemJSON401, response.ApplicationproblemJSON404)
}

func (s *Server) bankReviewQueue(ctx context.Context, raw json.RawMessage) (any, error) {
	if err := decodeNoArgs("bank_review_queue", raw); err != nil {
		return nil, err
	}
	response, err := s.client.BankingGetReviewQueueWithResponse(ctx)
	if err != nil {
		return nil, apiUnavailable(err)
	}
	if response.JSON200 != nil {
		return responsePayload(response.Body, response.JSON200), nil
	}
	return nil, apiProblem(response.StatusCode(), response.Status(), response.Body, response.ApplicationproblemJSON401)
}

func (s *Server) callerInfo(ctx context.Context) (callerInfo, error) {
	response, err := s.client.IdentityCurrentUserWithResponse(ctx)
	if err != nil {
		return callerInfo{}, apiUnavailable(err)
	}
	if response.JSON200 == nil {
		return callerInfo{}, apiProblem(response.StatusCode(), response.Status(), response.Body, response.ApplicationproblemJSON401)
	}
	info := callerInfo{PATName: "unknown", Scope: ""}
	if response.JSON200.TokenName != nil && strings.TrimSpace(*response.JSON200.TokenName) != "" {
		info.PATName = strings.TrimSpace(*response.JSON200.TokenName)
	}
	if response.JSON200.TokenScope != nil {
		info.Scope = *response.JSON200.TokenScope
	}
	return info, nil
}

func (s *Server) logToolCall(toolName string, patName string, start time.Time) {
	if strings.TrimSpace(patName) == "" {
		patName = "unknown"
	}
	s.logger.Info("mcp tool call",
		"tool", strings.TrimSpace(toolName),
		"pat_name", patName,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

func requireFullScope(toolName string, caller callerInfo) error {
	if caller.Scope == gen.Full {
		return nil
	}
	scope := string(caller.Scope)
	if strings.TrimSpace(scope) == "" {
		scope = "unknown"
	}
	patName := strings.TrimSpace(caller.PATName)
	if patName == "" {
		patName = "unknown"
	}
	detail := fmt.Sprintf("%s requires a full-scope personal access token; current token %q has scope %s.", toolName, patName, scope)
	return &toolExecutionError{
		message: detail,
		structuredContent: map[string]any{
			"type":           "https://ledgerly.local/problems/mcp/full-scope-required",
			"title":          "Full-scope personal access token required",
			"detail":         detail,
			"tool":           toolName,
			"pat_name":       patName,
			"required_scope": "full",
			"current_scope":  scope,
		},
	}
}

func (s *Server) resolveInvoiceClientID(ctx context.Context, args createDraftInvoiceArgs) (string, error) {
	clientID := strings.TrimSpace(args.ClientID)
	clientName := strings.TrimSpace(args.ClientName)
	switch {
	case clientID != "" && clientName != "":
		return "", invalidParams("create_draft_invoice accepts either clientId or clientName, not both")
	case clientID == "" && clientName == "":
		return "", invalidParams("create_draft_invoice requires clientId or clientName")
	}

	activeClients, err := s.activeInvoiceClients(ctx)
	if err != nil {
		return "", err
	}

	if clientID != "" {
		for _, client := range activeClients {
			if strings.TrimSpace(client.Id) == clientID {
				return clientID, nil
			}
		}
		return "", invalidParams(fmt.Sprintf("create_draft_invoice.clientId %q did not match an active client", clientID))
	}

	var matches []gen.InvoicingClient
	for _, client := range activeClients {
		if strings.EqualFold(strings.TrimSpace(client.Name), clientName) {
			matches = append(matches, client)
		}
	}
	switch len(matches) {
	case 0:
		return "", invalidParams(fmt.Sprintf("create_draft_invoice.clientName %q did not match an active client", clientName))
	case 1:
		return strings.TrimSpace(matches[0].Id), nil
	default:
		return "", invalidParams(fmt.Sprintf("create_draft_invoice.clientName %q matched multiple active clients; use clientId", clientName))
	}
}

func (s *Server) activeInvoiceClients(ctx context.Context) ([]gen.InvoicingClient, error) {
	includeArchived := false
	response, err := s.client.InvoicingListClientsWithResponse(ctx, &gen.InvoicingListClientsParams{IncludeArchived: &includeArchived})
	if err != nil {
		return nil, apiUnavailable(err)
	}
	if response.JSON200 == nil {
		return nil, apiProblem(response.StatusCode(), response.Status(), response.Body, response.ApplicationproblemJSON401)
	}
	activeClients := make([]gen.InvoicingClient, 0, len(response.JSON200.Clients))
	for _, client := range response.JSON200.Clients {
		if client.ArchivedAt != nil {
			continue
		}
		activeClients = append(activeClients, client)
	}
	return activeClients, nil
}

func invoiceLinesFromArgs(args []draftInvoiceLineArg) ([]gen.InvoicingInvoiceLineInput, string, error) {
	if len(args) == 0 {
		return nil, "", invalidParams("create_draft_invoice.lines must include at least one invoice line")
	}
	lines := make([]gen.InvoicingInvoiceLineInput, 0, len(args))
	var invoiceCurrency string
	for i, line := range args {
		description := strings.TrimSpace(line.Description)
		if description == "" {
			return nil, "", invalidParams(fmt.Sprintf("create_draft_invoice.lines[%d].description is required", i))
		}
		qty, err := parseQuantityArg(line.Qty)
		if err != nil {
			return nil, "", invalidParams(fmt.Sprintf("create_draft_invoice.lines[%d].qty %s", i, err.Error()))
		}
		if line.UnitPriceMinor <= 0 {
			return nil, "", invalidParams(fmt.Sprintf("create_draft_invoice.lines[%d].unitPriceMinor must be greater than zero", i))
		}
		currency := strings.ToUpper(strings.TrimSpace(line.Currency))
		if currency != "EUR" && currency != "GBP" {
			return nil, "", invalidParams(fmt.Sprintf("create_draft_invoice.lines[%d].currency must be EUR or GBP", i))
		}
		if invoiceCurrency == "" {
			invoiceCurrency = currency
		} else if currency != invoiceCurrency {
			return nil, "", invalidParams("create_draft_invoice.lines must all use the same currency")
		}
		lines = append(lines, gen.InvoicingInvoiceLineInput{
			Description: description,
			Qty:         qty,
			UnitPrice: gen.InvoicingMoney{
				Amount:   line.UnitPriceMinor,
				Currency: gen.InvoicingMoneyCurrency(currency),
			},
		})
	}
	return lines, invoiceCurrency, nil
}

func parseQuantityArg(raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", errors.New("is required")
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		qty := strings.TrimSpace(asString)
		if qty == "" {
			return "", errors.New("must not be empty")
		}
		if err := validatePositiveDecimalQuantity(qty); err != nil {
			return "", err
		}
		return qty, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err == nil {
		qty := strings.TrimSpace(number.String())
		if qty == "" {
			return "", errors.New("must not be empty")
		}
		if err := validatePositiveDecimalQuantity(qty); err != nil {
			return "", err
		}
		return qty, nil
	}
	return "", errors.New("must be a decimal string or JSON number")
}

func validatePositiveDecimalQuantity(value string) error {
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return errors.New("must be a positive decimal")
	}
	positive := false
	for _, part := range parts {
		if part == "" {
			return errors.New("must be a positive decimal")
		}
		for _, char := range part {
			if char < '0' || char > '9' {
				return errors.New("must be a positive decimal")
			}
			if char != '0' {
				positive = true
			}
		}
	}
	if !positive {
		return errors.New("must be greater than zero")
	}
	return nil
}

func (s *Server) editorURL(invoiceID string) string {
	return strings.TrimRight(s.baseURL, "/") + "/invoices/" + url.PathEscape(strings.TrimSpace(invoiceID))
}

func problemFromValidation(problem *gen.ValidationProblem) *gen.Problem {
	if problem == nil {
		return nil
	}
	return &gen.Problem{Type: problem.Type, Title: problem.Title, Status: problem.Status, Detail: problem.Detail, Instance: problem.Instance}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type protocolError struct {
	code    int
	message string
}

func (e *protocolError) Error() string {
	return e.message
}

type toolExecutionError struct {
	message           string
	structuredContent any
}

func (e *toolExecutionError) Error() string {
	return e.message
}

type scanResult struct {
	line []byte
	err  error
	done bool
}

type callerInfo struct {
	PATName string
	Scope   gen.IdentityPATScope
}

func resultResponse(id json.RawMessage, result any) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: responseID(id), Result: result}
}

func errorResponse(id json.RawMessage, code int, message string) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: responseID(id), Error: &rpcError{Code: code, Message: message}}
}

func responseID(id json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(id)) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func errorFromTool(id json.RawMessage, err error) *rpcResponse {
	var protocol *protocolError
	if errors.As(err, &protocol) {
		return errorResponse(id, protocol.code, protocol.message)
	}
	return errorResponse(id, -32603, "internal MCP server error")
}

func invalidParams(message string) error {
	return &protocolError{code: -32602, message: message}
}

func apiUnavailable(err error) error {
	message := fmt.Sprintf("unable to reach Ledgerly API: %v", err)
	return &toolExecutionError{
		message: message,
		structuredContent: map[string]any{
			"title":  "Unable to reach Ledgerly API",
			"detail": message,
		},
	}
}

func apiProblem(statusCode int, status string, body []byte, problems ...*gen.Problem) error {
	if statusCode < 400 {
		message := unexpectedAPIResponse(status)
		return &toolExecutionError{
			message: message,
			structuredContent: map[string]any{
				"title":  "Unexpected Ledgerly API response",
				"detail": message,
			},
		}
	}
	for _, problem := range problems {
		if problem == nil {
			continue
		}
		return problemToolError(*problem, body)
	}
	var problem gen.Problem
	if len(body) > 0 && json.Unmarshal(body, &problem) == nil && strings.TrimSpace(problem.Title) != "" {
		if problem.Status == 0 {
			problem.Status = int32(statusCode)
		}
		return problemToolError(problem, body)
	}
	message := unexpectedAPIResponse(status)
	return &toolExecutionError{
		message: message,
		structuredContent: map[string]any{
			"status": statusCode,
			"title":  "Unexpected Ledgerly API response",
			"detail": message,
		},
	}
}

func responsePayload(body []byte, fallback any) any {
	if len(body) == 0 {
		return fallback
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return fallback
	}
	return payload
}

func unexpectedAPIResponse(status string) string {
	if strings.TrimSpace(status) == "" {
		status = "unknown status"
	}
	return fmt.Sprintf("unexpected response from Ledgerly API: %s", status)
}

func renderProblem(problem gen.Problem) string {
	title := strings.TrimSpace(problem.Title)
	if title == "" {
		title = fmt.Sprintf("HTTP %d", problem.Status)
	}
	detail := ""
	if problem.Detail != nil {
		detail = strings.TrimSpace(*problem.Detail)
	}
	if detail == "" {
		return title
	}
	return title + " - " + detail
}

func problemToolError(problem gen.Problem, body []byte) error {
	return &toolExecutionError{
		message:           renderProblem(problem),
		structuredContent: responsePayload(body, problem),
	}
}

func decodeArgs(toolName string, raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return invalidParams(fmt.Sprintf("invalid %s params: %v", toolName, err))
	}
	return nil
}

func decodeNoArgs(toolName string, raw json.RawMessage) error {
	var args map[string]json.RawMessage
	if err := decodeArgs(toolName, raw, &args); err != nil {
		return err
	}
	if len(args) > 0 {
		return invalidParams(fmt.Sprintf("%s does not accept parameters", toolName))
	}
	return nil
}

func parseDateArg(name string, value string) (openapi_types.Date, error) {
	parsed, err := time.Parse(openapi_types.DateFormat, strings.TrimSpace(value))
	if err != nil {
		return openapi_types.Date{}, invalidParams(fmt.Sprintf("%s must be an ISO date in YYYY-MM-DD form", name))
	}
	return openapi_types.Date{Time: parsed}, nil
}

func structuredToolResult(payload any) (toolResult, error) {
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return toolResult{}, err
	}
	return toolResult{
		Content: []contentItem{{
			Type: "text",
			Text: string(encoded),
		}},
		StructuredContent: payload,
	}, nil
}

func toolErrorResult(err *toolExecutionError) toolResult {
	return toolResult{
		Content: []contentItem{{
			Type: "text",
			Text: err.message,
		}},
		StructuredContent: err.structuredContent,
		IsError:           true,
	}
}

type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      implementationInfo `json:"serverInfo"`
	Instructions    string             `json:"instructions"`
}

type serverCapabilities struct {
	Tools toolsCapability `json:"tools"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type implementationInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type listToolsResult struct {
	Tools []toolDefinition `json:"tools"`
}

type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolResult struct {
	Content           []contentItem `json:"content"`
	StructuredContent any           `json:"structuredContent,omitempty"`
	IsError           bool          `json:"isError,omitempty"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type listInvoicesArgs struct {
	Status []string `json:"status,omitempty"`
	Search string   `json:"search,omitempty"`
	Limit  *int     `json:"limit,omitempty"`
	Cursor string   `json:"cursor,omitempty"`
}

type getInvoiceArgs struct {
	ID string `json:"id"`
}

type advisorInsightsArgs struct {
	Surface string `json:"surface,omitempty"`
}

type dateCursorArgs struct {
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type profitAndLossArgs struct {
	Period *periodArg `json:"period,omitempty"`
	From   string     `json:"from,omitempty"`
	To     string     `json:"to,omitempty"`
}

type periodArg struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type vatPositionArgs struct {
	Period string `json:"period"`
}

type createDraftInvoiceArgs struct {
	ClientID   string                `json:"clientId,omitempty"`
	ClientName string                `json:"clientName,omitempty"`
	Lines      []draftInvoiceLineArg `json:"lines"`
}

type draftInvoiceLineArg struct {
	Description    string          `json:"description"`
	Qty            json.RawMessage `json:"qty"`
	UnitPriceMinor int64           `json:"unitPriceMinor"`
	Currency       string          `json:"currency"`
}

type sendInvoiceReminderArgs struct {
	InvoiceID string `json:"invoiceId"`
}
