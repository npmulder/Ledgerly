//go:build integration

package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/npmulder/ledgerly/internal/banking"
	"github.com/npmulder/ledgerly/internal/dividends"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

func TestCLIWriteMoneyMoversConfirmSemanticsAgainstHarness(t *testing.T) {
	ctx := context.Background()
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Company(t, h)
	fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{
		time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC): "0.8500",
		time.Date(2025, 5, 2, 0, 0, 0, 0, time.UTC): "0.8600",
	}))

	binary := buildLedgerlyBinary(t)
	configPath := writeCLIConfig(t, h.BaseURL, createFullPAT(t, h))
	invoiceService := newInvoiceService(t, h)
	bankingService := newBankingCommandService(t, h, invoiceService)

	clientAddResult := runLedgerlyBinary(t, binary, configPath,
		"client", "add",
		"--name", "CLI Draft Client",
		"--email", "cli-draft@example.test",
		"--currency", "GBP",
		"--terms", "14",
		"--vat-treatment", "domestic",
		"--address-line1", "18 Athol St",
		"--locality", "Douglas",
		"--postal-code", "IM1 1JA",
		"--country", "IM",
	)
	assertCLIExit(t, clientAddResult, 0)
	assertOutputContains(t, clientAddResult.output, "CLI Draft Client")

	fabrikam := fixtures.Fabrikam(t, h)
	invoiceCreateResult := runLedgerlyBinary(t, binary, configPath, "invoice", "create", "--client", fabrikam.ID, "--line", "Consulting:1:1000")
	assertCLIExit(t, invoiceCreateResult, 0)
	assertOutputContains(t, invoiceCreateResult.output, "STATUS", "draft", "AMOUNT")

	dlaAddResult := runLedgerlyBinary(t, binary, configPath,
		"dla", "add",
		"--kind", "expense-owed",
		"--date", "2025-05-01",
		"--amount", "2500",
		"--description", "Personally paid software",
		"--expense-category", "5010-software",
		"--source-ref", "manual:cli-expense-owed",
	)
	assertCLIExit(t, dlaAddResult, 0)
	assertOutputContains(t, dlaAddResult.output, "SOURCE REF", "manual:cli-expense-owed")

	draftToSend := createEURInvoiceDraft(t, h, invoiceService, 450_000)
	assertRequiresYes(t, runLedgerlyBinary(t, binary, configPath, "invoice", "send", draftToSend.ID), "invoice send")
	assertInvoiceStatus(t, invoiceService, draftToSend.ID, invoicing.InvoiceStatusDraft)
	sendResult := runLedgerlyBinary(t, binary, configPath, "--yes", "invoice", "send", draftToSend.ID)
	assertCLIExit(t, sendResult, 0)
	assertOutputContains(t, sendResult.output, "LOCKED RATE", "0.85")
	assertInvoiceStatus(t, invoiceService, draftToSend.ID, invoicing.InvoiceStatusSent)

	sentToRevert, err := invoiceService.Send(ctx, createEURInvoiceDraft(t, h, invoiceService, 125_000).ID)
	if err != nil {
		t.Fatalf("Send(revert fixture) error = %v", err)
	}
	assertRequiresYes(t, runLedgerlyBinary(t, binary, configPath, "invoice", "revert", sentToRevert.ID), "invoice revert")
	assertInvoiceStatus(t, invoiceService, sentToRevert.ID, invoicing.InvoiceStatusSent)
	revertResult := runLedgerlyBinary(t, binary, configPath, "--yes", "invoice", "revert", sentToRevert.ID)
	assertCLIExit(t, revertResult, 0)
	assertInvoiceStatus(t, invoiceService, sentToRevert.ID, invoicing.InvoiceStatusDraft)

	confirmInvoice, err := invoiceService.Send(ctx, createEURInvoiceDraft(t, h, invoiceService, 450_000).ID)
	if err != nil {
		t.Fatalf("Send(confirm fixture) error = %v", err)
	}
	confirmAccount := mustCreateBankingAccount(t, ctx, bankingService, "CLI Confirm EUR", "EUR")
	confirmTxn := importDashboardBankTxn(t, ctx, h, bankingService, confirmAccount.ID, dashboardBankTxn{
		ID:        "cli-confirm-match",
		Date:      time.Date(2025, 5, 2, 12, 0, 0, 0, time.UTC),
		Payee:     "Contoso GmbH",
		Reference: "INV-2025-03",
		Amount:    money.Money{Amount: 450_000, Currency: "EUR"},
	})
	mustRecordDashboardSuggestion(t, ctx, bankingService, confirmTxn, banking.SuggestionKindInvoiceMatch, 0.98, confirmInvoice.ID, "invoice match")
	assertRequiresYes(t, runLedgerlyBinary(t, binary, configPath, "bank", "confirm", stringInt64(int64(confirmTxn))), "bank confirm")
	assertBankingTxnState(t, h, confirmTxn, banking.TransactionStateSuggested)
	confirmResult := runLedgerlyBinary(t, binary, configPath, "--yes", "bank", "confirm", stringInt64(int64(confirmTxn)))
	assertCLIExit(t, confirmResult, 0)
	assertOutputContains(t, confirmResult.output, "REALISED FX", "4500 GBP")
	assertBankingTxnState(t, h, confirmTxn, banking.TransactionStateReconciled)
	assertInvoiceStatus(t, invoiceService, confirmInvoice.ID, invoicing.InvoiceStatusPaid)

	dlaAccount := mustCreateBankingAccount(t, ctx, bankingService, "CLI DLA GBP", "GBP")
	dlaTxn := importDashboardBankTxn(t, ctx, h, bankingService, dlaAccount.ID, dashboardBankTxn{
		ID:        "cli-file-dla",
		Date:      time.Date(2025, 5, 2, 13, 0, 0, 0, time.UTC),
		Payee:     "N Meyer personal card",
		Reference: "director drawing",
		Amount:    money.Money{Amount: -12_000, Currency: "GBP"},
	})
	mustRecordDashboardSuggestion(t, ctx, bankingService, dlaTxn, banking.SuggestionKindDLA, 0.88, "director-loan", "director drawing")
	assertRequiresYes(t, runLedgerlyBinary(t, binary, configPath, "bank", "file-dla", stringInt64(int64(dlaTxn))), "bank file-dla")
	assertBankingTxnState(t, h, dlaTxn, banking.TransactionStateSuggested)
	fileDLAResult := runLedgerlyBinary(t, binary, configPath, "--yes", "bank", "file-dla", stringInt64(int64(dlaTxn)))
	assertCLIExit(t, fileDLAResult, 0)
	assertBankingTxnState(t, h, dlaTxn, banking.TransactionStateReconciled)
	assertDLAEntry(t, h, bankingTxnRef(dlaTxn), 12_000)
	repeatFileDLAResult := runLedgerlyBinary(t, binary, configPath, "--yes", "bank", "file-dla", stringInt64(int64(dlaTxn)))
	assertCLIExit(t, repeatFileDLAResult, 1)
	assertOutputContains(t, repeatFileDLAResult.output, "already")

	recodeAccount := mustCreateBankingAccount(t, ctx, bankingService, "CLI Recode GBP", "GBP")
	recodeTxn := importDashboardBankTxn(t, ctx, h, bankingService, recodeAccount.ID, dashboardBankTxn{
		ID:        "cli-recode",
		Date:      time.Date(2025, 5, 2, 14, 0, 0, 0, time.UTC),
		Payee:     "Acme SaaS",
		Reference: "subscription",
		Amount:    money.Money{Amount: -3_450, Currency: "GBP"},
	})
	mustRecordDashboardSuggestion(t, ctx, bankingService, recodeTxn, banking.SuggestionKindPayeeRule, 0.72, "5010-software", "payee rule")
	assertRequiresYes(t, runLedgerlyBinary(t, binary, configPath, "bank", "recode", stringInt64(int64(recodeTxn)), "--account", "5010-software"), "bank recode")
	assertBankingTxnState(t, h, recodeTxn, banking.TransactionStateSuggested)
	recodeResult := runLedgerlyBinary(t, binary, configPath, "--yes", "bank", "recode", stringInt64(int64(recodeTxn)), "--account", "5010-software")
	assertCLIExit(t, recodeResult, 0)
	assertBankingTxnState(t, h, recodeTxn, banking.TransactionStateReconciled)

	loadDividendsPack(t, "")
	postRetainedEarnings(t, h, "2025-03-31", 2_000_000)
	dividendService := newDividendsService(t, h)
	assertRequiresYes(t, runLedgerlyBinary(t, binary, configPath, "dividend", "declare", "123400"), "dividend declare")
	assertDividendHistoryCount(t, dividendService, 0)
	declareResult := runLedgerlyBinary(t, binary, configPath, "--yes", "dividend", "declare", "123400")
	assertCLIExit(t, declareResult, 0)
	assertDividendHistoryCount(t, dividendService, 1)
}

