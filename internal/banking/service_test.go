package banking

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if bankingPostgres.container != nil {
		_ = testcontainers.TerminateContainer(bankingPostgres.container)
	}
	os.Exit(code)
}

func TestCreateAccountEnsuresLedgerOnceAndRetryIsIdempotent(t *testing.T) {
	pool, _ := temporaryMigratedBankingDatabase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ensurer := &recordingEnsurer{}
	service := NewService(pool, ensurer)

	first, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut Main",
		Provider: ProviderRevolut,
		Currency: "gbp",
	})
	if err != nil {
		t.Fatalf("CreateAccount() first error = %v", err)
	}
	second, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut Main",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() retry error = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("retry ID = %d, want original %d", second.ID, first.ID)
	}
	if ensurer.calls != 1 {
		t.Fatalf("EnsureAccount calls = %d, want 1", ensurer.calls)
	}
	if len(ensurer.specs) != 1 {
		t.Fatalf("recorded specs = %d, want 1", len(ensurer.specs))
	}
	spec := ensurer.specs[0]
	if spec.Code != first.LedgerAccountCode {
		t.Fatalf("EnsureAccount code = %q, account ledger code = %q", spec.Code, first.LedgerAccountCode)
	}
	if spec.Type != ledger.AccountTypeAsset {
		t.Fatalf("EnsureAccount type = %q, want asset", spec.Type)
	}
	if spec.Currency == nil || *spec.Currency != "GBP" {
		t.Fatalf("EnsureAccount currency = %v, want GBP", spec.Currency)
	}

	var accounts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bank_accounts`).Scan(&accounts); err != nil {
		t.Fatalf("count bank accounts: %v", err)
	}
	if accounts != 1 {
		t.Fatalf("bank account count = %d, want 1", accounts)
	}
}

func TestCreateAccountConcurrentDuplicateReturnsExistingAccount(t *testing.T) {
	pool, _ := temporaryMigratedBankingDatabase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ensurer := newBlockingEnsurer()
	t.Cleanup(ensurer.Release)
	service := NewService(pool, ensurer)

	results := make(chan accountCreateResult, 2)
	for range 2 {
		go func() {
			account, err := service.CreateAccount(ctx, AccountInput{
				Name:     "Revolut Race",
				Provider: ProviderRevolut,
				Currency: "GBP",
			})
			results <- accountCreateResult{account: account, err: err}
		}()
	}

	for i := 0; i < 2; i++ {
		select {
		case <-ensurer.arrived:
		case <-ctx.Done():
			t.Fatalf("waiting for concurrent EnsureAccount calls: %v", ctx.Err())
		}
	}
	ensurer.Release()

	first := <-results
	second := <-results
	if first.err != nil {
		t.Fatalf("first CreateAccount() error = %v", first.err)
	}
	if second.err != nil {
		t.Fatalf("second CreateAccount() error = %v", second.err)
	}
	if first.account.ID != second.account.ID {
		t.Fatalf("concurrent IDs = %d and %d, want same account", first.account.ID, second.account.ID)
	}

	var accounts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bank_accounts`).Scan(&accounts); err != nil {
		t.Fatalf("count bank accounts: %v", err)
	}
	if accounts != 1 {
		t.Fatalf("bank account count = %d, want 1", accounts)
	}
	if ensurer.Calls() != 2 {
		t.Fatalf("EnsureAccount calls = %d, want 2 concurrent attempts", ensurer.Calls())
	}
}

func TestCreateAccountDisambiguatesLedgerCodesForSlugCollisions(t *testing.T) {
	pool, _ := temporaryMigratedBankingDatabase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ensurer := &recordingEnsurer{}
	service := NewService(pool, ensurer)

	first, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut Main",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() first error = %v", err)
	}
	second, err := service.CreateAccount(ctx, AccountInput{
		Name:     "revolut-main",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() second error = %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("account IDs both = %d, want distinct accounts", first.ID)
	}
	if first.LedgerAccountCode == second.LedgerAccountCode {
		t.Fatalf("ledger account code collision = %q", first.LedgerAccountCode)
	}
	if ensurer.calls != 2 {
		t.Fatalf("EnsureAccount calls = %d, want 2", ensurer.calls)
	}
}

