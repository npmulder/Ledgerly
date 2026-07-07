//go:build integration

package it_test

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"pgregory.net/rapid"

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

var ledgerFXBalanceAsOf = day(9999, 12, 31)

func TestLedgerFXTrialBalanceProperty(t *testing.T) {
	f := newLedgerFXFixture(t, day(2030, 1, 2))
	service := ledger.New(f.h.LedgerPool, f.h.Bus)

	rapid.Check(t, func(rt *rapid.T) {
		batch := rapidLedgerFXEntryBatch(rt)
		f.h.Tx(func(ctx context.Context, tx db.Tx) error {
			for i, entry := range batch {
				if _, err := service.Post(ctx, tx, entry); err != nil {
					return fmt.Errorf("post generated batch entry %d source_ref=%s: %w; entry=%#v", i, entry.SourceRef, err, entry)
				}
			}
			return nil
		})
	})

	report, err := service.TrialBalance(f.ctx, ledgerFXBalanceAsOf)
	if err != nil {
		t.Fatalf("TrialBalance() error = %v; report=%+v", err, report)
	}
	assertLedgerFXBalancedReport(t, report)
	it.AssertLedgerBalanced(t, f.h)
}

func TestLedgerFXRejectionPropertyPerturbationWritesNoRows(t *testing.T) {
	f := newLedgerFXFixture(t, day(2030, 1, 2))
	service := ledger.New(f.h.LedgerPool, f.h.Bus)

	rapid.Check(t, func(rt *rapid.T) {
		entry := rapidLedgerFXBalancedEntry(rt)

		gbpPerturbed := entry
		gbpPerturbed.Postings = cloneLedgerFXPostings(entry.Postings)
		gbpIndex := rapid.IntRange(0, len(gbpPerturbed.Postings)-1).Draw(rt, "gbp_perturbed_posting")
		gbpPerturbed.Postings[gbpIndex].AmountGBP.Amount += rapidLedgerFXPerturbation(rt, "gbp_perturbation")
		assertLedgerFXRejectedWithoutRows(rt, f.ctx, f.h.DB, service, gbpPerturbed, ledger.ErrUnbalancedGBP)

		nativePerturbed := entry
		nativePerturbed.Postings = cloneLedgerFXPostings(entry.Postings)
		nativeIndex := rapid.IntRange(0, len(nativePerturbed.Postings)-1).Draw(rt, "native_perturbed_posting")
		nativePerturbed.Postings[nativeIndex].Amount.Amount += rapidLedgerFXPerturbation(rt, "native_perturbation")
		assertLedgerFXRejectedWithoutRows(rt, f.ctx, f.h.DB, service, nativePerturbed, ledger.ErrUnbalancedCurrency)
	})

	it.AssertLedgerBalanced(t, f.h)
}

func TestLedgerFXReversalExactnessRestoresAccountBalances(t *testing.T) {
	f := newLedgerFXFixture(t, day(2030, 1, 8))
	service := ledger.New(f.h.LedgerPool, f.h.Bus)
	asOf := day(2030, 1, 31)
	batch := []ledger.NewJournalEntry{
		{
			Date:         day(2030, 1, 9),
			Description:  "ledgerfx reversal retained earnings",
			SourceModule: ledger.ModuleName,
			SourceRef:    "ledgerfx-reversal-equity",
			Postings: []ledger.NewPosting{
				{AccountCode: "1101-debtors-gbp", Amount: gbp(123_45), AmountGBP: gbp(123_45)},
				{AccountCode: "3000-retained-earnings", Amount: gbp(-123_45), AmountGBP: gbp(-123_45)},
			},
		},
		{
			Date:         day(2030, 1, 10),
			Description:  "ledgerfx reversal director loan",
			SourceModule: ledger.ModuleName,
			SourceRef:    "ledgerfx-reversal-gbp",
			Postings: []ledger.NewPosting{
				{AccountCode: "2300-directors-loan", Amount: gbp(57_89), AmountGBP: gbp(57_89)},
				{AccountCode: "4900-fx-gain-loss", Amount: gbp(-57_89), AmountGBP: gbp(-57_89)},
			},
		},
	}
	accounts := ledgerFXPostingAccounts(batch)

	before := ledgerFXAccountBalances(t, f.ctx, service, accounts, asOf)
	ids := make([]ledger.EntryID, 0, len(batch))
	f.h.Tx(func(ctx context.Context, tx db.Tx) error {
		for _, entry := range batch {
			id, err := service.Post(ctx, tx, entry)
			if err != nil {
				return fmt.Errorf("post %s: %w", entry.SourceRef, err)
			}
			ids = append(ids, id)
		}
		for _, id := range ids {
			if _, err := service.Reverse(ctx, tx, id, "ledgerfx exactness"); err != nil {
				return fmt.Errorf("reverse entry %d: %w", id, err)
			}
		}
		return nil
	})

	after := ledgerFXAccountBalances(t, f.ctx, service, accounts, asOf)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("account balances after reversal = %+v, want pre-batch %+v; accounts=%v entry_ids=%v", after, before, accounts, ids)
	}
	it.AssertLedgerBalanced(t, f.h)
}