func TestCLIBankImportRoundTripWithFixtureCSV(t *testing.T) {
	ctx := context.Background()
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Company(t, h)
	fixtures.Rates(t, h)
	binary := buildLedgerlyBinary(t)
	configPath := writeCLIConfig(t, h.BaseURL, createFullPAT(t, h))
	invoiceService := newInvoiceService(t, h)
	bankingService := newBankingCommandService(t, h, invoiceService)
	account := mustCreateBankingAccount(t, ctx, bankingService, "CLI Import GBP", "GBP")

	csvPath := filepath.Join(t.TempDir(), "statement.csv")
	csvBody := `Date started (UTC),Date completed (UTC),ID,Type,Description,Reference,Amount,Fee,Currency,State,Balance
2025-06-01 10:00:00,2025-06-01 10:00:05,cli-import-1,CARD_PAYMENT,Acme SaaS,Subscription,-34.50,0.00,GBP,COMPLETED,100.00
2025-06-02 10:00:00,2025-06-02 10:00:05,cli-import-2,TRANSFER,Contoso GmbH,INV-2025-01,4500.00,0.00,GBP,COMPLETED,4600.00
`
	if err := os.WriteFile(csvPath, []byte(csvBody), 0o644); err != nil {
		t.Fatalf("write CSV fixture: %v", err)
	}

	result := runLedgerlyBinary(t, binary, configPath, "--yes", "bank", "import", csvPath, "--account", stringInt64(int64(account.ID)))
	assertCLIExit(t, result, 0)
	assertOutputContains(t, result.output, "TOTAL", "NEW", "DUPLICATES", "statement.csv")
	assertBankTransactionCount(t, h, account.ID, 2)
}

