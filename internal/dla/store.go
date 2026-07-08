package dla

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

// Store owns DLA persistence. SQL qualifies dla objects so callers can share
// transactions from other module pools.
type Store struct{}

// InsertEntry appends one immutable DLA entry. It never updates existing rows.
func (Store) InsertEntry(ctx context.Context, tx db.Tx, entry NewEntry) (EntryID, error) {
	var id int64
	if err := tx.QueryRow(ctx, `
INSERT INTO dla.dla_entries (director, date, kind, description, amount, currency, source)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id`,
		entry.Director,
		entry.Date,
		string(entry.Kind),
		entry.Description,
		entry.Amount.Amount,
		entry.Amount.Currency,
		entry.Source,
	).Scan(&id); err != nil {
		if isDuplicateSource(err) {
			return 0, &DuplicateSourceError{Source: entry.Source}
		}
		return 0, fmt.Errorf("dla: insert entry: %w", err)
	}
	return EntryID(id), nil
}

// Entries loads DLA presentation-ledger rows ordered by date then id. Running
// balance is derived with a window function rather than stored.
func (Store) Entries(ctx context.Context, tx db.Tx, filter LedgerFilter) ([]Entry, error) {
	query, args := buildEntriesQuery(filter)
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("dla: list entries: %w", err)
	}
	defer rows.Close()

	entries := []Entry{}
	for rows.Next() {
		var (
			entry          Entry
			kind           string
			director       string
			amount         int64
			currency       string
			owedToYou      int64
			drawn          int64
			runningBalance int64
		)
		if err := rows.Scan(
			&entry.ID,
			&director,
			&entry.Date,
			&kind,
			&entry.Description,
			&amount,
			&currency,
			&entry.Source,
			&entry.CreatedAt,
			&owedToYou,
			&drawn,
			&runningBalance,
		); err != nil {
			return nil, fmt.Errorf("dla: scan entry: %w", err)
		}
		entry.Director = DirectorID(director)
		entry.Kind = EntryKind(kind)
		entry.Amount = money.Money{Amount: amount, Currency: currency}
		entry.OwedToYou = money.Money{Amount: owedToYou, Currency: currency}
		entry.Drawn = money.Money{Amount: drawn, Currency: currency}
		entry.RunningBalance = money.Money{Amount: runningBalance, Currency: currency}
		entry.BalanceSide = balanceSide(runningBalance)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dla: collect entries: %w", err)
	}
	return entries, nil
}

// CurrentBalance returns the signed DLA balance using DLA convention: positive
// is credit, negative is overdrawn.
func (Store) CurrentBalance(ctx context.Context, tx db.Tx, director DirectorID) (money.Money, error) {
	normalized, _, err := normalizeDirectorID(director)
	if err != nil {
		return money.Money{}, err
	}
	return balanceQuery(ctx, tx, "WHERE director = $1", normalized)
}

// CurrentBalanceAsOf returns the signed DLA balance for entries dated on or
// before asOf.
func (Store) CurrentBalanceAsOf(ctx context.Context, tx db.Tx, director DirectorID, asOf time.Time) (money.Money, error) {
	normalized, _, err := normalizeDirectorID(director)
	if err != nil {
		return money.Money{}, err
	}
	return balanceQuery(ctx, tx, "WHERE director = $1 AND date <= $2", normalized, asOf)
}

func buildEntriesQuery(filter LedgerFilter) (string, []any) {
	args := []any{}
	addArg := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}

	innerWhere := []string{"true"}
	if filter.Director != "" {
		innerWhere = append(innerWhere, "director = "+addArg(filter.Director))
	}
	if filter.To != nil {
		innerWhere = append(innerWhere, "date <= "+addArg(*filter.To))
	}

	outerWhere := []string{"true"}
	if filter.From != nil {
		outerWhere = append(outerWhere, "date >= "+addArg(*filter.From))
	}
	if filter.After != nil {
		afterDate := addArg(filter.After.Date)
		afterID := addArg(int64(filter.After.ID))
		outerWhere = append(outerWhere, "(date, id) > ("+afterDate+", "+afterID+")")
	}
	limit := addArg(filter.Limit)

	query := `
WITH ordered AS (
	SELECT id,
		director,
		date,
		kind::text AS kind,
		description,
		amount,
		currency,
		source,
		created_at,
		sum(
			CASE kind
				WHEN 'drawing' THEN -amount
				ELSE amount
			END
		) OVER (
			ORDER BY date, id
			ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
		)::bigint AS running_balance
	FROM dla.dla_entries
	WHERE ` + strings.Join(innerWhere, "\n\t\tAND ") + `
)
SELECT id,
	director,
	date,
	kind,
	description,
	amount,
	currency,
	source,
	created_at,
	CASE WHEN kind = 'drawing' THEN 0 ELSE amount END AS owed_to_you,
	CASE WHEN kind = 'drawing' THEN amount ELSE 0 END AS drawn,
	running_balance
FROM ordered
WHERE ` + strings.Join(outerWhere, "\n\tAND ") + `
ORDER BY date, id
LIMIT ` + limit
	return query, args
}

func balanceQuery(ctx context.Context, tx db.Tx, where string, args ...any) (money.Money, error) {
	query := `
SELECT COALESCE(sum(
	CASE kind
		WHEN 'drawing' THEN -amount
		ELSE amount
	END
), 0)::bigint
FROM dla.dla_entries
` + where

	var amount int64
	if err := tx.QueryRow(ctx, query, args...).Scan(&amount); err != nil {
		return money.Money{}, fmt.Errorf("dla: current balance: %w", err)
	}
	return money.Money{Amount: amount, Currency: "GBP"}, nil
}

func balanceSide(amount int64) BalanceSide {
	switch {
	case amount > 0:
		return BalanceSideCredit
	case amount < 0:
		return BalanceSideDebit
	default:
		return BalanceSideZero
	}
}

func isDuplicateSource(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "dla_entries_source_key"
}
