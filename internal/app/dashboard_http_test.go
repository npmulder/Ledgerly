package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestDashboardSummaryPartialFailureReturnsNullSectionAndErrors(t *testing.T) {
	router := newDashboardTestRouter(t, dashboardTestDeps{
		dividendsErr: errors.New("headroom source failed"),
	})

	response := performDashboardRequest(router, true)
	if response.Code != nethttp.StatusOK {
		t.Fatalf("summary status = %d, want %d; body=%s", response.Code, nethttp.StatusOK, response.Body.String())
	}

	var body dashboardSummaryResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode summary: %v; body=%s", err, response.Body.String())
	}
	if body.Cash == nil || body.Outstanding == nil || body.DLA == nil || body.RecentInvoices == nil || body.ToReconcile == nil || body.Rate == nil || body.Greeting == nil {
		t.Fatalf("healthy sections were not preserved: %+v", body)
	}
	if body.DividendHeadroom != nil {
		t.Fatalf("dividendHeadroom = %+v, want null", body.DividendHeadroom)
	}
	if len(body.Errors) != 1 || body.Errors[0].Section != "dividendHeadroom" || body.Errors[0].Detail == "" {
		t.Fatalf("errors = %+v, want dividendHeadroom error", body.Errors)
	}
}

func TestDashboardSummaryReturnsUnavailableWhenEverySectionFails(t *testing.T) {
	router := newDashboardTestRouter(t, dashboardTestDeps{
		bankingErr:         errors.New("banking failed"),
		ledgerErr:          errors.New("ledger failed"),
		moneyFXErr:         errors.New("fx failed"),
		invoicingListErr:   errors.New("invoice list failed"),
		invoicingTotalsErr: errors.New("invoice totals failed"),
		dlaErr:             errors.New("dla failed"),
		dividendsErr:       errors.New("dividends failed"),
		identityErr:        errors.New("identity failed"),
	})

	response := performDashboardRequest(router, true)
	if response.Code != nethttp.StatusServiceUnavailable {
		t.Fatalf("summary status = %d, want %d; body=%s", response.Code, nethttp.StatusServiceUnavailable, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
		t.Fatalf("Content-Type = %q, want %s", got, httpserver.ProblemContentType)
	}
}

func TestDashboardSummaryRequiresAuthentication(t *testing.T) {
	router := newDashboardTestRouter(t, dashboardTestDeps{})

	response := performDashboardRequest(router, false)
	if response.Code != nethttp.StatusUnauthorized {
		t.Fatalf("summary status = %d, want %d; body=%s", response.Code, nethttp.StatusUnauthorized, response.Body.String())
	}
}

func TestDashboardOpenAPIFragmentDocumentsSummaryPath(t *testing.T) {
	document := httpserver.OpenAPIDocument("test", dashboardOpenAPIFragment())
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatalf("openapi paths missing or wrong type: %+v", document["paths"])
	}
	if _, ok := paths["/api/dashboard/summary"]; !ok {
		t.Fatalf("openapi path /api/dashboard/summary missing from %+v", paths)
	}
}

type dashboardTestDeps struct {
	bankingErr         error
	ledgerErr          error
	moneyFXErr         error
	invoicingListErr   error
	invoicingTotalsErr error
	dlaErr             error
	dividendsErr       error
	identityErr        error
}

func newDashboardTestRouter(t *testing.T, testDeps dashboardTestDeps) nethttp.Handler {
	t.Helper()

	account := banking.BankAccount{
		ID:                1,
		Name:              "Revolut GBP",
		Provider:          banking.ProviderRevolut,
		Currency:          "GBP",
		LedgerAccountCode: "1000-cash-test",
	}
	invoiceNumber := "INV-2025-01"
	return httpserver.NewRouter(httpserver.Config{
		Version: "test",
		DB:      pingerFunc(func(context.Context) error { return nil }),
		APIAuth: testAuthMiddleware,
		Modules: []httpserver.Module{
			dashboardHTTPModule(dashboardDependencies{
				clock: fixedDashboardClock{},
				ledger: dashboardFakeLedger{
					balances: map[ledger.AccountCode]ledger.AccountBalance{
						account.LedgerAccountCode: {
							AccountCode: account.LedgerAccountCode,
							AccountName: "Cash - Revolut GBP",
							AccountType: ledger.AccountTypeAsset,
							Native:      []money.Money{{Amount: 100_00, Currency: "GBP"}},
							AmountGBP:   money.Money{Amount: 100_00, Currency: "GBP"},
						},
					},
					err: testDeps.ledgerErr,
				},
				moneyFX: dashboardFakeMoneyFX{err: testDeps.moneyFXErr},
				invoicing: dashboardFakeInvoicing{
					list: invoicing.InvoiceListResult{
						Invoices: []invoicing.InvoiceListItem{{
							Number:      &invoiceNumber,
							ClientName:  "Contoso",
							Status:      invoicing.InvoiceStatusSent,
							DueDate:     time.Date(2025, 7, 30, 0, 0, 0, 0, time.UTC),
							Currency:    invoicing.CurrencyGBP,
							Totals:      invoicing.InvoiceTotals{Total: invoicing.Money{Amount: 100_00, Currency: "GBP"}},
							DaysOverdue: 0,
						}},
						TotalCount: 1,
					},
					totals: invoicing.InvoiceTotalsSummary{
						Subtotals: []invoicing.Money{{Amount: 100_00, Currency: "GBP"}},
						TotalGBP:  invoicing.Money{Amount: 100_00, Currency: "GBP"},
					},
					listErr:   testDeps.invoicingListErr,
					totalsErr: testDeps.invoicingTotalsErr,
				},
				dla: dashboardFakeDLA{
					balance: money.Money{Amount: 25_00, Currency: "GBP"},
					status:  dla.StatusCredit,
					err:     testDeps.dlaErr,
				},
				dividends: dashboardFakeDividends{
					headroom: dividends.HeadroomBreakdown{
						Available:     money.Money{Amount: 50_00, Currency: "GBP"},
						Distributable: true,
					},
					err: testDeps.dividendsErr,
				},
				banking: dashboardFakeBanking{
					accounts: []banking.BankAccount{account},
					counts:   map[banking.AccountID]int{account.ID: 1},
					queue: banking.ReviewQueue{
						InvoiceMatches: []banking.ReviewQueueItem{{
							Transaction: banking.Transaction{
								ID:        10,
								AccountID: account.ID,
								Date:      time.Date(2025, 7, 5, 0, 0, 0, 0, time.UTC),
								Amount:    money.Money{Amount: 100_00, Currency: "GBP"},
								Payee:     "Contoso",
							},
							Suggestion: banking.Suggestion{
								Kind:       banking.SuggestionKindInvoiceMatch,
								Confidence: 0.98,
							},
						}},
					},
					err: testDeps.bankingErr,
				},
				identity: dashboardFakeIdentity{
					profile: identity.CompanyProfile{TradingName: "NPM Limited"},
					err:     testDeps.identityErr,
				},
				principal: func(context.Context) (identity.Principal, bool) {
					return identity.Principal{User: identity.User{Name: "Owner"}}, true
				},
			}),
		},
		OpenAPIFragments: []httpserver.OpenAPIFragment{dashboardOpenAPIFragment()},
	})
}

