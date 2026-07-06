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
	"github.com/npmulder/ledgerly/internal/platform/bus"
)

type Service struct {
	pool                       *pgxpool.Pool
	ledger                     LedgerAccountEnsurer
	ledgerJournal              LedgerJournal
	moneyFX                    MoneyFX
	invoices                   InvoiceSettler
	dla                        DLAFileDrawer
	eventBus                   *bus.Bus
	store                      Store
	parsers                    map[Provider]StatementParser
	invoiceCandidates          InvoiceCandidateSource
	directorNames              DirectorNameSource
	payeeRuleAutoPostThreshold int
	dlaPersonalPatterns        []string
	reconciliationHooks        ReconciliationCommandHooks
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

func WithInvoiceCandidates(source InvoiceCandidateSource) ServiceOption {
	return func(s *Service) {
		s.invoiceCandidates = source
	}
}

func WithDirectorNames(source DirectorNameSource) ServiceOption {
	return func(s *Service) {
		s.directorNames = source
	}
}

func WithPayeeRuleAutoPostThreshold(threshold int) ServiceOption {
	return func(s *Service) {
		s.payeeRuleAutoPostThreshold = threshold
	}
}

func WithDLAPersonalPatterns(patterns []string) ServiceOption {
	return func(s *Service) {
		s.dlaPersonalPatterns = append([]string{}, patterns...)
	}
}

func WithLedgerJournal(journal LedgerJournal) ServiceOption {
	return func(s *Service) {
		s.ledgerJournal = journal
	}
}

func WithMoneyFX(fx MoneyFX) ServiceOption {
	return func(s *Service) {
		s.moneyFX = fx
	}
}

func WithInvoicingSettler(settler InvoiceSettler) ServiceOption {
	return func(s *Service) {
		s.invoices = settler
	}
}

func WithDLAFileDrawer(drawer DLAFileDrawer) ServiceOption {
	return func(s *Service) {
		s.dla = drawer
	}
}

func WithEventBus(eventBus *bus.Bus) ServiceOption {
	return func(s *Service) {
		s.eventBus = eventBus
	}
}

func WithReconciliationCommandHooks(hooks ReconciliationCommandHooks) ServiceOption {
	return func(s *Service) {
		s.reconciliationHooks = hooks
	}
}

func NewService(pool *pgxpool.Pool, ledgerEnsurer LedgerAccountEnsurer, opts ...ServiceOption) *Service {
	ledgerJournal, _ := ledgerEnsurer.(LedgerJournal)
	service := &Service{
		pool:                       pool,
		ledger:                     ledgerEnsurer,
		ledgerJournal:              ledgerJournal,
		eventBus:                   bus.New(),
		store:                      Store{},
		parsers:                    defaultParserSnapshot(),
		invoiceCandidates:          defaultInvoiceCandidateSource(),
		payeeRuleAutoPostThreshold: DefaultPayeeRuleAutoPostThreshold,
		dlaPersonalPatterns:        defaultDLAPersonalPatterns(),
	}
	for _, opt := range opts {
		opt(service)
	}
	if service.payeeRuleAutoPostThreshold < 0 {
		service.payeeRuleAutoPostThreshold = DefaultPayeeRuleAutoPostThreshold
	}
	if len(service.dlaPersonalPatterns) == 0 {
		service.dlaPersonalPatterns = defaultDLAPersonalPatterns()
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
	var newTxnIDs []TransactionID
	for _, txn := range validated {
		txn.ImportBatchID = batch.BatchID
		txnID, inserted, err := s.store.InsertTransaction(ctx, tx, txn)
		if err != nil {
			return BatchSummary{}, err
		}
		if inserted {
			summary.NewRows++
			newTxnIDs = append(newTxnIDs, txnID)
		} else {
			summary.DuplicateRows++
		}
	}
	summary, err = s.store.UpdateImportBatchCounts(ctx, tx, summary)
	if err != nil {
		return BatchSummary{}, err
	}
	if len(newTxnIDs) > 0 {
		if _, err := s.runMatchEngineTx(ctx, tx, MatchEngineTriggerImportCompletion, newTxnIDs); err != nil {
			return BatchSummary{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return BatchSummary{}, fmt.Errorf("banking: commit import transaction: %w", err)
	}
	return summary, nil
}

func (s *Service) TransitionTransactionState(ctx context.Context, id TransactionID, to TransactionState, actor string) (_ TransactionStateChange, err error) {
	if s.pool == nil {
		return TransactionStateChange{}, fmt.Errorf("banking: state transition requires pool")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TransactionStateChange{}, fmt.Errorf("banking: begin state transition transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	change, err := s.store.TransitionTransactionState(ctx, tx, id, to, actor)
	if err != nil {
		return TransactionStateChange{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return TransactionStateChange{}, fmt.Errorf("banking: commit state transition transaction: %w", err)
	}
	return change, nil
}

func (s *Service) RecordSuggestion(ctx context.Context, input SuggestionInput) (_ Suggestion, err error) {
	if s.pool == nil {
		return Suggestion{}, fmt.Errorf("banking: suggestion storage requires pool")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Suggestion{}, fmt.Errorf("banking: begin suggestion transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	suggestion, err := s.store.InsertSuggestion(ctx, tx, input)
	if err != nil {
		return Suggestion{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Suggestion{}, fmt.Errorf("banking: commit suggestion transaction: %w", err)
	}
	return suggestion, nil
}

func (s *Service) SuggestionsForTransaction(ctx context.Context, txnID TransactionID) ([]Suggestion, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("banking: suggestion history requires pool")
	}
	if txnID <= 0 {
		return nil, fmt.Errorf("banking: suggestion transaction id is required: %w", ErrInvalidSuggestion)
	}
	return s.store.SuggestionsForTransaction(ctx, s.pool, txnID)
}

func (s *Service) CreatePayeeRule(ctx context.Context, input PayeeRuleInput) (PayeeRule, error) {
	if s.pool == nil {
		return PayeeRule{}, fmt.Errorf("banking: payee rule storage requires pool")
	}
	return s.store.InsertPayeeRule(ctx, s.pool, input)
}

func (s *Service) RecordPayeeRuleApplied(ctx context.Context, id PayeeRuleID) (PayeeRule, error) {
	if s.pool == nil {
		return PayeeRule{}, fmt.Errorf("banking: payee rule update requires pool")
	}
	if id <= 0 {
		return PayeeRule{}, fmt.Errorf("banking: payee rule id is required: %w", ErrInvalidPayeeRule)
	}
	return s.store.RecordPayeeRuleApplied(ctx, s.pool, id)
}

func (s *Service) LearnFromRecode(ctx context.Context, txnID TransactionID, accountCode ledger.AccountCode) (_ PayeeRule, err error) {
	if s.pool == nil {
		return PayeeRule{}, fmt.Errorf("banking: rule learning requires pool")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PayeeRule{}, fmt.Errorf("banking: begin rule learning transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	txn, err := s.store.Transaction(ctx, tx, txnID)
	if err != nil {
		return PayeeRule{}, err
	}
	rule, err := s.store.LearnPayeeRuleFromRecode(ctx, tx, txn, accountCode)
	if err != nil {
		return PayeeRule{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PayeeRule{}, fmt.Errorf("banking: commit rule learning transaction: %w", err)
	}
	return rule, nil
}

func (s *Service) MatchingPayeeRules(ctx context.Context, payee string) ([]PayeeRule, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("banking: payee rule matching requires pool")
	}
	return s.store.MatchingPayeeRules(ctx, s.pool, payee)
}

func (s *Service) Feed(ctx context.Context, filter FeedFilter) ([]Transaction, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("banking: feed requires pool")
	}
	normalized, err := normalizeFeedFilter(filter)
	if err != nil {
		return nil, err
	}
	return s.store.Feed(ctx, s.pool, normalized)
}

func (s *Service) ReviewQueue(ctx context.Context) (ReviewQueue, error) {
	if s.pool == nil {
		return ReviewQueue{}, fmt.Errorf("banking: review queue requires pool")
	}
	return s.store.ReviewQueue(ctx, s.pool)
}

func (s *Service) RecentlyReconciled(ctx context.Context, accountID AccountID, limit int) ([]ReconciledTransaction, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("banking: recently reconciled requires pool")
	}
	return s.store.RecentlyReconciled(ctx, s.pool, accountID, normalizeRecentlyReconciledLimit(limit))
}

func (s *Service) Accounts(ctx context.Context) ([]BankAccount, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("banking: accounts requires pool")
	}
	return s.store.ListAccounts(ctx, s.pool)
}

func (s *Service) UnreconciledCount(ctx context.Context, accountID AccountID) (int, error) {
	if s.pool == nil {
		return 0, fmt.Errorf("banking: unreconciled count requires pool")
	}
	if accountID <= 0 {
		return 0, fmt.Errorf("banking: account id is required: %w", ErrInvalidTransactionFilter)
	}
	return s.store.UnreconciledCount(ctx, s.pool, accountID)
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
	return ledger.AccountCode("1000-cash-" +
		string(account.Provider) + "-" +
		slug(account.Name) + "-" +
		strings.ToLower(account.Currency) + "-" +
		accountCodeHash(account))
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

func accountCodeHash(account AccountInput) string {
	sum := sha256.Sum256([]byte(string(account.Provider) + "\x00" + account.Name + "\x00" + account.Currency))
	return hex.EncodeToString(sum[:4])
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

func normalizeFeedFilter(filter FeedFilter) (FeedFilter, error) {
	normalized := filter
	if normalized.State != "" && !validTransactionState(normalized.State) {
		return FeedFilter{}, fmt.Errorf("banking: feed state %q: %w", normalized.State, ErrInvalidTransactionFilter)
	}
	if normalized.From != nil {
		from, dateErr := dateOnly(*normalized.From)
		if dateErr != nil {
			return FeedFilter{}, fmt.Errorf("banking: feed from date: %w", ErrInvalidTransactionFilter)
		}
		normalized.From = &from
	}
	if normalized.To != nil {
		to, dateErr := dateOnly(*normalized.To)
		if dateErr != nil {
			return FeedFilter{}, fmt.Errorf("banking: feed to date: %w", ErrInvalidTransactionFilter)
		}
		normalized.To = &to
	}
	if normalized.From != nil && normalized.To != nil && normalized.From.After(*normalized.To) {
		return FeedFilter{}, fmt.Errorf("banking: feed from date %s is after to date %s: %w",
			normalized.From.Format(time.DateOnly),
			normalized.To.Format(time.DateOnly),
			ErrInvalidTransactionFilter,
		)
	}
	if normalized.After != nil {
		afterDate, dateErr := dateOnly(normalized.After.Date)
		if dateErr != nil {
			return FeedFilter{}, fmt.Errorf("banking: feed cursor date: %w", ErrInvalidTransactionFilter)
		}
		if normalized.After.ID <= 0 {
			return FeedFilter{}, fmt.Errorf("banking: feed cursor id is required: %w", ErrInvalidTransactionFilter)
		}
		normalized.After = &FeedCursor{Date: afterDate, ID: normalized.After.ID}
	}
	if normalized.Limit <= 0 {
		normalized.Limit = DefaultFeedLimit
	}
	if normalized.Limit > MaxFeedLimit {
		normalized.Limit = MaxFeedLimit
	}
	return normalized, nil
}

func normalizeRecentlyReconciledLimit(limit int) int {
	if limit <= 0 {
		return DefaultRecentlyReconciledLimit
	}
	if limit > MaxRecentlyReconciledLimit {
		return MaxRecentlyReconciledLimit
	}
	return limit
}
