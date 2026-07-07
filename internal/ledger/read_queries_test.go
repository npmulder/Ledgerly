package ledger

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

func TestReadSideQueriesFixtureScenarios(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)
	service := New(ledgerPool, discardLedgerBus())

	fixture := seedReadQueryFixture(t, ctx, ledgerPool, service)

	debtors, err := service.AccountBalance(ctx, "1105-debtors-multi", fixture.to)
	if err != nil {
		t.Fatalf("AccountBalance(debtors multi) error = %v", err)
	}
	assertAccountBalance(t, debtors, "1105-debtors-multi", AccountTypeAsset, []money.Money{
		moneyAmount(10000, "EUR"),
		moneyAmount(5000, "GBP"),
	}, moneyAmount(13500, "GBP"))

	software, err := service.AccountBalance(ctx, "5010-software", fixture.to)
	if err != nil {
		t.Fatalf("AccountBalance(software) error = %v", err)
	}
	assertAccountBalance(t, software, "5010-software", AccountTypeExpense, []money.Money{
		moneyAmount(3700, "GBP"),
	}, moneyAmount(3700, "GBP"))

	reversedDebtor, err := service.AccountBalance(ctx, "1101-debtors-gbp", fixture.to)
	if err != nil {
		t.Fatalf("AccountBalance(reversed debtor) error = %v", err)
	}
	assertAccountBalance(t, reversedDebtor, "1101-debtors-gbp", AccountTypeAsset, []money.Money{
		moneyAmount(0, "GBP"),
	}, moneyAmount(0, "GBP"))

	balances, err := service.BalancesByType(ctx, fixture.from, fixture.to)
	if err != nil {
		t.Fatalf("BalancesByType() error = %v", err)
	}
	assertTypeBalance(t, balances, AccountTypeAsset, []money.Money{
		moneyAmount(10000, "EUR"),
		moneyAmount(11300, "GBP"),
	}, moneyAmount(19800, "GBP"))
	assertTypeBalance(t, balances, AccountTypeEquity, []money.Money{
		moneyAmount(-10000, "GBP"),
	}, moneyAmount(-10000, "GBP"))
	assertTypeBalance(t, balances, AccountTypeIncome, []money.Money{
		moneyAmount(-10000, "EUR"),
		moneyAmount(-5000, "GBP"),
	}, moneyAmount(-13500, "GBP"))
	assertTypeBalance(t, balances, AccountTypeExpense, []money.Money{
		moneyAmount(2500, "GBP"),
	}, moneyAmount(2500, "GBP"))
	assertTypeBalance(t, balances, AccountTypeLiability, nil, moneyAmount(0, "GBP"))

	periodEntries, err := service.Entries(ctx, EntryFilter{
		From:         &fixture.from,
		To:           &fixture.to,
		SourceModule: fixture.sourceModule,
		Limit:        20,
	})
	if err != nil {
		t.Fatalf("Entries(period) error = %v", err)
	}
	assertPeriodEntryBoundaries(t, periodEntries, fixture.from, fixture.to)

	firstPage, err := service.Entries(ctx, EntryFilter{
		From:         &fixture.from,
		To:           &fixture.to,
		SourceModule: fixture.sourceModule,
		AccountCode:  "4000-sales",
		Limit:        2,
	})
	if err != nil {
		t.Fatalf("Entries(first page) error = %v", err)
	}
	if len(firstPage) != 2 {
		t.Fatalf("Entries(first page) count = %d, want 2", len(firstPage))
	}
	if !firstPage[0].Date.Equal(fixture.from) {
		t.Fatalf("Entries(first page)[0].Date = %s, want from boundary %s", firstPage[0].Date, fixture.from)
	}
	assertEntryHasPosting(t, firstPage[0], "1105-debtors-multi")

	cursor := EntryCursor{Date: firstPage[1].Date, ID: firstPage[1].ID}
	secondPage, err := service.Entries(ctx, EntryFilter{
		From:         &fixture.from,
		To:           &fixture.to,
		SourceModule: fixture.sourceModule,
		AccountCode:  "4000-sales",
		After:        &cursor,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("Entries(second page) error = %v", err)
	}
	if len(secondPage) != 2 {
		t.Fatalf("Entries(second page) count = %d, want original reversal pair", len(secondPage))
	}
	assertEntriesStrictlyAfter(t, secondPage, cursor)

	reversalEntries, err := service.Entries(ctx, EntryFilter{
		From:         &fixture.from,
		To:           &fixture.to,
		SourceModule: fixture.sourceModule,
		AccountCode:  "1101-debtors-gbp",
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("Entries(reversal account) error = %v", err)
	}
	nativeTotals, gbpTotal := sumEntriesForAccount(t, reversalEntries, "1101-debtors-gbp")
	if nativeTotals["GBP"] != 0 || gbpTotal != 0 {
		t.Fatalf("reversal entries total = native=%v gbp=%d, want zero", nativeTotals, gbpTotal)
	}
}

func TestReadSnapshotKeepsMultiQueryReadsStableAcrossConcurrentPost(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)
	service := New(ledgerPool, discardLedgerBus())
	from := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)

	ensureFixtureAccount(t, ctx, ledgerPool, service, AccountSpec{
		Code:     "1000-cash-gbp",
		Name:     "Fixture cash GBP",
		Type:     AccountTypeAsset,
		Currency: stringPtr("GBP"),
	})
	postFixtureEntry(t, ctx, ledgerPool, service, NewJournalEntry{
		Date:         from,
		Description:  "snapshot initial income",
		SourceModule: "ledger-read-snapshot-test",
		SourceRef:    "initial-income",
		Postings: []NewPosting{
			{AccountCode: "1000-cash-gbp", Amount: moneyAmount(1000, "GBP"), AmountGBP: moneyAmount(1000, "GBP")},
			{AccountCode: "4000-sales", Amount: moneyAmount(-1000, "GBP"), AmountGBP: moneyAmount(-1000, "GBP")},
		},
	})

	var snapshotBalances []AccountBalance
	var snapshotEntries []JournalEntry
	if err := service.ReadSnapshot(ctx, func(ctx context.Context, snapshot ReadSnapshot) error {
		var err error
		snapshotBalances, err = snapshot.BalancesByType(ctx, from, to)
		if err != nil {
			return err
		}
		postFixtureEntry(t, ctx, ledgerPool, service, NewJournalEntry{
			Date:         from.AddDate(0, 0, 1),
			Description:  "snapshot interleaved income",
			SourceModule: "ledger-read-snapshot-test",
			SourceRef:    "interleaved-income",
			Postings: []NewPosting{
				{AccountCode: "1000-cash-gbp", Amount: moneyAmount(500, "GBP"), AmountGBP: moneyAmount(500, "GBP")},
				{AccountCode: "4000-sales", Amount: moneyAmount(-500, "GBP"), AmountGBP: moneyAmount(-500, "GBP")},
			},
		})
		snapshotEntries, err = snapshot.Entries(ctx, EntryFilter{
			From:         &from,
			To:           &to,
			SourceModule: "ledger-read-snapshot-test",
			Limit:        10,
		})
		return err
	}); err != nil {
		t.Fatalf("ReadSnapshot() error = %v", err)
	}

	assertTypeBalance(t, snapshotBalances, AccountTypeIncome, []money.Money{
		moneyAmount(-1000, "GBP"),
	}, moneyAmount(-1000, "GBP"))
	if len(snapshotEntries) != 1 || snapshotEntries[0].SourceRef != "initial-income" {
		t.Fatalf("snapshot entries = %#v, want only initial income", snapshotEntries)
	}

	rootBalances, err := service.BalancesByType(ctx, from, to)
	if err != nil {
		t.Fatalf("BalancesByType() after snapshot error = %v", err)
	}
	assertTypeBalance(t, rootBalances, AccountTypeIncome, []money.Money{
		moneyAmount(-1500, "GBP"),
	}, moneyAmount(-1500, "GBP"))
	rootEntries, err := service.Entries(ctx, EntryFilter{
		From:         &from,
		To:           &to,
		SourceModule: "ledger-read-snapshot-test",
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("Entries() after snapshot error = %v", err)
	}
	if len(rootEntries) != 2 {
		t.Fatalf("root entries = %d, want both committed entries", len(rootEntries))
	}
}

