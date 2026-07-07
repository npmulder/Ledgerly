//go:build integration

package it_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/banking"
	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

func TestCLIMCPContracts(t *testing.T) {
	ctx := context.Background()
	if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("load default jurisdiction pack: %v", err)
	}

	issueDate := contractDay(2025, time.May, 1)
	settlementDate := contractDay(2025, time.May, 2)
	h := harness.New(t, harness.Options{ClockStart: issueDate.Add(9 * time.Hour)})
	fixtures.Company(t, h, fixtures.CompanyYearEnd(time.March, 31))
	fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{
		issueDate:      "0.8500",
		settlementDate: "0.8600",
	}))

	binary := buildContractLedgerlyBinary(t)
	fullPAT := createContractPAT(t, h, "CLI/MCP full contract", "full", "2030-01-01T00:00:00Z")
	readOnlyPAT := createContractPAT(t, h, "CLI/MCP read contract", "read-only", "2030-01-01T00:00:00Z")
	expiringPAT := createContractPAT(t, h, "CLI/MCP expiring contract", "read-only", "2025-05-03T00:00:00Z")
	fullConfig := writeContractCLIConfig(t, h.BaseURL, fullPAT)
	readOnlyConfig := writeContractCLIConfig(t, h.BaseURL, readOnlyPAT)
	expiredConfig := writeContractCLIConfig(t, h.BaseURL, expiringPAT)

	invoiceService := newContractInvoiceService(t, h)
	bankingService := newContractBankingService(t, h, invoiceService)
	contoso := fixtures.Contoso(t, h)
	fabrikam := fixtures.Fabrikam(t, h)

	contosoInvoice := sendContractInvoice(t, invoiceService, contoso.ID, "Monthly retainer", invoicing.Money{
		Amount:   450_000,
		Currency: string(invoicing.CurrencyEUR),
	})
	fabrikamInvoice := sendContractInvoice(t, invoiceService, fabrikam.ID, "Delivery", invoicing.Money{
		Amount:   360_000,
		Currency: string(invoicing.CurrencyGBP),
	})
	if contosoInvoice.Number == nil || fabrikamInvoice.Number == nil {
		t.Fatalf("sent invoice numbers contoso=%v fabrikam=%v, want both assigned", contosoInvoice.Number, fabrikamInvoice.Number)
	}
	postContractExpense(t, h, "2025-05-12", "5010-software", 25_000)
	postContractExpense(t, h, "2025-05-20", "5020-travel", 10_000)

	account := mustCreateContractBankingAccount(t, ctx, bankingService, "CLI MCP EUR", "EUR")
	statementPath := writeContractStatement(t, fixtures.RevolutCSV(fixtures.RevolutTxn{
		Date:      settlementDate.Add(10 * time.Hour),
		ID:        "cli-mcp-paid-contoso",
		Type:      "TRANSFER",
		Payee:     contoso.Name,
		Reference: *contosoInvoice.Number,
		Amount:    money.Money{Amount: 450_000, Currency: "EUR"},
		Balance:   money.Money{Amount: 450_000, Currency: "EUR"},
	}))
	importResult := runContractLedgerly(t, binary, fullConfig, "--yes", "bank", "import", statementPath, "--account", stringInt64(int64(account.ID)))
	assertContractExit(t, importResult, 0)
	assertContractOutputContains(t, importResult.output, "TOTAL", "NEW", "statement.csv")
	confirmTxn := contractBankTxnByReference(t, h, account.ID, *contosoInvoice.Number)

	reviewResult := runContractLedgerly(t, binary, fullConfig, "bank", "review")
	assertContractExit(t, reviewResult, 0)
	assertContractOutputContains(t, reviewResult.output, "match", *contosoInvoice.Number)

	dlaAddResult := runContractLedgerly(t, binary, fullConfig,
		"dla", "add",
		"--kind", "repayment",
		"--date", "2025-05-01",
		"--amount", "2500",
		"--description", "Director repayment contract fixture",
		"--cash-account", "1000-cash-gbp",
		"--source-ref", "manual:cli-mcp-dla",
	)
	assertContractExit(t, dlaAddResult, 0)

	seedDividendResult := runContractLedgerly(t, binary, fullConfig, "--yes", "dividend", "declare", "1000")
	assertContractExit(t, seedDividendResult, 0)
	assertContractOutputContains(t, seedDividendResult.output, "ID", "SHAREHOLDER", "AMOUNT")

	t.Run("json schema snapshots", func(t *testing.T) {
		readCommands := []struct {
			name string
			args []string
		}{
			{name: "auth-status", args: []string{"--json", "auth", "status"}},
			{name: "invoice-list", args: []string{"--json", "invoice", "list", "--status", "sent"}},
			{name: "invoice-show", args: []string{"--json", "invoice", "show", *contosoInvoice.Number}},
			{name: "client-list", args: []string{"--json", "client", "list"}},
			{name: "bank-accounts", args: []string{"--json", "bank", "accounts"}},
			{name: "bank-review", args: []string{"--json", "bank", "review"}},
			{name: "bank-feed", args: []string{"--json", "bank", "feed", "--account", stringInt64(int64(account.ID))}},
			{name: "dla-ledger", args: []string{"--json", "dla", "ledger"}},
			{name: "dla-balance", args: []string{"--json", "dla", "balance"}},
			{name: "dividend-headroom", args: []string{"--json", "dividend", "headroom"}},
			{name: "dividend-history", args: []string{"--json", "dividend", "history"}},
			{name: "report-pl", args: []string{"--json", "report", "pl", "--from", "2025-05-01", "--to", "2025-05-31"}},
			{name: "report-vat", args: []string{"--json", "report", "vat", "--period", "2025-Q2"}},
			{name: "report-calendar", args: []string{"--json", "report", "calendar"}},
			{name: "report-profit-ytd", args: []string{"--json", "report", "profit-ytd", "--tax-year", "2025-26"}},
			{name: "advisor-insights", args: []string{"--json", "advisor", "insights", "--surface", "invoices"}},
			{name: "rates-today", args: []string{"--json", "rates", "today", "--from", "EUR", "--to", "GBP"}},
		}
		for _, tt := range readCommands {
			t.Run(tt.name, func(t *testing.T) {
				result := runContractLedgerly(t, binary, readOnlyConfig, tt.args...)
				assertContractExit(t, result, 0)
				assertContractJSONShapeSnapshot(t, tt.name, result.output)
			})
		}
	})

	var draftFromNoYesCreate string
	t.Run("confirm semantics", func(t *testing.T) {
		draft := createContractInvoiceDraft(t, invoiceService, contoso.ID, "Confirmation preview", invoicing.Money{
			Amount:   125_000,
			Currency: string(invoicing.CurrencyEUR),
		})
		assertContractRequiresYes(t, runContractLedgerly(t, binary, fullConfig, "invoice", "send", draft.ID), "invoice send")
		assertContractRequiresYes(t, runContractLedgerly(t, binary, fullConfig, "bank", "confirm", stringInt64(int64(confirmTxn))), "bank confirm")
		assertContractRequiresYes(t, runContractLedgerly(t, binary, fullConfig, "dividend", "declare", "123400"), "dividend declare")

		createResult := runContractLedgerly(t, binary, fullConfig, "invoice", "create", "--client", fabrikam.ID, "--line", "Draft support:1:1000")
		assertContractExit(t, createResult, 0)
		assertContractOutputContains(t, createResult.output, "draft")
		draftFromNoYesCreate = firstContractInvoiceID(t, createResult.output)

		readResult := runContractLedgerly(t, binary, fullConfig, "invoice", "list", "--status", "sent")
		assertContractExit(t, readResult, 0)
		assertContractOutputContains(t, readResult.output, *contosoInvoice.Number)
	})

	t.Run("cli round trip mirrors IT-2", func(t *testing.T) {
		confirmResult := runContractLedgerly(t, binary, fullConfig, "--yes", "bank", "confirm", stringInt64(int64(confirmTxn)))
		assertContractExit(t, confirmResult, 0)
		assertContractOutputContains(t, confirmResult.output, "REALISED FX", "4500 GBP")

		listResult := runContractLedgerly(t, binary, fullConfig, "--json", "invoice", "list", "--status", "paid")
		assertContractExit(t, listResult, 0)
		assertContractInvoiceListed(t, listResult.output, contosoInvoice.ID, "paid")

		plResult := runContractLedgerly(t, binary, fullConfig, "--json", "report", "pl", "--from", "2025-05-01", "--to", "2025-05-31")
		assertContractExit(t, plResult, 0)
		assertContractPLMatchesIT2(t, plResult.output)
	})

	t.Run("read only PAT", func(t *testing.T) {
		writeCases := []struct {
			name string
			args []string
		}{
			{name: "invoice create", args: []string{"invoice", "create", "--client", fabrikam.ID, "--line", "Blocked draft:1:1000"}},
			{name: "invoice send", args: []string{"--yes", "invoice", "send", draftFromNoYesCreate}},
			{name: "invoice remind", args: []string{"--yes", "invoice", "remind", contosoInvoice.ID}},
			{name: "invoice revert", args: []string{"--yes", "invoice", "revert", contosoInvoice.ID}},
			{name: "client add", args: []string{"client", "add", "--name", "Blocked Client", "--email", "blocked@example.test", "--currency", "GBP", "--terms", "14", "--vat-treatment", "domestic", "--address-line1", "1 Test St", "--locality", "Douglas", "--postal-code", "IM1 1AA", "--country", "IM"}},
			{name: "bank import", args: []string{"--yes", "bank", "import", statementPath, "--account", stringInt64(int64(account.ID))}},
			{name: "bank confirm", args: []string{"--yes", "bank", "confirm", stringInt64(int64(confirmTxn))}},
			{name: "bank file-dla", args: []string{"--yes", "bank", "file-dla", stringInt64(int64(confirmTxn))}},
			{name: "bank recode", args: []string{"--yes", "bank", "recode", stringInt64(int64(confirmTxn)), "--account", "5010-software"}},
			{name: "bank exclude", args: []string{"--yes", "bank", "exclude", stringInt64(int64(confirmTxn)), "--reason", "blocked"}},
			{name: "dla add", args: []string{"dla", "add", "--kind", "repayment", "--date", "2025-05-01", "--amount", "1000", "--description", "Blocked DLA", "--cash-account", "1000-cash-gbp"}},
			{name: "dividend declare", args: []string{"--yes", "dividend", "declare", "1000"}},
		}
		for _, tt := range writeCases {
			t.Run(tt.name, func(t *testing.T) {
				result := runContractLedgerly(t, binary, readOnlyConfig, tt.args...)
				assertContractExit(t, result, 1)
				assertContractOutputContains(t, result.output, "Forbidden", "read-only personal access tokens cannot modify resources")
			})
		}

		readResult := runContractLedgerly(t, binary, readOnlyConfig, "--json", "invoice", "list", "--status", "paid")
		assertContractExit(t, readResult, 0)
		assertContractInvoiceListed(t, readResult.output, contosoInvoice.ID, "paid")

		readOnlyInput := strings.Join([]string{
			`{"jsonrpc":"2.0","id":"create-readonly","method":"tools/call","params":{"name":"create_draft_invoice","arguments":{"clientId":"` + fabrikam.ID + `","lines":[{"description":"Blocked draft","qty":"1","unitPriceMinor":1000,"currency":"GBP"}]}}}`,
			`{"jsonrpc":"2.0","id":"remind-readonly","method":"tools/call","params":{"name":"send_invoice_reminder","arguments":{"invoiceId":"` + contosoInvoice.ID + `"}}}`,
			"",
		}, "\n")
		responses := runContractMCP(t, binary, readOnlyConfig, readOnlyInput)
		assertContractMCPToolErrorContains(t, responses[`"create-readonly"`], "requires a full-scope personal access token")
		assertContractMCPToolErrorContains(t, responses[`"remind-readonly"`], "requires a full-scope personal access token")
	})

	t.Run("mcp discovery parity and write path", func(t *testing.T) {
		readInput := strings.Join([]string{
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"cli-mcp-contract","version":"1"}}}`,
			`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
			`{"jsonrpc":"2.0","id":"tools","method":"tools/list","params":{}}`,
			`{"jsonrpc":"2.0","id":"advisor_insights","method":"tools/call","params":{"name":"advisor_insights","arguments":{"surface":"invoices"}}}`,
			`{"jsonrpc":"2.0","id":"dividend_headroom","method":"tools/call","params":{"name":"dividend_headroom","arguments":{}}}`,
			`{"jsonrpc":"2.0","id":"missing_money_mover","method":"tools/call","params":{"name":"bank_confirm","arguments":{"transactionId":` + stringInt64(int64(confirmTxn)) + `}}}`,
			"",
		}, "\n")
		readResponses := runContractMCP(t, binary, readOnlyConfig, readInput)
		assertContractMCPToolNames(t, readResponses[`"tools"`])
		assertContractMCPMatchesHTTP(t, readResponses[`"advisor_insights"`], h.Client, readOnlyPAT, h.BaseURL+"/api/advisor/insights?surface=invoices")
		assertContractMCPMatchesHTTP(t, readResponses[`"dividend_headroom"`], h.Client, readOnlyPAT, h.BaseURL+"/api/dividends/headroom")
		assertContractMCPProtocolErrorContains(t, readResponses[`"missing_money_mover"`], `unknown Ledgerly MCP tool "bank_confirm"`)

		writeInput := strings.Join([]string{
			`{"jsonrpc":"2.0","id":"create","method":"tools/call","params":{"name":"create_draft_invoice","arguments":{"clientName":"Fabrikam Ltd","lines":[{"description":"Next month retainer","qty":"1","unitPriceMinor":125000,"currency":"GBP"}]}}}`,
			"",
		}, "\n")
		writeResponses := runContractMCP(t, binary, fullConfig, writeInput)
		var createResult struct {
			StructuredContent struct {
				DraftID string `json:"draft_id"`
				Invoice struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				} `json:"invoice"`
			} `json:"structuredContent"`
		}
		decodeContractMCPResult(t, writeResponses[`"create"`], &createResult)
		if createResult.StructuredContent.DraftID == "" || createResult.StructuredContent.Invoice.ID != createResult.StructuredContent.DraftID || createResult.StructuredContent.Invoice.Status != "draft" {
			t.Fatalf("create_draft_invoice result = %+v, want matching draft invoice", createResult.StructuredContent)
		}
		draftList := runContractLedgerly(t, binary, fullConfig, "--json", "invoice", "list", "--status", "draft")
		assertContractExit(t, draftList, 0)
		assertContractInvoiceListed(t, draftList.output, createResult.StructuredContent.DraftID, "draft")
	})

	t.Run("auth failures", func(t *testing.T) {
		missingConfig := filepath.Join(t.TempDir(), "missing-config.toml")
		missingResult := runContractLedgerly(t, binary, missingConfig, "auth", "status")
		assertContractExit(t, missingResult, 3)
		assertContractOutputContains(t, missingResult.output, "not logged in", "ledgerly auth login")
		assertNoContractStackTrace(t, missingResult.output)

		h.Clock.Set(contractDay(2025, time.May, 4))
		expiredResult := runContractLedgerly(t, binary, expiredConfig, "auth", "status")
		assertContractExit(t, expiredResult, 3)
		assertContractOutputContains(t, expiredResult.output, "Unauthorized", "authentication required")
		assertNoContractStackTrace(t, expiredResult.output)
	})

	t.Run("openapi drift gate configured", func(t *testing.T) {
		repoRoot := findContractRepoRoot(t)
		taskfile := readContractFile(t, filepath.Join(repoRoot, "Taskfile.yml"))
		ci := readContractFile(t, filepath.Join(repoRoot, ".github", "workflows", "ci.yml"))
		for _, want := range []string{"api:generate-go", "internal/cli/gen/client.gen.go", "git status --porcelain -- internal/cli/gen"} {
			if !strings.Contains(taskfile, want) {
				t.Fatalf("Taskfile.yml missing OpenAPI Go drift gate fragment %q", want)
			}
		}
		if !strings.Contains(ci, "task api:drift-check") {
			t.Fatal("CI workflow missing task api:drift-check")
		}
	})
}

