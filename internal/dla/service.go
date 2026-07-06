package dla

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/npmulder/ledgerly/internal/platform/clock"

	ledgerapi "github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

const defaultDrawingDescription = "Director drawing"

// Service orchestrates DLA entry filing and read-side queries.
type Service struct {
	pool   *pgxpool.Pool
	ledger ledgerapi.Ledger
	bus    *bus.Bus
	store  Store
	clock  clock.Clock
}

// New creates a DLA service. If ledgerService is nil or omitted, a ledger
// service backed by pool is used for transaction-scoped postings.
func New(pool *pgxpool.Pool, ledgerService ...ledgerapi.Ledger) *Service {
	return newService(pool, nil, nil, ledgerService...)
}

// NewWithBus creates a DLA service that publishes DLA transition events through
// eventBus.
func NewWithBus(pool *pgxpool.Pool, eventBus *bus.Bus, ledgerService ...ledgerapi.Ledger) *Service {
	return newService(pool, eventBus, nil, ledgerService...)
}

// NewWithBusAndClock creates a DLA service with explicit event and time
// dependencies for app wiring and deterministic tests.
func NewWithBusAndClock(
	pool *pgxpool.Pool,
	eventBus *bus.Bus,
	clk clock.Clock,
	ledgerService ...ledgerapi.Ledger,
) *Service {
	return newService(pool, eventBus, clk, ledgerService...)
}

func newService(pool *pgxpool.Pool, eventBus *bus.Bus, clk clock.Clock, ledgerService ...ledgerapi.Ledger) *Service {
	var l ledgerapi.Ledger
	if len(ledgerService) > 0 {
		l = ledgerService[0]
	}
	if l == nil {
		l = ledgerapi.New(pool, eventBus)
	}
	if clk == nil {
		clk = clock.New()
	}
	return &Service{pool: pool, ledger: l, bus: eventBus, clock: clk}
}

// FileDrawing appends a banking-origin drawing and posts Dr DLA / Cr Cash
// inside the caller's transaction.
func (s *Service) FileDrawing(ctx context.Context, tx db.Tx, src TxnRef) error {
	if tx == nil {
		return fmt.Errorf("dla: file drawing requires transaction: %w", ErrInvalidEntry)
	}

	description := strings.TrimSpace(src.Description)
	if description == "" {
		description = defaultDrawingDescription
	}
	return s.appendEntry(ctx, tx, NewEntry{
		Date:            src.Date,
		Kind:            EntryKindDrawing,
		Description:     description,
		Amount:          src.Amount,
		Source:          src.Ref,
		CashAccountCode: src.CashAccountCode,
	}, true)
}

// RecordExternalCredit appends a presentation-ledger credit created by another
// module that already posted the authoritative ledger entry in tx.
func (s *Service) RecordExternalCredit(
	ctx context.Context,
	tx db.Tx,
	ref string,
	date time.Time,
	amount money.Money,
	description string,
) error {
	if tx == nil {
		return fmt.Errorf("dla: record external credit requires transaction: %w", ErrInvalidEntry)
	}
	normalized, err := normalizeExternalCredit(ref, date, amount, description)
	if err != nil {
		return err
	}
	return s.appendPresentationEntry(ctx, tx, normalized)
}

// AddEntry appends a manual repayment or expense-owed entry in its own transaction.
func (s *Service) AddEntry(ctx context.Context, e NewEntry) (err error) {
	if s.pool == nil {
		return fmt.Errorf("dla: add entry requires pool")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("dla: begin add entry transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(context.Background())
		}
	}()

	if err = s.appendEntry(ctx, tx, e, false); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("dla: commit add entry transaction: %w", err)
	}
	return nil
}

// Ledger returns DLA entries with derived running balance and split columns.
func (s *Service) Ledger(ctx context.Context, filter LedgerFilter) ([]Entry, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("dla: ledger requires pool")
	}
	normalized, err := normalizeLedgerFilter(filter)
	if err != nil {
		return nil, err
	}
	return s.store.Entries(ctx, s.pool, normalized)
}

