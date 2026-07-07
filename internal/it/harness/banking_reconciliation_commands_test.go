//go:build integration

package harness_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/banking"
	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/invoicing"
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

func TestBankingConfirmMatchPostsSettlesAndReturnsRealisedFX(t *testing.T) {
	ctx := context.Background()
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{
		time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC): "0.8500",
		time.Date(2025, 5, 2, 0, 0, 0, 0, time.UTC): "0.8600",
	}))

	invoiceService := newInvoiceService(t, h)
	sent, err := invoiceService.Send(ctx, createEURInvoiceDraft(t, h, invoiceService, 450_000).ID)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	lockID := mustInvoiceLockID(t, sent)

	bankingService := newBankingCommandService(t, h, invoiceService)
	account := mustCreateBankingAccount(t, ctx, bankingService, "Revolut EUR", "EUR")
	txnID := importDashboardBankTxn(t, ctx, h, bankingService, account.ID, dashboardBankTxn{
		ID:        "confirm-match-eur",
		Date:      time.Date(2025, 5, 2, 12, 0, 0, 0, time.UTC),
		Payee:     "Contoso GmbH",
		Reference: "INV-2025-01",
		Amount:    money.Money{Amount: 450_000, Currency: "EUR"},
	})
	mustRecordDashboardSuggestion(t, ctx, bankingService, txnID, banking.SuggestionKindInvoiceMatch, 0.98, sent.ID, "invoice match")

	var reconciledEvents []banking.TransactionReconciled
	h.Bus.Subscribe(banking.TransactionReconciledName, func(_ context.Context, _ db.Tx, evt bus.Event) error {
		reconciled, ok := evt.(banking.TransactionReconciled)
		if !ok {
			t.Fatalf("TransactionReconciled event = %T, want banking.TransactionReconciled", evt)
		}
		reconciledEvents = append(reconciledEvents, reconciled)
		return nil
	})

	result, err := bankingService.ConfirmMatch(ctx, txnID)
	if err != nil {
		t.Fatalf("ConfirmMatch() error = %v", err)
	}
	if result.Transaction.ID != txnID || result.Transaction.State != banking.TransactionStateReconciled || result.InvoiceID != sent.ID {
		t.Fatalf("ConfirmMatch() result = %#v, want txn %d reconciled invoice %s", result, txnID, sent.ID)
	}
	if result.RealisedFXGBP != (money.Money{Amount: 4_500, Currency: "GBP"}) {
		t.Fatalf("ConfirmMatch() realised FX = %#v, want GBP 45.00 gain", result.RealisedFXGBP)
	}
	wantEvents := []banking.TransactionReconciled{{
		TransactionID: txnID,
		Kind:          banking.SuggestionKindInvoiceMatch,
	}}
	if !reflect.DeepEqual(reconciledEvents, wantEvents) {
		t.Fatalf("TransactionReconciled events = %#v, want %#v", reconciledEvents, wantEvents)
	}

	settled, err := invoiceService.Invoice(ctx, sent.ID)
	if err != nil {
		t.Fatalf("Invoice(%s) error = %v", sent.ID, err)
	}
	if settled.Status != invoicing.InvoiceStatusPaid {
		t.Fatalf("invoice Status = %q, want paid", settled.Status)
	}
	assertBankingTxnState(t, h, txnID, banking.TransactionStateReconciled)
	assertActiveSuggestionCount(t, h, txnID, 0)
	assertModuleLedgerPostings(t, h, banking.ModuleName, bankingCommandSourceRef(txnID, "confirm-match"), []wantInvoicePosting{
		{account: string(account.LedgerAccountCode), amount: 450_000, currency: "EUR", amountGBP: 387_000},
		{account: "1100-debtors-eur", amount: -450_000, currency: "EUR", amountGBP: -387_000},
	})
	assertRealisedFXRow(t, h, sent.ID, lockID, 1, 4_500)
	assertModuleLedgerPostings(t, h, moneyfx.ModuleName, invoiceSettlementSourceRefForTest(sent.ID), []wantInvoicePosting{
		{account: "1101-debtors-gbp", amount: 4_500, currency: "GBP", amountGBP: 4_500},
		{account: "4900-fx-gain-loss", amount: -4_500, currency: "GBP", amountGBP: -4_500},
	})
	it.AssertLedgerBalanced(t, h)
}

