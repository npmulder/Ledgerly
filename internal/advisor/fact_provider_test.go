package advisor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/dividends"
	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/moneyfx"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/reports"
)

func TestInvoicingFactProviderMapsOverdueInvoices(t *testing.T) {
	dueDate := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	provider := NewInvoicingFactProvider(fakeInvoicingReadAPI{
		overdue: []invoicing.OverdueInvoiceFact{{
			InvoiceID:     "inv_123",
			InvoiceNumber: "2026-0001",
			ClientName:    "Acme Ltd",
			DueDate:       dueDate,
			DaysOverdue:   5,
			Amount:        money.Money{Amount: 120000, Currency: "GBP"},
		}},
		recurringDrafts: []invoicing.RecurringDraftInvoiceFact{{
			InvoiceID:  "draft_456",
			ClientName: "Fabrikam Ltd",
			RunDate:    time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			Amount:     money.Money{Amount: 150000, Currency: "GBP"},
		}},
	})

	facts, err := provider.Gather(context.Background())
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	overdue := facts[FactInvoicesOverdue].([]OverdueInvoiceFact)
	if len(overdue) != 1 {
		t.Fatalf("invoices.overdue length = %d, want 1", len(overdue))
	}
	got := overdue[0]
	if got.ID != "inv_123" || got.Number != "2026-0001" || got.Client != "Acme Ltd" {
		t.Fatalf("overdue invoice identity = %#v", got)
	}
	if got.DaysOverdue != 5 {
		t.Fatalf("DaysOverdue = %d, want 5", got.DaysOverdue)
	}
	if got.Amount != (money.Money{Amount: 120000, Currency: "GBP"}) {
		t.Fatalf("Amount = %#v, want GBP 1200.00", got.Amount)
	}
	if got := facts[FactInvoiceCount]; got != 1 {
		t.Fatalf("count = %#v, want 1", got)
	}
	if got := facts[FactInvoiceClientName]; got != "Acme Ltd" {
		t.Fatalf("client_name = %#v, want Acme Ltd", got)
	}
	if got := facts[FactInvoiceDaysOverdue]; got != 5 {
		t.Fatalf("days_overdue = %#v, want 5", got)
	}
	if got := facts[FactInvoiceID]; got != "inv_123" {
		t.Fatalf("invoice_id = %#v, want inv_123", got)
	}
	if got := facts[FactInvoiceNumber]; got != "2026-0001" {
		t.Fatalf("invoice_number = %#v, want 2026-0001", got)
	}
	recurring := facts[FactRecurringDrafts].([]RecurringDraftFact)
	if len(recurring) != 1 {
		t.Fatalf("invoices.recurringDrafts length = %d, want 1", len(recurring))
	}
	if got := recurring[0]; got.ID != "draft_456" || got.Client != "Fabrikam Ltd" {
		t.Fatalf("recurring draft identity = %#v", got)
	}
	if got := facts[FactRecurringDraftCount]; got != 1 {
		t.Fatalf("recurring_draft_count = %#v, want 1", got)
	}
	if got := facts[FactRecurringDraftClientName]; got != "Fabrikam Ltd" {
		t.Fatalf("recurring_draft_client_name = %#v, want Fabrikam Ltd", got)
	}
	if got := facts[FactRecurringDraftInvoiceID]; got != "draft_456" {
		t.Fatalf("recurring_draft_invoice_id = %#v, want draft_456", got)
	}
}

func TestDLAFactProviderMapsOverdrawnStatus(t *testing.T) {
	provider := NewDLAFactProvider(fakeDLAReadAPI{
		statuses: []dla.StatusPayload{{
			DirectorID:   dla.DefaultDirectorID,
			DirectorName: "N. Meyer",
			Balance:      money.Money{Amount: -50000, Currency: "GBP"},
			Status:       dla.StatusOverdrawn,
		}},
	})

	facts, err := provider.Gather(context.Background())
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	if got := facts[FactDLABalance]; got != (money.Money{Amount: -50000, Currency: "GBP"}) {
		t.Fatalf("dla.balance = %#v", got)
	}
	if got := facts[FactDLAStatus]; got != "overdrawn" {
		t.Fatalf("dla.status = %#v, want overdrawn", got)
	}
	if got := facts[FactDLASuggestedClearance]; got != (money.Money{Amount: 50000, Currency: "GBP"}) {
		t.Fatalf("dla.suggestedClearance = %#v", got)
	}
	if got := facts[FactRuleDLABalance]; got != (money.Money{Amount: -50000, Currency: "GBP"}) {
		t.Fatalf("balance = %#v", got)
	}
	if got := facts[FactRuleDLAStatus]; got != "overdrawn" {
		t.Fatalf("status = %#v, want overdrawn", got)
	}
	if got := facts[FactRuleDLADirectorName]; got != "N. Meyer" {
		t.Fatalf("director_name = %#v, want N. Meyer", got)
	}
	statuses := facts[FactDLADirectorStatuses].([]DLADirectorStatusFact)
	if len(statuses) != 1 || statuses[0].DirectorID != "director-1" || statuses[0].DirectorName != "N. Meyer" {
		t.Fatalf("dla.statuses = %#v, want N. Meyer director-1", statuses)
	}
}

