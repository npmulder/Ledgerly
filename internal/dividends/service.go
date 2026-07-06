package dividends

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	"github.com/npmulder/ledgerly/internal/reports"
)

const gbpCurrency = "GBP"

// Service composes ledger, reports, jurisdiction, identity, and declaration
// storage into the live dividend headroom read model.
type Service struct {
	pool     *pgxpool.Pool
	ledger   Ledger
	reports  reports.Reports
	identity Identity
	clock    clock.Clock
	store    Store
}

var _ Dividends = (*Service)(nil)

// Option customizes a dividends service.
type Option func(*Service)

// WithClock injects the time source used to resolve the current financial year.
func WithClock(clk clock.Clock) Option {
	return func(s *Service) {
		s.clock = clk
	}
}

// New returns the dividends read API.
func New(pool *pgxpool.Pool, ledgerAPI Ledger, reportsAPI reports.Reports, identityAPI Identity, opts ...Option) *Service {
	service := &Service{
		pool:     pool,
		ledger:   ledgerAPI,
		reports:  reportsAPI,
		identity: identityAPI,
		clock:    clock.New(),
	}
	for _, opt := range opts {
		opt(service)
	}
	if service.clock == nil {
		service.clock = clock.New()
	}
	return service
}

// Headroom returns the live distributable-reserves calculation. It stores no
// derived balance.
func (s *Service) Headroom(ctx context.Context) (HeadroomBreakdown, error) {
	if s.ledger == nil {
		return HeadroomBreakdown{}, fmt.Errorf("ledger: %w", ErrMissingProvider)
	}
	if s.reports == nil {
		return HeadroomBreakdown{}, fmt.Errorf("reports: %w", ErrMissingProvider)
	}
	if s.identity == nil {
		return HeadroomBreakdown{}, fmt.Errorf("identity: %w", ErrMissingProvider)
	}

	facts, err := s.identity.CompanyFacts(ctx)
	if err != nil {
		return HeadroomBreakdown{}, err
	}
	asOf, err := normalizeDate(s.now())
	if err != nil {
		return HeadroomBreakdown{}, err
	}
	financialYear, err := financialYearForDate(asOf, facts.YearEnd.Month, facts.YearEnd.Day)
	if err != nil {
		return HeadroomBreakdown{}, err
	}
	period, err := financialYearPeriod(financialYear, facts.YearEnd.Month, facts.YearEnd.Day)
	if err != nil {
		return HeadroomBreakdown{}, err
	}
	priorYearEnd := period.From.AddDate(0, 0, -1)

	retainedBalance, err := s.ledger.AccountBalance(ctx, RetainedEarningsAccountCode, priorYearEnd)
	if err != nil {
		return HeadroomBreakdown{}, err
	}
	retained, err := retainedEarningsAmount(retainedBalance.AmountGBP)
	if err != nil {
		return HeadroomBreakdown{}, err
	}

	ytdProfit, err := s.reports.ProfitYTD(ctx, financialYear)
	if err != nil {
		return HeadroomBreakdown{}, err
	}
	rate, err := jurisdiction.CorporateRate(financialYear)
	if err != nil {
		return HeadroomBreakdown{}, err
	}
	corporationTax, err := corporateTaxAmount(ytdProfit, rate)
	if err != nil {
		return HeadroomBreakdown{}, err
	}
	declared, err := s.declaredInYearWithFacts(ctx, financialYear, facts)
	if err != nil {
		return HeadroomBreakdown{}, err
	}

	available, err := retained.Add(ytdProfit)
	if err != nil {
		return HeadroomBreakdown{}, fmt.Errorf("dividends: add YTD profit: %w", err)
	}
	available, err = available.Sub(corporationTax)
	if err != nil {
		return HeadroomBreakdown{}, fmt.Errorf("dividends: subtract corporation tax: %w", err)
	}
	available, err = available.Sub(declared)
	if err != nil {
		return HeadroomBreakdown{}, fmt.Errorf("dividends: subtract declared dividends: %w", err)
	}

	corporationTaxLine, err := corporationTax.Negate()
	if err != nil {
		return HeadroomBreakdown{}, fmt.Errorf("dividends: corporation tax line: %w", err)
	}
	declaredLine, err := declared.Negate()
	if err != nil {
		return HeadroomBreakdown{}, fmt.Errorf("dividends: declared dividends line: %w", err)
	}

	return HeadroomBreakdown{
		AsOf:          asOf,
		FinancialYear: financialYear,
		Lines: []MoneyLine{
			{Label: retainedEarningsLineLabel, Amount: retained},
			{Label: profitYTDLineLabel, Amount: ytdProfit},
			{Label: corporationTaxLabel(formatRatePercent(rate)), Amount: corporationTaxLine},
			{Label: dividendsDeclaredLabel, Amount: declaredLine},
			{Label: availableHeadroomLabel, Amount: available},
		},
		Available:     available,
		Distributable: available.Amount >= 0,
	}, nil
}

// DeclaredInYear returns total declared dividends inside the company financial
// year identified by financialYear, using identity CompanyFacts boundaries.
func (s *Service) DeclaredInYear(ctx context.Context, financialYear string) (money.Money, error) {
	if s.identity == nil {
		return money.Money{}, fmt.Errorf("identity: %w", ErrMissingProvider)
	}
	facts, err := s.identity.CompanyFacts(ctx)
	if err != nil {
		return money.Money{}, err
	}
	return s.declaredInYearWithFacts(ctx, financialYear, facts)
}

