package ledger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

func TestSeededChartOfAccounts(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)
	service := New(ledgerPool)

	accounts, err := service.Accounts(ctx)
	if err != nil {
		t.Fatalf("Accounts() error = %v", err)
	}

	byCode := make(map[AccountCode]Account, len(accounts))
	for _, account := range accounts {
		byCode[account.Code] = account
	}

	assertSeedAccount(t, byCode, "1100-debtors-eur", "Trade debtors EUR", AccountTypeAsset, "EUR")
	assertSeedAccount(t, byCode, "1101-debtors-gbp", "Trade debtors GBP", AccountTypeAsset, "GBP")
	assertSeedAccount(t, byCode, "2200-vat-control", "VAT control", AccountTypeLiability, "")
	assertSeedAccount(t, byCode, "2300-directors-loan", "Director's loan account", AccountTypeLiability, "GBP")
	assertSeedAccount(t, byCode, "3000-retained-earnings", "Retained earnings", AccountTypeEquity, "GBP")
	assertSeedAccount(t, byCode, "4000-sales", "Sales", AccountTypeIncome, "")
	assertSeedAccount(t, byCode, "4900-fx-gain-loss", "Realised FX gain/loss", AccountTypeIncome, "GBP")
	assertSeedAccount(t, byCode, "5000-fees", "Fees", AccountTypeExpense, "")
	assertSeedAccount(t, byCode, "5010-software", "Software", AccountTypeExpense, "")
	assertSeedAccount(t, byCode, "5020-travel", "Travel", AccountTypeExpense, "")
	assertSeedAccount(t, byCode, "5030-office", "Office", AccountTypeExpense, "")

	for code := range byCode {
		if strings.Contains(string(code), "cash") {
			t.Fatalf("seeded account %q looks like a cash account; cash accounts should be created by banking EnsureAccount", code)
		}
	}

	var tableComment string
	if err := ledgerPool.QueryRow(ctx, "SELECT obj_description('accounts'::regclass)").Scan(&tableComment); err != nil {
		t.Fatalf("read ledger.accounts comment: %v", err)
	}
	if !strings.Contains(tableComment, "banking creates real cash accounts") {
		t.Fatalf("ledger.accounts comment = %q, want cash placeholder note", tableComment)
	}
}

func TestEnsureAccountIdempotentAndConflicts(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)
	service := New(ledgerPool)

	tx, err := ledgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback(context.Background())
	})

	currency := "usd"
	spec := AccountSpec{
		Code:     "1005-cash-usd",
		Name:     "Revolut USD",
		Type:     AccountTypeAsset,
		Currency: &currency,
	}

	first, err := service.EnsureAccount(ctx, tx, spec)
	if err != nil {
		t.Fatalf("EnsureAccount() first error = %v", err)
	}
	second, err := service.EnsureAccount(ctx, tx, spec)
	if err != nil {
		t.Fatalf("EnsureAccount() second error = %v", err)
	}
	if first != second || first != "1005-cash-usd" {
		t.Fatalf("EnsureAccount() codes = %q then %q, want 1005-cash-usd twice", first, second)
	}

	var created int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM accounts WHERE code = $1", string(first)).Scan(&created); err != nil {
		t.Fatalf("count ensured account: %v", err)
	}
	if created != 1 {
		t.Fatalf("ensured account count = %d, want 1", created)
	}

	conflictingName := spec
	conflictingName.Name = "Different USD account"
	_, err = service.EnsureAccount(ctx, tx, conflictingName)
	assertAccountConflict(t, err, "name", "Revolut USD", "Different USD account")

	conflictingType := spec
	conflictingType.Type = AccountTypeLiability
	_, err = service.EnsureAccount(ctx, tx, conflictingType)
	assertAccountConflict(t, err, "type", "asset", "liability")

	conflictingCurrencyValue := "EUR"
	conflictingCurrency := spec
	conflictingCurrency.Currency = &conflictingCurrencyValue
	_, err = service.EnsureAccount(ctx, tx, conflictingCurrency)
	assertAccountConflict(t, err, "currency", "USD", "EUR")
}

func TestEnsureAccountWorksFromBankingTransaction(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)
	service := New(ledgerPool)

	bankingPool := openDatabasePool(
		t,
		ctx,
		testDatabaseURL(t),
		ledgerPool.Config().ConnConfig.Database,
		db.WithModule("banking"),
	)
	t.Cleanup(bankingPool.Close)

	tx, err := bankingPool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() banking error = %v", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	var searchPath string
	if err := tx.QueryRow(ctx, "SHOW search_path").Scan(&searchPath); err != nil {
		t.Fatalf("SHOW search_path in banking tx: %v", err)
	}
	if searchPath != "banking" {
		t.Fatalf("banking tx search_path = %q, want banking", searchPath)
	}

	currency := "gbp"
	code, err := service.EnsureAccount(ctx, tx, AccountSpec{
		Code:     "1000-cash-banking-gbp",
		Name:     "Banking cash GBP",
		Type:     AccountTypeAsset,
		Currency: &currency,
	})
	if err != nil {
		t.Fatalf("EnsureAccount() from banking tx error = %v", err)
	}
	if code != "1000-cash-banking-gbp" {
		t.Fatalf("EnsureAccount() from banking tx code = %q, want 1000-cash-banking-gbp", code)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit() banking tx error = %v", err)
	}

	var created int
	if err := ledgerPool.QueryRow(ctx, `
SELECT count(*)
FROM ledger.accounts
WHERE code = $1
	AND name = 'Banking cash GBP'
	AND type = 'asset'
	AND currency = 'GBP'`, string(code)).Scan(&created); err != nil {
		t.Fatalf("count banking-created account: %v", err)
	}
	if created != 1 {
		t.Fatalf("banking-created account count = %d, want 1", created)
	}

	_, err = bankingPool.Exec(ctx, `
INSERT INTO ledger.accounts (code, name, type, currency)
VALUES ('1001-direct-banking-gbp', 'Direct banking cash GBP', 'asset', 'GBP')`)
	assertPermissionDenied(t, err)
}

