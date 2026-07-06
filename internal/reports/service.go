package reports

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/clock"
)

const (
	gbpCurrency             = "GBP"
	otherIncomeLabel        = "Other income"
	realisedFXAccount       = ledger.AccountCode("4900-fx-gain-loss")
	realisedFXLabel         = "Realised FX gains"
	invoiceSourcePrefix     = "invoice:"
	invoiceSendSourceSuffix = ":send"
)

var (
	ErrInvalidPeriod   = errors.New("reports: invalid period")
	ErrInvalidTaxYear  = errors.New("reports: invalid tax year")
	ErrMissingProvider = errors.New("reports: missing provider")
)

// Service composes existing module read APIs into derived report read models.
type Service struct {
	ledger    Ledger
	identity  Identity
	invoicing Invoicing
	clock     clock.Clock
}

var _ Reports = (*Service)(nil)

// Option customizes a reports service.
type Option func(*Service)

// WithClock injects the time source used for deadline status calculations.
func WithClock(clk clock.Clock) Option {
	return func(s *Service) {
		s.clock = clk
	}
}

// New returns the reports read API. Reports v1 is derived reads only; it owns no
// persistence or cache.
func New(ledgerAPI Ledger, identityAPI Identity, invoicingAPI Invoicing, opts ...Option) *Service {
	service := &Service{
		ledger:    ledgerAPI,
		identity:  identityAPI,
		invoicing: invoicingAPI,
		clock:     clock.New(),
	}
	for _, opt := range opts {
		opt(service)
	}
	if service.clock == nil {
		service.clock = clock.New()
	}
	return service
}

// NewService returns the identity-backed reports service used by filing
// calendar callers.
func NewService(identityAPI CompanyFactsProvider, opts ...Option) (*Service, error) {
	if identityAPI == nil {
		return nil, fmt.Errorf("reports: identity facts provider is required")
	}
	return New(nil, identityAPI, nil, opts...), nil
}

// ProfitAndLoss returns the inclusive-period P&L using frozen ledger GBP
// postings. It never retranslates native amounts.
func (s *Service) ProfitAndLoss(ctx context.Context, period Period) (PL, error) {
	if s.ledger == nil {
		return PL{}, fmt.Errorf("ledger: %w", ErrMissingProvider)
	}
	if s.invoicing == nil {
		return PL{}, fmt.Errorf("invoicing: %w", ErrMissingProvider)
	}
	normalized, err := normalizePeriod(period)
	if err != nil {
		return PL{}, err
	}
	return s.profitAndLoss(ctx, normalized)
}

// ProfitYTD returns net profit for the company financial year identified by
// taxYear, using the identity year end rather than the calendar year.
func (s *Service) ProfitYTD(ctx context.Context, taxYear string) (money.Money, error) {
	if s.identity == nil {
		return money.Money{}, fmt.Errorf("identity: %w", ErrMissingProvider)
	}
	facts, err := s.identity.CompanyFacts(ctx)
	if err != nil {
		return money.Money{}, err
	}
	period, err := financialYearPeriod(taxYear, facts.YearEnd.Month, facts.YearEnd.Day)
	if err != nil {
		return money.Money{}, err
	}
	pl, err := s.ProfitAndLoss(ctx, period)
	if err != nil {
		return money.Money{}, err
	}
	return pl.NetProfit, nil
}