func TestBankingConfirmMatchRollsBackInjectedFailures(t *testing.T) {
	tests := []struct {
		name  string
		hooks banking.ReconciliationCommandHooks
	}{
		{
			name: "after ledger post",
			hooks: banking.ReconciliationCommandHooks{
				AfterConfirmLedgerPost: forcedReconciliationFailure,
			},
		},
		{
			name: "after invoice settled",
			hooks: banking.ReconciliationCommandHooks{
				AfterConfirmInvoiceSettled: forcedReconciliationFailure,
			},
		},
		{
			name: "after state transition",
			hooks: banking.ReconciliationCommandHooks{
				AfterConfirmStateTransition: forcedReconciliationFailure,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
			fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{
				time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC): "0.8500",
				time.Date(2025, 5, 2, 0, 0, 0, 0, time.UTC): "0.8600",
			}))
			invoiceService := newInvoiceService(t, h)
			sent, err := invoiceService.Send(ctx, createEURInvoiceDraft(t, h, invoiceService, 450_000).ID)
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			lockID := mustInvoiceLockID(t, sent)
			bankingService := newBankingCommandService(t, h, invoiceService, banking.WithReconciliationCommandHooks(test.hooks))
			account := mustCreateBankingAccount(t, ctx, bankingService, "Revolut EUR", "EUR")
			txnID := importDashboardBankTxn(t, ctx, h, bankingService, account.ID, dashboardBankTxn{
				ID:        "confirm-rollback-" + test.name,
				Date:      time.Date(2025, 5, 2, 12, 0, 0, 0, time.UTC),
				Payee:     "Contoso GmbH",
				Reference: "INV-2025-01 " + test.name,
				Amount:    money.Money{Amount: 450_000, Currency: "EUR"},
			})
			mustRecordDashboardSuggestion(t, ctx, bankingService, txnID, banking.SuggestionKindInvoiceMatch, 0.98, sent.ID, "invoice match")

			_, err = bankingService.ConfirmMatch(ctx, txnID)
			if !errors.Is(err, forcedReconciliationErr) {
				t.Fatalf("ConfirmMatch() error = %v, want forced failure", err)
			}
			assertBankingTxnState(t, h, txnID, banking.TransactionStateSuggested)
			assertActiveSuggestionCount(t, h, txnID, 1)
			invoice, err := invoiceService.Invoice(ctx, sent.ID)
			if err != nil {
				t.Fatalf("Invoice(%s) error = %v", sent.ID, err)
			}
			if invoice.Status != invoicing.InvoiceStatusSent {
				t.Fatalf("invoice Status after rollback = %q, want sent", invoice.Status)
			}
			assertLedgerEntryCountForSource(t, h, banking.ModuleName, bankingCommandSourceRef(txnID, "confirm-match"), 0)
			assertLedgerEntryCountForSource(t, h, moneyfx.ModuleName, invoiceSettlementSourceRefForTest(sent.ID), 0)
			assertRealisedFXRow(t, h, sent.ID, lockID, 0, 0)
			it.AssertLedgerBalanced(t, h)
		})
	}
}