// CurrentBalance returns the current DLA balance and advisor-facing status.
func (s *Service) CurrentBalance(ctx context.Context) (money.Money, Status, error) {
	if s.pool == nil {
		return money.Money{}, "", fmt.Errorf("dla: current balance requires pool")
	}
	asOf, err := s.currentDate()
	if err != nil {
		return money.Money{}, "", err
	}
	balance, err := s.store.CurrentBalanceAsOf(ctx, s.pool, asOf)
	if err != nil {
		return money.Money{}, "", err
	}
	return balance, statusForBalance(balance), nil
}

// CurrentStatus returns the current DLA fact payload for advisor/UI consumers.
func (s *Service) CurrentStatus(ctx context.Context) (StatusPayload, error) {
	balance, status, err := s.CurrentBalance(ctx)
	if err != nil {
		return StatusPayload{}, err
	}
	return StatusPayload{
		Balance:                  balance,
		Status:                   status,
		Policy:                   policyPayloadFromJurisdiction(),
		SuggestedClearanceAmount: clearanceAmountForBalance(balance),
	}, nil
}

// SuggestedClearanceAmount returns the positive DR amount needed to return the
// DLA to zero; in-credit balances return GBP zero.
func (s *Service) SuggestedClearanceAmount(ctx context.Context) (money.Money, error) {
	balance, _, err := s.CurrentBalance(ctx)
	if err != nil {
		return money.Money{}, err
	}
	return clearanceAmountForBalance(balance), nil
}

func (s *Service) appendEntry(ctx context.Context, tx db.Tx, entry NewEntry, allowDrawing bool) error {
	if s.ledger == nil {
		return fmt.Errorf("dla: append entry requires ledger")
	}
	normalized, err := normalizeNewEntry(entry, allowDrawing)
	if err != nil {
		return err
	}
	if err := lockBalanceMutation(ctx, tx); err != nil {
		return err
	}
	currentDate, err := s.currentDate()
	if err != nil {
		return err
	}
	preBalance, err := s.store.CurrentBalanceAsOf(ctx, tx, currentDate)
	if err != nil {
		return err
	}
	publishTransition := !normalized.Date.After(currentDate)
	postBalance := preBalance
	if publishTransition {
		postBalance, err = balanceAfterEntry(preBalance, normalized)
		if err != nil {
			return err
		}
	}
	if _, err := s.store.InsertEntry(ctx, tx, normalized); err != nil {
		return err
	}
	if _, err := s.ledger.Post(ctx, tx, journalEntryFor(normalized)); err != nil {
		return err
	}
	if !publishTransition {
		return nil
	}
	if err := s.publishTransition(ctx, tx, preBalance, postBalance); err != nil {
		return err
	}
	return nil
}

func (s *Service) appendPresentationEntry(ctx context.Context, tx db.Tx, normalized NewEntry) error {
	if err := lockBalanceMutation(ctx, tx); err != nil {
		return err
	}
	currentDate, err := s.currentDate()
	if err != nil {
		return err
	}
	preBalance, err := s.store.CurrentBalanceAsOf(ctx, tx, currentDate)
	if err != nil {
		return err
	}
	publishTransition := !normalized.Date.After(currentDate)
	postBalance := preBalance
	if publishTransition {
		postBalance, err = balanceAfterEntry(preBalance, normalized)
		if err != nil {
			return err
		}
	}
	if _, err := s.store.InsertEntry(ctx, tx, normalized); err != nil {
		return err
	}
	if !publishTransition {
		return nil
	}
	if err := s.publishTransition(ctx, tx, preBalance, postBalance); err != nil {
		return err
	}
	return nil
}

func (s *Service) currentDate() (time.Time, error) {
	clk := s.clock
	if clk == nil {
		clk = clock.New()
	}
	return normalizeDate(clk.Now())
}

func lockBalanceMutation(ctx context.Context, tx db.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, int32(0x444c4102), int32(1)); err != nil {
		return fmt.Errorf("dla: lock balance mutation: %w", err)
	}
	return nil
}

func (s *Service) publishTransition(ctx context.Context, tx db.Tx, preBalance, postBalance money.Money) error {
	if s.bus == nil {
		return nil
	}

	preStatus := statusForBalance(preBalance)
	postStatus := statusForBalance(postBalance)
	switch {
	case preStatus == StatusCredit && postStatus == StatusOverdrawn:
		if err := s.bus.Publish(ctx, tx, WentOverdrawn{Balance: postBalance}); err != nil {
			return fmt.Errorf("dla: publish went overdrawn: %w", err)
		}
	case preStatus == StatusOverdrawn && postStatus == StatusCredit:
		if err := s.bus.Publish(ctx, tx, BackInCredit{Balance: postBalance}); err != nil {
			return fmt.Errorf("dla: publish back in credit: %w", err)
		}
	}
	return nil
}