func (s *Service) profitAndLoss(ctx context.Context, period Period) (PL, error) {
	accounts, err := s.ledger.Accounts(ctx)
	if err != nil {
		return PL{}, err
	}
	accountByCode := accountsByCode(accounts)

	balances, err := s.ledger.BalancesByType(ctx, period.From, period.To)
	if err != nil {
		return PL{}, err
	}
	incomeTotal, err := incomeTotalFromBalances(balances)
	if err != nil {
		return PL{}, err
	}
	expenseTotal := balanceForType(balances, ledger.AccountTypeExpense)

	breakdown, err := s.entryBreakdown(ctx, period, accountByCode)
	if err != nil {
		return PL{}, err
	}
	if err := verifyIncomeBreakdown(incomeTotal, breakdown); err != nil {
		return PL{}, err
	}
	if err := verifyExpenseBreakdown(expenseTotal, breakdown.ExpenseTotal); err != nil {
		return PL{}, err
	}

	profitBeforeTax, err := incomeTotal.Sub(expenseTotal)
	if err != nil {
		return PL{}, err
	}
	taxYear, err := jurisdiction.TaxYearForDate(period.To)
	if err != nil {
		return PL{}, err
	}
	rate, err := jurisdiction.CorporateRate(taxYear)
	if err != nil {
		return PL{}, err
	}
	taxAmount, err := corporateTaxAmount(profitBeforeTax, rate)
	if err != nil {
		return PL{}, err
	}
	netProfit, err := profitBeforeTax.Sub(taxAmount)
	if err != nil {
		return PL{}, err
	}

	return PL{
		Period:          period,
		TaxYear:         taxYear,
		Income:          breakdown.Income,
		IncomeTotal:     incomeTotal,
		RealisedFXGains: LineItem{Label: realisedFXLabel, Amount: breakdown.RealisedFXGains},
		Expenses:        breakdown.Expenses,
		ExpenseTotal:    expenseTotal,
		ProfitBeforeTax: profitBeforeTax,
		CorporateTax: TaxLine{
			Label:   "IoM income tax at " + formatRatePercent(rate),
			TaxYear: taxYear,
			Rate:    rate,
			Amount:  taxAmount,
		},
		NetProfit: netProfit,
	}, nil
}

type entryBreakdown struct {
	Income          []IncomeLine
	IncomeTotal     money.Money
	RealisedFXGains money.Money
	Expenses        []ExpenseLine
	ExpenseTotal    money.Money
}

type incomeKey struct {
	clientID string
	currency string
	label    string
}

func (s *Service) entryBreakdown(ctx context.Context, period Period, accounts map[ledger.AccountCode]ledger.Account) (entryBreakdown, error) {
	income := map[incomeKey]IncomeLine{}
	expenses := map[ledger.AccountCode]ExpenseLine{}
	invoiceIncome := map[string]money.Money{}
	invoiceCache := map[string]invoiceAttribution{}

	breakdown := entryBreakdown{
		IncomeTotal:     money.Zero(gbpCurrency),
		RealisedFXGains: money.Zero(gbpCurrency),
		ExpenseTotal:    money.Zero(gbpCurrency),
	}
	var cursor *ledger.EntryCursor
	for {
		entries, err := s.ledger.Entries(ctx, ledger.EntryFilter{
			From:  &period.From,
			To:    &period.To,
			After: cursor,
			Limit: ledger.MaxEntriesLimit,
		})
		if err != nil {
			return entryBreakdown{}, err
		}
		for _, entry := range entries {
			if err := s.addEntryToBreakdown(entry, accounts, income, expenses, invoiceIncome, &breakdown); err != nil {
				return entryBreakdown{}, err
			}
		}
		if len(entries) < ledger.MaxEntriesLimit {
			break
		}
		last := entries[len(entries)-1]
		cursor = &ledger.EntryCursor{Date: last.Date, ID: last.ID}
	}

	if err := s.addInvoiceIncomeLines(ctx, invoiceIncome, income, invoiceCache, &breakdown); err != nil {
		return entryBreakdown{}, err
	}
	breakdown.Income = sortedIncomeLines(income)
	breakdown.Expenses = sortedExpenseLines(expenses)
	return breakdown, nil
}