type contractCLIResult struct {
	output   string
	exitCode int
}

func buildContractLedgerlyBinary(t *testing.T) string {
	t.Helper()
	repoRoot := findContractRepoRoot(t)
	output := filepath.Join(t.TempDir(), "ledgerly")
	cmd := exec.Command("go", "build", "-o", output, "./cmd/ledgerly")
	cmd.Dir = repoRoot
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	if err := cmd.Run(); err != nil {
		t.Fatalf("build ledgerly CLI: %v\n%s", err, combined.String())
	}
	return output
}

func runContractLedgerly(t *testing.T, binary string, configPath string, args ...string) contractCLIResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fullArgs := append([]string{"--config", configPath}, args...)
	cmd := exec.CommandContext(ctx, binary, fullArgs...)
	cmd.Dir = findContractRepoRoot(t)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("ledgerly %s timed out; output=%s", strings.Join(fullArgs, " "), output.String())
	}
	if err == nil {
		return contractCLIResult{output: output.String(), exitCode: 0}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return contractCLIResult{output: output.String(), exitCode: exitErr.ExitCode()}
	}
	t.Fatalf("ledgerly %s failed to start: %v\n%s", strings.Join(fullArgs, " "), err, output.String())
	return contractCLIResult{}
}

func createContractPAT(t *testing.T, h *harness.Harness, name string, scope string, expiresAt string) string {
	t.Helper()
	body := strings.NewReader(fmt.Sprintf(`{"name":%s,"scope":%s,"expires_at":%s}`, strconvQuote(name), strconvQuote(scope), strconvQuote(expiresAt)))
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
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read PAT response: %v", err)
	}
	var decoded struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		t.Fatalf("decode PAT response: %v; body=%s", err, string(responseBody))
	}
	if resp.StatusCode != http.StatusCreated || decoded.Token == "" {
		t.Fatalf("create PAT status = %d token empty=%t body=%s", resp.StatusCode, decoded.Token == "", string(responseBody))
	}
	return decoded.Token
}