func TestImportCSVDedupesOverlappingExportsAndReferenceWhitespace(t *testing.T) {
	bankingPool, ledgerPool := temporaryMigratedBankingDatabase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	service := NewService(bankingPool, ledger.New(ledgerPool))

	account, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut GBP",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	txnA := revolutTestTxn{
		Date:      time.Date(2026, 4, 1, 8, 30, 0, 0, time.UTC),
		ID:        "rev-a-1",
		Payee:     "ACME & Sons",
		Reference: "Invoice   1001",
		Amount:    money.Money{Amount: 123456, Currency: "GBP"},
		Balance:   money.Money{Amount: 123456, Currency: "GBP"},
	}
	first, err := service.ImportCSV(ctx, account.ID, ImportFile{
		Filename: "statement-a.csv",
		Reader:   bytes.NewReader(revolutTestCSV(txnA)),
	})
	if err != nil {
		t.Fatalf("ImportCSV() first error = %v", err)
	}
	assertBatchCounts(t, first, 1, 1, 0)

	txnADuplicate := txnA
	txnADuplicate.ID = "rev-a-1-export-2"
	txnADuplicate.Reference = " Invoice 1001 "
	txnB := revolutTestTxn{
		Date:      time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC),
		ID:        "rev-b-1",
		Payee:     "Beta Ltd",
		Reference: "Invoice 1002",
		Amount:    money.Money{Amount: -2500, Currency: "GBP"},
		Balance:   money.Money{Amount: 120956, Currency: "GBP"},
	}
	second, err := service.ImportCSV(ctx, account.ID, ImportFile{
		Filename: "statement-a-plus-b.csv",
		Reader:   bytes.NewReader(revolutTestCSV(txnADuplicate, txnB)),
	})
	if err != nil {
		t.Fatalf("ImportCSV() second error = %v", err)
	}
	assertBatchCounts(t, second, 2, 1, 1)

	var transactions int
	if err := bankingPool.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE account_id = $1`, int64(account.ID)).Scan(&transactions); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	if transactions != 2 {
		t.Fatalf("transaction count = %d, want 2", transactions)
	}

	var state string
	if err := bankingPool.QueryRow(ctx, `
SELECT state::text
FROM transactions
WHERE account_id = $1
ORDER BY date, id
LIMIT 1`, int64(account.ID)).Scan(&state); err != nil {
		t.Fatalf("load transaction state: %v", err)
	}
	if state != string(TransactionStateUnreconciled) {
		t.Fatalf("state = %q, want unreconciled", state)
	}
}

func TestImportCSVBlankReferenceFallsBackToPayeeForDedupe(t *testing.T) {
	bankingPool, ledgerPool := temporaryMigratedBankingDatabase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	service := NewService(bankingPool, ledger.New(ledgerPool))

	account, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut GBP",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	date := time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC)
	summary, err := service.ImportCSV(ctx, account.ID, ImportFile{
		Filename: "blank-reference.csv",
		Reader: bytes.NewReader(revolutTestCSV(
			revolutTestTxn{
				Date:           date,
				ID:             "blank-ref-a",
				Payee:          "Alpha Ltd",
				BlankReference: true,
				Amount:         money.Money{Amount: -2500, Currency: "GBP"},
			},
			revolutTestTxn{
				Date:           date,
				ID:             "blank-ref-b",
				Payee:          "Beta Ltd",
				BlankReference: true,
				Amount:         money.Money{Amount: -2500, Currency: "GBP"},
			},
		)),
	})
	if err != nil {
		t.Fatalf("ImportCSV() error = %v", err)
	}
	assertBatchCounts(t, summary, 2, 2, 0)

	rows, err := bankingPool.Query(ctx, `
SELECT payee, reference
FROM transactions
WHERE account_id = $1
ORDER BY payee`, int64(account.ID))
	if err != nil {
		t.Fatalf("query transactions: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var payee, reference string
		if err := rows.Scan(&payee, &reference); err != nil {
			t.Fatalf("scan transaction: %v", err)
		}
		got[payee] = reference
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate transactions: %v", err)
	}
	if len(got) != 2 || got["Alpha Ltd"] != "Alpha Ltd" || got["Beta Ltd"] != "Beta Ltd" {
		t.Fatalf("transaction references = %#v, want payee fallbacks", got)
	}
}

func TestImportCSVRejectsMalformedRowsWithoutBatch(t *testing.T) {
	pool, _ := temporaryMigratedBankingDatabase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	service := NewService(pool, &recordingEnsurer{})
	account, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut GBP",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	badCSV := strings.NewReader(`Date started (UTC),Date completed (UTC),ID,Type,Description,Reference,Amount,Fee,Currency,State,Balance
