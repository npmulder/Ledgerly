//go:build integration

package it_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	nethttp "net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/banking"
	"github.com/npmulder/ledgerly/internal/dla"
	it "github.com/npmulder/ledgerly/internal/it"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

func TestReconciliationFlows(t *testing.T) {
	t.Run("dedupes overlapping Revolut CSV files", testReconciliationDedupe)
	t.Run("files director payee to DLA through review queue", testReconciliationFileToDLA)
	t.Run("keeps DLA presentation ledger consistent with ledger account", testReconciliationDLAConsistency)
	t.Run("recodes spend and learns payee rule for next import", testReconciliationRecodeLearning)
	t.Run("emits overdrawn round-trip events once per side crossing", testReconciliationOverdrawnRoundTrip)
	t.Run("excludes and unexcludes without ledger postings", testReconciliationExclude)
}

func testReconciliationDedupe(t *testing.T) {
	f := newReconciliationFixture(t, time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC))
	account := f.createAccount(t, "Revolut GBP", "GBP")

	txnA := fixtures.RevolutTxn{
		Date:      time.Date(2026, 4, 1, 8, 30, 0, 0, time.UTC),
		ID:        "it3-dedupe-a",
		Payee:     "ACME & Sons",
		Reference: "Invoice   1001",
		Amount:    reconMoney(123_456, "GBP"),
		Balance:   reconMoney(123_456, "GBP"),
	}
	first := f.importCSV(t, account.ID, "statement-a.csv", txnA)
	assertBatchSummary(t, first, 1, 1, 0)

	txnADuplicate := txnA
	txnADuplicate.ID = "it3-dedupe-a-export-2"
	txnADuplicate.Reference = "  Invoice 1001  "
	txnB := fixtures.RevolutTxn{
		Date:      time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC),
		ID:        "it3-dedupe-b",
		Payee:     "Beta Ltd",
		Reference: "Invoice 1002",
		Amount:    reconMoney(-2_500, "GBP"),
		Balance:   reconMoney(120_956, "GBP"),
	}
	second := f.importCSV(t, account.ID, "statement-a-plus-b.csv", txnADuplicate, txnB)
	assertBatchSummary(t, second, 2, 1, 1)
	assertBankingTransactionCount(t, f, account.ID, 2)
	it.AssertLedgerBalanced(t, f.h)
}