func TestDividendsFactProviderMapsHeadroom(t *testing.T) {
	if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("LoadActive() error = %v", err)
	}
	provider := NewDividendsFactProvider(fakeDividendsReadAPI{
		headroom: dividends.HeadroomBreakdown{
			AsOf:          time.Date(2025, 7, 6, 0, 0, 0, 0, time.UTC),
			FinancialYear: "2025-26",
			Available:     money.Money{Amount: 250000, Currency: "GBP"},
			Distributable: true,
		},
		declared: money.Money{Amount: 2000000, Currency: "GBP"},
	})

	facts, err := provider.Gather(context.Background())
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	if got := facts[FactDividendsHeadroom]; got != (money.Money{Amount: 250000, Currency: "GBP"}) {
		t.Fatalf("dividends.headroom = %#v", got)
	}
	if got := facts[FactDividendsDistributable]; got != true {
		t.Fatalf("dividends.distributable = %#v, want true", got)
	}
	if got := facts[FactDividendHeadroom]; got != (money.Money{Amount: 250000, Currency: "GBP"}) {
		t.Fatalf("dividend_headroom = %#v", got)
	}
	if got := facts[FactDividendHeadroomMinor]; got != int64(250000) {
		t.Fatalf("headroom_minor_units = %#v, want 250000", got)
	}
	if got := facts[FactDividendsYTD]; got != (money.Money{Amount: 2000000, Currency: "GBP"}) {
		t.Fatalf("dividends_ytd = %#v", got)
	}
	if got := facts[FactDividendEstimate]; got != (money.Money{Amount: 38750, Currency: "GBP"}) {
		t.Fatalf("estimate = %#v, want GBP 387.50", got)
	}
	if got := facts[FactDividendEstimateMinor]; got != int64(38750) {
		t.Fatalf("estimate_minor_units = %#v, want 38750", got)
	}
}

func TestReportsFactProviderMapsVATAndFilings(t *testing.T) {
	dueDate := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	provider := NewReportsFactProvider(fakeReportsReadAPI{
		position: reports.VATPosition{
			Status: reports.VATRegistrationRegistered,
			Figures: &reports.VATFigures{
				NetPosition: money.Money{Amount: 42000, Currency: "GBP"},
			},
			DueDate: &dueDate,
		},
		filings: []reports.Filing{{
			Key:        "vat_return",
			Label:      "VAT return",
			Authority:  "Isle of Man Customs & Excise",
			DueDate:    dueDate,
			DaysUntil:  24,
			Status:     reports.FilingStatusDueSoon,
			WarnWindow: 30,
		}},
	})

	facts, err := provider.Gather(context.Background())
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	position := facts[FactVATPosition].(reports.VATPosition)
	if position.Figures == nil {
		t.Fatal("vat.position figures = nil, want registered VAT figures")
	}
	if got := position.Figures.NetPosition; got != (money.Money{Amount: 42000, Currency: "GBP"}) {
		t.Fatalf("vat.position net = %#v", got)
	}
	if got := facts[FactVATDueDate]; got != dueDate {
		t.Fatalf("vat.dueDate = %#v, want %s", got, dueDate.Format(time.DateOnly))
	}
	filings := facts[FactFilings].([]FilingFact)
	if len(filings) != 1 {
		t.Fatalf("filings length = %d, want 1", len(filings))
	}
	if filings[0].Key != "vat_return" || filings[0].Authority != "Isle of Man Customs & Excise" || filings[0].WarnWindow != Days(30) {
		t.Fatalf("filing fact = %#v", filings[0])
	}
	if got := facts[FactFilingAuthority]; got != "Isle of Man Customs & Excise" {
		t.Fatalf("authority = %#v, want Isle of Man Customs & Excise", got)
	}
	if got := facts[FactFilingDueDate]; got != dueDate {
		t.Fatalf("due_date = %#v, want %s", got, dueDate.Format(time.DateOnly))
	}
	if got := facts[FactFilingDaysUntil]; got != 24 {
		t.Fatalf("days_until = %#v, want 24", got)
	}
	if got := facts[FactFilingName]; got != "VAT return" {
		t.Fatalf("filing_name = %#v, want VAT return", got)
	}
	if got := facts[FactFilingStatus]; got != "due-soon" {
		t.Fatalf("filing_status = %#v, want due-soon", got)
	}
	if got := facts[FactFilingWarnWindow]; got != Days(30) {
		t.Fatalf("warn_window_days = %#v, want 30", got)
	}
}

