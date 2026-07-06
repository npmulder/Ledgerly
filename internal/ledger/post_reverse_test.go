package ledger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

func TestPostValidationRulesRejectWholeEntry(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)
	service := New(ledgerPool, discardLedgerBus())

	tests := []struct {
		name   string
		mutate func(*NewJournalEntry)
		want   error
	}{
		{
			name: "fewer than two postings",
			mutate: func(entry *NewJournalEntry) {
				entry.Postings = entry.Postings[:1]
			},
			want: ErrInsufficientPostings,
		},
		{
			name: "GBP total must balance",
			mutate: func(entry *NewJournalEntry) {
				entry.Postings[0].AmountGBP.Amount++
			},
			want: ErrUnbalancedGBP,
		},
		{
			name: "native totals must balance per currency",
			mutate: func(entry *NewJournalEntry) {
				entry.Postings[0].Amount.Amount++
			},
			want: ErrUnbalancedCurrency,
		},
		{
			name: "posting account must exist",
			mutate: func(entry *NewJournalEntry) {
				entry.Postings[0].AccountCode = "9999-missing"
			},
			want: ErrAccountNotFound,
		},
		{
			name: "native money currency is required",
			mutate: func(entry *NewJournalEntry) {
				entry.Postings[0].Amount.Currency = ""
			},
			want: ErrInvalidMoney,
		},
		{
			name: "GBP money currency is required",
			mutate: func(entry *NewJournalEntry) {
				entry.Postings[0].AmountGBP.Currency = ""
			},
			want: ErrInvalidMoney,
		},
		{
			name: "GBP money must be GBP",
			mutate: func(entry *NewJournalEntry) {
				entry.Postings[0].AmountGBP.Currency = "EUR"
			},
			want: ErrInvalidMoney,
		},
		{
			name: "GBP sign must match native sign",
			mutate: func(entry *NewJournalEntry) {
				entry.Postings[0].AmountGBP.Amount = -entry.Postings[0].AmountGBP.Amount
				entry.Postings[1].AmountGBP.Amount = -entry.Postings[1].AmountGBP.Amount
			},
			want: ErrPostingSignMismatch,
		},
		{
			name: "native zero amount is rejected",
			mutate: func(entry *NewJournalEntry) {
				entry.Postings[0].Amount.Amount = 0
			},
			want: ErrZeroPosting,
		},
		{
			name: "GBP zero amount is rejected",
			mutate: func(entry *NewJournalEntry) {
				entry.Postings[0].AmountGBP.Amount = 0
			},
			want: ErrZeroPosting,
		},
		{
			name: "date is required",
			mutate: func(entry *NewJournalEntry) {
				entry.Date = time.Time{}
			},
			want: ErrInvalidEntryDate,
		},
		{
			name: "date must be sane",
			mutate: func(entry *NewJournalEntry) {
				entry.Date = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)
			},
			want: ErrInvalidEntryDate,
		},
		{
			name: "description is required",
			mutate: func(entry *NewJournalEntry) {
				entry.Description = " "
			},
			want: ErrInvalidJournalEntry,
		},
		{
			name: "source module is required",
			mutate: func(entry *NewJournalEntry) {
				entry.SourceModule = " "
			},
			want: ErrInvalidJournalEntry,
		},
		{
			name: "source ref is required",
			mutate: func(entry *NewJournalEntry) {
				entry.SourceRef = " "
			},
			want: ErrInvalidJournalEntry,
		},
		{
			name: "fixed account currency must match native currency",
			mutate: func(entry *NewJournalEntry) {
				entry.Postings[0].Amount.Currency = "EUR"
				entry.Postings[1].Amount.Currency = "EUR"
			},
			want: ErrAccountCurrencyMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := ledgerPool.Begin(ctx)
			if err != nil {
				t.Fatalf("Begin() error = %v", err)
			}
			defer func() {
				_ = tx.Rollback(context.Background())
			}()

			entry := validJournalEntry()
			tt.mutate(&entry)
			_, err = service.Post(ctx, tx, entry)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Post() error = %v, want %v", err, tt.want)
			}
			assertJournalEntryCount(t, ctx, tx, 0)
		})
	}
}

