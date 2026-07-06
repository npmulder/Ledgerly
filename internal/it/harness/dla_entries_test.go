package harness_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/it"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

const dlaCashAccount ledger.AccountCode = "1000-cash-gbp"

func TestDLAEntriesPostLedgerShapesAndRunningBalance(t *testing.T) {
	fixture := newDLAFixture(t)
	sameDay := time.Date(2026, 7, 1, 11, 15, 0, 0, time.UTC)
	nextDay := time.Date(2026, 7, 2, 9, 0, 0, 0, time.UTC)

	fixture.fileDrawingFromBanking(t, dla.TxnRef{
		Ref:             "banking:txn-100",
		Date:            sameDay,
		Amount:          gbp(10_000),
		CashAccountCode: dlaCashAccount,
	})
	if err := fixture.dla.AddEntry(fixture.ctx, dla.NewEntry{
		Date:               sameDay,
		Kind:               dla.EntryKindExpenseOwed,
		Description:        "Software paid personally",
		Amount:             gbp(2_500),
		Source:             "manual:expense-1",
		ExpenseAccountCode: "5010-software",
	}); err != nil {
		t.Fatalf("AddEntry(expense-owed) error = %v", err)
	}
	if err := fixture.dla.AddEntry(fixture.ctx, dla.NewEntry{
		Date:            nextDay,
		Kind:            dla.EntryKindRepayment,
		Description:     "Director repayment",
		Amount:          gbp(4_000),
		Source:          "manual:repayment-1",
		CashAccountCode: dlaCashAccount,
	}); err != nil {
		t.Fatalf("AddEntry(repayment) error = %v", err)
	}

	entries, err := fixture.dla.Ledger(fixture.ctx, dla.LedgerFilter{Limit: 10})
	if err != nil {
		t.Fatalf("Ledger() error = %v", err)
	}
	assertDLAEntries(t, entries, []wantDLAEntry{
		{
			kind:           dla.EntryKindDrawing,
			source:         "banking:txn-100",
			amount:         10_000,
			owedToYou:      0,
			drawn:          10_000,
			runningBalance: -10_000,
			side:           dla.BalanceSideDebit,
		},
		{
			kind:           dla.EntryKindExpenseOwed,
			source:         "manual:expense-1",
			amount:         2_500,
			owedToYou:      2_500,
			drawn:          0,
			runningBalance: -7_500,
			side:           dla.BalanceSideDebit,
		},
		{
			kind:           dla.EntryKindRepayment,
			source:         "manual:repayment-1",
			amount:         4_000,
			owedToYou:      4_000,
			drawn:          0,
			runningBalance: -3_500,
			side:           dla.BalanceSideDebit,
		},
	})
	if !entries[0].Date.Equal(entries[1].Date) || entries[0].ID >= entries[1].ID {
		t.Fatalf("same-day entries ordered by IDs = %d then %d on dates %s/%s",
			entries[0].ID,
			entries[1].ID,
			entries[0].Date,
			entries[1].Date,
		)
	}

	fixture.assertLedgerPostings(t, "banking:txn-100", []wantPosting{
		{account: dla.DLAAccountCode, amount: 10_000},
		{account: dlaCashAccount, amount: -10_000},
	})
	fixture.assertLedgerPostings(t, "manual:expense-1", []wantPosting{
		{account: "5010-software", amount: 2_500},
		{account: dla.DLAAccountCode, amount: -2_500},
	})
	fixture.assertLedgerPostings(t, "manual:repayment-1", []wantPosting{
		{account: dlaCashAccount, amount: 4_000},
		{account: dla.DLAAccountCode, amount: -4_000},
	})
	it.AssertLedgerBalanced(t, fixture.harness)
}

func TestDLADuplicateSourceRejectedWithoutSecondLedgerPost(t *testing.T) {
	fixture := newDLAFixture(t)
	ref := "banking:txn-duplicate"

	fixture.fileDrawingFromBanking(t, dla.TxnRef{
		Ref:             ref,
		Date:            time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC),
		Amount:          gbp(7_500),
		CashAccountCode: dlaCashAccount,
	})

	err := fixture.tryFileDrawingFromBanking(dla.TxnRef{
		Ref:             ref,
		Date:            time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC),
		Amount:          gbp(7_500),
		CashAccountCode: dlaCashAccount,
	})
	if !errors.Is(err, dla.ErrDuplicateSource) {
		t.Fatalf("FileDrawing(duplicate) error = %v, want ErrDuplicateSource", err)
	}
	var duplicate *dla.DuplicateSourceError
	if !errors.As(err, &duplicate) || duplicate.Source != ref {
		t.Fatalf("duplicate error = %#v, want source %q", err, ref)
	}
	assertCountWhere(t, fixture.ctx, fixture.harness.DB, "dla.dla_entries", "source = $1", 1, ref)
	assertCountWhere(t, fixture.ctx, fixture.harness.DB, "ledger.journal_entries", "source_module = 'dla' AND source_ref = $1", 1, ref)
	it.AssertLedgerBalanced(t, fixture.harness)
}

