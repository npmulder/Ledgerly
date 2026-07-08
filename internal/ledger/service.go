package ledger

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

// Service orchestrates ledger account commands and queries.
type Service struct {
	pool  *pgxpool.Pool
	bus   *bus.Bus
	store Store
}

// New creates a ledger service.
func New(pool *pgxpool.Pool, eventBus ...*bus.Bus) *Service {
	b := bus.New()
	if len(eventBus) > 0 && eventBus[0] != nil {
		b = eventBus[0]
	}
	return &Service{pool: pool, bus: b}
}

// Post validates and appends entry inside tx, then publishes EntryPosted in that same tx.
func (s *Service) Post(ctx context.Context, tx db.Tx, entry NewJournalEntry) (EntryID, error) {
	return s.post(ctx, tx, entry, nil)
}

// Reverse appends a reversing entry for id inside tx.
func (s *Service) Reverse(ctx context.Context, tx db.Tx, id EntryID, reason string) (EntryID, error) {
	if tx == nil {
		return 0, fmt.Errorf("ledger: reverse requires transaction: %w", ErrInvalidJournalEntry)
	}
	if id <= 0 {
		return 0, fmt.Errorf("ledger: reverse entry id %d: %w", id, ErrInvalidReversal)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return 0, fmt.Errorf("ledger: reversal reason is required: %w", ErrInvalidReversal)
	}

	original, err := s.store.JournalEntry(ctx, tx, id)
	if err != nil {
		return 0, err
	}
	if original.ReversalOf != nil {
		return 0, fmt.Errorf("ledger: entry %d reverses %d: %w", id, *original.ReversalOf, ErrReversalOfReversal)
	}
	reversed, err := s.store.HasReversal(ctx, tx, id)
	if err != nil {
		return 0, err
	}
	if reversed {
		return 0, fmt.Errorf("ledger: entry %d already has a reversal: %w", id, ErrEntryAlreadyReversed)
	}

	postings := make([]NewPosting, len(original.Postings))
	for i, posting := range original.Postings {
		amount, err := posting.Amount.Negate()
		if err != nil {
			return 0, fmt.Errorf("ledger: reverse posting %d native amount: %w", i, ErrInvalidMoney)
		}
		amountGBP, err := posting.AmountGBP.Negate()
		if err != nil {
			return 0, fmt.Errorf("ledger: reverse posting %d GBP amount: %w", i, ErrInvalidMoney)
		}
		postings[i] = NewPosting{
			AccountCode: posting.AccountCode,
			Amount:      amount,
			AmountGBP:   amountGBP,
		}
	}

	reversal := NewJournalEntry{
		Date:         original.Date,
		Description:  fmt.Sprintf("Reversal of %d: %s", id, reason),
		SourceModule: original.SourceModule,
		SourceRef:    original.SourceRef,
		Postings:     postings,
	}
	return s.post(ctx, tx, reversal, &id)
}

// EntryBySource loads the latest original entry for a source module/ref inside tx.
func (s *Service) EntryBySource(ctx context.Context, tx db.Tx, sourceModule string, sourceRef string) (JournalEntry, error) {
	if tx == nil {
		return JournalEntry{}, fmt.Errorf("ledger: source lookup requires transaction: %w", ErrInvalidEntryFilter)
	}
	sourceModule, sourceRef, err := normalizeSourceLookup(sourceModule, sourceRef)
	if err != nil {
		return JournalEntry{}, err
	}
	return s.store.JournalEntryBySource(ctx, tx, sourceModule, sourceRef)
}

