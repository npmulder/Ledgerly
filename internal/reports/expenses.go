package reports

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

const (
	bankingSourceModule      = "banking"
	unattributedExpensePayee = "Ledger entry"
)

type expenseLedgerData struct {
	total        money.Money
	categories   map[ledger.AccountCode]ExpenseCategory
	transactions []expenseLedgerTransaction
}

type expenseLedgerTransaction struct {
	entryID      ledger.EntryID
	date         time.Time
	description  string
	sourceModule string
	sourceRef    string
	accountCode  ledger.AccountCode
	category     string
	amount       money.Money
	amountGBP    money.Money
}

// ExpensesByCategory returns categorized expense totals, top payees, and the
// underlying expense postings for a period.
func (s *Service) ExpensesByCategory(ctx context.Context, period Period) (ExpensesReport, error) {
	if s.ledger == nil {
		return ExpensesReport{}, fmt.Errorf("ledger: %w", ErrMissingProvider)
	}
	if s.banking == nil {
		return ExpensesReport{}, fmt.Errorf("banking: %w", ErrMissingProvider)
	}
	normalized, err := normalizePeriod(period)
	if err != nil {
		return ExpensesReport{}, err
	}
	var data expenseLedgerData
	if err := s.ledger.ReadSnapshot(ctx, func(ctx context.Context, snapshot ledger.ReadSnapshot) error {
		var err error
		data, err = readExpenseLedgerData(ctx, normalized, snapshot)
		return err
	}); err != nil {
		return ExpensesReport{}, err
	}
	return s.expensesReportFromLedgerData(ctx, normalized, data)
}

// ExpensesCSV returns the accountant CSV export of categorized expense
// transactions for a period.
func (s *Service) ExpensesCSV(ctx context.Context, period Period) ([]byte, error) {
	report, err := s.ExpensesByCategory(ctx, period)
	if err != nil {
		return nil, err
	}
	return mustBuildCSV(expensesCSVRows(report.Transactions)), nil
}

func readExpenseLedgerData(ctx context.Context, period Period, ledgerView ledgerReader) (expenseLedgerData, error) {
	accounts, err := ledgerView.Accounts(ctx)
	if err != nil {
		return expenseLedgerData{}, err
	}
	accountByCode := accountsByCode(accounts)
	data := expenseLedgerData{
		total:      money.Zero(gbpCurrency),
		categories: map[ledger.AccountCode]ExpenseCategory{},
	}
	var cursor *ledger.EntryCursor
	for {
		entries, err := ledgerView.Entries(ctx, ledger.EntryFilter{
			From:  &period.From,
			To:    &period.To,
			After: cursor,
			Limit: ledger.MaxEntriesLimit,
		})
		if err != nil {
			return expenseLedgerData{}, err
		}
		for _, entry := range entries {
			if err := addEntryToExpenseData(entry, accountByCode, &data); err != nil {
				return expenseLedgerData{}, err
			}
		}
		if len(entries) < ledger.MaxEntriesLimit {
			break
		}
		last := entries[len(entries)-1]
		cursor = &ledger.EntryCursor{Date: last.Date, ID: last.ID}
	}
	return data, nil
}

func addEntryToExpenseData(entry ledger.JournalEntry, accounts map[ledger.AccountCode]ledger.Account, data *expenseLedgerData) error {
	for _, posting := range entry.Postings {
		account, ok := accounts[posting.AccountCode]
		if !ok {
			return fmt.Errorf("reports: ledger account %q missing from account list", posting.AccountCode)
		}
		if account.Type != ledger.AccountTypeExpense {
			continue
		}
		amountGBP := money.Money{Amount: posting.AmountGBP.Amount, Currency: gbpCurrency}
		category := data.categories[posting.AccountCode]
		if category.AccountCode == "" {
			category = ExpenseCategory{
				AccountCode: posting.AccountCode,
				Category:    account.Name,
				Amount:      money.Zero(gbpCurrency),
			}
		}
		nextCategoryAmount, err := category.Amount.Add(amountGBP)
		if err != nil {
			return err
		}
		category.Amount = nextCategoryAmount
		category.TransactionCount++
		data.categories[posting.AccountCode] = category
		data.total, err = data.total.Add(amountGBP)
		if err != nil {
			return err
		}
		data.transactions = append(data.transactions, expenseLedgerTransaction{
			entryID:      entry.ID,
			date:         entry.Date,
			description:  entry.Description,
			sourceModule: entry.SourceModule,
			sourceRef:    entry.SourceRef,
			accountCode:  posting.AccountCode,
			category:     account.Name,
			amount:       posting.Amount,
			amountGBP:    amountGBP,
		})
	}
	return nil
}