func TestDLAFileDrawingUsesCallerTransaction(t *testing.T) {
	fixture := newDLAFixture(t)
	ref := "banking:txn-rollback"

	tx, err := fixture.banking.Begin(fixture.ctx)
	if err != nil {
		t.Fatalf("Begin() banking transaction error = %v", err)
	}
	if err := fixture.dla.FileDrawing(fixture.ctx, tx, dla.TxnRef{
		Ref:             ref,
		Date:            time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC),
		Amount:          gbp(5_500),
		CashAccountCode: dlaCashAccount,
	}); err != nil {
		t.Fatalf("FileDrawing() inside rollback transaction error = %v", err)
	}
	if err := tx.Rollback(fixture.ctx); err != nil {
		t.Fatalf("Rollback() banking transaction error = %v", err)
	}

	assertCountWhere(t, fixture.ctx, fixture.harness.DB, "dla.dla_entries", "source = $1", 0, ref)
	assertCountWhere(t, fixture.ctx, fixture.harness.DB, "ledger.journal_entries", "source_module = 'dla' AND source_ref = $1", 0, ref)
	it.AssertLedgerBalanced(t, fixture.harness)
}

func TestDLACompensatingEntryCorrectsMistakeWithoutUpdate(t *testing.T) {
	fixture := newDLAFixture(t)

	fixture.fileDrawingFromBanking(t, dla.TxnRef{
		Ref:             "banking:mistaken-drawing",
		Date:            time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC),
		Amount:          gbp(10_000),
		CashAccountCode: dlaCashAccount,
	})
	if err := fixture.dla.AddEntry(fixture.ctx, dla.NewEntry{
		Date:            time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC),
		Kind:            dla.EntryKindRepayment,
		Description:     "Correction for overstated drawing",
		Amount:          gbp(2_000),
		Source:          "manual:correction-mistaken-drawing",
		CashAccountCode: dlaCashAccount,
	}); err != nil {
		t.Fatalf("AddEntry(correction) error = %v", err)
	}

	entries, err := fixture.dla.Ledger(fixture.ctx, dla.LedgerFilter{Limit: 10})
	if err != nil {
		t.Fatalf("Ledger() error = %v", err)
	}
	assertDLAEntries(t, entries, []wantDLAEntry{
		{
			kind:           dla.EntryKindDrawing,
			source:         "banking:mistaken-drawing",
			amount:         10_000,
			drawn:          10_000,
			runningBalance: -10_000,
			side:           dla.BalanceSideDebit,
		},
		{
			kind:           dla.EntryKindRepayment,
			source:         "manual:correction-mistaken-drawing",
			amount:         2_000,
			owedToYou:      2_000,
			runningBalance: -8_000,
			side:           dla.BalanceSideDebit,
		},
	})
	assertCountWhere(t, fixture.ctx, fixture.harness.DB, "dla.dla_entries", "true", 2)

	_, err = fixture.dlaPool.Exec(fixture.ctx, `
UPDATE dla.dla_entries
SET amount = 8000
WHERE source = 'banking:mistaken-drawing'`)
	assertPermissionDenied(t, err)
	_, err = fixture.dlaPool.Exec(fixture.ctx, `
DELETE FROM dla.dla_entries
WHERE source = 'banking:mistaken-drawing'`)
	assertPermissionDenied(t, err)
	it.AssertLedgerBalanced(t, fixture.harness)
}

type dlaFixture struct {
	ctx     context.Context
	harness *harness.Harness
	dlaPool *pgxpool.Pool
	banking *pgxpool.Pool
	dla     *dla.Service
}