func (s *Service) addEntryToBreakdown(
	entry ledger.JournalEntry,
	accounts map[ledger.AccountCode]ledger.Account,
	income map[incomeKey]IncomeLine,
	expenses map[ledger.AccountCode]ExpenseLine,
	invoiceIncome map[string]money.Money,
	breakdown *entryBreakdown,
) error {
	for _, posting := range entry.Postings {
		account, ok := accounts[posting.AccountCode]
		if !ok {
			return fmt.Errorf("reports: ledger account %q missing from account list", posting.AccountCode)
		}
		switch account.Type {
		case ledger.AccountTypeIncome:
			amount, err := pnlCreditAmount(posting.AmountGBP)
			if err != nil {
				return err
			}
			if posting.AccountCode == realisedFXAccount {
				next, err := breakdown.RealisedFXGains.Add(amount)
				if err != nil {
					return err
				}
				breakdown.RealisedFXGains = next
				continue
			}
			if entry.SourceModule == invoicing.ModuleName {
				if err := addInvoiceIncomeTotal(entry.SourceRef, amount, invoiceIncome); err != nil {
					return err
				}
				continue
			}
			if err := addOtherIncomeLine(amount, income, breakdown); err != nil {
				return err
			}
		case ledger.AccountTypeExpense:
			amount := money.Money{Amount: posting.AmountGBP.Amount, Currency: gbpCurrency}
			line := expenses[posting.AccountCode]
			if line.AccountCode == "" {
				line = ExpenseLine{
					AccountCode: posting.AccountCode,
					AccountName: account.Name,
					Amount:      money.Zero(gbpCurrency),
				}
			}
			next, err := line.Amount.Add(amount)
			if err != nil {
				return err
			}
			line.Amount = next
			expenses[posting.AccountCode] = line
			breakdown.ExpenseTotal, err = breakdown.ExpenseTotal.Add(amount)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func addInvoiceIncomeTotal(sourceRef string, amount money.Money, invoiceIncome map[string]money.Money) error {
	total, ok := invoiceIncome[sourceRef]
	if !ok {
		total = money.Zero(gbpCurrency)
	}
	next, err := total.Add(amount)
	if err != nil {
		return err
	}
	invoiceIncome[sourceRef] = next
	return nil
}

func (s *Service) addInvoiceIncomeLines(
	ctx context.Context,
	invoiceIncome map[string]money.Money,
	income map[incomeKey]IncomeLine,
	invoiceCache map[string]invoiceAttribution,
	breakdown *entryBreakdown,
) error {
	sourceRefs := make([]string, 0, len(invoiceIncome))
	for sourceRef := range invoiceIncome {
		sourceRefs = append(sourceRefs, sourceRef)
	}
	sort.Strings(sourceRefs)

	for _, sourceRef := range sourceRefs {
		amount := invoiceIncome[sourceRef]
		if amount.IsZero() {
			continue
		}
		attribution, err := s.invoiceAttribution(ctx, sourceRef, invoiceCache)
		if err != nil {
			return err
		}
		if err := addAttributedIncomeLine(attribution, amount, income, breakdown); err != nil {
			return err
		}
	}
	return nil
}

func addOtherIncomeLine(amount money.Money, income map[incomeKey]IncomeLine, breakdown *entryBreakdown) error {
	key := incomeKey{label: otherIncomeLabel}
	line := income[key]
	if line.Label == "" {
		line = IncomeLine{
			Label:  otherIncomeLabel,
			Amount: money.Zero(gbpCurrency),
		}
	}
	return addIncomeAmount(key, line, amount, income, breakdown)
}

func addAttributedIncomeLine(
	attribution invoiceAttribution,
	amount money.Money,
	income map[incomeKey]IncomeLine,
	breakdown *entryBreakdown,
) error {
	key := incomeKey{
		clientID: attribution.ClientID,
		currency: attribution.Currency,
		label:    incomeLabel(attribution.ClientName, attribution.Currency),
	}
	line := income[key]
	if line.Label == "" {
		line = IncomeLine{
			Label:      key.label,
			ClientID:   attribution.ClientID,
			ClientName: attribution.ClientName,
			Currency:   attribution.Currency,
			Amount:     money.Zero(gbpCurrency),
		}
	}
	return addIncomeAmount(key, line, amount, income, breakdown)
}

func addIncomeAmount(
	key incomeKey,
	line IncomeLine,
	amount money.Money,
	income map[incomeKey]IncomeLine,
	breakdown *entryBreakdown,
) error {
	next, err := line.Amount.Add(amount)
	if err != nil {
		return err
	}
	line.Amount = next
	income[key] = line
	breakdown.IncomeTotal, err = breakdown.IncomeTotal.Add(amount)
	return err
}

type invoiceAttribution struct {
	ClientID   string
	ClientName string
	Currency   string
}

func (s *Service) invoiceAttribution(ctx context.Context, sourceRef string, cache map[string]invoiceAttribution) (invoiceAttribution, error) {
	if cached, ok := cache[sourceRef]; ok {
		return cached, nil
	}
	ref, ok := invoiceRefFromSourceRef(sourceRef)
	if !ok {
		return invoiceAttribution{}, fmt.Errorf("reports: invoicing source ref %q is not an invoice send ref", sourceRef)
	}
	invoice, err := s.invoicing.InvoiceByNumber(ctx, ref)
	if errors.Is(err, invoicing.ErrInvoiceNotFound) {
		invoice, err = s.invoicing.Invoice(ctx, ref)
	}
	if err != nil {
		return invoiceAttribution{}, err
	}
	client, err := s.invoicing.Client(ctx, invoice.ClientID)
	if err != nil {
		return invoiceAttribution{}, err
	}
	attribution := invoiceAttribution{
		ClientID:   client.ID,
		ClientName: client.Name,
		Currency:   string(invoice.Currency),
	}
	cache[sourceRef] = attribution
	return attribution, nil
}

func invoiceRefFromSourceRef(sourceRef string) (string, bool) {
	ref := strings.TrimSpace(sourceRef)
	if !strings.HasPrefix(ref, invoiceSourcePrefix) || !strings.HasSuffix(ref, invoiceSendSourceSuffix) {
		return "", false
	}
	ref = strings.TrimSuffix(strings.TrimPrefix(ref, invoiceSourcePrefix), invoiceSendSourceSuffix)
	ref = strings.TrimSpace(ref)
	return ref, ref != ""
}

func accountsByCode(accounts []ledger.Account) map[ledger.AccountCode]ledger.Account {
	byCode := make(map[ledger.AccountCode]ledger.Account, len(accounts))
	for _, account := range accounts {
		byCode[account.Code] = account
	}
	return byCode
}

func incomeTotalFromBalances(balances []ledger.AccountBalance) (money.Money, error) {
	return pnlCreditAmount(balanceForType(balances, ledger.AccountTypeIncome))
}

func balanceForType(balances []ledger.AccountBalance, accountType ledger.AccountType) money.Money {
	for _, balance := range balances {
		if balance.AccountType == accountType {
			return money.Money{Amount: balance.AmountGBP.Amount, Currency: gbpCurrency}
		}
	}
	return money.Zero(gbpCurrency)
}

func pnlCreditAmount(amount money.Money) (money.Money, error) {
	return money.Money{Amount: amount.Amount, Currency: gbpCurrency}.Negate()
}

func verifyIncomeBreakdown(incomeTotal money.Money, breakdown entryBreakdown) error {
	linesAndFX, err := breakdown.IncomeTotal.Add(breakdown.RealisedFXGains)
	if err != nil {
		return err
	}
	if linesAndFX.Amount != incomeTotal.Amount || linesAndFX.Currency != incomeTotal.Currency {
		return fmt.Errorf("reports: income breakdown %v plus FX %v does not match ledger income total %v",
			breakdown.IncomeTotal,
			breakdown.RealisedFXGains,
			incomeTotal,
		)
	}
	return nil
}

func verifyExpenseBreakdown(expenseTotal money.Money, breakdownTotal money.Money) error {
	if breakdownTotal.Amount != expenseTotal.Amount || breakdownTotal.Currency != expenseTotal.Currency {
		return fmt.Errorf("reports: expense breakdown %v does not match ledger expense total %v",
			breakdownTotal,
			expenseTotal,
		)
	}
	return nil
}

func corporateTaxAmount(profit money.Money, rate jurisdiction.Rate) (money.Money, error) {
	if profit.Currency != gbpCurrency {
		return money.Money{}, fmt.Errorf("reports: profit currency %q, want GBP", profit.Currency)
	}
	if profit.Amount <= 0 {
		return money.Zero(gbpCurrency), nil
	}
	rat, ok := new(big.Rat).SetString(strings.TrimSpace(string(rate)))
	if !ok {
		return money.Money{}, fmt.Errorf("reports: parse corporate rate %q", rate)
	}
	tax := profit.MulRat(rat)
	tax.Currency = gbpCurrency
	return tax, nil
}

func financialYearPeriod(taxYear string, month time.Month, day int) (Period, error) {
	startYear, endYear, err := parseTaxYear(taxYear)
	if err != nil {
		return Period{}, err
	}
	previousEnd, err := financialYearEndDate(startYear, month, day)
	if err != nil {
		return Period{}, err
	}
	end, err := financialYearEndDate(endYear, month, day)
	if err != nil {
		return Period{}, err
	}
	start := previousEnd.AddDate(0, 0, 1)
	return Period{From: start, To: end}, nil
}

func financialYearEndDate(year int, month time.Month, day int) (time.Time, error) {
	if month < time.January || month > time.December || day < 1 {
		return time.Time{}, fmt.Errorf("reports: invalid year end %d-%02d: %w", month, day, ErrInvalidPeriod)
	}
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > lastDay {
		if month != time.February || day != 29 {
			return time.Time{}, fmt.Errorf("reports: invalid year end %d-%02d: %w", month, day, ErrInvalidPeriod)
		}
		day = lastDay
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
}

func parseTaxYear(taxYear string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(taxYear), "-")
	if len(parts) != 2 || len(parts[0]) != 4 || len(parts[1]) != 2 {
		return 0, 0, fmt.Errorf("reports: tax year %q must look like 2025-26: %w", taxYear, ErrInvalidTaxYear)
	}
	startYear, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("reports: tax year %q start: %w", taxYear, ErrInvalidTaxYear)
	}
	endSuffix, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("reports: tax year %q end: %w", taxYear, ErrInvalidTaxYear)
	}
	endYear := startYear/100*100 + endSuffix
	if endYear <= startYear {
		endYear += 100
	}
	if endYear != startYear+1 {
		return 0, 0, fmt.Errorf("reports: tax year %q must span one year: %w", taxYear, ErrInvalidTaxYear)
	}
	return startYear, endYear, nil
}

func normalizePeriod(period Period) (Period, error) {
	from := dateOnly(period.From)
	to := dateOnly(period.To)
	if from.IsZero() || to.IsZero() {
		return Period{}, fmt.Errorf("reports: period from and to are required: %w", ErrInvalidPeriod)
	}
	if from.After(to) {
		return Period{}, fmt.Errorf("reports: period from %s is after to %s: %w",
			from.Format(time.DateOnly),
			to.Format(time.DateOnly),
			ErrInvalidPeriod,
		)
	}
	return Period{From: from, To: to}, nil
}

func dateOnly(date time.Time) time.Time {
	if date.IsZero() {
		return time.Time{}
	}
	year, month, day := date.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func incomeLabel(clientName string, currency string) string {
	return "Consulting income - " + strings.TrimSpace(clientName) + " (" + strings.TrimSpace(currency) + ")"
}

func sortedIncomeLines(lines map[incomeKey]IncomeLine) []IncomeLine {
	out := make([]IncomeLine, 0, len(lines))
	for _, line := range lines {
		out = append(out, line)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ClientName != out[j].ClientName {
			return out[i].ClientName < out[j].ClientName
		}
		if out[i].Currency != out[j].Currency {
			return out[i].Currency < out[j].Currency
		}
		return out[i].Label < out[j].Label
	})
	return out
}

func sortedExpenseLines(lines map[ledger.AccountCode]ExpenseLine) []ExpenseLine {
	out := make([]ExpenseLine, 0, len(lines))
	for _, line := range lines {
		out = append(out, line)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].AccountCode < out[j].AccountCode
	})
	return out
}

func formatRatePercent(rate jurisdiction.Rate) string {
	rat, ok := new(big.Rat).SetString(strings.TrimSpace(string(rate)))
	if !ok {
		return strings.TrimSpace(string(rate))
	}
	rat.Mul(rat, big.NewRat(100, 1))
	formatted := rat.FloatString(2)
	formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	if formatted == "" {
		formatted = "0"
	}
	return formatted + "%"
}
