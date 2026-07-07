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

	"github.com/npmulder/ledgerly/internal/banking"
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

func TestProfitAndLossUsesOneLedgerSnapshotDuringInterleavedLedgerWrite(t *testing.T) {
	loadReportsPack(t, "")
	ctx := context.Background()
	fakeLedger := newFakeLedger(
		fakeEntry(1, "2025-05-01", "manual", "initial-income", fakePosting("4000-sales", -100_000)),
	)
	fakeLedger.afterBalances = func() {
		fakeLedger.addEntry(fakeEntry(2, "2025-05-02", "manual", "interleaved-income", fakePosting("4000-sales", -25_000)))
	}
	service := New(
		fakeLedger,
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
	if fakeLedger.snapshotCalls != 1 {
		t.Fatalf("ledger snapshot calls = %d, want 1", fakeLedger.snapshotCalls)
	}
	if len(fakeLedger.entries) != 2 {
		t.Fatalf("live ledger entries = %d, want interleaved write to be recorded", len(fakeLedger.entries))
	}
	assertReportMoney(t, pl.IncomeTotal, 100_000)
	if len(pl.Income) != 1 {
		t.Fatalf("Income = %#v, want one line from the original snapshot", pl.Income)
	}
	assertReportMoney(t, pl.Income[0].Amount, 100_000)
}

func TestProfitAndLossReleasesLedgerSnapshotBeforeInvoiceAttribution(t *testing.T) {
	loadReportsPack(t, "")
	ctx := context.Background()
	sourceRef := "invoice:INV-2025-0001:send"
	fakeLedger := newFakeLedger(
		fakeEntry(1, "2025-05-01", invoicing.ModuleName, sourceRef, fakePosting("4000-sales", -100_000)),
	)
	var invoiceLookupDuringSnapshot bool
	service := New(
		fakeLedger,
		fakeIdentity{yearEnd: identity.YearEnd{Month: time.March, Day: 31}},
		fakeInvoicing{
			invoiceByNumber: func(_ context.Context, number string) (invoicing.Invoice, error) {
				if number != "INV-2025-0001" {
					t.Fatalf("InvoiceByNumber(%q), want INV-2025-0001", number)
				}
				invoiceLookupDuringSnapshot = fakeLedger.snapshotOpen
				return invoicing.Invoice{
					ID:       "invoice-1",
					Number:   &number,
					ClientID: "client-1",
					Currency: invoicing.CurrencyGBP,
				}, nil
			},
			client: func(_ context.Context, id string) (invoicing.Client, error) {
				if id != "client-1" {
					t.Fatalf("Client(%q), want client-1", id)
				}
				return invoicing.Client{
					ID:              "client-1",
					Name:            "Acme Ltd",
					DefaultCurrency: invoicing.CurrencyGBP,
				}, nil
			},
		},
	)

	pl, err := service.ProfitAndLoss(ctx, Period{
		From: time.Date(2025, time.May, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2025, time.May, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ProfitAndLoss() error = %v", err)
	}
	if invoiceLookupDuringSnapshot {
		t.Fatal("invoice attribution ran while the ledger snapshot was open")
	}
	if len(pl.Income) != 1 {
		t.Fatalf("Income = %#v, want one attributed invoice line", pl.Income)
	}
	if pl.Income[0].ClientName != "Acme Ltd" {
		t.Fatalf("Income[0].ClientName = %q, want Acme Ltd", pl.Income[0].ClientName)
	}
	assertReportMoney(t, pl.Income[0].Amount, 100_000)
}

func TestExpensesByCategoryJoinsBankingTransactionsAndBuildsCSV(t *testing.T) {
	ctx := context.Background()
	fakeLedger := newFakeLedger(
		fakeEntry(1, "2025-05-02", banking.ModuleName, "banking:10:recode", fakePosting("5010-software", 12_345)),
		fakeEntry(2, "2025-05-04", banking.ModuleName, "banking:11:recode", fakePosting("6020-travel", 8_000)),
		fakeEntry(3, "2025-05-05", banking.ModuleName, "banking:12:recode", fakePosting("5010-software", 4_321)),
		fakeEntry(4, "2025-05-06", "manual", "manual-office", fakePosting("6030-office", 1_000)),
	)
	fakeLedger.accounts = append(fakeLedger.accounts,
		ledger.Account{Code: "6020-travel", Name: "Travel", Type: ledger.AccountTypeExpense},
		ledger.Account{Code: "6030-office", Name: "Office supplies", Type: ledger.AccountTypeExpense},
	)
	service := New(
		fakeLedger,
		fakeIdentity{yearEnd: identity.YearEnd{Month: time.March, Day: 31}},
		fakeInvoicing{},
		WithBanking(fakeBanking{
			transactions: map[banking.TransactionID]banking.Transaction{
				10: {
					ID:        10,
					Date:      testDate(2025, time.May, 2),
					Amount:    money.Money{Amount: -12_345, Currency: gbpCurrency},
					Payee:     "GitHub",
					Reference: "subscription may",
				},
				11: {
					ID:        11,
					Date:      testDate(2025, time.May, 4),
					Amount:    money.Money{Amount: -8_000, Currency: gbpCurrency},
					Payee:     "Steam Packet",
					Reference: "ferry",
				},
				12: {
					ID:        12,
					Date:      testDate(2025, time.May, 5),
					Amount:    money.Money{Amount: -4_321, Currency: gbpCurrency},
					Payee:     "GitHub",
					Reference: "actions minutes",
				},
			},
		}),
	)

	report, err := service.ExpensesByCategory(ctx, Period{
		From: testDate(2025, time.May, 1),
		To:   testDate(2025, time.May, 31),
	})
	if err != nil {
		t.Fatalf("ExpensesByCategory() error = %v", err)
	}
	assertReportMoney(t, report.Total, 25_666)
	if len(report.Categories) != 3 {
		t.Fatalf("Categories = %#v, want three", report.Categories)
	}
	if report.Categories[0].Category != "Software" || report.Categories[0].TransactionCount != 2 {
		t.Fatalf("first category = %#v, want Software with two rows", report.Categories[0])
	}
	assertReportMoney(t, report.Categories[0].Amount, 16_666)
	if report.TopPayees[0].Payee != "GitHub" || report.TopPayees[0].TransactionCount != 2 {
		t.Fatalf("top payee = %#v, want GitHub with two rows", report.TopPayees[0])
	}
	assertReportMoney(t, report.TopPayees[0].Amount, 16_666)
	if got := report.Transactions[0]; got.Payee != unattributedExpensePayee || got.Reference != "manual-office" || got.Category != "Office supplies" {
		t.Fatalf("newest fallback transaction = %#v, want manual fallback attribution", got)
	}
	if got := report.Transactions[1]; got.Payee != "GitHub" || got.Reference != "actions minutes" || got.Category != "Software" {
		t.Fatalf("second transaction = %#v, want GitHub software detail", got)
	}

	csvBytes, err := service.ExpensesCSV(ctx, Period{
		From: testDate(2025, time.May, 1),
		To:   testDate(2025, time.May, 31),
	})
	if err != nil {
		t.Fatalf("ExpensesCSV() error = %v", err)
	}
	csvText := string(csvBytes)
	for _, want := range []string{
		"date,payee,reference,amount,currency,category\r\n",
		"2025-05-05,GitHub,actions minutes,43.21,GBP,Software\r\n",
		"2025-05-02,GitHub,subscription may,123.45,GBP,Software\r\n",
	} {
		if !strings.Contains(csvText, want) {
			t.Fatalf("expenses CSV missing %q:\n%s", want, csvText)
		}
	}
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
	afterBalances    func()
	lastBalancesFrom time.Time
	lastBalancesTo   time.Time
	snapshotCalls    int
	snapshotOpen     bool
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

func (f *fakeLedger) ReadSnapshot(ctx context.Context, fn ledger.ReadSnapshotFunc) error {
	f.snapshotCalls++
	f.snapshotOpen = true
	defer func() {
		f.snapshotOpen = false
	}()
	snapshot := fakeLedgerSnapshot{
		accounts: cloneFakeAccounts(f.accounts),
		entries:  cloneFakeEntries(f.entries),
		recordBalances: func(from time.Time, to time.Time) {
			f.lastBalancesFrom = from
			f.lastBalancesTo = to
		},
		afterBalances: f.runAfterBalances,
	}
	return fn(ctx, snapshot)
}

func (f *fakeLedger) Accounts(context.Context) ([]ledger.Account, error) {
	return cloneFakeAccounts(f.accounts), nil
}

func (f *fakeLedger) BalancesByType(_ context.Context, from time.Time, to time.Time) ([]ledger.AccountBalance, error) {
	f.lastBalancesFrom = from
	f.lastBalancesTo = to
	balances, err := fakeBalancesByType(f.accounts, f.entries, from, to)
	if err != nil {
		return nil, err
	}
	f.runAfterBalances()
	return balances, nil
}

func (f *fakeLedger) Entries(_ context.Context, filter ledger.EntryFilter) ([]ledger.JournalEntry, error) {
	var from, to time.Time
	if filter.From != nil {
		from = *filter.From
	}
	if filter.To != nil {
		to = *filter.To
	}
	entries := fakeEntriesInWindow(f.entries, from, to, filter.After)
	if filter.Limit > 0 && len(entries) > filter.Limit {
		entries = entries[:filter.Limit]
	}
	return cloneFakeEntries(entries), nil
}

func (f *fakeLedger) addEntry(entry ledger.JournalEntry) {
	f.entries = append(f.entries, entry)
	sort.Slice(f.entries, func(i, j int) bool {
		if f.entries[i].Date.Equal(f.entries[j].Date) {
			return f.entries[i].ID < f.entries[j].ID
		}
		return f.entries[i].Date.Before(f.entries[j].Date)
	})
}

func (f *fakeLedger) runAfterBalances() {
	if f.afterBalances == nil {
		return
	}
	hook := f.afterBalances
	f.afterBalances = nil
	hook()
}

type fakeLedgerSnapshot struct {
	accounts       []ledger.Account
	entries        []ledger.JournalEntry
	recordBalances func(time.Time, time.Time)
	afterBalances  func()
}

func (s fakeLedgerSnapshot) AccountBalance(context.Context, ledger.AccountCode, time.Time) (ledger.AccountBalance, error) {
	return ledger.AccountBalance{}, errors.New("unexpected account balance lookup")
}

func (s fakeLedgerSnapshot) Accounts(context.Context) ([]ledger.Account, error) {
	return cloneFakeAccounts(s.accounts), nil
}

func (s fakeLedgerSnapshot) BalancesByType(_ context.Context, from time.Time, to time.Time) ([]ledger.AccountBalance, error) {
	if s.recordBalances != nil {
		s.recordBalances(from, to)
	}
	balances, err := fakeBalancesByType(s.accounts, s.entries, from, to)
	if err != nil {
		return nil, err
	}
	if s.afterBalances != nil {
		s.afterBalances()
	}
	return balances, nil
}

func (s fakeLedgerSnapshot) Entries(_ context.Context, filter ledger.EntryFilter) ([]ledger.JournalEntry, error) {
	var from, to time.Time
	if filter.From != nil {
		from = *filter.From
	}
	if filter.To != nil {
		to = *filter.To
	}
	entries := fakeEntriesInWindow(s.entries, from, to, filter.After)
	if filter.Limit > 0 && len(entries) > filter.Limit {
		entries = entries[:filter.Limit]
	}
	return cloneFakeEntries(entries), nil
}

func fakeBalancesByType(accounts []ledger.Account, entries []ledger.JournalEntry, from time.Time, to time.Time) ([]ledger.AccountBalance, error) {
	byCode := map[ledger.AccountCode]ledger.Account{}
	for _, account := range accounts {
		byCode[account.Code] = account
	}
	income := money.Zero(gbpCurrency)
	expense := money.Zero(gbpCurrency)
	for _, entry := range fakeEntriesInWindow(entries, from, to, nil) {
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

func fakeEntriesInWindow(entries []ledger.JournalEntry, from time.Time, to time.Time, after *ledger.EntryCursor) []ledger.JournalEntry {
	var out []ledger.JournalEntry
	for _, entry := range entries {
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

func cloneFakeAccounts(accounts []ledger.Account) []ledger.Account {
	return append([]ledger.Account(nil), accounts...)
}

func cloneFakeEntries(entries []ledger.JournalEntry) []ledger.JournalEntry {
	out := append([]ledger.JournalEntry(nil), entries...)
	for i := range out {
		out[i].Postings = append([]ledger.Posting(nil), out[i].Postings...)
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

func (f fakeIdentity) Profile(context.Context) (identity.CompanyProfile, error) {
	return identity.CompanyProfile{
		TradingName:       "NPM Limited",
		LegalName:         "NPM Limited",
		CompanyNumber:     "137792C",
		IncorporationDate: testDate(2020, time.January, 1),
		YearEnd:           f.yearEnd,
	}, nil
}

type fakeInvoicing struct {
	invoice         func(context.Context, string) (invoicing.Invoice, error)
	invoiceByNumber func(context.Context, string) (invoicing.Invoice, error)
	client          func(context.Context, string) (invoicing.Client, error)
}

func (f fakeInvoicing) Invoice(ctx context.Context, ref string) (invoicing.Invoice, error) {
	if f.invoice != nil {
		return f.invoice(ctx, ref)
	}
	return invoicing.Invoice{}, invoicing.ErrInvoiceNotFound
}

func (f fakeInvoicing) InvoiceByNumber(ctx context.Context, number string) (invoicing.Invoice, error) {
	if f.invoiceByNumber != nil {
		return f.invoiceByNumber(ctx, number)
	}
	return invoicing.Invoice{}, invoicing.ErrInvoiceNotFound
}

func (fakeInvoicing) InvoicesIssuedBetween(context.Context, time.Time, time.Time) ([]invoicing.Invoice, error) {
	return nil, nil
}

func (fakeInvoicing) InvoiceVATContextBySendEntryID(context.Context, ledger.EntryID) (invoicing.InvoiceVATContext, error) {
	return invoicing.InvoiceVATContext{}, invoicing.ErrInvoiceNotFound
}

func (f fakeInvoicing) Client(ctx context.Context, id string) (invoicing.Client, error) {
	if f.client != nil {
		return f.client(ctx, id)
	}
	return invoicing.Client{}, errors.New("unexpected client lookup")
}

type fakeBanking struct {
	transactions map[banking.TransactionID]banking.Transaction
}

func (f fakeBanking) Transaction(_ context.Context, id banking.TransactionID) (banking.Transaction, error) {
	txn, ok := f.transactions[id]
	if !ok {
		return banking.Transaction{}, banking.ErrTransactionNotFound
	}
	return txn, nil
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