func newDLAFixture(t *testing.T) dlaFixture {
	t.Helper()

	h := harness.New(t, harness.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	dlaPool := testdb.AsModule(t, dla.ModuleName)
	bankingPool := testdb.AsModule(t, "banking")
	ledgerService := ledger.New(h.LedgerPool)
	ensureLedgerAccount(t, ctx, h.LedgerPool, ledgerService, ledger.AccountSpec{
		Code:     dlaCashAccount,
		Name:     "DLA fixture cash GBP",
		Type:     ledger.AccountTypeAsset,
		Currency: stringPtr("GBP"),
	})

	return dlaFixture{
		ctx:     ctx,
		harness: h,
		dlaPool: dlaPool,
		banking: bankingPool,
		dla:     dla.New(dlaPool, ledgerService),
	}
}

func (f dlaFixture) fileDrawingFromBanking(t *testing.T, src dla.TxnRef) {
	t.Helper()
	if err := f.tryFileDrawingFromBanking(src); err != nil {
		t.Fatalf("FileDrawing(%s) error = %v", src.Ref, err)
	}
}

func (f dlaFixture) tryFileDrawingFromBanking(src dla.TxnRef) (err error) {
	tx, err := f.banking.Begin(f.ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(context.Background())
		}
	}()
	if err = f.dla.FileDrawing(f.ctx, tx, src); err != nil {
		return err
	}
	if err = tx.Commit(f.ctx); err != nil {
		return err
	}
	return nil
}

func (f dlaFixture) assertLedgerPostings(t *testing.T, source string, want []wantPosting) {
	t.Helper()

	rows, err := f.harness.DB.Query(f.ctx, `
SELECT p.account_code, p.amount, p.currency, p.amount_gbp
FROM ledger.journal_entries AS je
JOIN ledger.postings AS p ON p.entry_id = je.id
WHERE je.source_module = 'dla'
	AND je.source_ref = $1
ORDER BY p.id`, source)
	if err != nil {
		t.Fatalf("query ledger postings for %s: %v", source, err)
	}
	defer rows.Close()

	got := []wantPosting{}
	for rows.Next() {
		var posting wantPosting
		var currency string
		var amountGBP int64
		if err := rows.Scan(&posting.account, &posting.amount, &currency, &amountGBP); err != nil {
			t.Fatalf("scan ledger posting for %s: %v", source, err)
		}
		if currency != "GBP" || amountGBP != posting.amount {
			t.Fatalf("posting for %s has currency=%s amount_gbp=%d amount=%d, want GBP and matching GBP amount",
				source,
				currency,
				amountGBP,
				posting.amount,
			)
		}
		got = append(got, posting)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("collect ledger postings for %s: %v", source, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ledger postings for %s = %#v, want %#v", source, got, want)
	}
}

type wantPosting struct {
	account ledger.AccountCode
	amount  int64
}

type wantDLAEntry struct {
	kind           dla.EntryKind
	source         string
	amount         int64
	owedToYou      int64
	drawn          int64
	runningBalance int64
	side           dla.BalanceSide
}

func assertDLAEntries(t *testing.T, got []dla.Entry, want []wantDLAEntry) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("DLA entry count = %d (%#v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i].Kind != want[i].kind ||
			got[i].Source != want[i].source ||
			got[i].Amount != gbp(want[i].amount) ||
			got[i].OwedToYou != gbp(want[i].owedToYou) ||
			got[i].Drawn != gbp(want[i].drawn) ||
			got[i].RunningBalance != gbp(want[i].runningBalance) ||
			got[i].BalanceSide != want[i].side {
			t.Fatalf("DLA entry %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func ensureLedgerAccount(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	service *ledger.Service,
	spec ledger.AccountSpec,
) {
	t.Helper()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() ensure account error = %v", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()
	if _, err := service.EnsureAccount(ctx, tx, spec); err != nil {
		t.Fatalf("EnsureAccount(%s) error = %v", spec.Code, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit() ensure account error = %v", err)
	}
}

func gbp(amount int64) money.Money {
	return money.Money{Amount: amount, Currency: "GBP"}
}

func stringPtr(value string) *string {
	return &value
}

func assertCountWhere(t *testing.T, ctx context.Context, tx db.Tx, table string, predicate string, want int, args ...any) {
	t.Helper()

	query := "SELECT count(*) FROM " + table + " WHERE " + predicate
	var got int
	if err := tx.QueryRow(ctx, query, args...).Scan(&got); err != nil {
		t.Fatalf("count %s where %s: %v", table, predicate, err)
	}
	if got != want {
		t.Fatalf("count %s where %s = %d, want %d", table, predicate, got, want)
	}
}

func assertPermissionDenied(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("app role mutation succeeded, want PostgreSQL insufficient_privilege")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("app role mutation error = %v, want PostgreSQL insufficient_privilege 42501", err)
	}
}
