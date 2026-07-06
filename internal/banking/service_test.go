package banking

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

func TestMain(m *testing.M) {
	os.Exit(testdb.Main(m))
}

func TestCreateAccountEnsuresLedgerOnceAndRetryIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool := testdb.AsModule(t, ModuleName)
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

func TestImportCSVDedupesOverlappingExportsAndReferenceWhitespace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	bankingPool := testdb.AsModule(t, ModuleName)
	ledgerPool := testdb.AsModule(t, ledger.ModuleName)
	service := NewService(bankingPool, ledger.New(ledgerPool))

	account, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut GBP",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	txnA := fixtures.RevolutTxn{
		Date:      time.Date(2026, 4, 1, 8, 30, 0, 0, time.UTC),
		ID:        "rev-a-1",
		Payee:     "ACME & Sons",
		Reference: "Invoice   1001",
		Amount:    money.Money{Amount: 123456, Currency: "GBP"},
		Balance:   money.Money{Amount: 123456, Currency: "GBP"},
	}
	first, err := service.ImportCSV(ctx, account.ID, ImportFile{
		Filename: "statement-a.csv",
		Reader:   bytes.NewReader(fixtures.RevolutCSV(txnA)),
	})
	if err != nil {
		t.Fatalf("ImportCSV() first error = %v", err)
	}
	assertBatchCounts(t, first, 1, 1, 0)

	txnADuplicate := txnA
	txnADuplicate.ID = "rev-a-1-export-2"
	txnADuplicate.Reference = " Invoice 1001 "
	txnB := fixtures.RevolutTxn{
		Date:      time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC),
		ID:        "rev-b-1",
		Payee:     "Beta Ltd",
		Reference: "Invoice 1002",
		Amount:    money.Money{Amount: -2500, Currency: "GBP"},
		Balance:   money.Money{Amount: 120956, Currency: "GBP"},
	}
	second, err := service.ImportCSV(ctx, account.ID, ImportFile{
		Filename: "statement-a-plus-b.csv",
		Reader:   bytes.NewReader(fixtures.RevolutCSV(txnADuplicate, txnB)),
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

func TestImportCSVRejectsMalformedRowsWithoutBatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool := testdb.AsModule(t, ModuleName)
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool := testdb.AsModule(t, ModuleName)
	service := NewService(pool, &recordingEnsurer{})
	account, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut GBP",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	csvBytes := fixtures.RevolutCSV(fixtures.RevolutTxn{
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

type recordingEnsurer struct {
	calls int
	specs []ledger.AccountSpec
}

func (r *recordingEnsurer) EnsureAccount(_ context.Context, _ db.Tx, spec ledger.AccountSpec) (ledger.AccountCode, error) {
	r.calls++
	r.specs = append(r.specs, spec)
	return spec.Code, nil
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

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}