func writeContractCLIConfig(t *testing.T, baseURL string, token string) string {
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

func writeContractStatement(t *testing.T, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "statement.csv")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write statement CSV: %v", err)
	}
	return path
}

func newContractInvoiceService(t testing.TB, h *harness.Harness) *invoicing.Service {
	t.Helper()
	moneyFXPool := testdb.AsModule(t, moneyfx.ModuleName)
	rateLocks := contractRateLocker{service: moneyfx.NewService(moneyfx.NewStore(moneyFXPool), h.Clock)}
	return invoicing.NewService(testdb.AsModule(t, invoicing.ModuleName), invoicing.Store{},
		invoicing.WithClock(h.Clock),
		invoicing.WithTodayRate(contractTodayRate),
		invoicing.WithRateLocker(rateLocks),
		invoicing.WithRateLockReader(rateLocks),
		invoicing.WithLedger(ledger.New(h.LedgerPool, h.Bus)),
		invoicing.WithEventBus(h.Bus),
	)
}

type contractRateLocker struct {
	service *moneyfx.Service
}

func (l contractRateLocker) LockRate(ctx context.Context, tx db.Tx, ref invoicing.RateLockRef, from string, to string, date time.Time) (invoicing.RateLock, error) {
	lock, err := l.service.Lock(ctx, tx, moneyfx.LockRef{Module: ref.Module, Ref: ref.Ref}, from, to, date)
	if err != nil {
		return invoicing.RateLock{}, err
	}
	return invoicing.RateLock{
		ID:       int64(lock.ID),
		From:     lock.From,
		To:       lock.To,
		Rate:     lock.Rate,
		RateDate: lock.RateDate,
		Source:   lock.Source,
	}, nil
}