func testReconciliationFileToDLA(t *testing.T) {
	txDate := time.Date(2025, 6, 2, 12, 0, 0, 0, time.UTC)
	f := newReconciliationFixture(t, time.Date(2025, 6, 2, 9, 0, 0, 0, time.UTC))
	account := f.createAccount(t, "Revolut GBP", "GBP")

	summary := importCSVViaHTTP(t, f.h, account.ID, "director-drawing.csv", fixtures.RevolutCSV(fixtures.RevolutTxn{
		Date:      txDate,
		ID:        "it3-file-dla-http",
		Payee:     "N Meyer personal card",
		Reference: "director drawing",
		Amount:    reconMoney(-10_000, "GBP"),
		Balance:   reconMoney(-10_000, "GBP"),
	}))
	assertHTTPBatchSummary(t, summary, 1, 1, 0)

	queue := getReviewQueueViaHTTP(t, f.h)
	if len(queue.Suggestions) != 1 {
		t.Fatalf("DLA suggestions = %d, want 1; queue=%+v", len(queue.Suggestions), queue)
	}
	card := queue.Suggestions[0]
	if card.Kind != "suggestion" ||
		card.Target.Type != "dla" ||
		card.Target.ID != "director-1" ||
		!strings.Contains(card.Explanation, "N. Meyer") {
		t.Fatalf("DLA review card = %+v, want director-name DLA suggestion", card)
	}
	txnID := banking.TransactionID(card.Transaction.ID)

	recorder := newReconciliationEventRecorder(t, f.h)
	response := postBankingCommand(t, f.h, fmt.Sprintf("/api/banking/transactions/%d/file-dla", txnID), nil, nethttp.StatusOK)
	var command reconciliationCommandResponse
	decodeJSON(t, response, &command)
	if command.AmountGBP == nil || *command.AmountGBP != (reconciliationMoneyResponse{AmountMinor: 10_000, Currency: "GBP"}) {
		t.Fatalf("FileToDLA amount_gbp = %#v, want GBP 100.00", command.AmountGBP)
	}
	if command.Transaction == nil || command.Transaction.State != banking.TransactionStateReconciled {
		t.Fatalf("FileToDLA transaction = %#v, want reconciled transaction", command.Transaction)
	}
	recorder.assertReconciled(t, []banking.TransactionReconciled{{
		TransactionID: txnID,
		Kind:          banking.SuggestionKindDLA,
	}})
	assertBankingTxnState(t, f, txnID, banking.TransactionStateReconciled)
	assertActiveSuggestionCount(t, f, txnID, 0)
	assertDLAEntry(t, f, bankingTxnSource(txnID), 10_000)
	assertModuleLedgerPostings(t, f, dla.ModuleName, bankingTxnSource(txnID), []reconciliationPosting{
		{account: string(dla.DLAAccountCode), amount: 10_000, currency: "GBP", amountGBP: 10_000},
		{account: string(account.LedgerAccountCode), amount: -10_000, currency: "GBP", amountGBP: -10_000},
	})

	_, err := f.banking.FileToDLA(f.ctx, txnID)
	var already *banking.AlreadyReconciledError
	if !errors.Is(err, banking.ErrAlreadyReconciled) || !errors.As(err, &already) {
		t.Fatalf("FileToDLA duplicate service error = %v, want typed ErrAlreadyReconciled", err)
	}
	postBankingCommand(t, f.h, fmt.Sprintf("/api/banking/transactions/%d/file-dla", txnID), nil, nethttp.StatusConflict)
	assertDLAEntryCount(t, f, bankingTxnSource(txnID), 1)

	forced := errors.New("forced FileToDLA rollback")
	hooked := f.bankingService(banking.WithReconciliationCommandHooks(banking.ReconciliationCommandHooks{
		AfterFileDLADrawing: func(context.Context) error {
			return forced
		},
	}))
	rollbackTxn := fixtures.RevolutTxn{
		Date:      txDate,
		ID:        "it3-file-dla-rollback",
		Payee:     "N Meyer personal transfer",
		Reference: "director drawing rollback",
		Amount:    reconMoney(-20_000, "GBP"),
		Balance:   reconMoney(-30_000, "GBP"),
	}
	if _, err := hooked.ImportCSV(f.ctx, account.ID, banking.ImportFile{
		Filename: "director-drawing-rollback.csv",
		Reader:   bytes.NewReader(fixtures.RevolutCSV(rollbackTxn)),
	}); err != nil {
		t.Fatalf("ImportCSV(rollback txn) error = %v", err)
	}
	rollbackTxnID := mustTransactionIDByReference(t, f, rollbackTxn.Reference)
	_, err = hooked.FileToDLA(f.ctx, rollbackTxnID)
	if !errors.Is(err, forced) {
		t.Fatalf("FileToDLA forced rollback error = %v, want forced error", err)
	}
	assertBankingTxnState(t, f, rollbackTxnID, banking.TransactionStateSuggested)
	assertActiveSuggestionCount(t, f, rollbackTxnID, 1)
	assertDLAEntryCount(t, f, bankingTxnSource(rollbackTxnID), 0)
	assertJournalEntryCount(t, f, dla.ModuleName, bankingTxnSource(rollbackTxnID), 0)
	it.AssertLedgerBalanced(t, f.h)
}

func testReconciliationDLAConsistency(t *testing.T) {
	f := newReconciliationFixture(t, time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC))
	account := f.createAccount(t, "Revolut GBP", "GBP")
	entryDate := f.h.Clock.Now()

	f.fileDrawingFromBanking(t, dla.TxnRef{
		Ref:             "banking:it3-consistency-drawing",
		Date:            entryDate,
		Amount:          reconMoney(10_000, "GBP"),
		CashAccountCode: account.LedgerAccountCode,
		Description:     "Director drawing",
	})
	if err := f.dla.AddEntry(f.ctx, dla.NewEntry{
		Date:            entryDate,
		Kind:            dla.EntryKindRepayment,
		Description:     "Director repayment",
		Amount:          reconMoney(4_000, "GBP"),
		Source:          "manual:it3-consistency-repayment",
		CashAccountCode: account.LedgerAccountCode,
	}); err != nil {
		t.Fatalf("AddEntry(repayment) error = %v", err)
	}
	if err := f.dla.AddEntry(f.ctx, dla.NewEntry{
		Date:               entryDate,
		Kind:               dla.EntryKindExpenseOwed,
		Description:        "Software paid personally",
		Amount:             reconMoney(2_500, "GBP"),
		Source:             "manual:it3-consistency-expense",
		ExpenseAccountCode: "5010-software",
	}); err != nil {
		t.Fatalf("AddEntry(expense-owed) error = %v", err)
	}

	report, err := f.dla.CheckConsistency(f.ctx, entryDate)
	if err != nil {
		t.Fatalf("CheckConsistency() error = %v; report=%+v", err, report)
	}
	if !report.Consistent ||
		report.DerivedBalance != reconMoney(-3_500, "GBP") ||
		report.LedgerBalance != reconMoney(3_500, "GBP") ||
		report.ExpectedLedgerBalance != reconMoney(3_500, "GBP") {
		t.Fatalf("consistency report = %+v, want derived DR 35.00 and matching ledger DLA account", report)
	}
	it.AssertLedgerBalanced(t, f.h)
}