func (s *Service) expensesReportFromLedgerData(ctx context.Context, period Period, data expenseLedgerData) (ExpensesReport, error) {
	bankingCache := map[BankingTransactionID]BankingTransaction{}
	transactions := make([]ExpenseTransaction, 0, len(data.transactions))
	payees := map[string]ExpensePayeeTotal{}
	for _, row := range data.transactions {
		transaction, payeeAmount, err := s.expenseTransactionFromLedgerRow(ctx, row, bankingCache)
		if err != nil {
			return ExpensesReport{}, err
		}
		transactions = append(transactions, transaction)
		payee := payees[transaction.Payee]
		if payee.Payee == "" {
			payee = ExpensePayeeTotal{
				Payee:  transaction.Payee,
				Amount: money.Zero(gbpCurrency),
			}
		}
		next, err := payee.Amount.Add(payeeAmount)
		if err != nil {
			return ExpensesReport{}, err
		}
		payee.Amount = next
		payee.TransactionCount++
		payees[transaction.Payee] = payee
	}
	categories := sortedExpenseCategories(data.categories)
	topPayees := sortedExpensePayees(payees)
	sortExpenseTransactions(transactions)
	return ExpensesReport{
		Period:       period,
		Categories:   categories,
		TopPayees:    topPayees,
		Transactions: transactions,
		Total:        data.total,
	}, nil
}

func (s *Service) expenseTransactionFromLedgerRow(
	ctx context.Context,
	row expenseLedgerTransaction,
	bankingCache map[BankingTransactionID]BankingTransaction,
) (ExpenseTransaction, money.Money, error) {
	date := row.date
	payee := unattributedExpensePayee
	reference := firstNonBlank(row.sourceRef, row.description)
	if txnID, ok := bankingTransactionIDFromSourceRef(row.sourceModule, row.sourceRef); ok {
		txn, found := bankingCache[txnID]
		if !found {
			var err error
			txn, err = s.banking.Transaction(ctx, txnID)
			if err != nil {
				return ExpenseTransaction{}, money.Money{}, fmt.Errorf("reports: load banking transaction %d: %w", txnID, err)
			}
			bankingCache[txnID] = txn
		}
		date = txn.Date
		payee = firstNonBlank(txn.Payee, payee)
		reference = firstNonBlank(txn.Reference, reference)
	}
	return ExpenseTransaction{
		EntryID:      row.entryID,
		Date:         dateOnly(date),
		Payee:        payee,
		Reference:    reference,
		Amount:       row.amount,
		AccountCode:  row.accountCode,
		Category:     row.category,
		SourceModule: row.sourceModule,
		SourceRef:    row.sourceRef,
	}, row.amountGBP, nil
}

func bankingTransactionIDFromSourceRef(sourceModule string, sourceRef string) (BankingTransactionID, bool) {
	if strings.TrimSpace(sourceModule) != bankingSourceModule {
		return 0, false
	}
	parts := strings.Split(strings.TrimSpace(sourceRef), ":")
	if len(parts) < 2 || parts[0] != bankingSourceModule {
		return 0, false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return BankingTransactionID(id), true
}

func sortedExpenseCategories(categories map[ledger.AccountCode]ExpenseCategory) []ExpenseCategory {
	out := make([]ExpenseCategory, 0, len(categories))
	for _, category := range categories {
		if category.Amount.IsZero() {
			continue
		}
		out = append(out, category)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Amount.Amount != out[j].Amount.Amount {
			return out[i].Amount.Amount > out[j].Amount.Amount
		}
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].AccountCode < out[j].AccountCode
	})
	return out
}

func sortedExpensePayees(payees map[string]ExpensePayeeTotal) []ExpensePayeeTotal {
	out := make([]ExpensePayeeTotal, 0, len(payees))
	for _, payee := range payees {
		if payee.Amount.IsZero() {
			continue
		}
		out = append(out, payee)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Amount.Amount != out[j].Amount.Amount {
			return out[i].Amount.Amount > out[j].Amount.Amount
		}
		return out[i].Payee < out[j].Payee
	})
	return out
}

func sortExpenseTransactions(transactions []ExpenseTransaction) {
	sort.SliceStable(transactions, func(i, j int) bool {
		if !transactions[i].Date.Equal(transactions[j].Date) {
			return transactions[i].Date.After(transactions[j].Date)
		}
		if transactions[i].Payee != transactions[j].Payee {
			return transactions[i].Payee < transactions[j].Payee
		}
		if transactions[i].Category != transactions[j].Category {
			return transactions[i].Category < transactions[j].Category
		}
		return transactions[i].EntryID > transactions[j].EntryID
	})
}

func expensesCSVRows(transactions []ExpenseTransaction) [][]string {
	rows := [][]string{{"date", "payee", "reference", "amount", "currency", "category"}}
	for _, transaction := range transactions {
		rows = append(rows, []string{
			transaction.Date.UTC().Format(time.DateOnly),
			transaction.Payee,
			transaction.Reference,
			decimalString(transaction.Amount),
			transaction.Amount.Currency,
			transaction.Category,
		})
	}
	return rows
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