func TestLedgerFXAppendOnlyAndBoundaryPermissions(t *testing.T) {
	f := newLedgerFXFixture(t, day(2030, 1, 2))
	service := ledger.New(f.h.LedgerPool, f.h.Bus)

	var entryID ledger.EntryID
	f.h.Tx(func(ctx context.Context, tx db.Tx) error {
		id, err := service.Post(ctx, tx, ledgerFXPermissionProbeEntry())
		if err != nil {
			return fmt.Errorf("post permission probe: %w", err)
		}
		entryID = id
		return nil
	})

	ledgerPool := testdb.AsModule(t, ledger.ModuleName)
	if _, err := ledgerPool.Exec(f.ctx, `UPDATE ledger.journal_entries SET description = description WHERE id = $1`, int64(entryID)); !ledgerFXPermissionDenied(err) {
		t.Fatalf("ledger role UPDATE ledger.journal_entries entry=%d error = %v, want insufficient_privilege 42501", entryID, err)
	}
	if _, err := ledgerPool.Exec(f.ctx, `DELETE FROM ledger.journal_entries WHERE id = $1`, int64(entryID)); !ledgerFXPermissionDenied(err) {
		t.Fatalf("ledger role DELETE ledger.journal_entries entry=%d error = %v, want insufficient_privilege 42501", entryID, err)
	}
	if _, err := ledgerPool.Exec(f.ctx, `
UPDATE ledger.postings
SET amount = amount
WHERE entry_id = $1`, int64(entryID)); !ledgerFXPermissionDenied(err) {
		t.Fatalf("ledger role UPDATE ledger.postings entry=%d error = %v, want insufficient_privilege 42501", entryID, err)
	}
	if _, err := ledgerPool.Exec(f.ctx, `DELETE FROM ledger.postings WHERE entry_id = $1`, int64(entryID)); !ledgerFXPermissionDenied(err) {
		t.Fatalf("ledger role DELETE ledger.postings entry=%d error = %v, want insufficient_privilege 42501", entryID, err)
	}

	invoicingPool := testdb.AsModule(t, invoicing.ModuleName)
	if _, err := invoicingPool.Exec(f.ctx, `SELECT id FROM ledger.journal_entries LIMIT 1`); !ledgerFXPermissionDenied(err) {
		t.Fatalf("invoicing role SELECT ledger.journal_entries error = %v, want insufficient_privilege 42501", err)
	}
	if _, err := invoicingPool.Exec(f.ctx, `
INSERT INTO ledger.journal_entries (date, description, source_module, source_ref)
VALUES ($1, $2, $3, $4)`, day(2030, 1, 2), "boundary probe", "invoicing", "ledgerfx-boundary"); !ledgerFXPermissionDenied(err) {
		t.Fatalf("invoicing role INSERT ledger.journal_entries error = %v, want insufficient_privilege 42501", err)
	}

	it.AssertLedgerBalanced(t, f.h)
}