func TestCLIDividendDeclarePTYPreviewShowsValidationStrip(t *testing.T) {
	h := newDividendsHarness(t)
	loadDividendsPack(t, "")
	postRetainedEarnings(t, h, "2025-03-31", 2_000_000)
	binary := buildLedgerlyBinary(t)
	configPath := writeCLIConfig(t, h.BaseURL, createFullPAT(t, h))

	output, exitCode := runLedgerlyPTY(t, binary, "--config", configPath, "dividend", "declare", "123400")
	if exitCode != 2 {
		t.Fatalf("PTY exit code = %d, want 2; output=%s", exitCode, output)
	}
	assertOutputContains(t, output, "Preview: declare dividend 123400 GBP", "HEADROOM CHECK", "SET-ASIDE ESTIMATE", "Continue? [y/N]:")
}

type cliRunResult struct {
	output   string
	exitCode int
}

func buildLedgerlyBinary(t *testing.T) string {
	t.Helper()
	repoRoot := findHarnessRepoRoot(t)
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

func runLedgerlyBinary(t *testing.T, binary string, configPath string, args ...string) cliRunResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fullArgs := append([]string{"--config", configPath}, args...)
	cmd := exec.CommandContext(ctx, binary, fullArgs...)
	cmd.Dir = findHarnessRepoRoot(t)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("ledgerly %s timed out; output=%s", strings.Join(fullArgs, " "), output.String())
	}
	if err == nil {
		return cliRunResult{output: output.String(), exitCode: 0}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return cliRunResult{output: output.String(), exitCode: exitErr.ExitCode()}
	}
	t.Fatalf("ledgerly %s failed to start: %v\n%s", strings.Join(fullArgs, " "), err, output.String())
	return cliRunResult{}
}

