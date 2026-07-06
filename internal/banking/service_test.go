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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, _ := temporaryMigratedBankingDatabase(t)
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

	bankingPool, ledgerPool := temporaryMigratedBankingDatabase(t)
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

func TestImportCSVRejectsMalformedRowsWithoutBatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, _ := temporaryMigratedBankingDatabase(t)
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

	pool, _ := temporaryMigratedBankingDatabase(t)
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

type revolutTestTxn struct {
	Date      time.Time
	ID        string
	Payee     string
	Reference string
	Amount    money.Money
	Balance   money.Money
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
		if txn.Reference == "" {
			txn.Reference = txn.Payee
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
			txn.Reference,
			formatTestAmount(txn.Amount),
			"0.00",
			txn.Amount.Currency,
			"COMPLETED",
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
