package app

import (
	"context"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"sort"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/errgroup"

	"github.com/npmulder/ledgerly/internal/banking"
	"github.com/npmulder/ledgerly/internal/dividends"
	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const dashboardModuleName = "dashboard"

const dashboardProblemTypeUnavailable = "https://ledgerly.local/problems/dashboard/unavailable"

type dashboardLedger interface {
	AccountBalance(context.Context, ledger.AccountCode, time.Time) (ledger.AccountBalance, error)
}

type dashboardMoneyFX interface {
	TodayRate(context.Context, string, string) (moneyfx.Rate, time.Time, error)
	ToGBP(context.Context, money.Money, time.Time) (money.Money, error)
}

type dashboardInvoicing interface {
	List(context.Context, invoicing.InvoiceListFilter) (invoicing.InvoiceListResult, error)
	Totals(context.Context, invoicing.InvoiceListFilter) (invoicing.InvoiceTotalsSummary, error)
}

type dashboardDLA interface {
	CurrentBalance(context.Context) (money.Money, dla.Status, error)
}

type dashboardDividends interface {
	Headroom(context.Context) (dividends.HeadroomBreakdown, error)
}

type dashboardBanking interface {
	Accounts(context.Context) ([]banking.BankAccount, error)
	ReviewQueue(context.Context) (banking.ReviewQueue, error)
	UnreconciledCount(context.Context, banking.AccountID) (int, error)
}

type dashboardIdentity interface {
	Profile(context.Context) (identity.CompanyProfile, error)
}

type dashboardPrincipalFunc func(context.Context) (identity.Principal, bool)

type dashboardDependencies struct {
	clock     interface{ Now() time.Time }
	ledger    dashboardLedger
	moneyFX   dashboardMoneyFX
	invoicing dashboardInvoicing
	dla       dashboardDLA
	dividends dashboardDividends
	banking   dashboardBanking
	identity  dashboardIdentity
	principal dashboardPrincipalFunc
}

type dashboardHTTPHandler struct {
	deps dashboardDependencies
}

func dashboardHTTPModule(deps dashboardDependencies) httpserver.Module {
	handler := dashboardHTTPHandler{deps: deps}
	return httpserver.Module{
		Name:           dashboardModuleName,
		RegisterRoutes: handler.registerRoutes,
	}
}

func (h dashboardHTTPHandler) registerRoutes(r chi.Router) {
	r.Get("/summary", h.getSummary)
}

type dashboardSummaryResponse struct {
	Cash             *dashboardCashResponse             `json:"cash"`
	Outstanding      *dashboardOutstandingResponse      `json:"outstanding"`
	DLA              *dashboardDLAResponse              `json:"dla"`
	DividendHeadroom *dashboardDividendHeadroomResponse `json:"dividendHeadroom"`
	RecentInvoices   *dashboardRecentInvoicesResponse   `json:"recentInvoices"`
	ToReconcile      *dashboardToReconcileResponse      `json:"toReconcile"`
	Rate             *dashboardRateResponse             `json:"rate"`
	Greeting         *dashboardGreetingResponse         `json:"greeting"`
	Errors           []dashboardSectionError            `json:"errors"`
}

type dashboardSectionError struct {
	Section string `json:"section"`
	Detail  string `json:"detail"`
}

type dashboardCashResponse struct {
	Accounts []dashboardCashAccountResponse `json:"accounts"`
	TotalGBP money.Money                    `json:"total_gbp"`
}

type dashboardCashAccountResponse struct {
	ID                int64              `json:"id"`
	Name              string             `json:"name"`
	Provider          banking.Provider   `json:"provider"`
	Currency          string             `json:"currency"`
	LedgerAccountCode ledger.AccountCode `json:"ledger_account_code"`
	NativeBalance     money.Money        `json:"native_balance"`
	GBPBalance        money.Money        `json:"gbp_balance"`
}

type dashboardOutstandingResponse struct {
	Totals          []money.Money `json:"totals"`
	TotalGBP        money.Money   `json:"total_gbp"`
	EarliestDueDate *string       `json:"earliest_due_date"`
}

type dashboardDLAResponse struct {
	Balance money.Money `json:"balance"`
	Status  dla.Status  `json:"status"`
}

type dashboardDividendHeadroomResponse struct {
	Available     money.Money `json:"available"`
	Distributable bool        `json:"distributable"`
}

type dashboardRecentInvoicesResponse []dashboardRecentInvoiceResponse

type dashboardRecentInvoiceResponse struct {
	ID          string                  `json:"id"`
	Number      *string                 `json:"number"`
	Client      string                  `json:"client"`
	Amount      money.Money             `json:"amount"`
	Status      invoicing.InvoiceStatus `json:"status"`
	DaysOverdue *int                    `json:"days_overdue,omitempty"`
}

type dashboardToReconcileResponse struct {
	Accounts    []dashboardReconcileAccountResponse `json:"accounts"`
	ReviewQueue []dashboardReviewQueueItemResponse  `json:"review_queue"`
}

type dashboardReconcileAccountResponse struct {
	ID                int64              `json:"id"`
	Name              string             `json:"name"`
	Currency          string             `json:"currency"`
	LedgerAccountCode ledger.AccountCode `json:"ledger_account_code"`
	UnreconciledCount int                `json:"unreconciled_count"`
}

type dashboardReviewQueueItemResponse struct {
	Kind       banking.SuggestionKind `json:"kind"`
	Payee      string                 `json:"payee"`
	Amount     money.Money            `json:"amount"`
	Confidence float64                `json:"confidence"`
}

type dashboardRateResponse struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Rate      string    `json:"rate"`
	RateDate  string    `json:"rate_date"`
	FetchedAt time.Time `json:"fetched_at"`
	Source    string    `json:"source"`
}