func runLedgerlyPTY(t *testing.T, binary string, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = findHarnessRepoRoot(t)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("start PTY command: %v", err)
	}
	defer func() {
		_ = ptmx.Close()
	}()

	outputChunks := make(chan string, 64)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 512)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				outputChunks <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	var output strings.Builder
	promptSeen := false
	for !promptSeen {
		select {
		case chunk := <-outputChunks:
			output.WriteString(chunk)
			if strings.Contains(output.String(), "Continue? [y/N]:") {
				promptSeen = true
				if _, err := ptmx.Write([]byte("n\n")); err != nil {
					t.Fatalf("write PTY response: %v; output=%s", err, output.String())
				}
			}
		case <-ctx.Done():
			t.Fatalf("PTY command timed out waiting for prompt; output=%s", output.String())
		}
	}

	waitErr := cmd.Wait()
	<-readDone
closeDrain:
	for {
		select {
		case chunk := <-outputChunks:
			output.WriteString(chunk)
		default:
			break closeDrain
		}
	}
	if ctx.Err() != nil {
		t.Fatalf("PTY command timed out; output=%s", output.String())
	}
	if waitErr == nil {
		return output.String(), 0
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return output.String(), exitErr.ExitCode()
	}
	t.Fatalf("PTY command wait failed: %v; output=%s", waitErr, output.String())
	return output.String(), 1
}

func createFullPAT(t testing.TB, h *harness.Harness) string {
	t.Helper()
	body := strings.NewReader(`{"name":"CLI write integration","scope":"full","expires_at":"2030-01-01T00:00:00Z"}`)
	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodPost, h.BaseURL+"/api/identity/pats", body)
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
	if resp.StatusCode != nethttp.StatusCreated || decoded.Token == "" {
		t.Fatalf("create PAT status = %d token empty=%t", resp.StatusCode, decoded.Token == "")
	}
	return decoded.Token
}

func assertRequiresYes(t testing.TB, result cliRunResult, command string) {
	t.Helper()
	assertCLIExit(t, result, 2)
	assertOutputContains(t, result.output, "confirmation required", "ledgerly --yes", command)
}

func assertCLIExit(t testing.TB, result cliRunResult, want int) {
	t.Helper()
	if result.exitCode != want {
		t.Fatalf("exit code = %d, want %d; output=%s", result.exitCode, want, result.output)
	}
}

func assertOutputContains(t testing.TB, output string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q\n%s", want, output)
		}
	}
}

func assertInvoiceStatus(t testing.TB, service *invoicing.Service, id string, want invoicing.InvoiceStatus) {
	t.Helper()
	invoice, err := service.Invoice(context.Background(), id)
	if err != nil {
		t.Fatalf("Invoice(%s) error = %v", id, err)
	}
	if invoice.Status != want {
		t.Fatalf("invoice %s status = %q, want %q", id, invoice.Status, want)
	}
}

func assertDividendHistoryCount(t testing.TB, service *dividends.Service, want int) {
	t.Helper()
	history, err := service.History(context.Background())
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history) != want {
		t.Fatalf("dividend history count = %d, want %d", len(history), want)
	}
}

func assertBankTransactionCount(t testing.TB, h *harness.Harness, accountID banking.AccountID, want int) {
	t.Helper()
	var got int
	if err := h.BankingPool.QueryRow(context.Background(), `
SELECT count(*)::integer
FROM transactions
WHERE account_id = $1`, int64(accountID)).Scan(&got); err != nil {
		t.Fatalf("count bank transactions for account %d: %v", accountID, err)
	}
	if got != want {
		t.Fatalf("bank transaction count for account %d = %d, want %d", accountID, got, want)
	}
}