func (l contractRateLocker) RateLock(ctx context.Context, id int64) (invoicing.RateLock, error) {
	lock, err := l.service.GetLock(ctx, moneyfx.LockID(id))
	if err != nil {
		return invoicing.RateLock{}, err
	}
	return invoicing.RateLock{
		ID:       int64(lock.ID),
		From:     lock.From,
		To:       lock.To,
		Rate:     lock.Rate,
		RateDate: lock.RateDate,
		Source:   lock.Source,
	}, nil
}

func contractTodayRate(ctx context.Context, from string, to string) (invoicing.FXRate, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return invoicing.FXRate{}, time.Time{}, err
	}
	normalizedFrom := strings.ToUpper(strings.TrimSpace(from))
	normalizedTo := strings.ToUpper(strings.TrimSpace(to))
	rateDate := contractDay(2025, time.May, 1)
	if normalizedFrom == normalizedTo && normalizedFrom != "" {
		return invoicing.FXRate{From: normalizedFrom, To: normalizedTo, Value: "1", RateDate: rateDate, Source: "identity"}, rateDate, nil
	}
	if normalizedFrom == "EUR" && normalizedTo == "GBP" {
		return invoicing.FXRate{From: "EUR", To: "GBP", Value: "0.8500", RateDate: rateDate, Source: "ecb"}, rateDate, nil
	}
	return invoicing.FXRate{}, time.Time{}, fmt.Errorf("test rate unavailable for %s/%s", from, to)
}