func performDashboardRequest(router nethttp.Handler, authenticated bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(nethttp.MethodGet, "/api/dashboard/summary", bytes.NewReader(nil))
	if authenticated {
		request.AddCookie(&nethttp.Cookie{Name: "test_session", Value: "ok"})
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

type fixedDashboardClock struct{}

func (fixedDashboardClock) Now() time.Time {
	return time.Date(2025, time.July, 6, 9, 0, 0, 0, time.UTC)
}

type dashboardFakeLedger struct {
	balances map[ledger.AccountCode]ledger.AccountBalance
	err      error
}

func (f dashboardFakeLedger) AccountBalance(_ context.Context, code ledger.AccountCode, _ time.Time) (ledger.AccountBalance, error) {
	if f.err != nil {
		return ledger.AccountBalance{}, f.err
	}
	return f.balances[code], nil
}

type dashboardFakeMoneyFX struct {
	err error
}

func (f dashboardFakeMoneyFX) TodayRate(context.Context, string, string) (moneyfx.Rate, time.Time, error) {
	if f.err != nil {
		return moneyfx.Rate{}, time.Time{}, f.err
	}
	return moneyfx.Rate{
		From:     "EUR",
		To:       "GBP",
		Value:    "0.85",
		RateDate: time.Date(2025, time.July, 4, 0, 0, 0, 0, time.UTC),
		Source:   "test",
	}, time.Date(2025, time.July, 6, 9, 0, 0, 0, time.UTC), nil
}

func (f dashboardFakeMoneyFX) ToGBP(_ context.Context, amount money.Money, _ time.Time) (money.Money, error) {
	if f.err != nil {
		return money.Money{}, f.err
	}
	if amount.Currency == "GBP" {
		return amount, nil
	}
	converted := amount.MulRat(big.NewRat(85, 100))
	converted.Currency = "GBP"
	return converted, nil
}

type dashboardFakeInvoicing struct {
	list      invoicing.InvoiceListResult
	totals    invoicing.InvoiceTotalsSummary
	listErr   error
	totalsErr error
}

func (f dashboardFakeInvoicing) List(context.Context, invoicing.InvoiceListFilter) (invoicing.InvoiceListResult, error) {
	if f.listErr != nil {
		return invoicing.InvoiceListResult{}, f.listErr
	}
	return f.list, nil
}

func (f dashboardFakeInvoicing) Totals(context.Context, invoicing.InvoiceListFilter) (invoicing.InvoiceTotalsSummary, error) {
	if f.totalsErr != nil {
		return invoicing.InvoiceTotalsSummary{}, f.totalsErr
	}
	return f.totals, nil
}

type dashboardFakeDLA struct {
	balance money.Money
	status  dla.Status
	err     error
}

func (f dashboardFakeDLA) CurrentBalance(context.Context) (money.Money, dla.Status, error) {
	if f.err != nil {
		return money.Money{}, "", f.err
	}
	return f.balance, f.status, nil
}

type dashboardFakeDividends struct {
	headroom dividends.HeadroomBreakdown
	err      error
}

func (f dashboardFakeDividends) Headroom(context.Context) (dividends.HeadroomBreakdown, error) {
	if f.err != nil {
		return dividends.HeadroomBreakdown{}, f.err
	}
	return f.headroom, nil
}

type dashboardFakeBanking struct {
	accounts []banking.BankAccount
	counts   map[banking.AccountID]int
	queue    banking.ReviewQueue
	err      error
}

func (f dashboardFakeBanking) Accounts(context.Context) ([]banking.BankAccount, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.accounts, nil
}

func (f dashboardFakeBanking) ReviewQueue(context.Context) (banking.ReviewQueue, error) {
	if f.err != nil {
		return banking.ReviewQueue{}, f.err
	}
	return f.queue, nil
}

func (f dashboardFakeBanking) UnreconciledCount(_ context.Context, accountID banking.AccountID) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.counts[accountID], nil
}

type dashboardFakeIdentity struct {
	profile identity.CompanyProfile
	err     error
}

func (f dashboardFakeIdentity) Profile(context.Context) (identity.CompanyProfile, error) {
	if f.err != nil {
		return identity.CompanyProfile{}, f.err
	}
	return f.profile, nil
}