type dashboardGreetingResponse struct {
	UserName    string `json:"user_name"`
	TradingName string `json:"trading_name"`
}

func (h dashboardHTTPHandler) getSummary(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx := r.Context()
	response := dashboardSummaryResponse{
		Errors: []dashboardSectionError{},
	}

	var (
		mu        sync.Mutex
		successes int
		group     errgroup.Group
	)

	run := func(section string, fn func(context.Context) (func(), error)) {
		group.Go(func() error {
			apply, err := fn(ctx)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				response.Errors = append(response.Errors, dashboardSectionError{
					Section: section,
					Detail:  err.Error(),
				})
				return nil
			}
			if apply != nil {
				apply()
			}
			successes++
			return nil
		})
	}

	run("cash", func(ctx context.Context) (func(), error) {
		cash, err := h.cash(ctx)
		if err != nil {
			return nil, err
		}
		return func() { response.Cash = &cash }, nil
	})
	run("outstanding", func(ctx context.Context) (func(), error) {
		outstanding, err := h.outstanding(ctx)
		if err != nil {
			return nil, err
		}
		return func() { response.Outstanding = &outstanding }, nil
	})
	run("dla", func(ctx context.Context) (func(), error) {
		dlaResponse, err := h.dla(ctx)
		if err != nil {
			return nil, err
		}
		return func() { response.DLA = &dlaResponse }, nil
	})
	run("dividendHeadroom", func(ctx context.Context) (func(), error) {
		headroom, err := h.dividendHeadroom(ctx)
		if err != nil {
			return nil, err
		}
		return func() { response.DividendHeadroom = &headroom }, nil
	})
	run("recentInvoices", func(ctx context.Context) (func(), error) {
		recent, err := h.recentInvoices(ctx)
		if err != nil {
			return nil, err
		}
		return func() { response.RecentInvoices = &recent }, nil
	})
	run("toReconcile", func(ctx context.Context) (func(), error) {
		toReconcile, err := h.toReconcile(ctx)
		if err != nil {
			return nil, err
		}
		return func() { response.ToReconcile = &toReconcile }, nil
	})
	run("rate", func(ctx context.Context) (func(), error) {
		rate, err := h.rate(ctx)
		if err != nil {
			return nil, err
		}
		return func() { response.Rate = &rate }, nil
	})
	run("greeting", func(ctx context.Context) (func(), error) {
		greeting, err := h.greeting(ctx)
		if err != nil {
			return nil, err
		}
		return func() { response.Greeting = &greeting }, nil
	})

	_ = group.Wait()
	if successes == 0 {
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   dashboardProblemTypeUnavailable,
			Title:  nethttp.StatusText(nethttp.StatusServiceUnavailable),
			Status: nethttp.StatusServiceUnavailable,
			Detail: "dashboard summary is unavailable",
			Extensions: map[string]any{
				"errors": response.Errors,
			},
		})
		return
	}

	writeDashboardJSON(w, nethttp.StatusOK, response)
}