func balanceAfterEntry(balance money.Money, entry NewEntry) (money.Money, error) {
	delta := entry.Amount
	if entry.Kind == EntryKindDrawing {
		var err error
		delta, err = entry.Amount.Negate()
		if err != nil {
			return money.Money{}, fmt.Errorf("dla: drawing balance delta: %w", err)
		}
	}
	next, err := balance.Add(delta)
	if err != nil {
		return money.Money{}, fmt.Errorf("dla: apply balance delta: %w", err)
	}
	return next, nil
}

func journalEntryFor(entry NewEntry) ledgerapi.NewJournalEntry {
	negativeAmount := money.Money{Amount: -entry.Amount.Amount, Currency: entry.Amount.Currency}
	postingAmount := entry.Amount

	journal := ledgerapi.NewJournalEntry{
		Date:         entry.Date,
		Description:  entry.Description,
		SourceModule: ModuleName,
		SourceRef:    entry.Source,
	}

	switch entry.Kind {
	case EntryKindDrawing:
		journal.Postings = []ledgerapi.NewPosting{
			{AccountCode: DLAAccountCode, Amount: postingAmount, AmountGBP: postingAmount},
			{AccountCode: entry.CashAccountCode, Amount: negativeAmount, AmountGBP: negativeAmount},
		}
	case EntryKindRepayment:
		journal.Postings = []ledgerapi.NewPosting{
			{AccountCode: entry.CashAccountCode, Amount: postingAmount, AmountGBP: postingAmount},
			{AccountCode: DLAAccountCode, Amount: negativeAmount, AmountGBP: negativeAmount},
		}
	case EntryKindExpenseOwed:
		journal.Postings = []ledgerapi.NewPosting{
			{AccountCode: entry.ExpenseAccountCode, Amount: postingAmount, AmountGBP: postingAmount},
			{AccountCode: DLAAccountCode, Amount: negativeAmount, AmountGBP: negativeAmount},
		}
	}
	return journal
}

func normalizeNewEntry(entry NewEntry, allowDrawing bool) (NewEntry, error) {
	date, err := normalizeDate(entry.Date)
	if err != nil {
		return NewEntry{}, err
	}
	kind, err := normalizeEntryKind(entry.Kind)
	if err != nil {
		return NewEntry{}, err
	}
	if kind == EntryKindDrawing && !allowDrawing {
		return NewEntry{}, fmt.Errorf("dla: AddEntry does not accept drawing entries: %w", ErrInvalidEntry)
	}

	description := strings.TrimSpace(entry.Description)
	if description == "" {
		return NewEntry{}, fmt.Errorf("dla: description is required: %w", ErrInvalidEntry)
	}
	source := strings.TrimSpace(entry.Source)
	if source == "" {
		return NewEntry{}, fmt.Errorf("dla: source is required: %w", ErrInvalidEntry)
	}
	amount, err := normalizeAmount(entry.Amount)
	if err != nil {
		return NewEntry{}, err
	}

	normalized := NewEntry{
		Date:        date,
		Kind:        kind,
		Description: description,
		Amount:      amount,
		Source:      source,
	}
	switch kind {
	case EntryKindDrawing, EntryKindRepayment:
		code := normalizeAccountCode(entry.CashAccountCode)
		if code == "" {
			return NewEntry{}, fmt.Errorf("dla: cash account code is required: %w", ErrInvalidEntry)
		}
		normalized.CashAccountCode = code
	case EntryKindExpenseOwed:
		code := normalizeAccountCode(entry.ExpenseAccountCode)
		if code == "" {
			return NewEntry{}, fmt.Errorf("dla: expense account code is required: %w", ErrInvalidEntry)
		}
		normalized.ExpenseAccountCode = code
	}

	return normalized, nil
}