func TestLedgerFXECBIngestionStalenessAndRecovery(t *testing.T) {
	f := newLedgerFXFixture(t, day(2030, 1, 11).Add(16*time.Hour))
	pool := testdb.AsModule(t, moneyfx.ModuleName)
	store := moneyfx.NewStore(pool)
	staleBus, staleEvents := ledgerFXRatesStaleCaptureBus(t, f.h.Bus)
	server := ledgerFXECBServer(t, nethttp.StatusOK)

	fetcher := ledgerFXECBFetcher(t, pool, staleBus, server.URL, server.Client(), f.h.Clock)
	if err := fetcher.Run(f.ctx); err != nil {
		t.Fatalf("initial ECB fetch: %v", err)
	}
	stored, err := store.ECBRate(f.ctx, day(2030, 1, 11), "GBP")
	if err != nil {
		t.Fatalf("load stored ECB rate: %v", err)
	}
	if stored.Rate != "0.81234567" {
		t.Fatalf("stored GBP rate = %q, want exact decimal 0.81234567", stored.Rate)
	}

	server.Close()
	f.h.Clock.Advance(4 * 24 * time.Hour)
	if err := fetcher.Run(f.ctx); err == nil {
		t.Fatal("ECB fetch with killed server error = nil, want request failure")
	}
	if got := len(staleEvents()); got != 1 {
		t.Fatalf("RatesStale events after killed server = %d, want 1; events=%+v", got, staleEvents())
	}
	if got := staleEvents()[0].LastDate.Format(time.DateOnly); got != "2030-01-11" {
		t.Fatalf("RatesStale.LastDate = %s, want 2030-01-11", got)
	}

	recovered := ledgerFXECBServer(t, nethttp.StatusOK)
	defer recovered.Close()
	recoveryFetcher := ledgerFXECBFetcher(t, pool, staleBus, recovered.URL, recovered.Client(), f.h.Clock)
	if err := recoveryFetcher.Run(f.ctx); err != nil {
		t.Fatalf("recovery ECB fetch: %v", err)
	}
	if got := len(staleEvents()); got != 1 {
		t.Fatalf("RatesStale events after recovery = %d, want still 1; events=%+v", got, staleEvents())
	}
}

func TestLedgerFXRateLockImmutabilityAndRelock(t *testing.T) {
	f := newLedgerFXFixture(t, day(2030, 1, 6).Add(12*time.Hour))
	pool := testdb.AsModule(t, moneyfx.ModuleName)
	store := moneyfx.NewStore(pool)
	service := moneyfx.NewService(store, f.h.Clock)
	ref := moneyfx.LockRef{Module: invoicing.ModuleName, Ref: "INV-LEDGERFX-LOCK"}
	friday := day(2030, 1, 4)
	sunday := day(2030, 1, 6)

	first := ledgerFXLockRate(t, f.ctx, pool, service, ref, sunday)
	if first.Rate != "0.81234567" {
		t.Fatalf("Sunday Lock() rate = %q, want Friday exact decimal 0.81234567", first.Rate)
	}
	if !first.RateDate.Equal(friday) {
		t.Fatalf("Sunday Lock() rate_date = %s, want Friday %s", first.RateDate.Format(time.DateOnly), friday.Format(time.DateOnly))
	}
	if _, err := pool.Exec(f.ctx, `UPDATE moneyfx.rate_locks SET rate = rate WHERE id = $1`, int64(first.ID)); !ledgerFXPermissionDenied(err) {
		t.Fatalf("UPDATE moneyfx.rate_locks id=%d error = %v, want insufficient_privilege 42501", first.ID, err)
	}

	second := ledgerFXLockRate(t, f.ctx, pool, service, ref, sunday)
	if first.ID == second.ID {
		t.Fatalf("re-lock reused id %d, want a second immutable row", first.ID)
	}
	active, err := service.ActiveLockFor(f.ctx, ref)
	if err != nil {
		t.Fatalf("ActiveLockFor(%s) error = %v", ref.String(), err)
	}
	if active.ID != second.ID {
		t.Fatalf("ActiveLockFor(%s) id = %d, want newest second id %d", ref.String(), active.ID, second.ID)
	}
	assertCountWhere(t, f.ctx, pool, "moneyfx.rate_locks", "ref = $1", 2, ref.String())
}