func TestPostRunsInsideModuleOwnedTransaction(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)
	invoicingPool := openDatabasePool(t, ctx, testDatabaseURL(t), ledgerPool.Config().ConnConfig.Database, db.WithModule("invoicing"))
	t.Cleanup(invoicingPool.Close)

	service := New(invoicingPool, discardLedgerBus())
	tx, err := invoicingPool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	entry := validJournalEntry()
	entry.SourceModule = "invoicing"
	entry.SourceRef = "invoice-123"
	if _, err := service.Post(ctx, tx, entry); err != nil {
		t.Fatalf("Post() from invoicing-owned tx error = %v", err)
	}
	assertJournalEntryCount(t, ctx, tx, 1)
}

func TestPostAllowsNullCurrencyAccountsToUseAnyNativeCurrency(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)
	service := New(ledgerPool, discardLedgerBus())

	tx, err := ledgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	entry := NewJournalEntry{
		Date:         time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC),
		Description:  "multi-currency null account entry",
		SourceModule: "ledger",
		SourceRef:    "null-currency-usd",
		Postings: []NewPosting{
			{
				AccountCode: "4000-sales",
				Amount:      moneyAmount(100, "USD"),
				AmountGBP:   moneyAmount(80, "GBP"),
			},
			{
				AccountCode: "5000-fees",
				Amount:      moneyAmount(-100, "USD"),
				AmountGBP:   moneyAmount(-80, "GBP"),
			},
		},
	}
	id, err := service.Post(ctx, tx, entry)
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	stored, err := service.store.JournalEntry(ctx, tx, id)
	if err != nil {
		t.Fatalf("JournalEntry() error = %v", err)
	}
	assertStoredEntry(t, stored, entry, nil)
}

func TestPostStoresEntryPostingsAndPublishesExactlyOnce(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)

	eventBus := discardLedgerBus()
	var events []EntryPosted
	var publishTx db.Tx
	eventBus.Subscribe(EntryPostedName, func(_ context.Context, gotTx db.Tx, evt bus.Event) error {
		posted, ok := evt.(EntryPosted)
		if !ok {
			t.Fatalf("event = %T, want EntryPosted", evt)
		}
		publishTx = gotTx
		events = append(events, posted)
		return nil
	})
	service := New(ledgerPool, eventBus)

	tx, err := ledgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	entry := validJournalEntry()
	id, err := service.Post(ctx, tx, entry)
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if publishTx != tx {
		t.Fatalf("EntryPosted tx = %p, want caller tx %p", publishTx, tx)
	}
	if len(events) != 1 {
		t.Fatalf("EntryPosted count = %d, want 1", len(events))
	}
	wantEvent := EntryPosted{
		EntryID:      id,
		SourceModule: entry.SourceModule,
		Accounts:     []AccountCode{"1101-debtors-gbp", "4000-sales"},
		Date:         entry.Date,
	}
	if !reflect.DeepEqual(events[0], wantEvent) {
		t.Fatalf("EntryPosted = %#v, want %#v", events[0], wantEvent)
	}

	stored, err := service.store.JournalEntry(ctx, tx, id)
	if err != nil {
		t.Fatalf("JournalEntry() error = %v", err)
	}
	assertStoredEntry(t, stored, entry, nil)
}

func TestPostRollbackLeavesNoRowsOrEventEffects(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)
	adminDBPool := openDatabasePool(t, ctx, testDatabaseURL(t), ledgerPool.Config().ConnConfig.Database)
	defer adminDBPool.Close()

	if _, err := adminDBPool.Exec(ctx, `
CREATE TABLE ledger.event_effects (
	entry_id bigint PRIMARY KEY
)`); err != nil {
		t.Fatalf("create event effects table: %v", err)
	}
	if _, err := adminDBPool.Exec(ctx, `
GRANT SELECT, INSERT ON ledger.event_effects TO ledgerly_ledger`); err != nil {
		t.Fatalf("grant event effects table: %v", err)
	}

	eventBus := discardLedgerBus()
	eventBus.Subscribe(EntryPostedName, func(ctx context.Context, tx db.Tx, evt bus.Event) error {
		posted := evt.(EntryPosted)
		_, err := tx.Exec(ctx, `
INSERT INTO ledger.event_effects (entry_id)
VALUES ($1)`, int64(posted.EntryID))
		return err
	})
	service := New(ledgerPool, eventBus)

	tx, err := ledgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	id, err := service.Post(ctx, tx, validJournalEntry())
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	assertCountWhere(t, ctx, ledgerPool, "ledger.journal_entries", "id = $1", 0, int64(id))
	assertCountWhere(t, ctx, ledgerPool, "ledger.postings", "entry_id = $1", 0, int64(id))
	assertCountWhere(t, ctx, ledgerPool, "ledger.event_effects", "entry_id = $1", 0, int64(id))
}