func newContractBankingService(t testing.TB, h *harness.Harness, invoiceService *invoicing.Service) *banking.Service {
	t.Helper()
	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	moneyFXService := moneyfx.NewService(moneyfx.NewStore(testdb.AsModule(t, moneyfx.ModuleName)), h.Clock)
	dlaService := dla.NewWithBusAndClock(h.DLAPool, h.Bus, h.Clock, ledgerService)
	return banking.NewService(h.BankingPool, ledgerService,
		banking.WithLedgerJournal(ledgerService),
		banking.WithMoneyFX(moneyFXService),
		banking.WithInvoicingSettler(invoiceService),
		banking.WithDLAFileDrawer(dlaService),
		banking.WithEventBus(h.Bus),
	)
}

func mustCreateContractBankingAccount(t testing.TB, ctx context.Context, service *banking.Service, name string, currency string) banking.BankAccount {
	t.Helper()
	account, err := service.CreateAccount(ctx, banking.AccountInput{
		Name:     name,
		Provider: banking.ProviderRevolut,
		Currency: currency,
	})
	if err != nil {
		t.Fatalf("CreateAccount(%s/%s) error = %v", name, currency, err)
	}
	return account
}

func createContractInvoiceDraft(t testing.TB, service *invoicing.Service, clientID string, description string, amount invoicing.Money) invoicing.Invoice {
	t.Helper()
	draft, err := service.CreateDraft(context.Background(), clientID)
	if err != nil {
		t.Fatalf("CreateDraft(%s) error = %v", clientID, err)
	}
	lines := []invoicing.InvoiceLineInput{{
		Description: description,
		Qty:         invoicing.MustQuantity("1"),
		UnitPrice:   amount,
	}}
	updated, err := service.UpdateDraft(context.Background(), draft.ID, invoicing.DraftPatch{Lines: &lines})
	if err != nil {
		t.Fatalf("UpdateDraft(%s) error = %v", draft.ID, err)
	}
	return updated
}