func (h dashboardHTTPHandler) cash(ctx context.Context) (dashboardCashResponse, error) {
	if h.deps.banking == nil {
		return dashboardCashResponse{}, fmt.Errorf("dashboard: banking read API is required")
	}
	if h.deps.ledger == nil {
		return dashboardCashResponse{}, fmt.Errorf("dashboard: ledger read API is required")
	}
	if h.deps.moneyFX == nil {
		return dashboardCashResponse{}, fmt.Errorf("dashboard: moneyfx read API is required")
	}

	accounts, err := h.deps.banking.Accounts(ctx)
	if err != nil {
		return dashboardCashResponse{}, err
	}
	today := dashboardDateOnly(h.now())
	response := dashboardCashResponse{
		Accounts: []dashboardCashAccountResponse{},
		TotalGBP: money.Zero("GBP"),
	}
	for _, account := range accounts {
		balance, err := h.deps.ledger.AccountBalance(ctx, account.LedgerAccountCode, today)
		if err != nil {
			return dashboardCashResponse{}, err
		}
		native := accountNativeBalance(account, balance)
		gbp, err := h.deps.moneyFX.ToGBP(ctx, native, today)
		if err != nil {
			return dashboardCashResponse{}, err
		}
		response.TotalGBP, err = response.TotalGBP.Add(gbp)
		if err != nil {
			return dashboardCashResponse{}, err
		}
		response.Accounts = append(response.Accounts, dashboardCashAccountResponse{
			ID:                int64(account.ID),
			Name:              account.Name,
			Provider:          account.Provider,
			Currency:          account.Currency,
			LedgerAccountCode: account.LedgerAccountCode,
			NativeBalance:     native,
			GBPBalance:        gbp,
		})
	}
	return response, nil
}

func accountNativeBalance(account banking.BankAccount, balance ledger.AccountBalance) money.Money {
	native := money.Money{Currency: account.Currency}
	for _, candidate := range balance.Native {
		if candidate.Currency == account.Currency {
			return candidate
		}
	}
	return native
}

func (h dashboardHTTPHandler) outstanding(ctx context.Context) (dashboardOutstandingResponse, error) {
	if h.deps.invoicing == nil {
		return dashboardOutstandingResponse{}, fmt.Errorf("dashboard: invoicing read API is required")
	}
	filter := outstandingInvoiceFilter()
	totals, err := h.deps.invoicing.Totals(ctx, filter)
	if err != nil {
		return dashboardOutstandingResponse{}, err
	}
	list, err := h.deps.invoicing.List(ctx, invoicing.InvoiceListFilter{
		Statuses: filter.Statuses,
		Limit:    invoicing.MaxInvoiceListLimit,
	})
	if err != nil {
		return dashboardOutstandingResponse{}, err
	}
	response := dashboardOutstandingResponse{
		Totals:   emptyMoneySlice(totals.Subtotals),
		TotalGBP: totals.TotalGBP,
	}
	if dueDate, ok := earliestInvoiceDueDate(list.Invoices); ok {
		formatted := dueDate.Format(time.DateOnly)
		response.EarliestDueDate = &formatted
	}
	return response, nil
}