func TestReverseCreatesImmutableNegatingEntryAndRejectsInvalidReversals(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)

	eventBus := discardLedgerBus()
	var events []EntryPosted
	eventBus.Subscribe(EntryPostedName, func(_ context.Context, _ db.Tx, evt bus.Event) error {
		events = append(events, evt.(EntryPosted))
		return nil
	})
	service := New(ledgerPool, eventBus)

	tx, err := ledgerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	entry := validJournalEntry()
	originalID, err := service.Post(ctx, tx, entry)
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	reversalID, err := service.Reverse(ctx, tx, originalID, "duplicate invoice")
	if err != nil {
		t.Fatalf("Reverse() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("EntryPosted count = %d, want 2 for original and reversal", len(events))
	}

	reversal, err := service.store.JournalEntry(ctx, tx, reversalID)
	if err != nil {
		t.Fatalf("JournalEntry(reversal) error = %v", err)
	}
	wantReversal := NewJournalEntry{
		Date:         entry.Date,
		Description:  fmt.Sprintf("Reversal of %d: duplicate invoice", originalID),
		SourceModule: entry.SourceModule,
		SourceRef:    entry.SourceRef,
		Postings: []NewPosting{
			{
				AccountCode: "1101-debtors-gbp",
				Amount:      moneyAmount(-100, "GBP"),
				AmountGBP:   moneyAmount(-100, "GBP"),
			},
			{
				AccountCode: "4000-sales",
				Amount:      moneyAmount(100, "GBP"),
				AmountGBP:   moneyAmount(100, "GBP"),
			},
		},
	}
	assertStoredEntry(t, reversal, wantReversal, &originalID)
	assertEntryPairNetsToZero(t, ctx, tx, originalID, reversalID)

	_, err = service.Reverse(ctx, tx, reversalID, "nested")
	if !errors.Is(err, ErrReversalOfReversal) {
		t.Fatalf("Reverse(reversal) error = %v, want ErrReversalOfReversal", err)
	}
	_, err = service.Reverse(ctx, tx, originalID, "again")
	if !errors.Is(err, ErrEntryAlreadyReversed) {
		t.Fatalf("Reverse(original again) error = %v, want ErrEntryAlreadyReversed", err)
	}
}

func TestPostPropertiesBalancedEntriesAcceptedStoredExactlyAndPerturbationsRejected(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)
	service := New(ledgerPool, discardLedgerBus())

	rapid.Check(t, func(rt *rapid.T) {
		tx, err := ledgerPool.Begin(ctx)
		if err != nil {
			rt.Fatalf("Begin() error = %v", err)
		}
		rt.Cleanup(func() {
			_ = tx.Rollback(context.Background())
		})

		entry := rapidBalancedEntry(rt)
		id, err := service.Post(ctx, tx, entry)
		if err != nil {
			rt.Fatalf("Post(balanced) error = %v; entry=%#v", err, entry)
		}
		stored, err := service.store.JournalEntry(ctx, tx, id)
		if err != nil {
			rt.Fatalf("JournalEntry() error = %v", err)
		}
		assertStoredEntry(rt, stored, entry, nil)

		gbpPerturbed := entry
		gbpPerturbed.Postings = cloneNewPostings(entry.Postings)
		gbpIndex := rapid.IntRange(0, len(gbpPerturbed.Postings)-1).Draw(rt, "gbp_perturbed_posting")
		gbpPerturbed.Postings[gbpIndex].AmountGBP.Amount += rapidPerturbation(rt, "gbp_perturbation")
		_, err = service.Post(ctx, tx, gbpPerturbed)
		if !errors.Is(err, ErrUnbalancedGBP) {
			rt.Fatalf("Post(GBP perturbation) error = %v, want ErrUnbalancedGBP", err)
		}

		nativePerturbed := entry
		nativePerturbed.Postings = cloneNewPostings(entry.Postings)
		nativeIndex := rapid.IntRange(0, len(nativePerturbed.Postings)-1).Draw(rt, "native_perturbed_posting")
		nativePerturbed.Postings[nativeIndex].Amount.Amount += rapidPerturbation(rt, "native_perturbation")
		_, err = service.Post(ctx, tx, nativePerturbed)
		if !errors.Is(err, ErrUnbalancedCurrency) {
			rt.Fatalf("Post(native perturbation) error = %v, want ErrUnbalancedCurrency", err)
		}
	})
}

