package reports

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

const (
	retainedEarningsAccountCode = ledger.AccountCode("3000-retained-earnings")
	currentYearProfitCode       = ledger.AccountCode("current-year-profit")
	currentYearProfitLabel      = "Current-year profit"
	retainedEarningsLabel       = "Retained earnings"
)

var balanceSheetOpeningDate = time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)

type balanceSheetLedgerData struct {
	assets               []BalanceSheetLine
	liabilities          []BalanceSheetLine
	equity               []BalanceSheetLine
	currentYearProfit    money.Money
	priorYearProfit      money.Money
	retainedEarningsName string
}

// BalanceSheet returns Assets, Liabilities, and Equity as at a single date.
// Ledger balances keep debit-positive/credit-negative signs internally, then
// report liability and equity credit balances as positive presentation amounts.
func (s *Service) BalanceSheet(ctx context.Context, asOf time.Time) (BalanceSheet, error) {
	if s.ledger == nil {
		return BalanceSheet{}, fmt.Errorf("ledger: %w", ErrMissingProvider)
	}
	if s.facts == nil {
		return BalanceSheet{}, fmt.Errorf("identity: %w", ErrMissingProvider)
	}
	normalizedAsOf, err := normalizeAsOfDate(asOf)
	if err != nil {
		return BalanceSheet{}, err
	}
	facts, err := s.facts.CompanyFacts(ctx)
	if err != nil {
		return BalanceSheet{}, err
	}
	financialYear, err := financialYearForDate(normalizedAsOf, facts.YearEnd.Month, facts.YearEnd.Day)
	if err != nil {
		return BalanceSheet{}, err
	}
	financialPeriod, err := financialYearPeriod(financialYear, facts.YearEnd.Month, facts.YearEnd.Day)
	if err != nil {
		return BalanceSheet{}, err
	}

	var data balanceSheetLedgerData
	if err := s.ledger.ReadSnapshot(ctx, func(ctx context.Context, snapshot ledger.ReadSnapshot) error {
		var err error
		data, err = readBalanceSheetLedger(ctx, snapshot, normalizedAsOf, financialPeriod.From)
		return err
	}); err != nil {
		return BalanceSheet{}, err
	}
	return balanceSheetFromLedgerData(normalizedAsOf, financialYear, data)
}

func readBalanceSheetLedger(
	ctx context.Context,
	ledgerView ledger.ReadSnapshot,
	asOf time.Time,
	financialYearStart time.Time,
) (balanceSheetLedgerData, error) {
	accounts, err := ledgerView.Accounts(ctx)
	if err != nil {
		return balanceSheetLedgerData{}, err
	}
	sort.Slice(accounts, func(i, j int) bool {
		return accounts[i].Code < accounts[j].Code
	})

	data := balanceSheetLedgerData{
		currentYearProfit: money.Zero(gbpCurrency),
		priorYearProfit:   money.Zero(gbpCurrency),
	}
	for _, account := range accounts {
		if account.Code == retainedEarningsAccountCode {
			data.retainedEarningsName = account.Name
		}
		if !isBalanceSheetAccountType(account.Type) {
			continue
		}
		balance, err := ledgerView.AccountBalance(ctx, account.Code, asOf)
		if err != nil {
			return balanceSheetLedgerData{}, err
		}
		amount, err := balanceSheetPresentationAmount(account.Type, balance.AmountGBP)
		if err != nil {
			return balanceSheetLedgerData{}, err
		}
		if amount.IsZero() {
			continue
		}
		line := BalanceSheetLine{
			AccountCode: account.Code,
			AccountName: account.Name,
			Amount:      amount,
		}
		switch account.Type {
		case ledger.AccountTypeAsset:
			data.assets = append(data.assets, line)
		case ledger.AccountTypeLiability:
			data.liabilities = append(data.liabilities, line)
		case ledger.AccountTypeEquity:
			data.equity = append(data.equity, line)
		}
	}

	currentBalances, err := ledgerView.BalancesByType(ctx, financialYearStart, asOf)
	if err != nil {
		return balanceSheetLedgerData{}, err
	}
	currentProfit, err := profitFromTypeBalances(currentBalances)
	if err != nil {
		return balanceSheetLedgerData{}, err
	}
	data.currentYearProfit = currentProfit

	priorYearEnd := financialYearStart.AddDate(0, 0, -1)
	if !priorYearEnd.Before(balanceSheetOpeningDate) {
		priorBalances, err := ledgerView.BalancesByType(ctx, balanceSheetOpeningDate, priorYearEnd)
		if err != nil {
			return balanceSheetLedgerData{}, err
		}
		priorProfit, err := profitFromTypeBalances(priorBalances)
		if err != nil {
			return balanceSheetLedgerData{}, err
		}
		data.priorYearProfit = priorProfit
	}
	return data, nil
}