// ReadSnapshot opens a read-only repeatable-read snapshot for multi-query
// derived reads, then closes it when fn returns.
func (s *Service) ReadSnapshot(ctx context.Context, fn ReadSnapshotFunc) error {
	if s.pool == nil {
		return fmt.Errorf("ledger: read snapshot requires pool")
	}
	if fn == nil {
		return fmt.Errorf("ledger: read snapshot callback is required")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return fmt.Errorf("ledger: begin read snapshot: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	if err := fn(ctx, readSnapshot{service: s, tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("ledger: commit read snapshot: %w", err)
	}
	return nil
}

type readSnapshot struct {
	service *Service
	tx      db.Tx
}

func (s readSnapshot) AccountBalance(ctx context.Context, code AccountCode, asOf time.Time) (AccountBalance, error) {
	return s.service.AccountBalanceInTx(ctx, s.tx, code, asOf)
}

func (s readSnapshot) BalancesByType(ctx context.Context, from time.Time, to time.Time) ([]AccountBalance, error) {
	return s.service.BalancesByTypeInTx(ctx, s.tx, from, to)
}

func (s readSnapshot) Entries(ctx context.Context, filter EntryFilter) ([]JournalEntry, error) {
	return s.service.EntriesInTx(ctx, s.tx, filter)
}

func (s readSnapshot) Accounts(ctx context.Context) ([]Account, error) {
	return s.service.AccountsInTx(ctx, s.tx)
}

// AccountBalance returns the account's native balances grouped by currency and
// its frozen presentational GBP balance from postings dated on or before asOf.
func (s *Service) AccountBalance(ctx context.Context, code AccountCode, asOf time.Time) (AccountBalance, error) {
	if s.pool == nil {
		return AccountBalance{}, fmt.Errorf("ledger: account balance requires pool")
	}
	return s.AccountBalanceInTx(ctx, s.pool, code, asOf)
}

// AccountBalanceInTx returns AccountBalance using the caller's transaction or
// snapshot.
func (s *Service) AccountBalanceInTx(ctx context.Context, tx db.Tx, code AccountCode, asOf time.Time) (AccountBalance, error) {
	if tx == nil {
		return AccountBalance{}, fmt.Errorf("ledger: account balance requires transaction")
	}
	normalizedCode, err := normalizeAccountCode(code)
	if err != nil {
		return AccountBalance{}, err
	}
	normalizedAsOf, err := normalizeEntryDate(asOf)
	if err != nil {
		return AccountBalance{}, err
	}
	return s.store.AccountBalance(ctx, tx, normalizedCode, normalizedAsOf)
}

// BalancesByType returns one aggregate row per account type. Income and expense
// balances use P&L semantics, summing postings dated from through to inclusive.
// Asset, liability, and equity balances use balance-sheet semantics, summing all
// postings dated on or before to. GBP totals are sums of stored posting
// amount_gbp values; the ledger never retranslates historical native amounts.
func (s *Service) BalancesByType(ctx context.Context, from time.Time, to time.Time) ([]AccountBalance, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("ledger: balances by type requires pool")
	}
	return s.BalancesByTypeInTx(ctx, s.pool, from, to)
}

// BalancesByTypeInTx returns aggregate balances using the caller's transaction
// or snapshot.
func (s *Service) BalancesByTypeInTx(ctx context.Context, tx db.Tx, from time.Time, to time.Time) ([]AccountBalance, error) {
	if tx == nil {
		return nil, fmt.Errorf("ledger: balances by type requires transaction")
	}
	normalizedFrom, err := normalizeEntryDate(from)
	if err != nil {
		return nil, err
	}
	normalizedTo, err := normalizeEntryDate(to)
	if err != nil {
		return nil, err
	}
	if normalizedFrom.After(normalizedTo) {
		return nil, fmt.Errorf("ledger: from date %s is after to date %s: %w",
			normalizedFrom.Format(time.DateOnly),
			normalizedTo.Format(time.DateOnly),
			ErrInvalidEntryFilter,
		)
	}
	return s.store.BalancesByType(ctx, tx, normalizedFrom, normalizedTo)
}

// Entries returns journal entries with postings for browse/export. Filters use
// inclusive date bounds, stable ordering by date then id, and keyset pagination
// through EntryFilter.After.
func (s *Service) Entries(ctx context.Context, filter EntryFilter) ([]JournalEntry, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("ledger: entries requires pool")
	}
	return s.EntriesInTx(ctx, s.pool, filter)
}

// EntriesInTx returns journal entries using the caller's transaction or
// snapshot.
func (s *Service) EntriesInTx(ctx context.Context, tx db.Tx, filter EntryFilter) ([]JournalEntry, error) {
	if tx == nil {
		return nil, fmt.Errorf("ledger: entries requires transaction")
	}
	normalized, err := normalizeEntryFilter(filter)
	if err != nil {
		return nil, err
	}
	return s.store.Entries(ctx, tx, normalized)
}