2026-03-04 10:11:12,2026-03-04 10:11:30,rev-gbp-1,CARD_PAYMENT,ACME,Invoice 1001,not-money,0.00,GBP,COMPLETED,2.00
`)
	_, err = service.ImportCSV(ctx, account.ID, ImportFile{Filename: "bad.csv", Reader: badCSV})
	if err == nil {
		t.Fatal("ImportCSV() error = nil, want malformed row error")
	}
	var rowErr *ParseRowError
	if !errors.As(err, &rowErr) {
		t.Fatalf("ImportCSV() error = %T %[1]v, want *ParseRowError", err)
	}
	if rowErr.Row != 2 {
		t.Fatalf("ParseRowError.Row = %d, want 2", rowErr.Row)
	}
	assertNoImportRows(t, ctx, pool, account.ID)
}

func TestImportCSVRejectsCurrencyMismatchWithoutWrites(t *testing.T) {
	pool, _ := temporaryMigratedBankingDatabase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	service := NewService(pool, &recordingEnsurer{})
	account, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut GBP",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	csvBytes := revolutTestCSV(revolutTestTxn{
		Date:      time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC),
		ID:        "rev-eur-1",
		Payee:     "EUR payer",
		Reference: "EUR reference",
		Amount:    money.Money{Amount: 1200, Currency: "EUR"},
		Balance:   money.Money{Amount: 1200, Currency: "EUR"},
	})
	_, err = service.ImportCSV(ctx, account.ID, ImportFile{
		Filename: "eur.csv",
		Reader:   bytes.NewReader(csvBytes),
	})
	if !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("ImportCSV() error = %v, want ErrCurrencyMismatch", err)
	}
	var mismatch *CurrencyMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("ImportCSV() error = %T %[1]v, want *CurrencyMismatchError", err)
	}
	if mismatch.Expected != "GBP" || mismatch.Actual != "EUR" || mismatch.Row != 2 {
		t.Fatalf("CurrencyMismatchError = %#v, want GBP/EUR row 2", mismatch)
	}
	assertNoImportRows(t, ctx, pool, account.ID)
}

func TestTransactionStateTransitionMatrixRecordsAudit(t *testing.T) {
	pool, ledgerPool := temporaryMigratedBankingDatabase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	service := NewService(pool, ledger.New(ledgerPool))
	account, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut GBP",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	legal := []struct {
		name    string
		prepare func(TransactionID)
		from    TransactionState
		to      TransactionState
	}{
		{
			name: "unreconciled to suggested",
			from: TransactionStateUnreconciled,
			to:   TransactionStateSuggested,
		},
		{
			name: "unreconciled to excluded",
			from: TransactionStateUnreconciled,
			to:   TransactionStateExcluded,
		},
		{
			name: "suggested to reconciled",
			prepare: func(txnID TransactionID) {
				mustTransition(t, ctx, service, txnID, TransactionStateSuggested, "engine-run-1")
			},
			from: TransactionStateSuggested,
			to:   TransactionStateReconciled,
		},
		{
			name: "suggested to excluded",
			prepare: func(txnID TransactionID) {
				mustTransition(t, ctx, service, txnID, TransactionStateSuggested, "engine-run-1")
			},
			from: TransactionStateSuggested,
			to:   TransactionStateExcluded,
		},
		{
			name: "excluded to unreconciled",
			prepare: func(txnID TransactionID) {
				mustTransition(t, ctx, service, txnID, TransactionStateExcluded, "advisor")
			},
			from: TransactionStateExcluded,
			to:   TransactionStateUnreconciled,
		},
	}
	for i, tc := range legal {
		t.Run(tc.name, func(t *testing.T) {
			txnID := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
				Date:      time.Date(2026, 6, 1+i, 9, 0, 0, 0, time.UTC),
				ID:        fmt.Sprintf("state-legal-%d", i),
				Payee:     fmt.Sprintf("State legal %d", i),
				Reference: fmt.Sprintf("state-legal-%d", i),
				Amount:    money.Money{Amount: int64(1000 + i), Currency: "GBP"},
			})
			if tc.prepare != nil {
				tc.prepare(txnID)
			}

			change, err := service.TransitionTransactionState(ctx, txnID, tc.to, "reviewer")
			if err != nil {
				t.Fatalf("TransitionTransactionState() error = %v", err)
			}
			if change.TransactionID != txnID || change.From != tc.from || change.To != tc.to || change.Actor != "reviewer" || change.ChangedAt.IsZero() {
				t.Fatalf("state change = %#v, want txn/from/to/actor/timestamp", change)
			}
			assertStoredTransactionState(t, ctx, pool, txnID, tc.to)
		})
	}

	illegal := []struct {
		name    string
		prepare func(TransactionID)
		to      TransactionState
		want    TransactionState
	}{
		{
			name: "unreconciled cannot reconcile directly",
			to:   TransactionStateReconciled,
			want: TransactionStateUnreconciled,
		},
		{
			name: "suggested cannot return to unreconciled",
			prepare: func(txnID TransactionID) {
				mustTransition(t, ctx, service, txnID, TransactionStateSuggested, "engine-run-2")
			},
			to:   TransactionStateUnreconciled,
			want: TransactionStateSuggested,
		},
		{
			name: "excluded cannot become suggested",
			prepare: func(txnID TransactionID) {
				mustTransition(t, ctx, service, txnID, TransactionStateExcluded, "reviewer")
			},
			to:   TransactionStateSuggested,
			want: TransactionStateExcluded,
		},
		{
			name: "reconciled is terminal",
			prepare: func(txnID TransactionID) {
				mustTransition(t, ctx, service, txnID, TransactionStateSuggested, "engine-run-3")
				mustTransition(t, ctx, service, txnID, TransactionStateReconciled, "reviewer")
			},
			to:   TransactionStateExcluded,
			want: TransactionStateReconciled,
		},
	}
	for i, tc := range illegal {
		t.Run(tc.name, func(t *testing.T) {
			txnID := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
				Date:      time.Date(2026, 6, 20+i, 9, 0, 0, 0, time.UTC),
				ID:        fmt.Sprintf("state-illegal-%d", i),
				Payee:     fmt.Sprintf("State illegal %d", i),
				Reference: fmt.Sprintf("state-illegal-%d", i),
				Amount:    money.Money{Amount: int64(2000 + i), Currency: "GBP"},
			})
			if tc.prepare != nil {
				tc.prepare(txnID)
			}

			_, err := service.TransitionTransactionState(ctx, txnID, tc.to, "reviewer")
			if !errors.Is(err, ErrInvalidStateTransition) {
				t.Fatalf("TransitionTransactionState() error = %v, want ErrInvalidStateTransition", err)
			}
			assertStoredTransactionState(t, ctx, pool, txnID, tc.want)
		})
	}
}

func TestSuggestionSupersedeKeepsHistoryAndOneActive(t *testing.T) {
	pool, ledgerPool := temporaryMigratedBankingDatabase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	service := NewService(pool, ledger.New(ledgerPool))
	account, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut GBP",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	txnID := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC),
		ID:        "suggestion-1",
		Payee:     "Contoso GMBH",
		Reference: "invoice 1001",
		Amount:    money.Money{Amount: 98000, Currency: "GBP"},
	})

	first, err := service.RecordSuggestion(ctx, SuggestionInput{
		TransactionID: txnID,
		Kind:          SuggestionKindInvoiceMatch,
		Confidence:    0.981,
		Target:        "inv-1001",
		Explanation:   "98% match - amount + payee + date",
		CreatedBy:     "engine-run-a",
	})
	if err != nil {
		t.Fatalf("RecordSuggestion() first error = %v", err)
	}
	second, err := service.RecordSuggestion(ctx, SuggestionInput{
		TransactionID: txnID,
		Kind:          SuggestionKindPayeeRule,
		Confidence:    0.875,
		Target:        "6200-software",
		Explanation:   "88% match - recurring payee rule",
		CreatedBy:     "engine-run-b",
	})
	if err != nil {
		t.Fatalf("RecordSuggestion() second error = %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("suggestion IDs both = %d, want replacement row", first.ID)
	}
	assertStoredTransactionState(t, ctx, pool, txnID, TransactionStateSuggested)

	history, err := service.SuggestionsForTransaction(ctx, txnID)
	if err != nil {
		t.Fatalf("SuggestionsForTransaction() error = %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("suggestion history length = %d, want 2", len(history))
	}
	active := 0
	superseded := 0
	for _, suggestion := range history {
		if suggestion.SupersededAt == nil {
			active++
			if suggestion.ID != second.ID {
				t.Fatalf("active suggestion ID = %d, want second %d", suggestion.ID, second.ID)
			}
		} else {
			superseded++
			if suggestion.ID != first.ID {
				t.Fatalf("superseded suggestion ID = %d, want first %d", suggestion.ID, first.ID)
			}
		}
	}
	if active != 1 || superseded != 1 {
		t.Fatalf("active/superseded counts = %d/%d, want 1/1", active, superseded)
	}

	var dbActive int
	if err := pool.QueryRow(ctx, `