func balanceSheetFromLedgerData(asOf time.Time, financialYear string, data balanceSheetLedgerData) (BalanceSheet, error) {
	equityLines, err := addPriorProfitToRetainedEarnings(data.equity, data.retainedEarningsName, data.priorYearProfit)
	if err != nil {
		return BalanceSheet{}, err
	}
	equityLines = append(equityLines, BalanceSheetLine{
		AccountCode: currentYearProfitCode,
		AccountName: currentYearProfitLabel,
		Amount:      data.currentYearProfit,
	})

	assets, err := balanceSheetSection("Assets", data.assets)
	if err != nil {
		return BalanceSheet{}, err
	}
	liabilities, err := balanceSheetSection("Liabilities", data.liabilities)
	if err != nil {
		return BalanceSheet{}, err
	}
	equity, err := balanceSheetSection("Equity", equityLines)
	if err != nil {
		return BalanceSheet{}, err
	}
	liabilitiesAndEquity, err := liabilities.Total.Add(equity.Total)
	if err != nil {
		return BalanceSheet{}, err
	}
	return BalanceSheet{
		AsOf:                      asOf,
		FinancialYear:             financialYear,
		Assets:                    assets,
		Liabilities:               liabilities,
		Equity:                    equity,
		TotalAssets:               assets.Total,
		TotalLiabilities:          liabilities.Total,
		TotalEquity:               equity.Total,
		TotalLiabilitiesAndEquity: liabilitiesAndEquity,
		Balanced:                  moneyEqual(assets.Total, liabilitiesAndEquity),
	}, nil
}

func addPriorProfitToRetainedEarnings(lines []BalanceSheetLine, retainedName string, priorProfit money.Money) ([]BalanceSheetLine, error) {
	out := append([]BalanceSheetLine(nil), lines...)
	if priorProfit.IsZero() {
		return out, nil
	}
	for i := range out {
		if out[i].AccountCode != retainedEarningsAccountCode {
			continue
		}
		next, err := out[i].Amount.Add(priorProfit)
		if err != nil {
			return nil, err
		}
		out[i].Amount = next
		return out, nil
	}
	if retainedName == "" {
		retainedName = retainedEarningsLabel
	}
	out = append(out, BalanceSheetLine{
		AccountCode: retainedEarningsAccountCode,
		AccountName: retainedName,
		Amount:      priorProfit,
	})
	sort.Slice(out, func(i, j int) bool {
		return out[i].AccountCode < out[j].AccountCode
	})
	return out, nil
}

func balanceSheetSection(label string, lines []BalanceSheetLine) (BalanceSheetSection, error) {
	total := money.Zero(gbpCurrency)
	for _, line := range lines {
		next, err := total.Add(line.Amount)
		if err != nil {
			return BalanceSheetSection{}, err
		}
		total = next
	}
	return BalanceSheetSection{
		Label: label,
		Lines: append([]BalanceSheetLine(nil), lines...),
		Total: total,
	}, nil
}

func profitFromTypeBalances(balances []ledger.AccountBalance) (money.Money, error) {
	income, err := incomeTotalFromBalances(balances)
	if err != nil {
		return money.Money{}, err
	}
	expenses := balanceForType(balances, ledger.AccountTypeExpense)
	return income.Sub(expenses)
}

func balanceSheetPresentationAmount(accountType ledger.AccountType, amount money.Money) (money.Money, error) {
	presentation := money.Money{Amount: amount.Amount, Currency: gbpCurrency}
	switch accountType {
	case ledger.AccountTypeAsset:
		return presentation, nil
	case ledger.AccountTypeLiability, ledger.AccountTypeEquity:
		return presentation.Negate()
	default:
		return money.Money{}, fmt.Errorf("reports: account type %q is not balance-sheet reportable", accountType)
	}
}

func isBalanceSheetAccountType(accountType ledger.AccountType) bool {
	return accountType == ledger.AccountTypeAsset || accountType == ledger.AccountTypeLiability || accountType == ledger.AccountTypeEquity
}

func normalizeAsOfDate(asOf time.Time) (time.Time, error) {
	normalized := dateOnly(asOf)
	if normalized.IsZero() {
		return time.Time{}, fmt.Errorf("reports: asOf is required: %w", ErrInvalidPeriod)
	}
	return normalized, nil
}

func financialYearForDate(date time.Time, month time.Month, day int) (string, error) {
	normalized := dateOnly(date)
	if normalized.IsZero() {
		return "", fmt.Errorf("reports: date is required: %w", ErrInvalidPeriod)
	}
	yearEnd, err := financialYearEndDate(normalized.Year(), month, day)
	if err != nil {
		return "", err
	}
	startYear := normalized.Year() - 1
	endYear := normalized.Year()
	if normalized.After(yearEnd) {
		startYear = normalized.Year()
		endYear = normalized.Year() + 1
	}
	return fmt.Sprintf("%04d-%02d", startYear, endYear%100), nil
}

func moneyEqual(left money.Money, right money.Money) bool {
	return left.Amount == right.Amount && left.Currency == right.Currency
}