func TestReportsSplitProvidersKeepFilingsWhenVATFails(t *testing.T) {
	vatErr := errors.New("vat unavailable")
	dueDate := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	api := fakeReportsReadAPI{
		vatErr: vatErr,
		filings: []reports.Filing{{
			Key:       "annual_return",
			Label:     "Annual return",
			Authority: "IoM Companies Registry",
			DueDate:   dueDate,
		}},
	}
	registry := NewFactRegistry(
		RegisteredFactProvider{Name: "reports.vat", Provider: NewReportsVATFactProvider(api)},
		RegisteredFactProvider{Name: "reports.filings", Provider: NewReportsFilingFactProvider(api)},
	)

	facts, report := registry.GatherAll(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, ok := facts[FactVATPosition]; ok {
		t.Fatalf("vat.position gathered despite VAT error")
	}
	if _, ok := facts[FactFilings]; !ok {
		t.Fatalf("filings missing despite filing provider success")
	}
	if got := facts[FactFilingAuthority]; got != "IoM Companies Registry" {
		t.Fatalf("authority = %#v, want IoM Companies Registry", got)
	}
	if got := facts[FactFilingStatus]; got != "" {
		t.Fatalf("filing_status = %#v, want empty status from fixture", got)
	}
	if len(report.Providers) != 2 {
		t.Fatalf("providers reported = %d, want 2", len(report.Providers))
	}
	if report.Providers[0].Name != "reports.vat" || !errors.Is(report.Providers[0].Err, vatErr) {
		t.Fatalf("VAT provider report = %#v", report.Providers[0])
	}
	if report.Providers[1].Err != nil {
		t.Fatalf("filing provider error = %v, want nil", report.Providers[1].Err)
	}
}

func TestMoneyFXFactProviderMapsStaleRates(t *testing.T) {
	lastDate := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	provider := NewMoneyFXFactProvider(fakeMoneyFXReadAPI{
		staleness: moneyfx.RateStaleness{LastDate: &lastDate, Stale: true, StaleDays: 6},
	})

	facts, err := provider.Gather(context.Background())
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	if got := facts[FactRatesLastDate]; got != lastDate {
		t.Fatalf("rates.lastDate = %#v, want %s", got, lastDate.Format(time.DateOnly))
	}
	if got := facts[FactRatesStale]; got != true {
		t.Fatalf("rates.stale = %#v, want true", got)
	}
	if got := facts[FactStaleDays]; got != 6 {
		t.Fatalf("stale_days = %#v, want 6", got)
	}
}

func TestIdentityFactProviderMapsCompanyFacts(t *testing.T) {
	incorporated := time.Date(2025, 8, 14, 0, 0, 0, 0, time.UTC)
	provider := NewIdentityFactProvider(fakeIdentityReadAPI{
		facts: identity.CompanyFacts{
			IncorporationDate: incorporated,
			YearEnd:           identity.YearEnd{Month: time.March, Day: 31},
			IsVATRegistered:   true,
		},
	})

	facts, err := provider.Gather(context.Background())
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	if got := facts[FactCompanyIncorporationDate]; got != incorporated {
		t.Fatalf("company.incorporationDate = %#v", got)
	}
	if got := facts[FactCompanyYearEnd]; got != (CompanyYearEndFact{Month: 3, Day: 31}) {
		t.Fatalf("company.yearEnd = %#v", got)
	}
	if got := facts[FactCompanyYearEndMonth]; got != 3 {
		t.Fatalf("company.yearEnd.month = %#v", got)
	}
	if got := facts[FactCompanyYearEndDay]; got != 31 {
		t.Fatalf("company.yearEnd.day = %#v", got)
	}
	if got := facts[FactCompanyVATRegistered]; got != true {
		t.Fatalf("company.isVATRegistered = %#v", got)
	}
}

func TestGatherAllPartialFailureRecordsErrorAndEvaluationContinues(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	registry := NewFactRegistry(
		RegisteredFactProvider{
			Name:     "good",
			Provider: stubFactProvider{keys: []FactKey{"good.value"}, facts: map[FactKey]FactValue{"good.value": 3}},
		},
		RegisteredFactProvider{
			Name:     "bad",
			Provider: stubFactProvider{keys: []FactKey{"bad.value"}, err: providerErr},
		},
	)

	facts, report := registry.GatherAll(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if got := facts[FactKey("good.value")]; got != 3 {
		t.Fatalf("good.value = %#v, want 3", got)
	}
	if _, ok := facts[FactKey("bad.value")]; ok {
		t.Fatalf("bad.value gathered despite provider error")
	}
	if len(report.Providers) != 2 {
		t.Fatalf("providers reported = %d, want 2", len(report.Providers))
	}
	if report.Providers[1].Name != "bad" || !errors.Is(report.Providers[1].Err, providerErr) {
		t.Fatalf("bad provider report = %#v", report.Providers[1])
	}

	goodRule := compileTestRule(t, RuleDef{
		ID:           "good-rule",
		Severity:     SeverityTeal,
		Surfaces:     []Surface{SurfaceDashboard},
		FactQuery:    []FactKey{"good.value"},
		Condition:    "good.value > 0",
		TextTemplate: "good insight",
		CTA:          CTA{Label: "Open", Action: "test.open"},
	})
	badRule := compileTestRule(t, RuleDef{
		ID:           "bad-rule",
		Severity:     SeverityAmber,
		Surfaces:     []Surface{SurfaceDashboard},
		FactQuery:    []FactKey{"bad.value"},
		Condition:    "bad.value > 0",
		TextTemplate: "bad insight",
		CTA:          CTA{Label: "Open", Action: "test.open"},
	})

	delta, err := Evaluate([]RuleDef{badRule, goodRule}, facts, time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(delta.Insights) != 1 || delta.Insights[0].RuleID != "good-rule" {
		t.Fatalf("insights = %#v, want only good-rule", delta.Insights)
	}
	if len(delta.Warnings) != 1 || delta.Warnings[0].RuleID != "bad-rule" {
		t.Fatalf("warnings = %#v, want bad-rule unknown fact warning", delta.Warnings)
	}
}

type fakeInvoicingReadAPI struct {
	overdue         []invoicing.OverdueInvoiceFact
	recurringDrafts []invoicing.RecurringDraftInvoiceFact
	err             error
}

func (f fakeInvoicingReadAPI) OverdueInvoices(context.Context) ([]invoicing.OverdueInvoiceFact, error) {
	return f.overdue, f.err
}

func (f fakeInvoicingReadAPI) RecurringDraftInvoices(context.Context) ([]invoicing.RecurringDraftInvoiceFact, error) {
	return f.recurringDrafts, f.err
}

type fakeDLAReadAPI struct {
	statuses []dla.StatusPayload
	err      error
}

func (f fakeDLAReadAPI) Statuses(context.Context) ([]dla.StatusPayload, error) {
	return f.statuses, f.err
}

type fakeDividendsReadAPI struct {
	headroom dividends.HeadroomBreakdown
	declared money.Money
	err      error
}

func (f fakeDividendsReadAPI) Headroom(context.Context) (dividends.HeadroomBreakdown, error) {
	return f.headroom, f.err
}

func (f fakeDividendsReadAPI) DeclaredInYear(context.Context, string) (money.Money, error) {
	return f.declared, f.err
}

type fakeReportsReadAPI struct {
	position  reports.VATPosition
	filings   []reports.Filing
	vatErr    error
	filingErr error
}

func (f fakeReportsReadAPI) VATPosition(context.Context) (reports.VATPosition, error) {
	return f.position, f.vatErr
}

func (f fakeReportsReadAPI) FilingCalendarContext(context.Context) ([]reports.Filing, error) {
	return f.filings, f.filingErr
}

type fakeMoneyFXReadAPI struct {
	staleness moneyfx.RateStaleness
	err       error
}

func (f fakeMoneyFXReadAPI) RateStaleness(context.Context) (moneyfx.RateStaleness, error) {
	return f.staleness, f.err
}

type fakeIdentityReadAPI struct {
	facts identity.CompanyFacts
	err   error
}

func (f fakeIdentityReadAPI) CompanyFacts(context.Context) (identity.CompanyFacts, error) {
	return f.facts, f.err
}

type stubFactProvider struct {
	keys  []FactKey
	facts map[FactKey]FactValue
	err   error
}

func (p stubFactProvider) Keys() []FactKey {
	return p.keys
}

func (p stubFactProvider) Gather(context.Context) (map[FactKey]FactValue, error) {
	return p.facts, p.err
}