func testReconciliationRecodeLearning(t *testing.T) {
	f := newReconciliationFixture(t, time.Date(2025, 7, 1, 9, 0, 0, 0, time.UTC))
	f.banking = f.bankingService(banking.WithPayeeRuleAutoPostThreshold(1))
	account := f.createAccount(t, "Revolut GBP", "GBP")

	firstTxn := f.importOne(t, account.ID, "recode-first.csv", fixtures.RevolutTxn{
		Date:      time.Date(2025, 7, 1, 12, 0, 0, 0, time.UTC),
		ID:        "it3-recode-first",
		Payee:     "Acme SaaS",
		Reference: "subscription July",
		Amount:    reconMoney(-3_450, "GBP"),
		Balance:   reconMoney(-3_450, "GBP"),
	})
	f.recordSuggestion(t, firstTxn, banking.SuggestionKindPayeeRule, 0.72, "5010-software", "payee rule recode candidate")

	result, err := f.banking.Recode(f.ctx, firstTxn, "5010-software")
	if err != nil {
		t.Fatalf("Recode() error = %v", err)
	}
	if result.Rule.AccountCode != "5010-software" || result.Rule.TimesApplied != 1 || result.Rule.CreatedFrom != banking.PayeeRuleCreatedFromRecode {
		t.Fatalf("Recode() rule = %#v, want software rule applied once from recode", result.Rule)
	}
	assertBankingTxnState(t, f, firstTxn, banking.TransactionStateReconciled)
	assertModuleLedgerPostings(t, f, banking.ModuleName, bankingCommandSource(firstTxn, "recode"), []reconciliationPosting{
		{account: "5010-software", amount: 3_450, currency: "GBP", amountGBP: 3_450},
		{account: string(account.LedgerAccountCode), amount: -3_450, currency: "GBP", amountGBP: -3_450},
	})

	nextTxn := f.importOne(t, account.ID, "recode-next.csv", fixtures.RevolutTxn{
		Date:      time.Date(2025, 7, 2, 12, 0, 0, 0, time.UTC),
		ID:        "it3-recode-next",
		Payee:     "Acme SaaS",
		Reference: "subscription August",
		Amount:    reconMoney(-5_900, "GBP"),
		Balance:   reconMoney(-9_350, "GBP"),
	})
	suggestion := activeBankingSuggestion(t, f, nextTxn)
	if suggestion.Kind != banking.SuggestionKindPayeeRule ||
		suggestion.Target != "5010-software" ||
		!suggestion.AutoPostable ||
		!strings.Contains(suggestion.Explanation, "applied 2 times") {
		t.Fatalf("next import suggestion = %#v, want auto-postable payee-rule applied 2 times", suggestion)
	}
	assertPayeeRuleTimesApplied(t, f, "acme saas", "5010-software", 2)

	queue := getReviewQueueViaHTTP(t, f.h)
	if len(queue.Rules) != 1 {
		t.Fatalf("payee-rule review cards = %d, want 1; queue=%+v", len(queue.Rules), queue)
	}
	ruleCard := queue.Rules[0]
	if ruleCard.Target.AccountCode != "5010-software" ||
		ruleCard.Target.TimesApplied == nil ||
		*ruleCard.Target.TimesApplied != 2 ||
		!strings.Contains(ruleCard.Explanation, "applied 2 times") {
		t.Fatalf("payee-rule card = %+v, want software target with times_applied 2 and applied text", ruleCard)
	}
	it.AssertLedgerBalanced(t, f.h)
}