func TestReversePropertyPostThenReverseNetsEveryAccountToZero(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)
	service := New(ledgerPool, discardLedgerBus())

	rapid.Check(t, func(rt *rapid.T) {
		tx, err := ledgerPool.Begin(ctx)
		if err != nil {
			rt.Fatalf("Begin() error = %v", err)
		}
		rt.Cleanup(func() {
			_ = tx.Rollback(context.Background())
		})

		entry := rapidBalancedEntry(rt)
		originalID, err := service.Post(ctx, tx, entry)
		if err != nil {
			rt.Fatalf("Post() error = %v", err)
		}
		reversalID, err := service.Reverse(ctx, tx, originalID, "property reversal")
		if err != nil {
			rt.Fatalf("Reverse() error = %v", err)
		}
		assertEntryPairNetsToZero(rt, ctx, tx, originalID, reversalID)
	})
}

func TestConcurrentPostsToSameAccountsBothSucceed(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)
	service := New(ledgerPool, discardLedgerBus())

	const workers = 2
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()

			tx, err := ledgerPool.Begin(ctx)
			if err != nil {
				errs <- fmt.Errorf("worker %d begin: %w", i, err)
				return
			}
			defer func() {
				_ = tx.Rollback(context.Background())
			}()

			entry := validJournalEntry()
			entry.SourceRef = fmt.Sprintf("concurrent-%d", i)
			if _, err := service.Post(ctx, tx, entry); err != nil {
				errs <- fmt.Errorf("worker %d post: %w", i, err)
				return
			}
			if err := tx.Commit(ctx); err != nil {
				errs <- fmt.Errorf("worker %d commit: %w", i, err)
				return
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertJournalEntryCount(t, ctx, ledgerPool, workers)
}

func validJournalEntry() NewJournalEntry {
	return NewJournalEntry{
		Date:         time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC),
		Description:  "test journal entry",
		SourceModule: "ledger",
		SourceRef:    "test-entry-1",
		Postings: []NewPosting{
			{
				AccountCode: "1101-debtors-gbp",
				Amount:      moneyAmount(100, "GBP"),
				AmountGBP:   moneyAmount(100, "GBP"),
			},
			{
				AccountCode: "4000-sales",
				Amount:      moneyAmount(-100, "GBP"),
				AmountGBP:   moneyAmount(-100, "GBP"),
			},
		},
	}
}

func rapidBalancedEntry(t *rapid.T) NewJournalEntry {
	pairCount := rapid.IntRange(1, 3).Draw(t, "pair_count")
	postings := make([]NewPosting, 0, pairCount*2)
	accounts := []AccountCode{
		"4000-sales",
		"5000-fees",
		"5010-software",
		"5020-travel",
		"5030-office",
	}
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
		compatibleAccounts := compatiblePostingAccounts(currency, accounts)
		firstAccount := rapid.SampledFrom(compatibleAccounts).Draw(t, fmt.Sprintf("first_account_%d", i))
		secondAccount := rapid.SampledFrom(compatibleAccounts).Draw(t, fmt.Sprintf("second_account_%d", i))

		postings = append(postings,
			NewPosting{
				AccountCode: firstAccount,
				Amount:      moneyAmount(nativeAmount, currency),
				AmountGBP:   moneyAmount(gbpAmount, "GBP"),
			},
			NewPosting{
				AccountCode: secondAccount,
				Amount:      moneyAmount(-nativeAmount, currency),
				AmountGBP:   moneyAmount(-gbpAmount, "GBP"),
			},
		)
	}

	return NewJournalEntry{
		Date:         time.Date(2026, 7, rapid.IntRange(1, 28).Draw(t, "day"), 0, 0, 0, 0, time.UTC),
		Description:  "rapid balanced entry",
		SourceModule: "ledger",
		SourceRef:    fmt.Sprintf("rapid-%d", rapid.Int64().Draw(t, "source_ref")),
		Postings:     postings,
	}
}