func outstandingInvoiceFilter() invoicing.InvoiceListFilter {
	return invoicing.InvoiceListFilter{
		Statuses: []invoicing.InvoiceStatus{
			invoicing.InvoiceStatusSent,
			invoicing.InvoiceStatusOverdue,
		},
	}
}

func earliestInvoiceDueDate(invoices []invoicing.InvoiceListItem) (time.Time, bool) {
	var earliest time.Time
	for _, invoice := range invoices {
		due := dashboardDateOnly(invoice.DueDate)
		if due.IsZero() {
			continue
		}
		if earliest.IsZero() || due.Before(earliest) {
			earliest = due
		}
	}
	return earliest, !earliest.IsZero()
}

func (h dashboardHTTPHandler) dla(ctx context.Context) (dashboardDLAResponse, error) {
	if h.deps.dla == nil {
		return dashboardDLAResponse{}, fmt.Errorf("dashboard: DLA read API is required")
	}
	balance, status, err := h.deps.dla.CurrentBalance(ctx)
	if err != nil {
		return dashboardDLAResponse{}, err
	}
	return dashboardDLAResponse{Balance: balance, Status: status}, nil
}

func (h dashboardHTTPHandler) dividendHeadroom(ctx context.Context) (dashboardDividendHeadroomResponse, error) {
	if h.deps.dividends == nil {
		return dashboardDividendHeadroomResponse{}, fmt.Errorf("dashboard: dividends read API is required")
	}
	headroom, err := h.deps.dividends.Headroom(ctx)
	if err != nil {
		return dashboardDividendHeadroomResponse{}, err
	}
	return dashboardDividendHeadroomResponse{
		Available:     headroom.Available,
		Distributable: headroom.Distributable,
	}, nil
}

func (h dashboardHTTPHandler) recentInvoices(ctx context.Context) (dashboardRecentInvoicesResponse, error) {
	if h.deps.invoicing == nil {
		return nil, fmt.Errorf("dashboard: invoicing read API is required")
	}
	list, err := h.deps.invoicing.List(ctx, invoicing.InvoiceListFilter{Limit: 5})
	if err != nil {
		return nil, err
	}
	response := make(dashboardRecentInvoicesResponse, 0, len(list.Invoices))
	for _, invoice := range list.Invoices {
		var daysOverdue *int
		if invoice.DaysOverdue > 0 || invoice.Status == invoicing.InvoiceStatusOverdue {
			value := invoice.DaysOverdue
			daysOverdue = &value
		}
		response = append(response, dashboardRecentInvoiceResponse{
			ID:          invoice.ID,
			Number:      invoice.Number,
			Client:      invoice.ClientName,
			Amount:      invoice.Totals.Total,
			Status:      invoice.Status,
			DaysOverdue: daysOverdue,
		})
	}
	return response, nil
}

func (h dashboardHTTPHandler) toReconcile(ctx context.Context) (dashboardToReconcileResponse, error) {
	if h.deps.banking == nil {
		return dashboardToReconcileResponse{}, fmt.Errorf("dashboard: banking read API is required")
	}
	accounts, err := h.deps.banking.Accounts(ctx)
	if err != nil {
		return dashboardToReconcileResponse{}, err
	}
	response := dashboardToReconcileResponse{
		Accounts:    make([]dashboardReconcileAccountResponse, 0, len(accounts)),
		ReviewQueue: []dashboardReviewQueueItemResponse{},
	}
	for _, account := range accounts {
		count, err := h.deps.banking.UnreconciledCount(ctx, account.ID)
		if err != nil {
			return dashboardToReconcileResponse{}, err
		}
		response.Accounts = append(response.Accounts, dashboardReconcileAccountResponse{
			ID:                int64(account.ID),
			Name:              account.Name,
			Currency:          account.Currency,
			LedgerAccountCode: account.LedgerAccountCode,
			UnreconciledCount: count,
		})
	}
	queue, err := h.deps.banking.ReviewQueue(ctx)
	if err != nil {
		return dashboardToReconcileResponse{}, err
	}
	response.ReviewQueue = topReviewQueueItems(queue, 3)
	return response, nil
}

