package advisor

import (
	"time"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

const (
	FactInvoicesOverdue FactKey = "invoices.overdue"

	FactDLABalance            FactKey = "dla.balance"
	FactDLAStatus             FactKey = "dla.status"
	FactDLASuggestedClearance FactKey = "dla.suggestedClearance"

	FactDividendsHeadroom      FactKey = "dividends.headroom"
	FactDividendsDistributable FactKey = "dividends.distributable"

	FactVATPosition FactKey = "vat.position"
	FactVATDueDate  FactKey = "vat.dueDate"
	FactFilings     FactKey = "filings"

	FactRatesLastDate FactKey = "rates.lastDate"
	FactRatesStale    FactKey = "rates.stale"

	FactCompanyIncorporationDate FactKey = "company.incorporationDate"
	FactCompanyYearEnd           FactKey = "company.yearEnd"
	FactCompanyYearEndMonth      FactKey = "company.yearEnd.month"
	FactCompanyYearEndDay        FactKey = "company.yearEnd.day"
)

// OverdueInvoiceFact is the advisor contract shape for invoices.overdue[].
type OverdueInvoiceFact struct {
	ID          string      `json:"id"`
	Number      string      `json:"number"`
	Client      string      `json:"client"`
	Amount      money.Money `json:"amount"`
	DaysOverdue int         `json:"daysOverdue"`
}

// FilingFact is the advisor contract shape for filings[].
type FilingFact struct {
	Key        string    `json:"key"`
	Label      string    `json:"label"`
	DueDate    time.Time `json:"dueDate"`
	DaysUntil  int       `json:"daysUntil"`
	Status     string    `json:"status"`
	WarnWindow Days      `json:"warnWindow"`
}

// CompanyYearEndFact is the typed company year-end fact.
type CompanyYearEndFact struct {
	Month int `json:"month"`
	Day   int `json:"day"`
}