SELECT count(*)::integer
FROM suggestions
WHERE txn_id = $1
	AND superseded_at IS NULL`, int64(txnID)).Scan(&dbActive); err != nil {
		t.Fatalf("count active suggestions: %v", err)
	}
	if dbActive != 1 {
		t.Fatalf("active suggestion rows = %d, want 1", dbActive)
	}
	var suggestedChanges int
	if err := pool.QueryRow(ctx, `
SELECT count(*)::integer
FROM transaction_state_changes
WHERE txn_id = $1
	AND to_state = 'suggested'`, int64(txnID)).Scan(&suggestedChanges); err != nil {
		t.Fatalf("count suggested state changes: %v", err)
	}
	if suggestedChanges != 1 {
		t.Fatalf("suggested state changes = %d, want 1", suggestedChanges)
	}
}

func TestFeedReviewQueueRecentlyReconciledAndCounts(t *testing.T) {
	pool, ledgerPool := temporaryMigratedBankingDatabase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	service := NewService(pool, ledger.New(ledgerPool))
	account, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut GBP",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() account error = %v", err)
	}
	otherAccount, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut EUR",
		Provider: ProviderRevolut,
		Currency: "EUR",
	})
	if err != nil {
		t.Fatalf("CreateAccount() other account error = %v", err)
	}

	invoiceTxn := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC),
		ID:        "queue-invoice",
		Payee:     "Contoso GMBH",
		Reference: "invoice 1002",
		Amount:    money.Money{Amount: 120000, Currency: "GBP"},
	})
	dlaTxn := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC),
		ID:        "queue-dla",
		Payee:     "Director transfer",
		Reference: "drawing",
		Amount:    money.Money{Amount: -50000, Currency: "GBP"},
	})
	ruleTxn := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC),
		ID:        "queue-rule",
		Payee:     "SaaS Vendor",
		Reference: "subscription",
		Amount:    money.Money{Amount: -2400, Currency: "GBP"},
	})
	unreconciledTxn := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC),
		ID:        "queue-unreconciled",
		Payee:     "Unknown",
		Reference: "unknown",
		Amount:    money.Money{Amount: -1000, Currency: "GBP"},
	})
	excludedTxn := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC),
		ID:        "queue-excluded",
		Payee:     "Duplicate",
		Reference: "duplicate",
		Amount:    money.Money{Amount: -1, Currency: "GBP"},
	})
	otherUnreconciled := importSingleBankingTxn(t, ctx, pool, service, otherAccount.ID, revolutTestTxn{
		Date:      time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC),
		ID:        "queue-other",
		Payee:     "EUR Unknown",
		Reference: "eur-unknown",
		Amount:    money.Money{Amount: -1000, Currency: "EUR"},
		Balance:   money.Money{Amount: -1000, Currency: "EUR"},
	})
	otherReconciled := importSingleBankingTxn(t, ctx, pool, service, otherAccount.ID, revolutTestTxn{
		Date:      time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC),
		ID:        "queue-other-reconciled",
		Payee:     "EUR Client",
		Reference: "eur-client",
		Amount:    money.Money{Amount: 2000, Currency: "EUR"},
		Balance:   money.Money{Amount: 1000, Currency: "EUR"},
	})

	mustRecordSuggestion(t, ctx, service, invoiceTxn, SuggestionKindInvoiceMatch, 0.982, "inv-1002", "98% match - amount + payee + date")
	mustRecordSuggestion(t, ctx, service, dlaTxn, SuggestionKindDLA, 0.750, "director-loan", "75% match - director payee")
	mustRecordSuggestion(t, ctx, service, ruleTxn, SuggestionKindPayeeRule, 0.910, "6200-software", "91% match - recurring payee rule")
	mustRecordSuggestion(t, ctx, service, otherReconciled, SuggestionKindInvoiceMatch, 0.950, "eur-invoice", "95% match - other account")
	mustTransition(t, ctx, service, excludedTxn, TransactionStateExcluded, "reviewer")
	mustTransition(t, ctx, service, invoiceTxn, TransactionStateReconciled, "reviewer")
	mustTransition(t, ctx, service, otherReconciled, TransactionStateReconciled, "reviewer")

	queue, err := service.ReviewQueue(ctx)
	if err != nil {
		t.Fatalf("ReviewQueue() error = %v", err)
	}
	if len(queue.InvoiceMatches) != 0 || len(queue.DLA) != 1 || len(queue.PayeeRules) != 1 {
		t.Fatalf("ReviewQueue() group sizes = invoice %d dla %d rule %d, want 0/1/1 after invoice reconcile",
			len(queue.InvoiceMatches),
			len(queue.DLA),
			len(queue.PayeeRules),
		)
	}
	if queue.DLA[0].Transaction.ID != dlaTxn || queue.DLA[0].Suggestion.Explanation == "" {
		t.Fatalf("DLA queue item = %#v, want card-ready DLA transaction and explanation", queue.DLA[0])
	}
	if queue.PayeeRules[0].Transaction.ID != ruleTxn || queue.PayeeRules[0].Suggestion.Target != "6200-software" {
		t.Fatalf("payee-rule queue item = %#v, want transaction and account target", queue.PayeeRules[0])
	}

	count, err := service.UnreconciledCount(ctx, account.ID)
	if err != nil {
		t.Fatalf("UnreconciledCount() account error = %v", err)
	}
	if count != 1 {
		t.Fatalf("UnreconciledCount(account) = %d, want 1", count)
	}
	otherCount, err := service.UnreconciledCount(ctx, otherAccount.ID)
	if err != nil {
		t.Fatalf("UnreconciledCount() other account error = %v", err)
	}
	if otherCount != 1 {
		t.Fatalf("UnreconciledCount(otherAccount) = %d, want 1", otherCount)
	}

	feed, err := service.Feed(ctx, FeedFilter{
		AccountID: account.ID,
		State:     TransactionStateSuggested,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("Feed() suggested error = %v", err)
	}
	if len(feed) != 2 || feed[0].ID != ruleTxn || feed[1].ID != dlaTxn {
		t.Fatalf("Feed(suggested) IDs = %v, want rule then DLA by date desc", transactionIDs(feed))
	}
	cursorFeed, err := service.Feed(ctx, FeedFilter{
		AccountID: account.ID,
		After:     &FeedCursor{Date: feed[0].Date, ID: feed[0].ID},
		Limit:     2,
	})
	if err != nil {
		t.Fatalf("Feed() cursor error = %v", err)
	}
	if len(cursorFeed) != 2 || cursorFeed[0].ID != dlaTxn || cursorFeed[1].ID != invoiceTxn {
		t.Fatalf("Feed(cursor) IDs = %v, want DLA then reconciled invoice after cursor", transactionIDs(cursorFeed))
	}

	recent, err := service.RecentlyReconciled(ctx, 0, 5)
	if err != nil {
		t.Fatalf("RecentlyReconciled() error = %v", err)
	}
	if len(recent) != 2 || !hasRecentTransaction(recent, invoiceTxn) || !hasRecentTransaction(recent, otherReconciled) {
		t.Fatalf("RecentlyReconciled() = %#v, want both reconciled account transactions", recent)
	}
	recentForAccount, err := service.RecentlyReconciled(ctx, account.ID, 5)
	if err != nil {
		t.Fatalf("RecentlyReconciled(account) error = %v", err)
	}
	if len(recentForAccount) != 1 || recentForAccount[0].Transaction.ID != invoiceTxn || recentForAccount[0].Actor != "reviewer" || recentForAccount[0].ReconciledAt.IsZero() {
		t.Fatalf("RecentlyReconciled(account) = %#v, want invoice transaction only", recentForAccount)
	}

	assertStoredTransactionState(t, ctx, pool, unreconciledTxn, TransactionStateUnreconciled)
	assertStoredTransactionState(t, ctx, pool, otherUnreconciled, TransactionStateUnreconciled)
}

func TestPayeeNormalizationAndRules(t *testing.T) {
	normalization := []struct {
		input string
		want  string
	}{
		{input: "REVOLUT*Contoso GMBH  ", want: "revolut contoso gmbh"},
		{input: "  Revolut   Contoso\tGMBH ", want: "revolut contoso gmbh"},
		{input: "Card XX1234 Contoso GmbH", want: "contoso gmbh"},
		{input: "Contoso-GMBH/Online", want: "contoso gmbh online"},
	}
	for _, tc := range normalization {
		if got := NormalizePayee(tc.input); got != tc.want {
			t.Fatalf("NormalizePayee(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}

	pool, ledgerPool := temporaryMigratedBankingDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	service := NewService(pool, ledger.New(ledgerPool))
	exact, err := service.CreatePayeeRule(ctx, PayeeRuleInput{
		Matcher:     "REVOLUT*Contoso GMBH  ",
		MatchMode:   PayeeRuleMatchExact,
		AccountCode: "6200-software",
		CreatedFrom: PayeeRuleCreatedFromManual,
	})
	if err != nil {
		t.Fatalf("CreatePayeeRule() exact error = %v", err)
	}
	if exact.Matcher != "revolut contoso gmbh" || exact.TimesApplied != 0 || exact.LastAppliedAt != nil {
		t.Fatalf("exact payee rule = %#v, want normalized unused rule", exact)
	}
	contains, err := service.CreatePayeeRule(ctx, PayeeRuleInput{
		Matcher:     "contoso",
		MatchMode:   PayeeRuleMatchContains,
		AccountCode: "6200-software",
		CreatedFrom: PayeeRuleCreatedFromRecode,
	})
	if err != nil {
		t.Fatalf("CreatePayeeRule() contains error = %v", err)
	}
	applied, err := service.RecordPayeeRuleApplied(ctx, contains.ID)
	if err != nil {
		t.Fatalf("RecordPayeeRuleApplied() error = %v", err)
	}
	if applied.TimesApplied != 1 || applied.LastAppliedAt == nil {
		t.Fatalf("applied payee rule = %#v, want incremented count and timestamp", applied)
	}

	matches, err := service.MatchingPayeeRules(ctx, "Card XX1234 Revolut Contoso GMBH")
	if err != nil {
		t.Fatalf("MatchingPayeeRules() error = %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("MatchingPayeeRules() length = %d, want exact and contains matches", len(matches))
	}
	if matches[0].ID != contains.ID || matches[1].ID != exact.ID {
		t.Fatalf("MatchingPayeeRules() order IDs = %d, %d; want applied contains before exact", matches[0].ID, matches[1].ID)
	}
}

type recordingEnsurer struct {
	calls int
	specs []ledger.AccountSpec
}

func (r *recordingEnsurer) EnsureAccount(_ context.Context, _ db.Tx, spec ledger.AccountSpec) (ledger.AccountCode, error) {
	r.calls++
	r.specs = append(r.specs, spec)
	return spec.Code, nil
}

type accountCreateResult struct {
	account BankAccount
	err     error
}

type blockingEnsurer struct {
	mu      sync.Mutex
	once    sync.Once
	arrived chan struct{}
	release chan struct{}
	calls   int
	specs   []ledger.AccountSpec
}

func newBlockingEnsurer() *blockingEnsurer {
	return &blockingEnsurer{
		arrived: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
}

func (b *blockingEnsurer) EnsureAccount(_ context.Context, _ db.Tx, spec ledger.AccountSpec) (ledger.AccountCode, error) {
	b.mu.Lock()
	b.calls++
	b.specs = append(b.specs, spec)
	b.mu.Unlock()

	b.arrived <- struct{}{}
	<-b.release
	return spec.Code, nil
}

func (b *blockingEnsurer) Release() {
	b.once.Do(func() {
		close(b.release)
	})
}

func (b *blockingEnsurer) Calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func assertBatchCounts(t *testing.T, summary BatchSummary, total int, newRows int, duplicateRows int) {
	t.Helper()
	if summary.TotalRows != total || summary.NewRows != newRows || summary.DuplicateRows != duplicateRows {
		t.Fatalf("BatchSummary counts = total %d new %d duplicate %d, want %d/%d/%d",
			summary.TotalRows,
			summary.NewRows,
			summary.DuplicateRows,
			total,
			newRows,
			duplicateRows,
		)
	}
}

func assertNoImportRows(t *testing.T, ctx context.Context, pool queryRower, accountID AccountID) {
	t.Helper()
	var batches int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM import_batches WHERE account_id = $1`, int64(accountID)).Scan(&batches); err != nil {
		t.Fatalf("count import batches: %v", err)
	}
	if batches != 0 {
		t.Fatalf("import batch count = %d, want 0", batches)
	}
	var transactions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE account_id = $1`, int64(accountID)).Scan(&transactions); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	if transactions != 0 {
		t.Fatalf("transaction count = %d, want 0", transactions)
	}
}

func importSingleBankingTxn(t *testing.T, ctx context.Context, pool queryRower, service *Service, accountID AccountID, txn revolutTestTxn) TransactionID {
	t.Helper()
	if txn.Reference == "" {
		txn.Reference = txn.Payee
	}
	if txn.Balance.Currency == "" {
		txn.Balance = money.Money{Amount: txn.Amount.Amount, Currency: txn.Amount.Currency}
	}
	_, err := service.ImportCSV(ctx, accountID, ImportFile{
		Filename: txn.ID + ".csv",
		Reader:   bytes.NewReader(revolutTestCSV(txn)),
	})
	if err != nil {
		t.Fatalf("ImportCSV() for %q error = %v", txn.ID, err)
	}
	var id int64
	if err := pool.QueryRow(ctx, `