func BenchmarkAccountBalanceExplainPlan100kRows(b *testing.B) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(b)
	service := New(ledgerPool, discardLedgerBus())

	seedAccountBalanceBenchmarkRows(b, ctx, ledgerPool)
	if _, err := ledgerPool.Exec(ctx, "VACUUM ANALYZE ledger.postings"); err != nil {
		b.Fatalf("VACUUM ANALYZE ledger.postings: %v", err)
	}

	plan := explainAccountBalancePlan(b, ctx, ledgerPool)
	if strings.Contains(plan, "Seq Scan on postings") || strings.Contains(plan, "Seq Scan on ledger.postings") {
		b.Fatalf("AccountBalance EXPLAIN used a postings sequential scan:\n%s", plan)
	}
	if !strings.Contains(plan, "postings_account_entry_date_covering_idx") {
		b.Fatalf("AccountBalance EXPLAIN did not use account/date covering index:\n%s", plan)
	}
	b.Logf("AccountBalance EXPLAIN for 100k postings:\n%s", plan)

	asOf := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := service.AccountBalance(ctx, "1101-debtors-gbp", asOf); err != nil {
			b.Fatalf("AccountBalance() error = %v", err)
		}
	}
}

type readQueryFixture struct {
	from         time.Time
	to           time.Time
	sourceModule string
}

func seedReadQueryFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, service *Service) readQueryFixture {
	t.Helper()

	sourceModule := "ledger-read-test"
	from := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)

	ensureFixtureAccount(t, ctx, pool, service, AccountSpec{
		Code:     "1000-cash-gbp",
		Name:     "Fixture cash GBP",
		Type:     AccountTypeAsset,
		Currency: stringPtr("GBP"),
	})
	ensureFixtureAccount(t, ctx, pool, service, AccountSpec{
		Code: "1105-debtors-multi",
		Name: "Fixture multi-currency debtors",
		Type: AccountTypeAsset,
	})

	postFixtureEntry(t, ctx, pool, service, NewJournalEntry{
		Date:         time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
		Description:  "opening retained earnings",
		SourceModule: sourceModule,
		SourceRef:    "opening-retained-earnings",
		Postings: []NewPosting{
			{AccountCode: "1000-cash-gbp", Amount: moneyAmount(10000, "GBP"), AmountGBP: moneyAmount(10000, "GBP")},
			{AccountCode: "3000-retained-earnings", Amount: moneyAmount(-10000, "GBP"), AmountGBP: moneyAmount(-10000, "GBP")},
		},
	})
	postFixtureEntry(t, ctx, pool, service, NewJournalEntry{
		Date:         time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		Description:  "expense before period",
		SourceModule: sourceModule,
		SourceRef:    "expense-before-period",
		Postings: []NewPosting{
			{AccountCode: "5010-software", Amount: moneyAmount(1200, "GBP"), AmountGBP: moneyAmount(1200, "GBP")},
			{AccountCode: "1000-cash-gbp", Amount: moneyAmount(-1200, "GBP"), AmountGBP: moneyAmount(-1200, "GBP")},
		},
	})
	postFixtureEntry(t, ctx, pool, service, NewJournalEntry{
		Date:         from,
		Description:  "EUR invoice on from boundary",
		SourceModule: sourceModule,
		SourceRef:    "invoice-from-boundary",
		Postings: []NewPosting{
			{AccountCode: "1105-debtors-multi", Amount: moneyAmount(10000, "EUR"), AmountGBP: moneyAmount(8500, "GBP")},
			{AccountCode: "4000-sales", Amount: moneyAmount(-10000, "EUR"), AmountGBP: moneyAmount(-8500, "GBP")},
		},
	})
	postFixtureEntry(t, ctx, pool, service, NewJournalEntry{
		Date:         time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
		Description:  "GBP invoice in period",
		SourceModule: sourceModule,
		SourceRef:    "invoice-gbp",
		Postings: []NewPosting{
			{AccountCode: "1105-debtors-multi", Amount: moneyAmount(5000, "GBP"), AmountGBP: moneyAmount(5000, "GBP")},
			{AccountCode: "4000-sales", Amount: moneyAmount(-5000, "GBP"), AmountGBP: moneyAmount(-5000, "GBP")},
		},
	})
	reversedID := postFixtureEntry(t, ctx, pool, service, NewJournalEntry{
		Date:         time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
		Description:  "entry that will be reversed",
		SourceModule: sourceModule,
		SourceRef:    "reversal-pair",
		Postings: []NewPosting{
			{AccountCode: "1101-debtors-gbp", Amount: moneyAmount(700, "GBP"), AmountGBP: moneyAmount(700, "GBP")},
			{AccountCode: "4000-sales", Amount: moneyAmount(-700, "GBP"), AmountGBP: moneyAmount(-700, "GBP")},
		},
	})
	reverseFixtureEntry(t, ctx, pool, service, reversedID, "fixture reversal")
	postFixtureEntry(t, ctx, pool, service, NewJournalEntry{
		Date:         to,
		Description:  "expense on to boundary",
		SourceModule: sourceModule,
		SourceRef:    "expense-to-boundary",
		Postings: []NewPosting{
			{AccountCode: "5010-software", Amount: moneyAmount(2500, "GBP"), AmountGBP: moneyAmount(2500, "GBP")},
			{AccountCode: "1000-cash-gbp", Amount: moneyAmount(-2500, "GBP"), AmountGBP: moneyAmount(-2500, "GBP")},
		},
	})
	postFixtureEntry(t, ctx, pool, service, NewJournalEntry{
		Date:         time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		Description:  "expense after period",
		SourceModule: sourceModule,
		SourceRef:    "expense-after-period",
		Postings: []NewPosting{
			{AccountCode: "5010-software", Amount: moneyAmount(300, "GBP"), AmountGBP: moneyAmount(300, "GBP")},
			{AccountCode: "1000-cash-gbp", Amount: moneyAmount(-300, "GBP"), AmountGBP: moneyAmount(-300, "GBP")},
		},
	})

	return readQueryFixture{from: from, to: to, sourceModule: sourceModule}
}