func TestLedgerFXAllocationFuzzAndPennySplits(t *testing.T) {
	t.Run("hand asserted 1p across three shares", func(t *testing.T) {
		tests := []struct {
			name   string
			amount money.Money
			ratios []int
			want   []money.Money
		}{
			{
				name:   "positive equal shares use input order",
				amount: gbp(1),
				ratios: []int{1, 1, 1},
				want:   []money.Money{gbp(1), gbp(0), gbp(0)},
			},
			{
				name:   "negative equal shares use input order",
				amount: gbp(-1),
				ratios: []int{1, 1, 1},
				want:   []money.Money{gbp(-1), gbp(0), gbp(0)},
			},
			{
				name:   "weighted shares give penny to largest remainder",
				amount: gbp(1),
				ratios: []int{1, 2, 3},
				want:   []money.Money{gbp(0), gbp(0), gbp(1)},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got := tt.amount.Allocate(tt.ratios)
				if !reflect.DeepEqual(got, tt.want) {
					t.Fatalf("Allocate(%+v, %v) = %+v, want %+v", tt.amount, tt.ratios, got, tt.want)
				}
				assertLedgerFXAllocationSums(t, tt.amount, got)
			})
		}
	})

	rapid.Check(t, func(rt *rapid.T) {
		amount := money.Money{
			Amount:   int64(rapid.IntRange(-10_000_000, 10_000_000).Draw(rt, "amount")),
			Currency: rapid.SampledFrom([]string{"GBP", "EUR", "USD"}).Draw(rt, "currency"),
		}
		ratioCount := rapid.IntRange(0, 8).Draw(rt, "ratio_count")
		ratios := make([]int, ratioCount)
		for i := range ratios {
			ratios[i] = rapid.IntRange(-7, 20).Draw(rt, fmt.Sprintf("ratio_%d", i))
		}

		parts := amount.Allocate(ratios)
		if len(ratios) == 0 && amount.Amount != 0 {
			if len(parts) != 1 {
				rt.Fatalf("Allocate(%+v, empty ratios) returned %d parts, want single original part; parts=%+v", amount, len(parts), parts)
			}
		} else if len(parts) != len(ratios) {
			rt.Fatalf("Allocate(%+v, ratios=%v) returned %d parts, want %d; parts=%+v", amount, ratios, len(parts), len(ratios), parts)
		}
		assertLedgerFXAllocationSums(rt, amount, parts)
		for i, part := range parts {
			if part.Currency != amount.Currency {
				rt.Fatalf("allocation part %d currency = %q, want %q; original=%+v ratios=%v parts=%+v", i, part.Currency, amount.Currency, amount, ratios, parts)
			}
		}
	})
}

type ledgerFXFixture struct {
	ctx context.Context
	h   *harness.Harness
}

func newLedgerFXFixture(t *testing.T, clockStart time.Time) ledgerFXFixture {
	t.Helper()

	h := harness.New(t, harness.Options{ClockStart: clockStart})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	fixtures.Company(t, h)
	fixtures.Rates(t, h)
	fixtures.Rates(t, h, fixtures.RatesStep(map[time.Time]string{
		day(2030, 1, 4): "0.81234567",
		day(2030, 1, 7): "0.9000",
	}))
	return ledgerFXFixture{ctx: ctx, h: h}
}

func rapidLedgerFXEntryBatch(t *rapid.T) []ledger.NewJournalEntry {
	t.Helper()

	count := rapid.IntRange(1, 4).Draw(t, "entry_count")
	entries := make([]ledger.NewJournalEntry, count)
	for i := range entries {
		entry := rapidLedgerFXBalancedEntry(t)
		entry.SourceRef = fmt.Sprintf("%s-batch-%d", entry.SourceRef, i)
		entries[i] = entry
	}
	return entries
}

func rapidLedgerFXBalancedEntry(t *rapid.T) ledger.NewJournalEntry {
	t.Helper()

	pairCount := rapid.IntRange(1, 3).Draw(t, "pair_count")
	postings := make([]ledger.NewPosting, 0, pairCount*2)
	currencies := []string{"GBP", "EUR", "USD"}
	for i := 0; i < pairCount; i++ {
		currency := rapid.SampledFrom(currencies).Draw(t, fmt.Sprintf("currency_%d", i))
		nativeAmount := int64(rapid.IntRange(2, 100_000).Draw(t, fmt.Sprintf("native_amount_%d", i)))
		if rapid.Bool().Draw(t, fmt.Sprintf("negative_%d", i)) {
			nativeAmount = -nativeAmount
		}
		gbpAmount := int64(rapid.IntRange(2, 100_000).Draw(t, fmt.Sprintf("gbp_amount_%d", i)))
		if nativeAmount < 0 {
			gbpAmount = -gbpAmount
		}
		accounts := compatibleLedgerFXAccounts(currency)
		firstAccount := rapid.SampledFrom(accounts).Draw(t, fmt.Sprintf("first_account_%d", i))
		secondAccount := rapid.SampledFrom(accounts).Draw(t, fmt.Sprintf("second_account_%d", i))

		postings = append(postings,
			ledger.NewPosting{
				AccountCode: firstAccount,
				Amount:      money.Money{Amount: nativeAmount, Currency: currency},
				AmountGBP:   gbp(gbpAmount),
			},
			ledger.NewPosting{
				AccountCode: secondAccount,
				Amount:      money.Money{Amount: -nativeAmount, Currency: currency},
				AmountGBP:   gbp(-gbpAmount),
			},
		)
	}

	return ledger.NewJournalEntry{
		Date:         day(2030, time.January, rapid.IntRange(1, 28).Draw(t, "day")),
		Description:  "ledgerfx rapid balanced entry",
		SourceModule: ledger.ModuleName,
		SourceRef:    fmt.Sprintf("ledgerfx-rapid-%d", rapid.Int64().Draw(t, "source_ref")),
		Postings:     postings,
	}
}