SELECT id
FROM transactions
WHERE account_id = $1
	AND reference = $2
ORDER BY id DESC
LIMIT 1`, int64(accountID), txn.Reference).Scan(&id); err != nil {
		t.Fatalf("load imported transaction %q: %v", txn.Reference, err)
	}
	return TransactionID(id)
}

func mustTransition(t *testing.T, ctx context.Context, service *Service, txnID TransactionID, to TransactionState, actor string) {
	t.Helper()
	if _, err := service.TransitionTransactionState(ctx, txnID, to, actor); err != nil {
		t.Fatalf("TransitionTransactionState(%d, %s) error = %v", txnID, to, err)
	}
}

func mustRecordSuggestion(t *testing.T, ctx context.Context, service *Service, txnID TransactionID, kind SuggestionKind, confidence float64, target string, explanation string) {
	t.Helper()
	if _, err := service.RecordSuggestion(ctx, SuggestionInput{
		TransactionID: txnID,
		Kind:          kind,
		Confidence:    confidence,
		Target:        target,
		Explanation:   explanation,
		CreatedBy:     "engine-run-test",
	}); err != nil {
		t.Fatalf("RecordSuggestion(%d, %s) error = %v", txnID, kind, err)
	}
}

func assertStoredTransactionState(t *testing.T, ctx context.Context, pool queryRower, txnID TransactionID, want TransactionState) {
	t.Helper()
	var got string
	if err := pool.QueryRow(ctx, `