func TestJournalEntriesAndPostingsAppendOnlyForAppRole(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)

	var entryID int64
	if err := ledgerPool.QueryRow(ctx, `
INSERT INTO journal_entries (date, description, source_module, source_ref)
VALUES (DATE '2026-07-05', 'append-only proof', 'ledger-test', 'entry-1')
RETURNING id`).Scan(&entryID); err != nil {
		t.Fatalf("insert journal entry as app role: %v", err)
	}

	var postingID int64
	if err := ledgerPool.QueryRow(ctx, `
INSERT INTO postings (entry_id, account_code, amount, currency, amount_gbp)
VALUES ($1, '4000-sales', 100, 'GBP', 100)
RETURNING id`, entryID).Scan(&postingID); err != nil {
		t.Fatalf("insert posting as app role: %v", err)
	}

	_, err := ledgerPool.Exec(ctx, `
UPDATE journal_entries
SET description = 'mutated'
WHERE id = $1`, entryID)
	assertPermissionDenied(t, err)
	_, err = ledgerPool.Exec(ctx, `
DELETE FROM journal_entries
WHERE id = $1`, entryID)
	assertPermissionDenied(t, err)
	_, err = ledgerPool.Exec(ctx, `
UPDATE postings
SET amount = amount + 1
WHERE id = $1`, postingID)
	assertPermissionDenied(t, err)
	_, err = ledgerPool.Exec(ctx, `
DELETE FROM postings
WHERE id = $1`, postingID)
	assertPermissionDenied(t, err)
}

func assertSeedAccount(
	t *testing.T,
	accounts map[AccountCode]Account,
	code AccountCode,
	name string,
	accountType AccountType,
	currency string,
) {
	t.Helper()

	account, ok := accounts[code]
	if !ok {
		t.Fatalf("seed account %q missing", code)
	}
	if account.Name != name || account.Type != accountType {
		t.Fatalf("account %q = %#v, want name %q type %q", code, account, name, accountType)
	}
	if currency == "" {
		if account.Currency != nil {
			t.Fatalf("account %q currency = %q, want nil", code, *account.Currency)
		}
		return
	}
	if account.Currency == nil || *account.Currency != currency {
		t.Fatalf("account %q currency = %v, want %q", code, account.Currency, currency)
	}
}

func assertAccountConflict(
	t *testing.T,
	err error,
	field string,
	existing string,
	requested string,
) {
	t.Helper()

	if err == nil {
		t.Fatalf("EnsureAccount() conflict error = nil, want %s conflict", field)
	}
	if !errors.Is(err, ErrAccountConflict) {
		t.Fatalf("EnsureAccount() error = %v, want ErrAccountConflict", err)
	}
	var conflict *AccountConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("EnsureAccount() error = %T, want *AccountConflictError", err)
	}
	if conflict.Field != field || conflict.Existing != existing || conflict.Requested != requested {
		t.Fatalf("conflict = %#v, want field=%q existing=%q requested=%q", conflict, field, existing, requested)
	}
}

func assertPermissionDenied(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("app role write succeeded, want PostgreSQL insufficient_privilege")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("app role write error = %v, want PostgreSQL insufficient_privilege 42501", err)
	}
}

func temporaryMigratedLedgerDatabase(t testing.TB) (context.Context, *pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()

	databaseURL := testDatabaseURL(t)
	adminPool, err := db.OpenURL(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("OpenURL() admin error = %v", err)
	}
	t.Cleanup(adminPool.Close)

	dbName := fmt.Sprintf("ledgerly_test_ledger_%d", time.Now().UnixNano())
	if _, err := adminPool.Exec(context.Background(), "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()); err != nil {
		t.Skipf("CREATE DATABASE unavailable for ledger migration test: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{dbName}.Sanitize()+" WITH (FORCE)")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	migrationPool := openDatabasePool(t, ctx, databaseURL, dbName)
	if _, err := db.MigrateDir(ctx, migrationPool, filepath.Join(findRepoRoot(t), "db", "migrations")); err != nil {
		t.Fatalf("MigrateDir() error = %v", err)
	}
	migrationPool.Close()

	ledgerPool := openDatabasePool(t, ctx, databaseURL, dbName, db.WithModule("ledger"))
	t.Cleanup(ledgerPool.Close)

	return ctx, adminPool, ledgerPool
}

func openDatabasePool(t testing.TB, ctx context.Context, databaseURL string, dbName string, opts ...db.PoolOption) *pgxpool.Pool {
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

func testDatabaseURL(t testing.TB) string {
	t.Helper()

	databaseURL := strings.TrimSpace(os.Getenv("LEDGERLY_TEST_DB"))
	if databaseURL == "" {
		t.Skip("set LEDGERLY_TEST_DB to run ledger Postgres tests")
	}
	return databaseURL
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