func normalizeExternalCredit(ref string, date time.Time, amount money.Money, description string) (NewEntry, error) {
	normalizedDate, err := normalizeDate(date)
	if err != nil {
		return NewEntry{}, err
	}
	normalizedAmount, err := normalizeAmount(amount)
	if err != nil {
		return NewEntry{}, err
	}
	source := strings.TrimSpace(ref)
	if source == "" {
		return NewEntry{}, fmt.Errorf("dla: source is required: %w", ErrInvalidEntry)
	}
	normalizedDescription := strings.TrimSpace(description)
	if normalizedDescription == "" {
		return NewEntry{}, fmt.Errorf("dla: description is required: %w", ErrInvalidEntry)
	}
	return NewEntry{
		Date:        normalizedDate,
		Kind:        EntryKindExpenseOwed,
		Description: normalizedDescription,
		Amount:      normalizedAmount,
		Source:      source,
	}, nil
}

func normalizeLedgerFilter(filter LedgerFilter) (LedgerFilter, error) {
	normalized := LedgerFilter{Limit: filter.Limit}
	if filter.From != nil {
		from, err := normalizeDate(*filter.From)
		if err != nil {
			return LedgerFilter{}, err
		}
		normalized.From = &from
	}
	if filter.To != nil {
		to, err := normalizeDate(*filter.To)
		if err != nil {
			return LedgerFilter{}, err
		}
		normalized.To = &to
	}
	if normalized.From != nil && normalized.To != nil && normalized.From.After(*normalized.To) {
		return LedgerFilter{}, fmt.Errorf("dla: from date %s is after to date %s: %w",
			normalized.From.Format(time.DateOnly),
			normalized.To.Format(time.DateOnly),
			ErrInvalidLedgerFilter,
		)
	}
	if filter.After != nil {
		afterDate, err := normalizeDate(filter.After.Date)
		if err != nil {
			return LedgerFilter{}, err
		}
		if filter.After.ID <= 0 {
			return LedgerFilter{}, fmt.Errorf("dla: ledger cursor id %d: %w", filter.After.ID, ErrInvalidLedgerFilter)
		}
		normalized.After = &EntryCursor{Date: afterDate, ID: filter.After.ID}
	}
	if normalized.Limit == 0 {
		normalized.Limit = DefaultLedgerLimit
	}
	if normalized.Limit < 0 {
		return LedgerFilter{}, fmt.Errorf("dla: ledger limit %d: %w", normalized.Limit, ErrInvalidLedgerFilter)
	}
	if normalized.Limit > MaxLedgerLimit {
		normalized.Limit = MaxLedgerLimit
	}
	return normalized, nil
}

func normalizeEntryKind(kind EntryKind) (EntryKind, error) {
	switch EntryKind(strings.TrimSpace(string(kind))) {
	case EntryKindDrawing:
		return EntryKindDrawing, nil
	case EntryKindRepayment:
		return EntryKindRepayment, nil
	case EntryKindExpenseOwed:
		return EntryKindExpenseOwed, nil
	default:
		return "", fmt.Errorf("dla: entry kind %q is invalid: %w", kind, ErrInvalidEntry)
	}
}

func normalizeDate(date time.Time) (time.Time, error) {
	if date.IsZero() {
		return time.Time{}, fmt.Errorf("dla: date is required: %w", ErrInvalidEntry)
	}
	year, month, day := date.Date()
	if year < 1900 || year > 9999 {
		return time.Time{}, fmt.Errorf("dla: date %04d-%02d-%02d is outside supported range: %w", year, month, day, ErrInvalidEntry)
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
}

func normalizeAmount(amount money.Money) (money.Money, error) {
	currency := strings.ToUpper(strings.TrimSpace(amount.Currency))
	if currency != "GBP" {
		return money.Money{}, fmt.Errorf("dla: amount currency %q must be GBP: %w", amount.Currency, ErrInvalidEntry)
	}
	if amount.Amount <= 0 {
		return money.Money{}, fmt.Errorf("dla: amount must be positive: %w", ErrInvalidEntry)
	}
	return money.Money{Amount: amount.Amount, Currency: "GBP"}, nil
}

func normalizeAccountCode(code ledgerapi.AccountCode) ledgerapi.AccountCode {
	return ledgerapi.AccountCode(strings.ToLower(strings.TrimSpace(string(code))))
}