func TestBankingFileToDLAFilesDrawingAndRejectsDuplicate(t *testing.T) {
	ctx := context.Background()
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	invoiceService := newInvoiceService(t, h)
	bankingService := newBankingCommandService(t, h, invoiceService)
	account := mustCreateBankingAccount(t, ctx, bankingService, "Revolut GBP", "GBP")
	txnID := importDashboardBankTxn(t, ctx, h, bankingService, account.ID, dashboardBankTxn{
		ID:        "file-to-dla",
		Date:      time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC),
		Payee:     "N Meyer personal card",
		Reference: "director drawing",
		Amount:    money.Money{Amount: -12_000, Currency: "GBP"},
	})
	mustRecordDashboardSuggestion(t, ctx, bankingService, txnID, banking.SuggestionKindDLA, 0.88, "director-loan", "director drawing")

	result, err := bankingService.FileToDLA(ctx, txnID)
	if err != nil {
		t.Fatalf("FileToDLA() error = %v", err)
	}
	if result.AmountGBP != (money.Money{Amount: 12_000, Currency: "GBP"}) {
		t.Fatalf("FileToDLA() amount GBP = %#v, want 12000 GBP", result.AmountGBP)
	}
	assertBankingTxnState(t, h, txnID, banking.TransactionStateReconciled)
	assertDLAEntry(t, h, bankingTxnRef(txnID), 12_000)
	assertModuleLedgerPostings(t, h, dla.ModuleName, bankingTxnRef(txnID), []wantInvoicePosting{
		{account: string(dla.DLAAccountCode), amount: 12_000, currency: "GBP", amountGBP: 12_000},
		{account: string(account.LedgerAccountCode), amount: -12_000, currency: "GBP", amountGBP: -12_000},
	})

	_, err = bankingService.FileToDLA(ctx, txnID)
	if !errors.Is(err, banking.ErrAlreadyReconciled) {
		t.Fatalf("FileToDLA() duplicate error = %v, want ErrAlreadyReconciled", err)
	}
	it.AssertLedgerBalanced(t, h)
}

func TestBankingFileToDLASupportsEURCashAccountWithGBPAmount(t *testing.T) {
	ctx := context.Background()
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{
		time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC): "0.8500",
	}))
	invoiceService := newInvoiceService(t, h)
	bankingService := newBankingCommandService(t, h, invoiceService)
	account := mustCreateBankingAccount(t, ctx, bankingService, "Revolut EUR", "EUR")
	txnID := importDashboardBankTxn(t, ctx, h, bankingService, account.ID, dashboardBankTxn{
		ID:        "file-to-dla-eur-repro",
		Date:      time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC),
		Payee:     "N Meyer personal card",
		Reference: "director drawing eur",
		Amount:    money.Money{Amount: -10_000, Currency: "EUR"},
	})
	mustRecordDashboardSuggestion(t, ctx, bankingService, txnID, banking.SuggestionKindDLA, 0.88, "director-loan", "director drawing")

	result, err := bankingService.FileToDLA(ctx, txnID)
	if err != nil {
		t.Fatalf("FileToDLA(EUR) error = %v", err)
	}
	if result.AmountGBP != (money.Money{Amount: 8_500, Currency: "GBP"}) {
		t.Fatalf("FileToDLA(EUR) amount GBP = %#v, want 8500 GBP", result.AmountGBP)
	}
	assertBankingTxnState(t, h, txnID, banking.TransactionStateReconciled)
	assertDLAEntry(t, h, bankingTxnRef(txnID), 8_500)
	assertModuleLedgerPostings(t, h, dla.ModuleName, bankingTxnRef(txnID), []wantInvoicePosting{
		{account: string(dla.DLAAccountCode), amount: 10_000, currency: "EUR", amountGBP: 8_500},
		{account: string(account.LedgerAccountCode), amount: -10_000, currency: "EUR", amountGBP: -8_500},
	})
	it.AssertLedgerBalanced(t, h)
}