func compatibleLedgerFXAccounts(currency string) []ledger.AccountCode {
	accounts := []ledger.AccountCode{
		"4000-sales",
		"5000-fees",
		"5010-software",
		"5020-travel",
		"5030-office",
	}
	switch currency {
	case "GBP":
		accounts = append(accounts,
			"1101-debtors-gbp",
			"2200-vat-control",
			"2300-directors-loan",
			"3000-retained-earnings",
			"4900-fx-gain-loss",
		)
	case "EUR":
		accounts = append(accounts, "1100-debtors-eur")
	}
	return accounts
}

func rapidLedgerFXPerturbation(t *rapid.T, label string) int64 {
	t.Helper()
	if rapid.Bool().Draw(t, label) {
		return 1
	}
	return -1
}

func cloneLedgerFXPostings(postings []ledger.NewPosting) []ledger.NewPosting {
	cloned := make([]ledger.NewPosting, len(postings))
	copy(cloned, postings)
	return cloned
}

func assertLedgerFXRejectedWithoutRows(t interface {
	Helper()
	Fatalf(string, ...any)
}, ctx context.Context, pool *pgxpool.Pool, service *ledger.Service, entry ledger.NewJournalEntry, want error) {
	t.Helper()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rejection probe transaction for source_ref=%s: %v", entry.SourceRef, err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	beforeEntries := ledgerFXCountRows(t, ctx, tx, "ledger.journal_entries")
	beforePostings := ledgerFXCountRows(t, ctx, tx, "ledger.postings")
	_, err = service.Post(ctx, tx, entry)
	if !errors.Is(err, want) {
		t.Fatalf("Post(perturbed source_ref=%s) error = %v, want %v; entry=%#v", entry.SourceRef, err, want, entry)
	}
	afterEntries := ledgerFXCountRows(t, ctx, tx, "ledger.journal_entries")
	afterPostings := ledgerFXCountRows(t, ctx, tx, "ledger.postings")
	if afterEntries != beforeEntries || afterPostings != beforePostings {
		t.Fatalf("Post(perturbed source_ref=%s) wrote rows: journal_entries %d->%d postings %d->%d; entry=%#v",
			entry.SourceRef,
			beforeEntries,
			afterEntries,
			beforePostings,
			afterPostings,
			entry,
		)
	}
}