// History returns declarations newest first.
func (s *Service) History(ctx context.Context) ([]Declaration, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("dividends: history requires pool")
	}
	return s.store.Declarations(ctx, s.pool)
}

func (s *Service) declaredInYearWithFacts(ctx context.Context, financialYear string, facts identity.CompanyFacts) (money.Money, error) {
	if s.pool == nil {
		return money.Money{}, fmt.Errorf("dividends: declared-in-year requires pool")
	}
	period, err := financialYearPeriod(financialYear, facts.YearEnd.Month, facts.YearEnd.Day)
	if err != nil {
		return money.Money{}, err
	}
	return s.store.DeclaredInPeriod(ctx, s.pool, period.From, period.To)
}

func (s *Service) now() time.Time {
	clk := s.clock
	if clk == nil {
		clk = clock.New()
	}
	return clk.Now()
}

func retainedEarningsAmount(balance money.Money) (money.Money, error) {
	if balance.Currency != gbpCurrency {
		return money.Money{}, fmt.Errorf("dividends: retained earnings currency %q, want GBP", balance.Currency)
	}
	retained, err := balance.Negate()
	if err != nil {
		return money.Money{}, fmt.Errorf("dividends: retained earnings presentation amount: %w", err)
	}
	retained.Currency = gbpCurrency
	return retained, nil
}

func corporateTaxAmount(profit money.Money, rate jurisdiction.Rate) (money.Money, error) {
	if profit.Currency != gbpCurrency {
		return money.Money{}, fmt.Errorf("dividends: profit currency %q, want GBP", profit.Currency)
	}
	if profit.Amount <= 0 {
		return money.Zero(gbpCurrency), nil
	}
	rat, ok := new(big.Rat).SetString(strings.TrimSpace(string(rate)))
	if !ok {
		return money.Money{}, fmt.Errorf("dividends: parse corporate rate %q", rate)
	}
	tax := profit.MulRat(rat)
	tax.Currency = gbpCurrency
	return tax, nil
}

type financialPeriod struct {
	From time.Time
	To   time.Time
}

func financialYearForDate(date time.Time, month time.Month, day int) (string, error) {
	normalized, err := normalizeDate(date)
	if err != nil {
		return "", err
	}
	yearEnd, err := financialYearEndDate(normalized.Year(), month, day)
	if err != nil {
		return "", err
	}
	endYear := normalized.Year()
	if normalized.After(yearEnd) {
		endYear++
	}
	startYear := endYear - 1
	return fmt.Sprintf("%04d-%02d", startYear, endYear%100), nil
}

func financialYearPeriod(financialYear string, month time.Month, day int) (financialPeriod, error) {
	startYear, endYear, err := parseFinancialYear(financialYear)
	if err != nil {
		return financialPeriod{}, err
	}
	previousEnd, err := financialYearEndDate(startYear, month, day)
	if err != nil {
		return financialPeriod{}, err
	}
	end, err := financialYearEndDate(endYear, month, day)
	if err != nil {
		return financialPeriod{}, err
	}
	return financialPeriod{From: previousEnd.AddDate(0, 0, 1), To: end}, nil
}

func financialYearEndDate(year int, month time.Month, day int) (time.Time, error) {
	if month < time.January || month > time.December || day < 1 {
		return time.Time{}, fmt.Errorf("dividends: invalid year end %d-%02d: %w", month, day, ErrInvalidFinancialYear)
	}
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > lastDay {
		if month != time.February || day != 29 {
			return time.Time{}, fmt.Errorf("dividends: invalid year end %d-%02d: %w", month, day, ErrInvalidFinancialYear)
		}
		day = lastDay
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
}

func parseFinancialYear(financialYear string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(financialYear), "-")
	if len(parts) != 2 || len(parts[0]) != 4 || len(parts[1]) != 2 {
		return 0, 0, fmt.Errorf("dividends: financial year %q must look like 2025-26: %w", financialYear, ErrInvalidFinancialYear)
	}
	startYear, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("dividends: financial year %q start: %w", financialYear, ErrInvalidFinancialYear)
	}
	endSuffix, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("dividends: financial year %q end: %w", financialYear, ErrInvalidFinancialYear)
	}
	endYear := startYear/100*100 + endSuffix
	if endYear <= startYear {
		endYear += 100
	}
	if endYear != startYear+1 {
		return 0, 0, fmt.Errorf("dividends: financial year %q must span one year: %w", financialYear, ErrInvalidFinancialYear)
	}
	return startYear, endYear, nil
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

func normalizeDate(date time.Time) (time.Time, error) {
	if date.IsZero() {
		return time.Time{}, fmt.Errorf("dividends: date is required: %w", ErrInvalidDeclaration)
	}
	year, month, day := date.UTC().Date()
	if year < 1900 || year > 9999 {
		return time.Time{}, fmt.Errorf("dividends: date %04d-%02d-%02d is outside supported range: %w", year, month, day, ErrInvalidDeclaration)
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
}
