package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

// TrialBalance calculates whole-ledger balance totals and first offending
// entries up to asOf.
func (Store) TrialBalance(ctx context.Context, tx db.Tx, asOf time.Time, offendingLimit int) (Report, error) {
	if offendingLimit <= 0 {
		offendingLimit = trialBalanceOffendingLimit
	}

	report := Report{AsOf: asOf}
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(sum(p.amount_gbp), 0)::bigint
FROM ledger.postings p
JOIN ledger.journal_entries e ON e.id = p.entry_id
WHERE e.date <= $1`, asOf).Scan(&report.GBPTotal); err != nil {
		return Report{}, fmt.Errorf("ledger: trial balance GBP total: %w", err)
	}

	currencySums, err := trialBalanceCurrencySums(ctx, tx, asOf)
	if err != nil {
		return Report{}, err
	}
	report.CurrencySums = currencySums

	offending, err := trialBalanceOffendingEntries(ctx, tx, asOf, offendingLimit)
	if err != nil {
		return Report{}, err
	}
	report.OffendingEntries = offending
	report.Balanced = report.GBPTotal == 0 && currencySumsBalanced(report.CurrencySums) && len(report.OffendingEntries) == 0
	return report, nil
}

func trialBalanceCurrencySums(ctx context.Context, tx db.Tx, asOf time.Time) ([]CurrencySum, error) {
	rows, err := tx.Query(ctx, `
SELECT p.currency, sum(p.amount)::bigint
FROM ledger.postings p
JOIN ledger.journal_entries e ON e.id = p.entry_id
WHERE e.date <= $1
GROUP BY p.currency
ORDER BY p.currency`, asOf)
	if err != nil {
		return nil, fmt.Errorf("ledger: trial balance native totals: %w", err)
	}
	defer rows.Close()

	sums, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (CurrencySum, error) {
		var sum CurrencySum
		if err := row.Scan(&sum.Currency, &sum.Amount); err != nil {
			return CurrencySum{}, err
		}
		return sum, nil
	})
	if err != nil {
		return nil, fmt.Errorf("ledger: collect trial balance native totals: %w", err)
	}
	return sums, nil
}

func trialBalanceOffendingEntries(ctx context.Context, tx db.Tx, asOf time.Time, limit int) ([]OffendingEntry, error) {
	rows, err := tx.Query(ctx, `
WITH entry_totals AS (
	SELECT
		e.id,
		e.date,
		e.description,
		e.source_module,
		e.source_ref,
		count(p.id)::integer AS posting_count,
		COALESCE(sum(p.amount_gbp), 0)::bigint AS gbp_total
	FROM ledger.journal_entries e
	JOIN ledger.postings p ON p.entry_id = e.id
	WHERE e.date <= $1
	GROUP BY e.id, e.date, e.description, e.source_module, e.source_ref
	HAVING count(p.id) < 2
		OR COALESCE(sum(p.amount_gbp), 0) <> 0
		OR EXISTS (
			SELECT 1
			FROM ledger.postings native
			WHERE native.entry_id = e.id
			GROUP BY native.currency
			HAVING sum(native.amount) <> 0
		)
)
SELECT id, date, description, source_module, source_ref, posting_count, gbp_total
FROM entry_totals
ORDER BY date, id
LIMIT $2`, asOf, limit)
	if err != nil {
		return nil, fmt.Errorf("ledger: trial balance offending entries: %w", err)
	}
	defer rows.Close()

	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (OffendingEntry, error) {
		var entry OffendingEntry
		if err := row.Scan(
			&entry.ID,
			&entry.Date,
			&entry.Description,
			&entry.SourceModule,
			&entry.SourceRef,
			&entry.PostingCount,
			&entry.GBPTotal,
		); err != nil {
			return OffendingEntry{}, err
		}
		return entry, nil
	})
	if err != nil {
		return nil, fmt.Errorf("ledger: collect trial balance offending entries: %w", err)
	}

	for i := range entries {
		nativeSums, err := trialBalanceEntryNativeSums(ctx, tx, entries[i].ID)
		if err != nil {
			return nil, err
		}
		entries[i].NativeSums = nativeSums
	}
	return entries, nil
}

func trialBalanceEntryNativeSums(ctx context.Context, tx db.Tx, id EntryID) ([]CurrencySum, error) {
	rows, err := tx.Query(ctx, `
SELECT currency, sum(amount)::bigint
FROM ledger.postings
WHERE entry_id = $1
GROUP BY currency
HAVING sum(amount) <> 0
ORDER BY currency`, int64(id))
	if err != nil {
		return nil, fmt.Errorf("ledger: trial balance entry %d native totals: %w", id, err)
	}
	defer rows.Close()

	sums, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (CurrencySum, error) {
		var sum CurrencySum
		if err := row.Scan(&sum.Currency, &sum.Amount); err != nil {
			return CurrencySum{}, err
		}
		return sum, nil
	})
	if err != nil {
		return nil, fmt.Errorf("ledger: collect trial balance entry %d native totals: %w", id, err)
	}
	return sums, nil
}

func currencySumsBalanced(sums []CurrencySum) bool {
	for _, sum := range sums {
		if sum.Amount != 0 {
			return false
		}
	}
	return true
}