func testReconciliationOverdrawnRoundTrip(t *testing.T) {
	f := newReconciliationFixture(t, time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC))
	account := f.createAccount(t, "Revolut GBP", "GBP")
	events := newDLAEventRecorder(t, f.h)
	entryDate := f.h.Clock.Now()

	f.fileDrawingFromBanking(t, dla.TxnRef{
		Ref:             "banking:it3-overdrawn-first",
		Date:            entryDate,
		Amount:          reconMoney(10_000, "GBP"),
		CashAccountCode: account.LedgerAccountCode,
	})
	events.assertCounts(t, 1, 0)

	f.fileDrawingFromBanking(t, dla.TxnRef{
		Ref:             "banking:it3-overdrawn-further",
		Date:            entryDate,
		Amount:          reconMoney(500, "GBP"),
		CashAccountCode: account.LedgerAccountCode,
	})
	events.assertCounts(t, 1, 0)

	if err := f.dla.AddEntry(f.ctx, dla.NewEntry{
		Date:            entryDate,
		Kind:            dla.EntryKindRepayment,
		Description:     "Director repayment crossing back",
		Amount:          reconMoney(11_000, "GBP"),
		Source:          "manual:it3-overdrawn-back",
		CashAccountCode: account.LedgerAccountCode,
	}); err != nil {
		t.Fatalf("AddEntry(repayment crossing back) error = %v", err)
	}
	events.assertCounts(t, 1, 1)

	if err := f.dla.AddEntry(f.ctx, dla.NewEntry{
		Date:               entryDate,
		Kind:               dla.EntryKindExpenseOwed,
		Description:        "Further same-side credit",
		Amount:             reconMoney(250, "GBP"),
		Source:             "manual:it3-overdrawn-same-side",
		ExpenseAccountCode: "5010-software",
	}); err != nil {
		t.Fatalf("AddEntry(same-side credit) error = %v", err)
	}
	events.assertCounts(t, 1, 1)
	events.assertBalances(t, reconMoney(-10_000, "GBP"), reconMoney(500, "GBP"))
	it.AssertLedgerBalanced(t, f.h)
}

func testReconciliationExclude(t *testing.T) {
	f := newReconciliationFixture(t, time.Date(2025, 8, 1, 9, 0, 0, 0, time.UTC))
	account := f.createAccount(t, "Revolut GBP", "GBP")
	txnID := f.importOne(t, account.ID, "exclude.csv", fixtures.RevolutTxn{
		Date:      time.Date(2025, 8, 1, 12, 0, 0, 0, time.UTC),
		ID:        "it3-exclude",
		Payee:     "Duplicate export",
		Reference: "duplicate statement row",
		Amount:    reconMoney(-999, "GBP"),
		Balance:   reconMoney(-999, "GBP"),
	})
	before := ledgerPostingCount(t, f)

	if _, err := f.banking.Exclude(f.ctx, txnID, "duplicate statement row"); err != nil {
		t.Fatalf("Exclude() error = %v", err)
	}
	assertBankingTxnState(t, f, txnID, banking.TransactionStateExcluded)
	if got := ledgerPostingCount(t, f); got != before {
		t.Fatalf("ledger posting count after Exclude = %d, want unchanged %d", got, before)
	}

	if _, err := f.banking.Unexclude(f.ctx, txnID, "review again"); err != nil {
		t.Fatalf("Unexclude() error = %v", err)
	}
	assertBankingTxnState(t, f, txnID, banking.TransactionStateUnreconciled)
	if got := ledgerPostingCount(t, f); got != before {
		t.Fatalf("ledger posting count after Unexclude = %d, want unchanged %d", got, before)
	}
	it.AssertLedgerBalanced(t, f.h)
}

type reconciliationFixture struct {
	ctx           context.Context
	h             *harness.Harness
	company       fixtures.CompanyFixture
	ledger        *ledger.Service
	moneyFX       *moneyfx.Service
	dla           *dla.Service
	banking       *banking.Service
	directorNames []string
}

func newReconciliationFixture(t *testing.T, clockStart time.Time, rates ...fixtures.RateTable) *reconciliationFixture {
	t.Helper()

	h := harness.New(t, harness.Options{ClockStart: clockStart})
	company := fixtures.Company(t, h)
	fixtures.Rates(t, h, rates...)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	moneyFXService := moneyfx.NewService(moneyfx.NewStore(testdb.AsModule(t, moneyfx.ModuleName)), h.Clock)
	dlaService := dla.NewWithBusAndClock(h.DLAPool, h.Bus, h.Clock, ledgerService)
	f := &reconciliationFixture{
		ctx:           ctx,
		h:             h,
		company:       company,
		ledger:        ledgerService,
		moneyFX:       moneyFXService,
		dla:           dlaService,
		directorNames: company.DirectorNames(),
	}
	f.banking = f.bankingService()
	return f
}

func (f *reconciliationFixture) bankingService(opts ...banking.ServiceOption) *banking.Service {
	serviceOpts := []banking.ServiceOption{
		banking.WithLedgerJournal(f.ledger),
		banking.WithMoneyFX(f.moneyFX),
		banking.WithDLAFileDrawer(f.dla),
		banking.WithEventBus(f.h.Bus),
		banking.WithDirectorNames(staticDirectorNames(f.directorNames)),
	}
	serviceOpts = append(serviceOpts, opts...)
	return banking.NewService(f.h.BankingPool, f.ledger, serviceOpts...)
}

