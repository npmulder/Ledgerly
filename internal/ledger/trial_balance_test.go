package ledger

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

func TestTrialBalanceBalancedReportThenViolationAndRecovery(t *testing.T) {
	ctx, _, ledgerPool := temporaryMigratedLedgerDatabase(t)
	adminDBPool := openDatabasePool(t, ctx, testDatabaseURL(t), ledgerPool.Config().ConnConfig.Database)
	t.Cleanup(adminDBPool.Close)

	service := New(ledgerPool, discardLedgerBus())
	entryID := postAndCommitValidEntry(t, ctx, ledgerPool, service)
	asOf := time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)

	report, err := service.TrialBalance(ctx, asOf)
	if err != nil {
		t.Fatalf("TrialBalance() balanced error = %v", err)
	}
	if !report.Balanced {
		t.Fatalf("TrialBalance() balanced = false; report=%+v", report)
	}
	if report.GBPTotal != 0 {
		t.Fatalf("GBP total = %d, want 0", report.GBPTotal)
	}
	assertCurrencySum(t, report.CurrencySums, "GBP", 0)
	if len(report.OffendingEntries) != 0 {
		t.Fatalf("offending entries = %+v, want none", report.OffendingEntries)
	}

	corruptPosting(t, ctx, adminDBPool, entryID, 1)
	var logs bytes.Buffer
	status := NewTrialBalanceStatus()
	report, err = service.RunTrialBalanceInvariant(
		ctx,
		asOf,
		slog.New(slog.NewTextHandler(&logs, nil)),
		status,
	)
	if !errors.Is(err, ErrTrialBalanceViolation) {
		t.Fatalf("RunTrialBalanceInvariant() error = %v, want ErrTrialBalanceViolation", err)
	}
	if report.Balanced {
		t.Fatalf("corrupt TrialBalance() balanced = true; report=%+v", report)
	}
	if report.GBPTotal != 1 {
		t.Fatalf("corrupt GBP total = %d, want 1", report.GBPTotal)
	}
	assertCurrencySum(t, report.CurrencySums, "GBP", 1)
	if len(report.OffendingEntries) == 0 {
		t.Fatalf("corrupt report has no offending entries: %+v", report)
	}
	offending := report.OffendingEntries[0]
	if offending.ID != entryID || offending.SourceModule != "ledger" || offending.SourceRef != "test-entry-1" {
		t.Fatalf("offending entry = %+v, want id=%d ledger/test-entry-1", offending, entryID)
	}
	assertCurrencySum(t, offending.NativeSums, "GBP", 1)
	if logText := logs.String(); !strings.Contains(logText, "invariant=violated") {
		t.Fatalf("violation log %q missing invariant=violated", logText)
	}
	if err := status.Check(ctx); err == nil || !strings.Contains(err.Error(), "first offending entry id=") {
		t.Fatalf("status.Check() error = %v, want offending entry reason", err)
	} else if strings.Contains(err.Error(), "test journal entry") || strings.Contains(err.Error(), "test-entry-1") {
		t.Fatalf("status.Check() error = %q, want no journal description or source reference", err.Error())
	}

	corruptPosting(t, ctx, adminDBPool, entryID, -1)
	report, err = service.RunTrialBalanceInvariant(ctx, asOf, slog.New(slog.NewTextHandler(&logs, nil)), status)
	if err != nil {
		t.Fatalf("RunTrialBalanceInvariant() recovery error = %v", err)
	}
	if !report.Balanced {
		t.Fatalf("recovered report balanced = false: %+v", report)
	}
	if err := status.Check(ctx); err != nil {
		t.Fatalf("status.Check() after recovery = %v, want nil", err)
	}

	deletePostings(t, ctx, adminDBPool, entryID)
	report, err = service.TrialBalance(ctx, asOf)
	if !errors.Is(err, ErrTrialBalanceViolation) {
		t.Fatalf("TrialBalance() zero-posting error = %v, want ErrTrialBalanceViolation", err)
	}
	if report.Balanced {
		t.Fatalf("zero-posting report balanced = true; report=%+v", report)
	}
	if len(report.OffendingEntries) == 0 {
		t.Fatalf("zero-posting report has no offending entries: %+v", report)
	}
	if got := report.OffendingEntries[0].PostingCount; got != 0 {
		t.Fatalf("zero-posting offending posting count = %d, want 0; entry=%+v", got, report.OffendingEntries[0])
	}
}

func postAndCommitValidEntry(t *testing.T, ctx context.Context, pool *pgxpool.Pool, service *Service) EntryID {
	t.Helper()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	entryID, err := service.Post(ctx, tx, validJournalEntry())
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	return entryID
}

func corruptPosting(t *testing.T, ctx context.Context, tx db.Tx, entryID EntryID, delta int64) {
	t.Helper()

	var postingID int64
	if err := tx.QueryRow(ctx, `
SELECT id
FROM ledger.postings
WHERE entry_id = $1
ORDER BY id
LIMIT 1`, int64(entryID)).Scan(&postingID); err != nil {
		t.Fatalf("load posting to corrupt: %v", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE ledger.postings
SET amount = amount + $1,
	amount_gbp = amount_gbp + $1
WHERE id = $2`, delta, postingID); err != nil {
		t.Fatalf("corrupt posting %d by %+d: %v", postingID, delta, err)
	}
}

func deletePostings(t *testing.T, ctx context.Context, tx db.Tx, entryID EntryID) {
	t.Helper()

	if _, err := tx.Exec(ctx, `
DELETE FROM ledger.postings
WHERE entry_id = $1`, int64(entryID)); err != nil {
		t.Fatalf("delete postings for entry %d: %v", entryID, err)
	}
}

func assertCurrencySum(t *testing.T, sums []CurrencySum, currency string, amount int64) {
	t.Helper()

	for _, sum := range sums {
		if sum.Currency == currency {
			if sum.Amount != amount {
				t.Fatalf("%s amount = %d, want %d in %+v", currency, sum.Amount, amount, sums)
			}
			return
		}
	}
	t.Fatalf("%s sum missing from %+v", currency, sums)
}