func ensureFixtureAccount(t testing.TB, ctx context.Context, pool *pgxpool.Pool, service *Service, spec AccountSpec) {
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

func postFixtureEntry(t testing.TB, ctx context.Context, pool *pgxpool.Pool, service *Service, entry NewJournalEntry) EntryID {
	t.Helper()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() post fixture error = %v", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()
	id, err := service.Post(ctx, tx, entry)
	if err != nil {
		t.Fatalf("Post(%s) error = %v", entry.SourceRef, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit() post fixture error = %v", err)
	}
	return id
}

func reverseFixtureEntry(t testing.TB, ctx context.Context, pool *pgxpool.Pool, service *Service, id EntryID, reason string) EntryID {
	t.Helper()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() reverse fixture error = %v", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()
	reversalID, err := service.Reverse(ctx, tx, id, reason)
	if err != nil {
		t.Fatalf("Reverse(%d) error = %v", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit() reverse fixture error = %v", err)
	}
	return reversalID
}

func assertAccountBalance(t *testing.T, got AccountBalance, code AccountCode, accountType AccountType, native []money.Money, gbp money.Money) {
	t.Helper()

	if got.AccountCode != code || got.AccountType != accountType {
		t.Fatalf("balance identity = code %q type %q, want code %q type %q", got.AccountCode, got.AccountType, code, accountType)
	}
	assertMoneySlice(t, got.Native, native)
	if got.AmountGBP != gbp {
		t.Fatalf("balance GBP = %#v, want %#v", got.AmountGBP, gbp)
	}
}

func assertTypeBalance(t *testing.T, balances []AccountBalance, accountType AccountType, native []money.Money, gbp money.Money) {
	t.Helper()

	for _, balance := range balances {
		if balance.AccountType != accountType {
			continue
		}
		if balance.AccountCode != "" {
			t.Fatalf("type balance %s account code = %q, want empty aggregate code", accountType, balance.AccountCode)
		}
		assertMoneySlice(t, balance.Native, native)
		if balance.AmountGBP != gbp {
			t.Fatalf("type balance %s GBP = %#v, want %#v", accountType, balance.AmountGBP, gbp)
		}
		return
	}
	t.Fatalf("type balance %s missing from %#v", accountType, balances)
}

func assertMoneySlice(t *testing.T, got []money.Money, want []money.Money) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("money slice length = %d (%#v), want %d (%#v)", len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("money slice[%d] = %#v, want %#v; full slice %#v", i, got[i], want[i], got)
		}
	}
}

func assertPeriodEntryBoundaries(t *testing.T, entries []JournalEntry, from time.Time, to time.Time) {
	t.Helper()

	sawFrom := false
	sawTo := false
	for _, entry := range entries {
		if entry.Date.Before(from) || entry.Date.After(to) {
			t.Fatalf("period entry date = %s, want within [%s, %s]", entry.Date, from, to)
		}
		if entry.Date.Equal(from) {
			sawFrom = true
		}
		if entry.Date.Equal(to) {
			sawTo = true
		}
	}
	if !sawFrom || !sawTo {
		t.Fatalf("period entries saw from=%v to=%v, want both inclusive boundaries", sawFrom, sawTo)
	}
}

func assertEntryHasPosting(t *testing.T, entry JournalEntry, account AccountCode) {
	t.Helper()

	for _, posting := range entry.Postings {
		if posting.AccountCode == account {
			return
		}
	}
	t.Fatalf("entry %d postings = %#v, want account %s included", entry.ID, entry.Postings, account)
}

func assertEntriesStrictlyAfter(t *testing.T, entries []JournalEntry, cursor EntryCursor) {
	t.Helper()

	for _, entry := range entries {
		if entry.Date.Before(cursor.Date) || (entry.Date.Equal(cursor.Date) && entry.ID <= cursor.ID) {
			t.Fatalf("entry %d/%s is not strictly after cursor %d/%s", entry.ID, entry.Date, cursor.ID, cursor.Date)
		}
	}
}

func sumEntriesForAccount(t *testing.T, entries []JournalEntry, account AccountCode) (map[string]int64, int64) {
	t.Helper()

	native := map[string]int64{}
	var gbp int64
	for _, entry := range entries {
		for _, posting := range entry.Postings {
			if posting.AccountCode != account {
				continue
			}
			native[posting.Amount.Currency] += posting.Amount.Amount
			gbp += posting.AmountGBP.Amount
		}
	}
	return native, gbp
}

func seedAccountBalanceBenchmarkRows(b *testing.B, ctx context.Context, pool *pgxpool.Pool) {
	b.Helper()

	_, err := pool.Exec(ctx, `
WITH fixture(n, entry_date) AS (
	SELECT n, DATE '2026-01-01' + ((n % 180)::int)
	FROM generate_series(1, 100000) AS n
),
inserted AS (
	INSERT INTO ledger.journal_entries (date, description, source_module, source_ref)
	SELECT entry_date, 'balance explain fixture', 'ledger-benchmark', n::text
	FROM fixture
	RETURNING id, date, source_ref
)
INSERT INTO ledger.postings (entry_id, entry_date, account_code, amount, currency, amount_gbp)
SELECT id,
	date,
	CASE WHEN source_ref::int % 100 = 0 THEN '1101-debtors-gbp' ELSE '4000-sales' END,
	CASE WHEN source_ref::int % 100 = 0 THEN 1 ELSE -1 END,
	'GBP',
	CASE WHEN source_ref::int % 100 = 0 THEN 1 ELSE -1 END
FROM inserted`)
	if err != nil {
		b.Fatalf("seed account balance benchmark rows: %v", err)
	}
}

func explainAccountBalancePlan(b *testing.B, ctx context.Context, pool *pgxpool.Pool) string {
	b.Helper()

	rows, err := pool.Query(ctx, `
EXPLAIN (COSTS OFF)
SELECT currency, COALESCE(sum(amount), 0)::bigint, COALESCE(sum(amount_gbp), 0)::bigint
FROM ledger.postings
WHERE account_code = '1101-debtors-gbp'
	AND entry_date <= DATE '2026-06-30'
GROUP BY currency
ORDER BY currency`)
	if err != nil {
		b.Fatalf("EXPLAIN account balance query: %v", err)
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			b.Fatalf("scan EXPLAIN row: %v", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		b.Fatalf("collect EXPLAIN rows: %v", err)
	}
	return strings.Join(lines, "\n")
}

func stringPtr(value string) *string {
	return &value
}