func (f *reconciliationFixture) createAccount(t testing.TB, name string, currency string) banking.BankAccount {
	t.Helper()
	account, err := f.banking.CreateAccount(f.ctx, banking.AccountInput{
		Name:     name,
		Provider: banking.ProviderRevolut,
		Currency: currency,
	})
	if err != nil {
		t.Fatalf("CreateAccount(%s/%s) error = %v", name, currency, err)
	}
	return account
}

func (f *reconciliationFixture) importCSV(
	t testing.TB,
	accountID banking.AccountID,
	filename string,
	txns ...fixtures.RevolutTxn,
) banking.BatchSummary {
	t.Helper()
	summary, err := f.banking.ImportCSV(f.ctx, accountID, banking.ImportFile{
		Filename: filename,
		Reader:   bytes.NewReader(fixtures.RevolutCSV(txns...)),
	})
	if err != nil {
		t.Fatalf("ImportCSV(%s) error = %v", filename, err)
	}
	return summary
}

func (f *reconciliationFixture) importOne(
	t testing.TB,
	accountID banking.AccountID,
	filename string,
	txn fixtures.RevolutTxn,
) banking.TransactionID {
	t.Helper()
	f.importCSV(t, accountID, filename, txn)
	return mustTransactionIDByReference(t, f, txn.Reference)
}

func (f *reconciliationFixture) recordSuggestion(
	t testing.TB,
	txnID banking.TransactionID,
	kind banking.SuggestionKind,
	confidence float64,
	target string,
	explanation string,
) banking.Suggestion {
	t.Helper()
	suggestion, err := f.banking.RecordSuggestion(f.ctx, banking.SuggestionInput{
		TransactionID: txnID,
		Kind:          kind,
		Confidence:    confidence,
		Target:        target,
		Explanation:   explanation,
		CreatedBy:     "it3-reconciliation",
	})
	if err != nil {
		t.Fatalf("RecordSuggestion(%d) error = %v", txnID, err)
	}
	return suggestion
}

func (f *reconciliationFixture) fileDrawingFromBanking(t testing.TB, src dla.TxnRef) {
	t.Helper()
	tx, err := f.h.BankingPool.Begin(f.ctx)
	if err != nil {
		t.Fatalf("begin banking transaction for DLA drawing: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	if err := f.dla.FileDrawing(f.ctx, tx, src); err != nil {
		t.Fatalf("FileDrawing(%s) error = %v", src.Ref, err)
	}
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatalf("commit banking transaction for DLA drawing: %v", err)
	}
	committed = true
}

type staticDirectorNames []string

func (s staticDirectorNames) DirectorNames(context.Context) ([]string, error) {
	return append([]string(nil), s...), nil
}

type reconciliationBatchSummaryResponse struct {
	BatchID    int64  `json:"batch_id"`
	AccountID  int64  `json:"account_id"`
	Filename   string `json:"filename"`
	ImportedAt string `json:"imported_at"`
	Total      int    `json:"total"`
	New        int    `json:"new"`
	Duplicates int    `json:"duplicates"`
}

type reconciliationMoneyResponse struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

type reconciliationTransactionResponse struct {
	ID            int64                       `json:"id"`
	AccountID     int64                       `json:"account_id"`
	Date          string                      `json:"date"`
	Amount        reconciliationMoneyResponse `json:"amount"`
	Payee         string                      `json:"payee"`
	Reference     string                      `json:"reference"`
	ProviderMeta  map[string]string           `json:"provider_meta"`
	ImportBatchID int64                       `json:"import_batch_id"`
	State         banking.TransactionState    `json:"state"`
	CreatedAt     string                      `json:"created_at"`
}

type reconciliationReviewQueueResponse struct {
	Matches     []reconciliationReviewCardResponse `json:"matches"`
	Suggestions []reconciliationReviewCardResponse `json:"suggestions"`
	Rules       []reconciliationReviewCardResponse `json:"rules"`
}

type reconciliationReviewCardResponse struct {
	Kind         string                            `json:"kind"`
	SuggestionID int64                             `json:"suggestion_id"`
	Transaction  reconciliationTransactionResponse `json:"transaction"`
	Confidence   float64                           `json:"confidence"`
	Explanation  string                            `json:"explanation"`
	Target       reconciliationReviewTarget        `json:"target"`
}

type reconciliationReviewTarget struct {
	Type          string `json:"type"`
	ID            string `json:"id"`
	InvoiceNumber string `json:"invoice_number"`
	Client        string `json:"client"`
	AccountCode   string `json:"account_code"`
	TimesApplied  *int   `json:"times_applied"`
}

type reconciliationCommandResponse struct {
	Transaction *reconciliationTransactionResponse `json:"transaction"`
	Kind        string                             `json:"kind"`
	AmountGBP   *reconciliationMoneyResponse       `json:"amount_gbp"`
}

