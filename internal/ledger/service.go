package ledger

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

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

// EnsureAccount creates spec.Code or returns the existing account code when it is consistent.
func (s *Service) EnsureAccount(ctx context.Context, tx db.Tx, spec AccountSpec) (AccountCode, error) {
	if tx == nil {
		return "", fmt.Errorf("ledger: ensure account requires transaction: %w", ErrInvalidAccountSpec)
	}
	return s.store.EnsureAccount(ctx, tx, spec)
}

// Accounts lists the chart of accounts ordered by code.
func (s *Service) Accounts(ctx context.Context) ([]Account, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("ledger: list accounts requires pool")
	}
	return s.store.ListAccounts(ctx, s.pool)
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

func validatePosting(index int, posting NewPosting) (NewPosting, error) {
	code := AccountCode(strings.ToLower(strings.TrimSpace(string(posting.AccountCode))))
	if code == "" {
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