func ledgerFXCountRows(t interface {
	Helper()
	Fatalf(string, ...any)
}, ctx context.Context, tx db.Tx, table string) int {
	t.Helper()

	var count int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func assertLedgerFXBalancedReport(t *testing.T, report ledger.Report) {
	t.Helper()

	if !report.Balanced {
		t.Fatalf("trial balance Balanced = false; report=%+v", report)
	}
	if report.GBPTotal != 0 {
		t.Fatalf("trial balance GBP total = %d, want 0; report=%+v", report.GBPTotal, report)
	}
	for _, sum := range report.CurrencySums {
		if sum.Amount != 0 {
			t.Fatalf("trial balance %s native total = %d, want 0; report=%+v", sum.Currency, sum.Amount, report)
		}
	}
	if len(report.OffendingEntries) != 0 {
		t.Fatalf("trial balance offending entries = %+v, want none; report=%+v", report.OffendingEntries, report)
	}
}

func ledgerFXAccountBalances(t *testing.T, ctx context.Context, service *ledger.Service, accounts []ledger.AccountCode, asOf time.Time) map[ledger.AccountCode]ledger.AccountBalance {
	t.Helper()

	balances := make(map[ledger.AccountCode]ledger.AccountBalance, len(accounts))
	for _, account := range accounts {
		balance, err := service.AccountBalance(ctx, account, asOf)
		if err != nil {
			t.Fatalf("AccountBalance(%s) error = %v", account, err)
		}
		balances[account] = ledgerFXCanonicalBalance(balance)
	}
	return balances
}

func ledgerFXCanonicalBalance(balance ledger.AccountBalance) ledger.AccountBalance {
	// Fully reversed nullable accounts can report either no native rows or
	// explicit zero rows depending on whether postings ever touched them.
	native := make([]money.Money, 0, len(balance.Native))
	for _, amount := range balance.Native {
		if amount.IsZero() {
			continue
		}
		native = append(native, amount)
	}
	balance.Native = native
	return balance
}

func ledgerFXPostingAccounts(entries []ledger.NewJournalEntry) []ledger.AccountCode {
	seen := make(map[ledger.AccountCode]struct{})
	var accounts []ledger.AccountCode
	for _, entry := range entries {
		for _, posting := range entry.Postings {
			if _, ok := seen[posting.AccountCode]; ok {
				continue
			}
			seen[posting.AccountCode] = struct{}{}
			accounts = append(accounts, posting.AccountCode)
		}
	}
	return accounts
}

func ledgerFXPermissionProbeEntry() ledger.NewJournalEntry {
	return ledger.NewJournalEntry{
		Date:         day(2030, 1, 2),
		Description:  "ledgerfx permission probe",
		SourceModule: ledger.ModuleName,
		SourceRef:    "ledgerfx-permission-probe",
		Postings: []ledger.NewPosting{
			{AccountCode: "1101-debtors-gbp", Amount: gbp(100), AmountGBP: gbp(100)},
			{AccountCode: "4000-sales", Amount: gbp(-100), AmountGBP: gbp(-100)},
		},
	}
}

func ledgerFXPermissionDenied(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

func ledgerFXRatesStaleCaptureBus(t *testing.T, b *bus.Bus) (*bus.Bus, func() []moneyfx.RatesStale) {
	t.Helper()

	var mu sync.Mutex
	var events []moneyfx.RatesStale
	b.Subscribe(moneyfx.RatesStaleName, func(_ context.Context, _ db.Tx, evt bus.Event) error {
		stale, ok := evt.(moneyfx.RatesStale)
		if !ok {
			return fmt.Errorf("got %T, want moneyfx.RatesStale", evt)
		}
		mu.Lock()
		events = append(events, stale)
		mu.Unlock()
		return nil
	})
	return b, func() []moneyfx.RatesStale {
		mu.Lock()
		defer mu.Unlock()
		return append([]moneyfx.RatesStale(nil), events...)
	}
}

func ledgerFXECBServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		if status != nethttp.StatusOK {
			nethttp.Error(w, nethttp.StatusText(status), status)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(ledgerFXECBXML))
	}))
}

func ledgerFXECBFetcher(
	t *testing.T,
	pool *pgxpool.Pool,
	b *bus.Bus,
	feedURL string,
	client *nethttp.Client,
	clk interface{ Now() time.Time },
) *moneyfx.ECBFetcher {
	t.Helper()

	fetcher, err := moneyfx.NewECBFetcher(moneyfx.ECBFetcherConfig{
		Pool:         pool,
		Bus:          b,
		Clock:        clk,
		Location:     time.UTC,
		FeedURL:      feedURL,
		HTTPClient:   client,
		Retries:      -1,
		RetryBackoff: -1,
	})
	if err != nil {
		t.Fatalf("NewECBFetcher() error = %v", err)
	}
	return fetcher
}

func ledgerFXLockRate(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	service *moneyfx.Service,
	ref moneyfx.LockRef,
	date time.Time,
) moneyfx.RateLock {
	t.Helper()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rate lock transaction: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	lock, err := service.Lock(ctx, tx, ref, "EUR", "GBP", date)
	if err != nil {
		t.Fatalf("Lock(%s, %s) error = %v", ref.String(), date.Format(time.DateOnly), err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit rate lock transaction: %v", err)
	}
	committed = true
	return lock
}

func assertLedgerFXAllocationSums(t interface {
	Helper()
	Fatalf(string, ...any)
}, original money.Money, parts []money.Money) {
	t.Helper()

	total := money.Zero(original.Currency)
	for i, part := range parts {
		var err error
		total, err = total.Add(part)
		if err != nil {
			t.Fatalf("sum allocation part %d=%+v into total=%+v: %v; original=%+v parts=%+v", i, part, total, err, original, parts)
		}
	}
	if total != original {
		t.Fatalf("allocation sum = %+v, want %+v; parts=%+v", total, original, parts)
	}
}

const ledgerFXECBXML = `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
	<Cube>
		<Cube time="2030-01-11">
			<Cube currency="GBP" rate="0.81234567"/>
			<Cube currency="USD" rate="1.12345678"/>
		</Cube>
	</Cube>
</gesmes:Envelope>`