func compatiblePostingAccounts(currency string, nullCurrencyAccounts []AccountCode) []AccountCode {
	accounts := make([]AccountCode, 0, len(nullCurrencyAccounts)+4)
	accounts = append(accounts, nullCurrencyAccounts...)
	switch currency {
	case "GBP":
		accounts = append(accounts, "1101-debtors-gbp", "2200-vat-control", "3000-retained-earnings")
	case "EUR":
		accounts = append(accounts, "1100-debtors-eur")
	}
	return accounts
}

func rapidPerturbation(t *rapid.T, label string) int64 {
	if rapid.Bool().Draw(t, label) {
		return 1
	}
	return -1
}

func cloneNewPostings(postings []NewPosting) []NewPosting {
	cloned := make([]NewPosting, len(postings))
	copy(cloned, postings)
	return cloned
}

func moneyAmount(amount int64, currency string) money.Money {
	return money.Money{Amount: amount, Currency: currency}
}

func assertStoredEntry(t interface {
	Helper()
	Fatalf(string, ...any)
}, got JournalEntry, want NewJournalEntry, wantReversalOf *EntryID) {
	t.Helper()

	if !got.Date.Equal(want.Date) ||
		got.Description != want.Description ||
		got.SourceModule != want.SourceModule ||
		got.SourceRef != want.SourceRef {
		t.Fatalf("stored entry = %#v, want date=%v description=%q source=%s/%s", got, want.Date, want.Description, want.SourceModule, want.SourceRef)
	}
	if !sameEntryIDPtr(got.ReversalOf, wantReversalOf) {
		t.Fatalf("stored reversal_of = %v, want %v", got.ReversalOf, wantReversalOf)
	}
	if len(got.Postings) != len(want.Postings) {
		t.Fatalf("stored posting count = %d, want %d", len(got.Postings), len(want.Postings))
	}
	for i, posting := range got.Postings {
		wantPosting := want.Postings[i]
		if posting.AccountCode != wantPosting.AccountCode ||
			posting.Amount != wantPosting.Amount ||
			posting.AmountGBP != wantPosting.AmountGBP {
			t.Fatalf("stored posting %d = %#v, want %#v", i, posting, wantPosting)
		}
	}
}

func sameEntryIDPtr(a, b *EntryID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func assertEntryPairNetsToZero(t interface {
	Helper()
	Fatalf(string, ...any)
}, ctx context.Context, tx db.Tx, originalID EntryID, reversalID EntryID) {
	t.Helper()

	rows, err := tx.Query(ctx, `
SELECT account_code, currency, sum(amount)::bigint, sum(amount_gbp)::bigint
FROM ledger.postings
WHERE entry_id = ANY($1)
GROUP BY account_code, currency`, []int64{int64(originalID), int64(reversalID)})
	if err != nil {
		t.Fatalf("query entry pair totals: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			accountCode string
			currency    string
			amount      int64
			amountGBP   int64
		)
		if err := rows.Scan(&accountCode, &currency, &amount, &amountGBP); err != nil {
			t.Fatalf("scan entry pair totals: %v", err)
		}
		if amount != 0 || amountGBP != 0 {
			t.Fatalf("entry pair account %s %s totals amount=%d amount_gbp=%d, want zero", accountCode, currency, amount, amountGBP)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("collect entry pair totals: %v", err)
	}
}

func assertJournalEntryCount(t *testing.T, ctx context.Context, tx db.Tx, want int) {
	t.Helper()

	assertCountWhere(t, ctx, tx, "ledger.journal_entries", "true", want)
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

func discardLedgerBus() *bus.Bus {
	return bus.New(bus.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
}