// EnsureAccount creates spec.Code or returns the existing account code when it is consistent.
func (s *Service) EnsureAccount(ctx context.Context, tx db.Tx, spec AccountSpec) (AccountCode, error) {
	if tx == nil {
		return "", fmt.Errorf("ledger: ensure account requires transaction: %w", ErrInvalidAccountSpec)
	}
	return s.store.EnsureAccount(ctx, tx, spec)
}

// CreateExpenseAccount creates a user-managed expense account with a unique code.
func (s *Service) CreateExpenseAccount(ctx context.Context, spec AccountSpec) (_ Account, err error) {
	if s.pool == nil {
		return Account{}, fmt.Errorf("ledger: create expense account requires pool")
	}
	spec.Type = AccountTypeExpense
	spec.Currency = nil
	normalized, err := normalizeAccountSpec(spec)
	if err != nil {
		return Account{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Account{}, fmt.Errorf("ledger: begin expense account transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(context.Background())
		}
	}()

	account, err := s.store.CreateAccount(ctx, tx, normalized)
	if err != nil {
		return Account{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Account{}, fmt.Errorf("ledger: commit expense account transaction: %w", err)
	}
	return account, nil
}

// Accounts lists the chart of accounts ordered by code.
func (s *Service) Accounts(ctx context.Context) ([]Account, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("ledger: list accounts requires pool")
	}
	return s.AccountsInTx(ctx, s.pool)
}

// AccountsInTx lists chart accounts using the caller's transaction or snapshot.
func (s *Service) AccountsInTx(ctx context.Context, tx db.Tx) ([]Account, error) {
	if tx == nil {
		return nil, fmt.Errorf("ledger: list accounts requires transaction")
	}
	return s.store.ListAccounts(ctx, tx)
}

func (s *Service) post(ctx context.Context, tx db.Tx, entry NewJournalEntry, reversalOf *EntryID) (EntryID, error) {
	if tx == nil {
		return 0, fmt.Errorf("ledger: post requires transaction: %w", ErrInvalidJournalEntry)
	}

	validated, err := validateNewJournalEntry(entry)
	if err != nil {
		return 0, err
	}
	accountCurrencies, err := s.store.PostingAccountCurrencies(ctx, tx, postingAccountCodes(validated.Postings))
	if err != nil {
		return 0, err
	}
	if err := validatePostingAccountCurrencies(validated.Postings, accountCurrencies); err != nil {
		return 0, err
	}

	id, err := s.store.InsertJournalEntry(ctx, tx, validated, reversalOf)
	if err != nil {
		return 0, err
	}
	if err := s.store.CheckEntryInvariant(ctx, tx, id); err != nil {
		return 0, err
	}
	if err := s.publishEntryPosted(ctx, tx, id, validated); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Service) publishEntryPosted(ctx context.Context, tx db.Tx, id EntryID, entry NewJournalEntry) error {
	if s.bus == nil {
		return nil
	}
	if err := s.bus.Publish(ctx, tx, EntryPosted{
		EntryID:      id,
		SourceModule: entry.SourceModule,
		Accounts:     uniquePostingAccounts(entry.Postings),
		Date:         entry.Date,
	}); err != nil {
		return fmt.Errorf("ledger: publish entry posted: %w", err)
	}
	return nil
}

func validateNewJournalEntry(entry NewJournalEntry) (NewJournalEntry, error) {
	date, err := normalizeEntryDate(entry.Date)
	if err != nil {
		return NewJournalEntry{}, err
	}
	description := strings.TrimSpace(entry.Description)
	if description == "" {
		return NewJournalEntry{}, fmt.Errorf("ledger: description is required: %w", ErrInvalidJournalEntry)
	}
	sourceModule := strings.TrimSpace(entry.SourceModule)
	if sourceModule == "" {
		return NewJournalEntry{}, fmt.Errorf("ledger: source module is required: %w", ErrInvalidJournalEntry)
	}
	sourceRef := strings.TrimSpace(entry.SourceRef)
	if sourceRef == "" {
		return NewJournalEntry{}, fmt.Errorf("ledger: source ref is required: %w", ErrInvalidJournalEntry)
	}
	if len(entry.Postings) < 2 {
		return NewJournalEntry{}, fmt.Errorf("ledger: got %d posting(s): %w", len(entry.Postings), ErrInsufficientPostings)
	}

	validated := NewJournalEntry{
		Date:         date,
		Description:  description,
		SourceModule: sourceModule,
		SourceRef:    sourceRef,
		Postings:     make([]NewPosting, len(entry.Postings)),
	}
	nativeTotals := make(map[string]int64)
	var gbpTotal int64
	for i, posting := range entry.Postings {
		normalized, err := validatePosting(i, posting)
		if err != nil {
			return NewJournalEntry{}, err
		}
		validated.Postings[i] = normalized

		nativeTotal, err := addMinorUnits(nativeTotals[normalized.Amount.Currency], normalized.Amount.Amount)
		if err != nil {
			return NewJournalEntry{}, fmt.Errorf("ledger: posting %d native total overflow: %w", i, ErrInvalidMoney)
		}
		nativeTotals[normalized.Amount.Currency] = nativeTotal
		gbpTotal, err = addMinorUnits(gbpTotal, normalized.AmountGBP.Amount)
		if err != nil {
			return NewJournalEntry{}, fmt.Errorf("ledger: posting %d GBP total overflow: %w", i, ErrInvalidMoney)
		}
	}
	if gbpTotal != 0 {
		return NewJournalEntry{}, fmt.Errorf("ledger: GBP postings sum to %d: %w", gbpTotal, ErrUnbalancedGBP)
	}
	for currency, total := range nativeTotals {
		if total != 0 {
			return NewJournalEntry{}, fmt.Errorf("ledger: %s native postings sum to %d: %w", currency, total, ErrUnbalancedCurrency)
		}
	}
	return validated, nil
}

func normalizeSourceLookup(sourceModule string, sourceRef string) (string, string, error) {
	module := strings.TrimSpace(sourceModule)
	if module == "" {
		return "", "", fmt.Errorf("ledger: source module is required: %w", ErrInvalidEntryFilter)
	}
	ref := strings.TrimSpace(sourceRef)
	if ref == "" {
		return "", "", fmt.Errorf("ledger: source ref is required: %w", ErrInvalidEntryFilter)
	}
	return module, ref, nil
}

func validatePosting(index int, posting NewPosting) (NewPosting, error) {
	code, err := normalizeAccountCode(posting.AccountCode)
	if err != nil {
		return NewPosting{}, fmt.Errorf("ledger: posting %d account code is required: %w", index, ErrInvalidJournalEntry)
	}

	nativeCurrency, err := normalizeCurrencyCode(posting.Amount.Currency)
	if err != nil {
		return NewPosting{}, fmt.Errorf("ledger: posting %d native currency: %w", index, ErrInvalidMoney)
	}
	gbpCurrency, err := normalizeCurrencyCode(posting.AmountGBP.Currency)
	if err != nil {
		return NewPosting{}, fmt.Errorf("ledger: posting %d GBP currency: %w", index, ErrInvalidMoney)
	}
	if gbpCurrency != "GBP" {
		return NewPosting{}, fmt.Errorf("ledger: posting %d GBP currency is %s: %w", index, gbpCurrency, ErrInvalidMoney)
	}
	if posting.Amount.IsZero() || posting.AmountGBP.IsZero() {
		return NewPosting{}, fmt.Errorf("ledger: posting %d has zero amount: %w", index, ErrZeroPosting)
	}
	if postingSignsDisagree(posting.Amount.Amount, posting.AmountGBP.Amount) {
		return NewPosting{}, fmt.Errorf("ledger: posting %d native and GBP signs disagree: %w", index, ErrPostingSignMismatch)
	}

	return NewPosting{
		AccountCode: code,
		Amount: money.Money{
			Amount:   posting.Amount.Amount,
			Currency: nativeCurrency,
		},
		AmountGBP: money.Money{
			Amount:   posting.AmountGBP.Amount,
			Currency: "GBP",
		},
	}, nil
}

func postingSignsDisagree(nativeAmount, gbpAmount int64) bool {
	return (nativeAmount < 0) != (gbpAmount < 0)
}

func validatePostingAccountCurrencies(postings []NewPosting, accountCurrencies map[AccountCode]*string) error {
	for i, posting := range postings {
		accountCurrency, ok := accountCurrencies[posting.AccountCode]
		if !ok {
			return &AccountNotFoundError{Code: posting.AccountCode}
		}
		if accountCurrency != nil && *accountCurrency != posting.Amount.Currency {
			return fmt.Errorf("ledger: posting %d account currency mismatch: %w", i, &AccountCurrencyMismatchError{
				Code:      posting.AccountCode,
				Expected:  *accountCurrency,
				Requested: posting.Amount.Currency,
			})
		}
	}
	return nil
}

func normalizeEntryDate(date time.Time) (time.Time, error) {
	if date.IsZero() {
		return time.Time{}, fmt.Errorf("ledger: date is required: %w", ErrInvalidEntryDate)
	}
	year, month, day := date.Date()
	if year < 1900 || year > 9999 {
		return time.Time{}, fmt.Errorf("ledger: date %04d-%02d-%02d is outside supported range: %w", year, month, day, ErrInvalidEntryDate)
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
}

func normalizeAccountCode(code AccountCode) (AccountCode, error) {
	normalized := AccountCode(strings.ToLower(strings.TrimSpace(string(code))))
	if normalized == "" {
		return "", ErrInvalidAccountSpec
	}
	return normalized, nil
}

func normalizeCurrencyCode(currency string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(currency))
	if len(normalized) != 3 {
		return "", ErrInvalidMoney
	}
	for _, char := range normalized {
		if !unicode.IsUpper(char) || !unicode.IsLetter(char) {
			return "", ErrInvalidMoney
		}
	}
	return normalized, nil
}

func addMinorUnits(total, amount int64) (int64, error) {
	if amount > 0 && total > int64(^uint64(0)>>1)-amount {
		return 0, ErrInvalidMoney
	}
	minInt64 := -int64(^uint64(0)>>1) - 1
	if amount < 0 && total < minInt64-amount {
		return 0, ErrInvalidMoney
	}
	return total + amount, nil
}

func normalizeEntryFilter(filter EntryFilter) (EntryFilter, error) {
	normalized := EntryFilter{
		SourceModule: strings.TrimSpace(filter.SourceModule),
		Limit:        filter.Limit,
	}

	var err error
	if filter.From != nil {
		from, err := normalizeEntryDate(*filter.From)
		if err != nil {
			return EntryFilter{}, err
		}
		normalized.From = &from
	}
	if filter.To != nil {
		to, err := normalizeEntryDate(*filter.To)
		if err != nil {
			return EntryFilter{}, err
		}
		normalized.To = &to
	}
	if normalized.From != nil && normalized.To != nil && normalized.From.After(*normalized.To) {
		return EntryFilter{}, fmt.Errorf("ledger: from date %s is after to date %s: %w",
			normalized.From.Format(time.DateOnly),
			normalized.To.Format(time.DateOnly),
			ErrInvalidEntryFilter,
		)
	}
	if filter.AccountCode != "" {
		normalized.AccountCode, err = normalizeAccountCode(filter.AccountCode)
		if err != nil {
			return EntryFilter{}, fmt.Errorf("ledger: filter account code: %w", ErrInvalidEntryFilter)
		}
	}
	if filter.After != nil {
		afterDate, err := normalizeEntryDate(filter.After.Date)
		if err != nil {
			return EntryFilter{}, err
		}
		if filter.After.ID <= 0 {
			return EntryFilter{}, fmt.Errorf("ledger: entry cursor id %d: %w", filter.After.ID, ErrInvalidEntryFilter)
		}
		normalized.After = &EntryCursor{
			Date: afterDate,
			ID:   filter.After.ID,
		}
	}
	if normalized.Limit == 0 {
		normalized.Limit = DefaultEntriesLimit
	}
	if normalized.Limit < 0 {
		return EntryFilter{}, fmt.Errorf("ledger: entries limit %d: %w", normalized.Limit, ErrInvalidEntryFilter)
	}
	if normalized.Limit > MaxEntriesLimit {
		normalized.Limit = MaxEntriesLimit
	}
	return normalized, nil
}

func postingAccountCodes(postings []NewPosting) []AccountCode {
	codes := make([]AccountCode, len(postings))
	for i, posting := range postings {
		codes[i] = posting.AccountCode
	}
	return codes
}

func uniquePostingAccounts(postings []NewPosting) []AccountCode {
	seen := make(map[AccountCode]struct{}, len(postings))
	accounts := make([]AccountCode, 0, len(postings))
	for _, posting := range postings {
		if _, ok := seen[posting.AccountCode]; ok {
			continue
		}
		seen[posting.AccountCode] = struct{}{}
		accounts = append(accounts, posting.AccountCode)
	}
	return accounts
}
