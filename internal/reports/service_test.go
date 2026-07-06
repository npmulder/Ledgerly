package reports

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

func TestProfitYTDUsesCompanyYearEndBoundariesAndMatchesPLNet(t *testing.T) {
	loadReportsPack(t, "")
	ctx := context.Background()
	fakeLedger := newFakeLedger(
		fakeEntry(1, "2025-04-01", "manual", "from-boundary", fakePosting("4000-sales", -10_000)),
		fakeEntry(2, "2026-03-31", "manual", "to-boundary", fakePosting("4000-sales", -20_000)),
		fakeEntry(3, "2026-04-01", "manual", "outside", fakePosting("4000-sales", -40_000)),
	)
	service := New(
		fakeLedger,
		fakeIdentity{yearEnd: identity.YearEnd{Month: time.March, Day: 31}},
		fakeInvoicing{},
	)

	ytd, err := service.ProfitYTD(ctx, "2025-26")
	if err != nil {
		t.Fatalf("ProfitYTD() error = %v", err)
	}
	assertReportMoney(t, ytd, 30_000)
	assertDate(t, fakeLedger.lastBalancesFrom, "2025-04-01")
	assertDate(t, fakeLedger.lastBalancesTo, "2026-03-31")

	pl, err := service.ProfitAndLoss(ctx, Period{
		From: time.Date(2025, time.April, 1, 12, 0, 0, 0, time.UTC),
		To:   time.Date(2026, time.March, 31, 23, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ProfitAndLoss() error = %v", err)
	}
	if ytd != pl.NetProfit {
		t.Fatalf("ProfitYTD = %+v, ProfitAndLoss net = %+v", ytd, pl.NetProfit)
	}
	if len(pl.Income) != 1 || pl.Income[0].Label != otherIncomeLabel {
		t.Fatalf("Income = %#v, want one Other income line", pl.Income)
	}
	assertReportMoney(t, pl.Income[0].Amount, 30_000)
}

func TestProfitYTDClampsLeapDayYearEndInNonLeapYears(t *testing.T) {
	loadReportsPack(t, "")
	ctx := context.Background()
	fakeLedger := newFakeLedger(
		fakeEntry(1, "2025-02-28", "manual", "before-window", fakePosting("4000-sales", -1_000)),
		fakeEntry(2, "2025-03-01", "manual", "window-start", fakePosting("4000-sales", -10_000)),
		fakeEntry(3, "2026-02-28", "manual", "window-end", fakePosting("4000-sales", -20_000)),
		fakeEntry(4, "2026-03-01", "manual", "after-window", fakePosting("4000-sales", -40_000)),
	)
	service := New(
		fakeLedger,
		fakeIdentity{yearEnd: identity.YearEnd{Month: time.February, Day: 29}},
		fakeInvoicing{},
	)

	ytd, err := service.ProfitYTD(ctx, "2025-26")
	if err != nil {
		t.Fatalf("ProfitYTD(2025-26) error = %v", err)
	}
	assertReportMoney(t, ytd, 30_000)
	assertDate(t, fakeLedger.lastBalancesFrom, "2025-03-01")
	assertDate(t, fakeLedger.lastBalancesTo, "2026-02-28")

	period, err := financialYearPeriod("2023-24", time.February, 29)
	if err != nil {
		t.Fatalf("financialYearPeriod(2023-24) error = %v", err)
	}
	assertDate(t, period.From, "2023-03-01")
	assertDate(t, period.To, "2024-02-29")
}

func TestProfitAndLossSkipsNetZeroMissingInvoiceAttribution(t *testing.T) {
	loadReportsPack(t, "")
	ctx := context.Background()
	sourceRef := "invoice:INV-2025-0001:send"
	service := New(
		newFakeLedger(
			fakeEntry(1, "2025-05-01", invoicing.ModuleName, sourceRef, fakePosting("4000-sales", -100_000)),
			fakeEntry(2, "2025-05-01", invoicing.ModuleName, sourceRef, fakePosting("4000-sales", 100_000)),
		),
		fakeIdentity{yearEnd: identity.YearEnd{Month: time.March, Day: 31}},
		fakeInvoicing{},
	)

	pl, err := service.ProfitAndLoss(ctx, Period{
		From: time.Date(2025, time.May, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2025, time.May, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ProfitAndLoss() error = %v", err)
	}
	assertReportMoney(t, pl.IncomeTotal, 0)
	assertReportMoney(t, pl.NetProfit, 0)
	if len(pl.Income) != 0 {
		t.Fatalf("Income = %#v, want no zero-value invoice line", pl.Income)
	}
}

func TestProfitAndLossCorporateTaxLineUsesActivePackRate(t *testing.T) {
	loadReportsPack(t, "standard_rate: \"0.1\"")
	ctx := context.Background()
	service := New(
		newFakeLedger(fakeEntry(1, "2025-05-01", "manual", "income", fakePosting("4000-sales", -100_000))),
		fakeIdentity{yearEnd: identity.YearEnd{Month: time.March, Day: 31}},
		fakeInvoicing{},
	)

	pl, err := service.ProfitAndLoss(ctx, Period{
		From: time.Date(2025, time.May, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2025, time.May, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ProfitAndLoss() error = %v", err)
	}
	if pl.CorporateTax.Label != "IoM income tax at 10%" {
		t.Fatalf("CorporateTax.Label = %q, want IoM income tax at 10%%", pl.CorporateTax.Label)
	}
	if pl.CorporateTax.Rate != "0.1" {
		t.Fatalf("CorporateTax.Rate = %q, want pack rate", pl.CorporateTax.Rate)
	}
	assertReportMoney(t, pl.ProfitBeforeTax, 100_000)
	assertReportMoney(t, pl.CorporateTax.Amount, 10_000)
	assertReportMoney(t, pl.NetProfit, 90_000)
}

func loadReportsPack(t *testing.T, corporateRateLine string) {
	t.Helper()

	path := filepath.Join("..", "..", "packs", "isle-of-man", "1.0", "pack.yaml")
	pack, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pack fixture: %v", err)
	}
	if corporateRateLine != "" {
		pack = []byte(strings.Replace(string(pack), "standard_rate: \"0.0\"", corporateRateLine, 1))
	}
	files := fstest.MapFS{
		"packs/isle-of-man/1.0/pack.yaml": {Data: pack},
	}
	if err := jurisdiction.LoadActiveFromFS(files, jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("LoadActiveFromFS() error = %v", err)
	}
}

type fakeLedger struct {
	accounts         []ledger.Account
	entries          []ledger.JournalEntry
	lastBalancesFrom time.Time
	lastBalancesTo   time.Time
}

func newFakeLedger(entries ...ledger.JournalEntry) *fakeLedger {
	accounts := []ledger.Account{
		{Code: "4000-sales", Name: "Sales", Type: ledger.AccountTypeIncome},
		{Code: realisedFXAccount, Name: "Realised FX gain/loss", Type: ledger.AccountTypeIncome},
		{Code: "5010-software", Name: "Software", Type: ledger.AccountTypeExpense},
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Date.Equal(entries[j].Date) {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].Date.Before(entries[j].Date)
	})
	return &fakeLedger{accounts: accounts, entries: entries}
}

func (f *fakeLedger) Accounts(context.Context) ([]ledger.Account, error) {
	return append([]ledger.Account(nil), f.accounts...), nil
}

func (f *fakeLedger) BalancesByType(_ context.Context, from time.Time, to time.Time) ([]ledger.AccountBalance, error) {
	f.lastBalancesFrom = from
	f.lastBalancesTo = to
	byCode := map[ledger.AccountCode]ledger.Account{}
	for _, account := range f.accounts {
		byCode[account.Code] = account
	}
	income := money.Zero(gbpCurrency)
	expense := money.Zero(gbpCurrency)
	for _, entry := range f.entriesInWindow(from, to, nil) {
		for _, posting := range entry.Postings {
			account := byCode[posting.AccountCode]
			switch account.Type {
			case ledger.AccountTypeIncome:
				next, err := income.Add(posting.AmountGBP)
				if err != nil {
					return nil, err
				}
				income = next
			case ledger.AccountTypeExpense:
				next, err := expense.Add(posting.AmountGBP)
				if err != nil {
					return nil, err
				}
				expense = next
			}
		}
	}
	return []ledger.AccountBalance{
		{AccountType: ledger.AccountTypeIncome, AmountGBP: income},
		{AccountType: ledger.AccountTypeExpense, AmountGBP: expense},
	}, nil
}

func (f *fakeLedger) Entries(_ context.Context, filter ledger.EntryFilter) ([]ledger.JournalEntry, error) {
	var from, to time.Time
	if filter.From != nil {
		from = *filter.From
	}
	if filter.To != nil {
		to = *filter.To
	}
	entries := f.entriesInWindow(from, to, filter.After)
	if filter.Limit > 0 && len(entries) > filter.Limit {
		entries = entries[:filter.Limit]
	}
	return append([]ledger.JournalEntry(nil), entries...), nil
}

func (f *fakeLedger) entriesInWindow(from time.Time, to time.Time, after *ledger.EntryCursor) []ledger.JournalEntry {
	var out []ledger.JournalEntry
	for _, entry := range f.entries {
		if !from.IsZero() && entry.Date.Before(from) {
			continue
		}
		if !to.IsZero() && entry.Date.After(to) {
			continue
		}
		if after != nil && (entry.Date.Before(after.Date) || entry.Date.Equal(after.Date) && entry.ID <= after.ID) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

type fakeIdentity struct {
	yearEnd identity.YearEnd
}

func (f fakeIdentity) CompanyFacts(context.Context) (identity.CompanyFacts, error) {
	return identity.CompanyFacts{
		IncorporationDate: testDate(2020, time.January, 1),
		YearEnd:           f.yearEnd,
	}, nil
}

type fakeInvoicing struct{}

func (fakeInvoicing) Invoice(context.Context, string) (invoicing.Invoice, error) {
	return invoicing.Invoice{}, invoicing.ErrInvoiceNotFound
}

func (fakeInvoicing) InvoiceByNumber(context.Context, string) (invoicing.Invoice, error) {
	return invoicing.Invoice{}, invoicing.ErrInvoiceNotFound
}

func (fakeInvoicing) InvoiceVATContextBySendEntryID(context.Context, ledger.EntryID) (invoicing.InvoiceVATContext, error) {
	return invoicing.InvoiceVATContext{}, invoicing.ErrInvoiceNotFound
}

func (fakeInvoicing) Client(context.Context, string) (invoicing.Client, error) {
	return invoicing.Client{}, errors.New("unexpected client lookup")
}

func fakeEntry(id ledger.EntryID, date string, sourceModule string, sourceRef string, postings ...ledger.Posting) ledger.JournalEntry {
	parsed, err := time.ParseInLocation(time.DateOnly, date, time.UTC)
	if err != nil {
		panic(err)
	}
	return ledger.JournalEntry{
		ID:           id,
		Date:         parsed,
		Description:  sourceRef,
		SourceModule: sourceModule,
		SourceRef:    sourceRef,
		Postings:     postings,
	}
}

func fakePosting(account ledger.AccountCode, amountGBP int64) ledger.Posting {
	return ledger.Posting{
		AccountCode: account,
		Amount:      money.Money{Amount: amountGBP, Currency: gbpCurrency},
		AmountGBP:   money.Money{Amount: amountGBP, Currency: gbpCurrency},
	}
}

func assertReportMoney(t testing.TB, got money.Money, wantAmount int64) {
	t.Helper()
	if got.Amount != wantAmount || got.Currency != gbpCurrency {
		t.Fatalf("money = %+v, want %d GBP", got, wantAmount)
	}
}

func assertDate(t testing.TB, got time.Time, want string) {
	t.Helper()
	if got.Format(time.DateOnly) != want {
		t.Fatalf("date = %s, want %s", got.Format(time.DateOnly), want)
	}
}