func sendContractInvoice(t testing.TB, service *invoicing.Service, clientID string, description string, amount invoicing.Money) invoicing.Invoice {
	t.Helper()
	draft := createContractInvoiceDraft(t, service, clientID, description, amount)
	sent, err := service.Send(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("Send(%s) error = %v", draft.ID, err)
	}
	return sent
}

func postContractExpense(t testing.TB, h *harness.Harness, date string, account ledger.AccountCode, amount int64) {
	t.Helper()
	ctx := context.Background()
	entryDate, err := time.ParseInLocation(time.DateOnly, date, time.UTC)
	if err != nil {
		t.Fatalf("parse expense date %q: %v", date, err)
	}
	service := ledger.New(h.LedgerPool, h.Bus)
	tx, err := h.LedgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin ledger expense tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	ensureContractCashAccount(t, ctx, service, tx)
	if _, err := service.Post(ctx, tx, ledger.NewJournalEntry{
		Date:         entryDate,
		Description:  "CLI/MCP contract expense " + string(account),
		SourceModule: "cli-mcp-contract",
		SourceRef:    "expense:" + string(account) + ":" + date,
		Postings: []ledger.NewPosting{
			{AccountCode: account, Amount: money.Money{Amount: amount, Currency: "GBP"}, AmountGBP: money.Money{Amount: amount, Currency: "GBP"}},
			{AccountCode: "1000-cash-gbp", Amount: money.Money{Amount: -amount, Currency: "GBP"}, AmountGBP: money.Money{Amount: -amount, Currency: "GBP"}},
		},
	}); err != nil {
		t.Fatalf("post expense: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit expense: %v", err)
	}
	committed = true
}

func ensureContractCashAccount(t testing.TB, ctx context.Context, service *ledger.Service, tx db.Tx) {
	t.Helper()
	currency := "GBP"
	if _, err := service.EnsureAccount(ctx, tx, ledger.AccountSpec{
		Code:     "1000-cash-gbp",
		Name:     "Fixture cash GBP",
		Type:     ledger.AccountTypeAsset,
		Currency: &currency,
	}); err != nil {
		t.Fatalf("ensure cash account: %v", err)
	}
}

func contractBankTxnByReference(t testing.TB, h *harness.Harness, accountID banking.AccountID, reference string) banking.TransactionID {
	t.Helper()
	var id int64
	if err := h.BankingPool.QueryRow(context.Background(), `
SELECT id
FROM transactions
WHERE account_id = $1
	AND reference = $2
ORDER BY id DESC
LIMIT 1`, int64(accountID), reference).Scan(&id); err != nil {
		t.Fatalf("load bank transaction %q: %v", reference, err)
	}
	return banking.TransactionID(id)
}