SELECT state::text
FROM transactions
WHERE id = $1`, int64(txnID)).Scan(&got); err != nil {
		t.Fatalf("load transaction %d state: %v", txnID, err)
	}
	if TransactionState(got) != want {
		t.Fatalf("transaction %d state = %q, want %q", txnID, got, want)
	}
}

func hasRecentTransaction(recent []ReconciledTransaction, txnID TransactionID) bool {
	for _, item := range recent {
		if item.Transaction.ID == txnID {
			return true
		}
	}
	return false
}

func transactionIDs(txns []Transaction) []TransactionID {
	ids := make([]TransactionID, len(txns))
	for i, txn := range txns {
		ids[i] = txn.ID
	}
	return ids
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type revolutTestTxn struct {
	Date           time.Time
	ID             string
	Payee          string
	Reference      string
	BlankReference bool
	Amount         money.Money
	Fee            money.Money
	State          string
	Balance        money.Money
}

func revolutTestCSV(txns ...revolutTestTxn) []byte {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	if err := writer.Write([]string{
		"Date started (UTC)",
		"Date completed (UTC)",
		"ID",
		"Type",
		"Description",
		"Reference",
		"Amount",
		"Fee",
		"Currency",
		"State",
		"Balance",
	}); err != nil {
		panic(err)
	}
	for i, txn := range txns {
		if txn.Date.IsZero() {
			txn.Date = time.Date(2030, 1, 2+i, 12, 0, 0, 0, time.UTC)
		}
		if txn.ID == "" {
			txn.ID = fmt.Sprintf("rev-test-%03d", i+1)
		}
		if txn.Payee == "" {
			txn.Payee = fmt.Sprintf("Test payee %d", i+1)
		}
		reference := txn.Reference
		if reference == "" && !txn.BlankReference {
			reference = txn.Payee
		}
		fee := txn.Fee
		if fee.Currency == "" {
			fee = money.Money{Amount: 0, Currency: txn.Amount.Currency}
		}
		state := txn.State
		if state == "" {
			state = "COMPLETED"
		}
		if txn.Balance.Currency == "" {
			txn.Balance = money.Money{Amount: txn.Amount.Amount, Currency: txn.Amount.Currency}
		}
		if err := writer.Write([]string{
			txn.Date.Format("2006-01-02 15:04:05"),
			txn.Date.Format("2006-01-02 15:04:05"),
			txn.ID,
			"CARD_PAYMENT",
			txn.Payee,
			reference,
			formatTestAmount(txn.Amount),
			formatTestAmount(fee),
			txn.Amount.Currency,
			state,
			formatTestAmount(txn.Balance),
		}); err != nil {
			panic(err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func formatTestAmount(amount money.Money) string {
	sign := ""
	value := amount.Amount
	if value < 0 {
		sign = "-"
		value = -value
	}
	return fmt.Sprintf("%s%d.%02d", sign, value/100, value%100)
}

var bankingPostgres struct {
	once      sync.Once
	url       string
	container *postgres.PostgresContainer
	err       error
}

func temporaryMigratedBankingDatabase(t testing.TB) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()

	databaseURL := bankingTestDatabaseURL(t)
	adminPool, err := db.OpenURL(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("OpenURL() admin error = %v", err)
	}
	t.Cleanup(adminPool.Close)

	dbName := fmt.Sprintf("ledgerly_test_banking_%d", time.Now().UnixNano())
	if _, err := adminPool.Exec(context.Background(), "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()); err != nil {
		t.Skipf("CREATE DATABASE unavailable for banking migration test: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{dbName}.Sanitize()+" WITH (FORCE)")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	migrationPool := openBankingTestPool(t, ctx, databaseURL, dbName)
	if _, err := db.MigrateDir(ctx, migrationPool, filepath.Join(findRepoRoot(t), "db", "migrations")); err != nil {
		t.Fatalf("MigrateDir() error = %v", err)
	}
	migrationPool.Close()

	bankingPool := openBankingTestPool(t, ctx, databaseURL, dbName, db.WithModule(ModuleName))
	ledgerPool := openBankingTestPool(t, ctx, databaseURL, dbName, db.WithModule(ledger.ModuleName))
	t.Cleanup(bankingPool.Close)
	t.Cleanup(ledgerPool.Close)

	return bankingPool, ledgerPool
}

func bankingTestDatabaseURL(t testing.TB) string {
	t.Helper()

	if databaseURL := strings.TrimSpace(os.Getenv("LEDGERLY_TEST_DB")); databaseURL != "" {
		return databaseURL
	}

	bankingPostgres.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		container, err := postgres.Run(
			ctx,
			"postgres:16-alpine",
			postgres.WithDatabase("ledgerly_admin"),
			postgres.WithUsername("postgres"),
			postgres.WithPassword("postgres"),
			postgres.BasicWaitStrategies(),
		)
		if err != nil {
			bankingPostgres.err = err
			return
		}
		url, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			_ = testcontainers.TerminateContainer(container)
			bankingPostgres.err = err
			return
		}
		bankingPostgres.container = container
		bankingPostgres.url = url
	})
	if bankingPostgres.err != nil {
		t.Skipf("banking Postgres tests require Docker or LEDGERLY_TEST_DB: %v", bankingPostgres.err)
	}
	return bankingPostgres.url
}

func openBankingTestPool(t testing.TB, ctx context.Context, databaseURL string, dbName string, opts ...db.PoolOption) *pgxpool.Pool {
	t.Helper()

	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	cfg.ConnConfig.Database = dbName
	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			t.Fatalf("pool option error = %v", err)
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("NewWithConfig() error = %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("Ping() error = %v", err)
	}
	return pool
}

func openBankingTestRolePool(t testing.TB, ctx context.Context, databaseURL string, dbName string, role string, opts ...db.PoolOption) *pgxpool.Pool {
	t.Helper()

	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	cfg.ConnConfig.Database = dbName
	cfg.ConnConfig.User = role
	cfg.ConnConfig.Password = role
	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			t.Fatalf("pool option error = %v", err)
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("NewWithConfig() error = %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("Ping() error = %v", err)
	}
	return pool
}

func findRepoRoot(t testing.TB) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repository root containing go.mod")
		}
		dir = parent
	}
}