func importCSVViaHTTP(
	t testing.TB,
	h *harness.Harness,
	accountID banking.AccountID,
	filename string,
	content []byte,
) reconciliationBatchSummaryResponse {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create multipart CSV form: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write multipart CSV content: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart CSV form: %v", err)
	}

	response := postBankingCommand(
		t,
		h,
		fmt.Sprintf("/api/banking/accounts/%d/import", accountID),
		&httpRequestBody{reader: &body, contentType: writer.FormDataContentType()},
		nethttp.StatusOK,
	)
	var summary reconciliationBatchSummaryResponse
	decodeJSON(t, response, &summary)
	return summary
}

func getReviewQueueViaHTTP(t testing.TB, h *harness.Harness) reconciliationReviewQueueResponse {
	t.Helper()
	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, "/api/banking/review", nil)
	if err != nil {
		t.Fatalf("create GET /api/banking/review request: %v", err)
	}
	response := doHarnessRequest(t, h, req, nethttp.StatusOK)
	var queue reconciliationReviewQueueResponse
	decodeJSON(t, response, &queue)
	return queue
}

type httpRequestBody struct {
	reader      io.Reader
	contentType string
}

func postBankingCommand(
	t testing.TB,
	h *harness.Harness,
	path string,
	body *httpRequestBody,
	wantStatus int,
) []byte {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = body.reader
	}
	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodPost, path, reader)
	if err != nil {
		t.Fatalf("create POST %s request: %v", path, err)
	}
	if body != nil && body.contentType != "" {
		req.Header.Set("Content-Type", body.contentType)
	}
	return doHarnessRequest(t, h, req, wantStatus)
}

func doHarnessRequest(t testing.TB, h *harness.Harness, req *nethttp.Request, wantStatus int) []byte {
	t.Helper()
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL.String(), err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", req.Method, req.URL.String(), err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; body=%s", req.Method, req.URL.String(), resp.StatusCode, wantStatus, string(body))
	}
	return body
}

func decodeJSON(t testing.TB, body []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode JSON response: %v; body=%s", err, string(body))
	}
}

func assertBatchSummary(t testing.TB, summary banking.BatchSummary, total int, newRows int, duplicates int) {
	t.Helper()
	if summary.TotalRows != total || summary.NewRows != newRows || summary.DuplicateRows != duplicates {
		t.Fatalf("batch summary = %+v, want total/new/duplicates %d/%d/%d", summary, total, newRows, duplicates)
	}
}

func assertHTTPBatchSummary(t testing.TB, summary reconciliationBatchSummaryResponse, total int, newRows int, duplicates int) {
	t.Helper()
	if summary.Total != total || summary.New != newRows || summary.Duplicates != duplicates {
		t.Fatalf("HTTP batch summary = %+v, want total/new/duplicates %d/%d/%d", summary, total, newRows, duplicates)
	}
}

func assertBankingTransactionCount(t testing.TB, f *reconciliationFixture, accountID banking.AccountID, want int) {
	t.Helper()
	var got int
	if err := f.h.BankingPool.QueryRow(f.ctx, `
SELECT count(*)::integer
FROM transactions
WHERE account_id = $1`, int64(accountID)).Scan(&got); err != nil {
		t.Fatalf("count banking transactions for account %d: %v", accountID, err)
	}
	if got != want {
		t.Fatalf("banking transaction count = %d, want %d", got, want)
	}
}

func assertBankingTxnState(t testing.TB, f *reconciliationFixture, txnID banking.TransactionID, want banking.TransactionState) {
	t.Helper()
	var got string
	if err := f.h.BankingPool.QueryRow(f.ctx, `
SELECT state::text
FROM transactions
WHERE id = $1`, int64(txnID)).Scan(&got); err != nil {
		t.Fatalf("load transaction %d state: %v", txnID, err)
	}
	if banking.TransactionState(got) != want {
		t.Fatalf("transaction %d state = %q, want %q", txnID, got, want)
	}
}

func assertActiveSuggestionCount(t testing.TB, f *reconciliationFixture, txnID banking.TransactionID, want int) {
	t.Helper()
	var got int
	if err := f.h.BankingPool.QueryRow(f.ctx, `
SELECT count(*)::integer
FROM suggestions
WHERE txn_id = $1
	AND superseded_at IS NULL`, int64(txnID)).Scan(&got); err != nil {
		t.Fatalf("count active suggestions for %d: %v", txnID, err)
	}
	if got != want {
		t.Fatalf("active suggestion count for %d = %d, want %d", txnID, got, want)
	}
}

