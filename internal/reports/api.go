package reports

import (
	"context"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

// Period is an inclusive posting-date window.
type Period struct {
	From time.Time
	To   time.Time
}

// PL is the derived profit-and-loss read model.
type PL struct {
	Period          Period
	TaxYear         string
	Income          []IncomeLine
	IncomeTotal     money.Money
	RealisedFXGains LineItem
	Expenses        []ExpenseLine
	ExpenseTotal    money.Money
	ProfitBeforeTax money.Money
	CorporateTax    TaxLine
	NetProfit       money.Money
}

// IncomeLine is a GBP-presentational income row, grouped by client/currency or
// by the Other income fallback for non-invoice income.
type IncomeLine struct {
	Label      string
	ClientID   string
	ClientName string
	Currency   string
	Amount     money.Money
}

// ExpenseLine is a GBP-presentational expense row grouped by chart account.
type ExpenseLine struct {
	AccountCode ledger.AccountCode
	AccountName string
	Amount      money.Money
}

// LineItem is a named GBP-presentational P&L row.
type LineItem struct {
	Label  string
	Amount money.Money
}

// TaxLine is a data-driven corporate tax row sourced from jurisdiction packs.
type TaxLine struct {
	Label   string
	TaxYear string
	Rate    jurisdiction.Rate
	Amount  money.Money
}

// Reports is the v1 reports read API.
type Reports interface {
	ProfitAndLoss(context.Context, Period) (PL, error)
	ProfitYTD(context.Context, string) (money.Money, error)
}

type Ledger interface {
	BalancesByType(context.Context, time.Time, time.Time) ([]ledger.AccountBalance, error)
	Entries(context.Context, ledger.EntryFilter) ([]ledger.JournalEntry, error)
	Accounts(context.Context) ([]ledger.Account, error)
}

type Identity interface {
	CompanyFacts(context.Context) (identity.CompanyFacts, error)
}

type Invoicing interface {
	Invoice(context.Context, string) (invoicing.Invoice, error)
	InvoiceByNumber(context.Context, string) (invoicing.Invoice, error)
	Client(context.Context, string) (invoicing.Client, error)
}
