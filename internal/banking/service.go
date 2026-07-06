package banking

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

type Service struct {
	pool    *pgxpool.Pool
	ledger  LedgerAccountEnsurer
	store   Store
	parsers map[Provider]StatementParser
}

type ServiceOption func(*Service)

func WithParser(provider Provider, parser StatementParser) ServiceOption {
	return func(s *Service) {
		if parser == nil {
			delete(s.parsers, provider)
			return
		}
		s.parsers[provider] = parser
	}
}

func NewService(pool *pgxpool.Pool, ledgerEnsurer LedgerAccountEnsurer, opts ...ServiceOption) *Service {
	service := &Service{
		pool:    pool,
		ledger:  ledgerEnsurer,
		store:   Store{},
		parsers: defaultParserSnapshot(),
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

func (s *Service) CreateAccount(ctx context.Context, input AccountInput) (_ BankAccount, err error) {
	if s.pool == nil {
		return BankAccount{}, fmt.Errorf("banking: account creation requires pool")
	}
	if s.ledger == nil {
		return BankAccount{}, fmt.Errorf("banking: account creation requires ledger")
	}
	normalized, err := normalizeAccountInput(input)
	if err != nil {
		return BankAccount{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BankAccount{}, fmt.Errorf("banking: begin account transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	existing, found, err := s.store.AccountByNaturalKey(ctx, tx, normalized)
	if err != nil {
		return BankAccount{}, err
	}
	if found {
		if err = tx.Commit(ctx); err != nil {
			return BankAccount{}, fmt.Errorf("banking: commit existing account lookup: %w", err)
		}
		return existing, nil
	}

	currency := normalized.Currency
	code := ledgerAccountCode(normalized)
	ensuredCode, err := s.ledger.EnsureAccount(ctx, tx, ledger.AccountSpec{
		Code:     code,
		Name:     ledgerAccountName(normalized),
		Type:     ledger.AccountTypeAsset,
		Currency: &currency,
	})
	if err != nil {
		return BankAccount{}, fmt.Errorf("banking: ensure ledger account: %w", err)
	}
	account, err := s.store.InsertAccount(ctx, tx, normalized, ensuredCode)
	if err != nil {
		return BankAccount{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return BankAccount{}, fmt.Errorf("banking: commit account transaction: %w", err)
	}
	return account, nil
}

func (s *Service) ImportCSV(ctx context.Context, accountID AccountID, file ImportFile) (_ BatchSummary, err error) {
	if s.pool == nil {
		return BatchSummary{}, fmt.Errorf("banking: import requires pool")
	}
	if file.Reader == nil {
		return BatchSummary{}, fmt.Errorf("banking: import reader is required: %w", ErrInvalidImport)
	}
	filename := strings.TrimSpace(file.Filename)
	if filename == "" {
		return BatchSummary{}, fmt.Errorf("banking: import filename is required: %w", ErrInvalidImport)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BatchSummary{}, fmt.Errorf("banking: begin import transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	account, err := s.store.Account(ctx, tx, accountID)
	if err != nil {
		return BatchSummary{}, err
	}
	parser, ok := s.parsers[account.Provider]
	if !ok || parser == nil {
		return BatchSummary{}, fmt.Errorf("banking: provider %q has no parser: %w", account.Provider, ErrUnsupportedProvider)
	}

	rawTxns, err := parser.Parse(file.Reader)
	if err != nil {
		return BatchSummary{}, err
	}
	validated := make([]newTransaction, len(rawTxns))
	for i, raw := range rawTxns {
		txn, err := validateRawTxn(account, raw, i+2)
		if err != nil {
			return BatchSummary{}, err
		}
		validated[i] = txn
	}

	batch, err := s.store.InsertImportBatch(ctx, tx, account.ID, filename, len(validated))
	if err != nil {
		return BatchSummary{}, err
	}
	summary := batch
	for _, txn := range validated {
		txn.ImportBatchID = batch.BatchID
		inserted, err := s.store.InsertTransaction(ctx, tx, txn)
		if err != nil {
			return BatchSummary{}, err
		}
		if inserted {
			summary.NewRows++
		} else {
			summary.DuplicateRows++
		}
	}
	summary, err = s.store.UpdateImportBatchCounts(ctx, tx, summary)
	if err != nil {
		return BatchSummary{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return BatchSummary{}, fmt.Errorf("banking: commit import transaction: %w", err)
	}
	return summary, nil
}

func validateRawTxn(account BankAccount, raw RawTxn, row int) (newTransaction, error) {
	date, err := dateOnly(raw.Date)
	if err != nil {
		return newTransaction{}, fmt.Errorf("banking: row %d date: %w", row, err)
	}
	amount := normalizeMoney(raw.Amount)
	if amount.Currency != account.Currency {
		return newTransaction{}, &CurrencyMismatchError{
			AccountID: account.ID,
			Expected:  account.Currency,
			Actual:    amount.Currency,
			Row:       row,
		}
	}
	payee := strings.TrimSpace(raw.Payee)
	if payee == "" {
		return newTransaction{}, &ParseRowError{Row: row, Err: fmt.Errorf("payee is required: %w", ErrInvalidImport)}
	}
	reference := strings.TrimSpace(raw.Reference)
	dedupeReference := normalizeReference(reference)
	return newTransaction{
		AccountID:    account.ID,
		Date:         date,
		Amount:       amount,
		Payee:        payee,
		Reference:    reference,
		ProviderMeta: raw.ProviderMeta,
		DedupeHash:   dedupeHash(account.ID, date, amount, dedupeReference),
	}, nil
}

func normalizeAccountInput(input AccountInput) (AccountInput, error) {
	normalized := AccountInput{
		Name:     strings.TrimSpace(input.Name),
		Provider: Provider(strings.ToLower(strings.TrimSpace(string(input.Provider)))),
		Currency: strings.ToUpper(strings.TrimSpace(input.Currency)),
	}
	if normalized.Name == "" {
		return AccountInput{}, fmt.Errorf("banking: account name is required: %w", ErrInvalidAccount)
	}
	if normalized.Provider != ProviderRevolut {
		return AccountInput{}, fmt.Errorf("banking: provider %q: %w", input.Provider, ErrUnsupportedProvider)
	}
	switch normalized.Currency {
	case "GBP", "EUR":
	default:
		return AccountInput{}, fmt.Errorf("banking: currency %q: %w", input.Currency, ErrUnsupportedCurrency)
	}
	return normalized, nil
}

func ledgerAccountCode(account AccountInput) ledger.AccountCode {
	return ledger.AccountCode("1000-cash-" + string(account.Provider) + "-" + slug(account.Name) + "-" + strings.ToLower(account.Currency))
}

func ledgerAccountName(account AccountInput) string {
	return fmt.Sprintf("Cash - %s (%s)", account.Name, account.Currency)
}

func slug(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "account"
	}
	return result
}

func normalizeMoney(value money.Money) money.Money {
	return money.Money{
		Amount:   value.Amount,
		Currency: strings.ToUpper(strings.TrimSpace(value.Currency)),
	}
}

func dateOnly(value time.Time) (time.Time, error) {
	if value.IsZero() {
		return time.Time{}, fmt.Errorf("date is required: %w", ErrInvalidImport)
	}
	year, month, day := value.Date()
	if year < 1900 || year > 9999 {
		return time.Time{}, fmt.Errorf("date %04d-%02d-%02d is outside supported range: %w", year, month, day, ErrInvalidImport)
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
}

func normalizeReference(reference string) string {
	return strings.Join(strings.Fields(reference), " ")
}

func dedupeHash(accountID AccountID, date time.Time, amount money.Money, normalizedReference string) string {
	payload := fmt.Sprintf("%d|%s|%s|%d|%s",
		accountID,
		date.Format(time.DateOnly),
		amount.Currency,
		amount.Amount,
		normalizedReference,
	)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}