func activeBankingSuggestion(t testing.TB, f *reconciliationFixture, txnID banking.TransactionID) banking.Suggestion {
	t.Helper()
	var suggestion banking.Suggestion
	var (
		id     int64
		txn    int64
		kind   string
		target string
	)
	if err := f.h.BankingPool.QueryRow(f.ctx, `
SELECT id, txn_id, kind::text, target, explanation, auto_postable
FROM suggestions
WHERE txn_id = $1
	AND superseded_at IS NULL`, int64(txnID)).Scan(
		&id,
		&txn,
		&kind,
		&target,
		&suggestion.Explanation,
		&suggestion.AutoPostable,
	); err != nil {
		t.Fatalf("load active suggestion for %d: %v", txnID, err)
	}
	suggestion.ID = banking.SuggestionID(id)
	suggestion.TransactionID = banking.TransactionID(txn)
	suggestion.Kind = banking.SuggestionKind(kind)
	suggestion.Target = target
	return suggestion
}

func mustTransactionIDByReference(t testing.TB, f *reconciliationFixture, reference string) banking.TransactionID {
	t.Helper()
	var id int64
	if err := f.h.BankingPool.QueryRow(f.ctx, `
SELECT id
FROM transactions
WHERE reference = $1`, reference).Scan(&id); err != nil {
		t.Fatalf("load transaction by reference %q: %v", reference, err)
	}
	return banking.TransactionID(id)
}

func assertDLAEntry(t testing.TB, f *reconciliationFixture, source string, wantAmount int64) {
	t.Helper()
	var amount int64
	if err := f.h.DB.QueryRow(f.ctx, `
SELECT amount
FROM dla.dla_entries
WHERE source = $1`, source).Scan(&amount); err != nil {
		t.Fatalf("load DLA entry %s: %v", source, err)
	}
	if amount != wantAmount {
		t.Fatalf("DLA entry %s amount = %d, want %d", source, amount, wantAmount)
	}
}

func assertDLAEntryCount(t testing.TB, f *reconciliationFixture, source string, want int) {
	t.Helper()
	assertCount(t, f, "dla.dla_entries", "source = $1", want, source)
}

func assertJournalEntryCount(t testing.TB, f *reconciliationFixture, module string, sourceRef string, want int) {
	t.Helper()
	assertCount(t, f, "ledger.journal_entries", "source_module = $1 AND source_ref = $2", want, module, sourceRef)
}

func assertCount(t testing.TB, f *reconciliationFixture, table string, predicate string, want int, args ...any) {
	t.Helper()
	var got int
	query := fmt.Sprintf("SELECT count(*)::integer FROM %s WHERE %s", table, predicate)
	if err := f.h.DB.QueryRow(f.ctx, query, args...).Scan(&got); err != nil {
		t.Fatalf("count %s where %s: %v", table, predicate, err)
	}
	if got != want {
		t.Fatalf("count %s where %s = %d, want %d", table, predicate, got, want)
	}
}

type reconciliationPosting struct {
	account   string
	amount    int64
	currency  string
	amountGBP int64
}

func assertModuleLedgerPostings(
	t testing.TB,
	f *reconciliationFixture,
	module string,
	sourceRef string,
	want []reconciliationPosting,
) {
	t.Helper()
	rows, err := f.h.DB.Query(f.ctx, `
SELECT p.account_code, p.amount, p.currency, p.amount_gbp
FROM ledger.journal_entries AS je
JOIN ledger.postings AS p ON p.entry_id = je.id
WHERE je.source_module = $1
	AND je.source_ref = $2`, module, sourceRef)
	if err != nil {
		t.Fatalf("query ledger postings for %s/%s: %v", module, sourceRef, err)
	}
	defer rows.Close()

	var got []reconciliationPosting
	for rows.Next() {
		var posting reconciliationPosting
		if err := rows.Scan(&posting.account, &posting.amount, &posting.currency, &posting.amountGBP); err != nil {
			t.Fatalf("scan ledger posting for %s/%s: %v", module, sourceRef, err)
		}
		got = append(got, posting)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate ledger postings for %s/%s: %v", module, sourceRef, err)
	}
	sortReconciliationPostings(got)
	sortReconciliationPostings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ledger postings for %s/%s = %#v, want %#v", module, sourceRef, got, want)
	}
}

func sortReconciliationPostings(postings []reconciliationPosting) {
	sort.Slice(postings, func(i int, j int) bool {
		if postings[i].account != postings[j].account {
			return postings[i].account < postings[j].account
		}
		if postings[i].amount != postings[j].amount {
			return postings[i].amount < postings[j].amount
		}
		return postings[i].currency < postings[j].currency
	})
}

