package advisor

import (
	"time"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

const (
	FactInvoicesOverdue    FactKey = "invoices.overdue"
	FactInvoiceClientName  FactKey = "client_name"
	FactInvoiceCount       FactKey = "count"
	FactInvoiceDaysOverdue FactKey = "days_overdue"
	FactInvoiceID          FactKey = "invoice_id"
	FactInvoiceNumber      FactKey = "invoice_number"

	FactDLABalance            FactKey = "dla.balance"
	FactDLAStatus             FactKey = "dla.status"
	FactDLASuggestedClearance FactKey = "dla.suggestedClearance"
	FactRuleDLABalance        FactKey = "balance"
	FactRuleDLAStatus         FactKey = "status"
	FactRuleDLAClearance      FactKey = "clearance_amount"
	FactRuleDLAClearanceMinor FactKey = "clearance_amount_minor_units"

	FactDividendsHeadroom      FactKey = "dividends.headroom"
	FactDividendsDistributable FactKey = "dividends.distributable"
	FactDividendHeadroom       FactKey = "dividend_headroom"
	FactDividendHeadroomMinor  FactKey = "headroom_minor_units"
	FactDividendsYTD           FactKey = "dividends_ytd"
	FactDividendEstimate       FactKey = "estimate"
	FactDividendEstimateMinor  FactKey = "estimate_minor_units"

	FactVATPosition      FactKey = "vat.position"
	FactVATDueDate       FactKey = "vat.dueDate"
	FactFilings          FactKey = "filings"
	FactFilingAuthority  FactKey = "authority"
	FactFilingDueDate    FactKey = "due_date"
	FactFilingName       FactKey = "filing_name"
	FactFilingDaysUntil  FactKey = "days_until"
	FactFilingStatus     FactKey = "filing_status"
	FactFilingWarnWindow FactKey = "warn_window_days"

	FactRatesLastDate FactKey = "rates.lastDate"
	FactRatesStale    FactKey = "rates.stale"
	FactStaleDays     FactKey = "stale_days"

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
	Authority  string    `json:"authority"`
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