func TestBankingRecodePostsLearnsRuleAndNextImportOnlySuggests(t *testing.T) {
	ctx := context.Background()
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 7, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	invoiceService := newInvoiceService(t, h)
	bankingService := newBankingCommandService(t, h, invoiceService, banking.WithPayeeRuleAutoPostThreshold(1))
	account := mustCreateBankingAccount(t, ctx, bankingService, "Revolut GBP", "GBP")
	txnID := importDashboardBankTxn(t, ctx, h, bankingService, account.ID, dashboardBankTxn{
		ID:        "recode-first",
		Date:      time.Date(2025, 7, 1, 12, 0, 0, 0, time.UTC),
		Payee:     "Acme SaaS",
		Reference: "subscription July",
		Amount:    money.Money{Amount: -3_450, Currency: "GBP"},
	})
	mustRecordDashboardSuggestion(t, ctx, bankingService, txnID, banking.SuggestionKindPayeeRule, 0.72, "5010-software", "payee rule")

	result, err := bankingService.Recode(ctx, txnID, "5010-software")
	if err != nil {
		t.Fatalf("Recode() error = %v", err)
	}
	if result.Rule.AccountCode != "5010-software" || result.Rule.TimesApplied != 1 {
		t.Fatalf("Recode() rule = %#v, want software times_applied 1", result.Rule)
	}
	assertBankingTxnState(t, h, txnID, banking.TransactionStateReconciled)
	assertModuleLedgerPostings(t, h, banking.ModuleName, bankingCommandSourceRef(txnID, "recode"), []wantInvoicePosting{
		{account: "5010-software", amount: 3_450, currency: "GBP", amountGBP: 3_450},
		{account: string(account.LedgerAccountCode), amount: -3_450, currency: "GBP", amountGBP: -3_450},
	})

	nextTxnID := importDashboardBankTxn(t, ctx, h, bankingService, account.ID, dashboardBankTxn{
		ID:        "recode-next",
		Date:      time.Date(2025, 7, 2, 12, 0, 0, 0, time.UTC),
		Payee:     "Acme SaaS",
		Reference: "subscription August",
		Amount:    money.Money{Amount: -5_900, Currency: "GBP"},
	})
	active := activeBankingSuggestion(t, h, nextTxnID)
	if active.Kind != banking.SuggestionKindPayeeRule || active.Target != "5010-software" || !active.AutoPostable {
		t.Fatalf("next import suggestion = %#v, want auto-postable payee-rule to software", active)
	}
	assertBankingTxnState(t, h, nextTxnID, banking.TransactionStateSuggested)
	assertLedgerEntryCountForSource(t, h, banking.ModuleName, bankingCommandSourceRef(nextTxnID, "recode"), 0)
	it.AssertLedgerBalanced(t, h)
}

func TestBankingExcludeAndUnexcludeLeaveLedgerUntouched(t *testing.T) {
	ctx := context.Background()
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 8, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	invoiceService := newInvoiceService(t, h)
	bankingService := newBankingCommandService(t, h, invoiceService)
	account := mustCreateBankingAccount(t, ctx, bankingService, "Revolut GBP", "GBP")
	txnID := importDashboardBankTxn(t, ctx, h, bankingService, account.ID, dashboardBankTxn{
		ID:        "exclude-only",
		Date:      time.Date(2025, 8, 1, 12, 0, 0, 0, time.UTC),
		Payee:     "Duplicate import",
		Reference: "duplicate",
		Amount:    money.Money{Amount: -999, Currency: "GBP"},
	})
	before := ledgerPostingCount(t, h)

	if _, err := bankingService.Exclude(ctx, txnID, "duplicate statement row"); err != nil {
		t.Fatalf("Exclude() error = %v", err)
	}
	assertBankingTxnState(t, h, txnID, banking.TransactionStateExcluded)
	if got := ledgerPostingCount(t, h); got != before {
		t.Fatalf("ledger posting count after Exclude = %d, want unchanged %d", got, before)
	}
	if _, err := bankingService.Unexclude(ctx, txnID, "review again"); err != nil {
		t.Fatalf("Unexclude() error = %v", err)
	}
	assertBankingTxnState(t, h, txnID, banking.TransactionStateUnreconciled)
	if got := ledgerPostingCount(t, h); got != before {
		t.Fatalf("ledger posting count after Unexclude = %d, want unchanged %d", got, before)
	}
	if _, err := bankingService.Exclude(ctx, txnID, ""); !errors.Is(err, banking.ErrInvalidReconciliation) {
		t.Fatalf("Exclude(empty reason) error = %v, want ErrInvalidReconciliation", err)
	}
	it.AssertLedgerBalanced(t, h)
}