func topReviewQueueItems(queue banking.ReviewQueue, limit int) []dashboardReviewQueueItemResponse {
	items := flattenReviewQueue(queue)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Suggestion.Confidence == items[j].Suggestion.Confidence {
			if items[i].Transaction.Date.Equal(items[j].Transaction.Date) {
				return items[i].Transaction.ID > items[j].Transaction.ID
			}
			return items[i].Transaction.Date.After(items[j].Transaction.Date)
		}
		return items[i].Suggestion.Confidence > items[j].Suggestion.Confidence
	})
	if len(items) > limit {
		items = items[:limit]
	}
	response := make([]dashboardReviewQueueItemResponse, 0, len(items))
	for _, item := range items {
		response = append(response, dashboardReviewQueueItemResponse{
			Kind:       item.Suggestion.Kind,
			Payee:      item.Transaction.Payee,
			Amount:     item.Transaction.Amount,
			Confidence: item.Suggestion.Confidence,
		})
	}
	return response
}

func flattenReviewQueue(queue banking.ReviewQueue) []banking.ReviewQueueItem {
	total := len(queue.InvoiceMatches) + len(queue.DLA) + len(queue.PayeeRules)
	items := make([]banking.ReviewQueueItem, 0, total)
	items = append(items, queue.InvoiceMatches...)
	items = append(items, queue.DLA...)
	items = append(items, queue.PayeeRules...)
	return items
}

func (h dashboardHTTPHandler) rate(ctx context.Context) (dashboardRateResponse, error) {
	if h.deps.moneyFX == nil {
		return dashboardRateResponse{}, fmt.Errorf("dashboard: moneyfx read API is required")
	}
	rate, fetchedAt, err := h.deps.moneyFX.TodayRate(ctx, "EUR", "GBP")
	if err != nil {
		return dashboardRateResponse{}, err
	}
	return dashboardRateResponse{
		From:      rate.From,
		To:        rate.To,
		Rate:      rate.Value,
		RateDate:  dashboardDateOnly(rate.RateDate).Format(time.DateOnly),
		FetchedAt: fetchedAt.UTC(),
		Source:    rate.Source,
	}, nil
}

func (h dashboardHTTPHandler) greeting(ctx context.Context) (dashboardGreetingResponse, error) {
	if h.deps.identity == nil {
		return dashboardGreetingResponse{}, fmt.Errorf("dashboard: identity read API is required")
	}
	principalFromContext := h.deps.principal
	if principalFromContext == nil {
		principalFromContext = identity.PrincipalFromContext
	}
	principal, ok := principalFromContext(ctx)
	if !ok {
		return dashboardGreetingResponse{}, fmt.Errorf("dashboard: authenticated principal is required")
	}
	profile, err := h.deps.identity.Profile(ctx)
	if err != nil {
		return dashboardGreetingResponse{}, err
	}
	return dashboardGreetingResponse{
		UserName:    principal.User.Name,
		TradingName: profile.TradingName,
	}, nil
}

func (h dashboardHTTPHandler) now() time.Time {
	if h.deps.clock == nil {
		return time.Now().UTC()
	}
	return h.deps.clock.Now().UTC()
}

func dashboardDateOnly(date time.Time) time.Time {
	if date.IsZero() {
		return time.Time{}
	}
	year, month, day := date.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func emptyMoneySlice(items []money.Money) []money.Money {
	if items == nil {
		return []money.Money{}
	}
	return items
}

func writeDashboardJSON(w nethttp.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		panic(err)
	}
}