func assertContractJSONShapeSnapshot(t *testing.T, name string, output string) {
	t.Helper()
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("decode %s JSON: %v\n%s", name, err, output)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("%s JSON has trailing data after first document: %v\n%s", name, err, output)
	}
	body, err := json.MarshalIndent(contractJSONShape(decoded), "", "  ")
	if err != nil {
		t.Fatalf("marshal %s JSON shape: %v", name, err)
	}
	body = append(body, '\n')
	path := filepath.Join("testdata", "cli_mcp", name+".json")
	if os.Getenv("LEDGERLY_UPDATE_CONTRACT_SNAPSHOTS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create snapshot dir: %v", err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatalf("write snapshot %s: %v", path, err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot %s: %v; set LEDGERLY_UPDATE_CONTRACT_SNAPSHOTS=1 to regenerate", path, err)
	}
	if string(body) != string(want) {
		t.Fatalf("%s JSON shape snapshot mismatch\n--- got ---\n%s--- want ---\n%s", name, string(body), string(want))
	}
}

func contractJSONShape(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		shape := make(map[string]any, len(typed))
		for key, item := range typed {
			shape[key] = contractJSONShape(item)
		}
		return shape
	case []any:
		if len(typed) == 0 {
			return []any{}
		}
		shape := contractJSONShape(typed[0])
		for _, item := range typed[1:] {
			shape = mergeContractJSONShape(shape, contractJSONShape(item))
		}
		return []any{shape}
	case json.Number:
		return "number"
	case string:
		return "string"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", typed)
	}
}

func mergeContractJSONShape(left any, right any) any {
	leftMap, leftOK := left.(map[string]any)
	rightMap, rightOK := right.(map[string]any)
	if leftOK && rightOK {
		merged := make(map[string]any, len(leftMap)+len(rightMap))
		for key, value := range leftMap {
			merged[key] = value
		}
		for key, value := range rightMap {
			if existing, ok := merged[key]; ok {
				merged[key] = mergeContractJSONShape(existing, value)
				continue
			}
			merged[key] = value
		}
		return merged
	}
	if reflect.DeepEqual(left, right) {
		return left
	}
	return []any{left, right}
}

func assertContractPLMatchesIT2(t *testing.T, output string) {
	t.Helper()
	var pl struct {
		IncomeTotal struct {
			AmountMinor int64  `json:"amount_minor"`
			Currency    string `json:"currency"`
		} `json:"income_total"`
		RealisedFXGains struct {
			Amount struct {
				AmountMinor int64  `json:"amount_minor"`
				Currency    string `json:"currency"`
			} `json:"amount"`
		} `json:"realised_fx_gains"`
		ExpenseTotal struct {
			AmountMinor int64  `json:"amount_minor"`
			Currency    string `json:"currency"`
		} `json:"expense_total"`
		ProfitBeforeTax struct {
			AmountMinor int64  `json:"amount_minor"`
			Currency    string `json:"currency"`
		} `json:"profit_before_tax"`
		NetProfit struct {
			AmountMinor int64  `json:"amount_minor"`
			Currency    string `json:"currency"`
		} `json:"net_profit"`
	}
	if err := json.Unmarshal([]byte(output), &pl); err != nil {
		t.Fatalf("decode P&L JSON: %v\n%s", err, output)
	}
	assertContractMoney(t, "income_total", pl.IncomeTotal.AmountMinor, pl.IncomeTotal.Currency, 747_000, "GBP")
	assertContractMoney(t, "realised_fx_gains", pl.RealisedFXGains.Amount.AmountMinor, pl.RealisedFXGains.Amount.Currency, 4_500, "GBP")
	assertContractMoney(t, "expense_total", pl.ExpenseTotal.AmountMinor, pl.ExpenseTotal.Currency, 35_000, "GBP")
	assertContractMoney(t, "profit_before_tax", pl.ProfitBeforeTax.AmountMinor, pl.ProfitBeforeTax.Currency, 712_000, "GBP")
	assertContractMoney(t, "net_profit", pl.NetProfit.AmountMinor, pl.NetProfit.Currency, 712_000, "GBP")
}

func assertContractMoney(t testing.TB, label string, gotAmount int64, gotCurrency string, wantAmount int64, wantCurrency string) {
	t.Helper()
	if gotAmount != wantAmount || gotCurrency != wantCurrency {
		t.Fatalf("%s = %d %s, want %d %s", label, gotAmount, gotCurrency, wantAmount, wantCurrency)
	}
}