var forcedReconciliationErr = errors.New("forced reconciliation failure")

func forcedReconciliationFailure(context.Context) error {
	return forcedReconciliationErr
}

func newBankingCommandService(t testing.TB, h *harness.Harness, invoiceService *invoicing.Service, opts ...banking.ServiceOption) *banking.Service {
	t.Helper()
	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	moneyFXService := moneyfx.NewService(moneyfx.NewStore(testdb.AsModule(t, moneyfx.ModuleName)), h.Clock)
	dlaService := dla.NewWithBusAndClock(h.DLAPool, h.Bus, h.Clock, ledgerService)
	serviceOpts := []banking.ServiceOption{
		banking.WithLedgerJournal(ledgerService),
		banking.WithMoneyFX(moneyFXService),
		banking.WithInvoicingSettler(invoiceService),
		banking.WithDLAFileDrawer(dlaService),
		banking.WithEventBus(h.Bus),
	}
	serviceOpts = append(serviceOpts, opts...)
	return banking.NewService(h.BankingPool, ledgerService, serviceOpts...)
}

func mustCreateBankingAccount(t testing.TB, ctx context.Context, service *banking.Service, name string, currency string) banking.BankAccount {
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

func assertBankingTxnState(t testing.TB, h *harness.Harness, txnID banking.TransactionID, want banking.TransactionState) {
	t.Helper()
	var got string
	if err := h.BankingPool.QueryRow(context.Background(), `
SELECT state::text
FROM transactions
WHERE id = $1`, int64(txnID)).Scan(&got); err != nil {
		t.Fatalf("load transaction %d state: %v", txnID, err)
	}
	if banking.TransactionState(got) != want {
		t.Fatalf("transaction %d state = %q, want %q", txnID, got, want)
	}
}

func assertActiveSuggestionCount(t testing.TB, h *harness.Harness, txnID banking.TransactionID, want int) {
	t.Helper()
	var got int
	if err := h.BankingPool.QueryRow(context.Background(), `
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

func activeBankingSuggestion(t testing.TB, h *harness.Harness, txnID banking.TransactionID) banking.Suggestion {
	t.Helper()
	var suggestion banking.Suggestion
	var (
		id     int64
		txn    int64
		kind   string
		target string
	)
	if err := h.BankingPool.QueryRow(context.Background(), `
SELECT id, txn_id, kind::text, target, auto_postable
FROM suggestions
WHERE txn_id = $1
	AND superseded_at IS NULL`, int64(txnID)).Scan(
		&id,
		&txn,
		&kind,
		&target,
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

func assertDLAEntry(t testing.TB, h *harness.Harness, source string, wantAmount int64) {
	t.Helper()
	var amount int64
	if err := h.DB.QueryRow(context.Background(), `
SELECT amount
FROM dla.dla_entries
WHERE source = $1`, source).Scan(&amount); err != nil {
		t.Fatalf("load DLA entry %s: %v", source, err)
	}
	if amount != wantAmount {
		t.Fatalf("DLA entry %s amount = %d, want %d", source, amount, wantAmount)
	}
}

func ledgerPostingCount(t testing.TB, h *harness.Harness) int {
	t.Helper()
	var count int
	if err := h.DB.QueryRow(context.Background(), `SELECT count(*)::integer FROM ledger.postings`).Scan(&count); err != nil {
		t.Fatalf("count ledger postings: %v", err)
	}
	return count
}

func bankingTxnRef(txnID banking.TransactionID) string {
	return fmt.Sprintf("banking:%d", txnID)
}

func bankingCommandSourceRef(txnID banking.TransactionID, command string) string {
	return fmt.Sprintf("%s:%s", bankingTxnRef(txnID), command)
}