func assertPayeeRuleTimesApplied(
	t testing.TB,
	f *reconciliationFixture,
	matcher string,
	accountCode ledger.AccountCode,
	want int,
) {
	t.Helper()
	var got int
	if err := f.h.BankingPool.QueryRow(f.ctx, `
SELECT times_applied
FROM payee_rules
WHERE matcher = $1
	AND account_code = $2`, matcher, string(accountCode)).Scan(&got); err != nil {
		t.Fatalf("load payee rule %s/%s: %v", matcher, accountCode, err)
	}
	if got != want {
		t.Fatalf("payee rule %s/%s times_applied = %d, want %d", matcher, accountCode, got, want)
	}
}

func ledgerPostingCount(t testing.TB, f *reconciliationFixture) int {
	t.Helper()
	var count int
	if err := f.h.DB.QueryRow(f.ctx, `SELECT count(*)::integer FROM ledger.postings`).Scan(&count); err != nil {
		t.Fatalf("count ledger postings: %v", err)
	}
	return count
}

type reconciliationEventRecorder struct {
	mu     sync.Mutex
	events []banking.TransactionReconciled
}

func newReconciliationEventRecorder(t testing.TB, h *harness.Harness) *reconciliationEventRecorder {
	t.Helper()
	recorder := &reconciliationEventRecorder{}
	h.Bus.Subscribe(banking.TransactionReconciledName, func(_ context.Context, _ db.Tx, evt bus.Event) error {
		reconciled, ok := evt.(banking.TransactionReconciled)
		if !ok {
			return fmt.Errorf("TransactionReconciled event = %T, want banking.TransactionReconciled", evt)
		}
		recorder.mu.Lock()
		recorder.events = append(recorder.events, reconciled)
		recorder.mu.Unlock()
		return nil
	})
	return recorder
}

func (r *reconciliationEventRecorder) assertReconciled(t testing.TB, want []banking.TransactionReconciled) {
	t.Helper()
	r.mu.Lock()
	got := append([]banking.TransactionReconciled(nil), r.events...)
	r.mu.Unlock()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TransactionReconciled events = %#v, want %#v", got, want)
	}
}

type dlaEventRecorder struct {
	mu            sync.Mutex
	wentOverdrawn []dla.WentOverdrawn
	backInCredit  []dla.BackInCredit
}

func newDLAEventRecorder(t testing.TB, h *harness.Harness) *dlaEventRecorder {
	t.Helper()
	recorder := &dlaEventRecorder{}
	h.Bus.Subscribe(dla.WentOverdrawnName, func(_ context.Context, _ db.Tx, evt bus.Event) error {
		event, ok := evt.(dla.WentOverdrawn)
		if !ok {
			return fmt.Errorf("WentOverdrawn event = %T, want dla.WentOverdrawn", evt)
		}
		recorder.mu.Lock()
		recorder.wentOverdrawn = append(recorder.wentOverdrawn, event)
		recorder.mu.Unlock()
		return nil
	})
	h.Bus.Subscribe(dla.BackInCreditName, func(_ context.Context, _ db.Tx, evt bus.Event) error {
		event, ok := evt.(dla.BackInCredit)
		if !ok {
			return fmt.Errorf("BackInCredit event = %T, want dla.BackInCredit", evt)
		}
		recorder.mu.Lock()
		recorder.backInCredit = append(recorder.backInCredit, event)
		recorder.mu.Unlock()
		return nil
	})
	return recorder
}

func (r *dlaEventRecorder) assertCounts(t testing.TB, wantWent int, wantBack int) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.wentOverdrawn) != wantWent || len(r.backInCredit) != wantBack {
		t.Fatalf(
			"DLA event counts WentOverdrawn=%d BackInCredit=%d, want %d/%d",
			len(r.wentOverdrawn),
			len(r.backInCredit),
			wantWent,
			wantBack,
		)
	}
}

func (r *dlaEventRecorder) assertBalances(t testing.TB, wantWent money.Money, wantBack money.Money) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.wentOverdrawn) != 1 || r.wentOverdrawn[0].Balance != wantWent {
		t.Fatalf("WentOverdrawn events = %#v, want one with balance %#v", r.wentOverdrawn, wantWent)
	}
	if len(r.backInCredit) != 1 || r.backInCredit[0].Balance != wantBack {
		t.Fatalf("BackInCredit events = %#v, want one with balance %#v", r.backInCredit, wantBack)
	}
}

func reconMoney(amount int64, currency string) money.Money {
	return money.Money{Amount: amount, Currency: currency}
}

func bankingTxnSource(txnID banking.TransactionID) string {
	return fmt.Sprintf("banking:%d", txnID)
}

func bankingCommandSource(txnID banking.TransactionID, command string) string {
	return fmt.Sprintf("%s:%s", bankingTxnSource(txnID), command)
}