func firstContractInvoiceID(t *testing.T, output string) string {
	t.Helper()
	for _, field := range strings.Fields(output) {
		if strings.HasPrefix(field, "inv_") || strings.HasPrefix(field, "invoice_") || strings.HasPrefix(field, "invoice-") {
			return strings.TrimSpace(field)
		}
	}
	var invoice struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(output), &invoice) == nil && invoice.ID != "" {
		return invoice.ID
	}
	t.Fatalf("invoice id not found in output:\n%s", output)
	return ""
}

func assertContractInvoiceListed(t *testing.T, output string, id string, status string) {
	t.Helper()
	var decoded struct {
		Invoices []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"invoices"`
	}
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("decode invoice list: %v\n%s", err, output)
	}
	for _, invoice := range decoded.Invoices {
		if invoice.ID == id && invoice.Status == status {
			return
		}
	}
	t.Fatalf("invoice %s with status %s not found in %s", id, status, output)
}

func assertContractRequiresYes(t testing.TB, result contractCLIResult, command string) {
	t.Helper()
	assertContractExit(t, result, 2)
	assertContractOutputContains(t, result.output, "confirmation required", "ledgerly --yes", command)
}

func assertContractExit(t testing.TB, result contractCLIResult, want int) {
	t.Helper()
	if result.exitCode != want {
		t.Fatalf("exit code = %d, want %d; output=%s", result.exitCode, want, result.output)
	}
}

func assertContractOutputContains(t testing.TB, output string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q\n%s", want, output)
		}
	}
}

func assertNoContractStackTrace(t testing.TB, output string) {
	t.Helper()
	for _, forbidden := range []string{"panic:", "goroutine ", "\n\t"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("output contains stack-trace marker %q:\n%s", forbidden, output)
		}
	}
}

func runContractMCP(t *testing.T, binary string, configPath string, input string) map[string]contractMCPResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--config", configPath, "mcp")
	cmd.Dir = findContractRepoRoot(t)
	cmd.Env = contractEnvWithout("LEDGERLY_URL", "LEDGERLY_TOKEN")
	cmd.Stdin = strings.NewReader(input)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ledgerly mcp failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	responses := map[string]contractMCPResponse{}
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var response contractMCPResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("decode MCP response %q: %v", line, err)
		}
		responses[string(response.ID)] = response
	}
	return responses
}

func contractEnvWithout(keys ...string) []string {
	blocked := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		blocked[key] = struct{}{}
	}
	env := os.Environ()
	filtered := env[:0]
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			filtered = append(filtered, entry)
			continue
		}
		if _, skip := blocked[name]; skip {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

type contractMCPResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func assertContractMCPToolNames(t *testing.T, response contractMCPResponse) {
	t.Helper()
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	decodeContractMCPResult(t, response, &result)
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
	for _, forbidden := range []string{"invoice_send", "bank_confirm", "dividend_declare"} {
		if containsString(got, forbidden) {
			t.Fatalf("money-moving MCP tool %q unexpectedly exposed in %#v", forbidden, got)
		}
	}
}

func assertContractMCPMatchesHTTP(t *testing.T, response contractMCPResponse, client *http.Client, token string, endpoint string) {
	t.Helper()
	var result struct {
		StructuredContent json.RawMessage `json:"structuredContent"`
	}
	decodeContractMCPResult(t, response, &result)
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
		t.Fatalf("MCP structuredContent mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", endpoint, string(gotBytes), string(wantBytes))
	}
}

func assertContractMCPToolErrorContains(t *testing.T, response contractMCPResponse, want string) {
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
	decodeContractMCPResult(t, response, &result)
	if !result.IsError {
		t.Fatalf("isError = false, want true")
	}
	if len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, want) {
		t.Fatalf("tool error content = %+v, want %q", result.Content, want)
	}
}

func assertContractMCPProtocolErrorContains(t *testing.T, response contractMCPResponse, want string) {
	t.Helper()
	if response.Error == nil {
		t.Fatalf("MCP protocol error = nil, want message containing %q", want)
	}
	if !strings.Contains(response.Error.Message, want) {
		t.Fatalf("MCP protocol error = %+v, want message containing %q", response.Error, want)
	}
}

func decodeContractMCPResult(t *testing.T, response contractMCPResponse, target any) {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("MCP error = %+v", response.Error)
	}
	if err := json.Unmarshal(response.Result, target); err != nil {
		t.Fatalf("decode MCP result: %v\n%s", err, string(response.Result))
	}
}

func findContractRepoRoot(t testing.TB) string {
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

func readContractFile(t testing.TB, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func stringInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func contractDay(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
