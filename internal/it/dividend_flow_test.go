//go:build integration

package it_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/banking"
	"github.com/npmulder/ledgerly/internal/dividends"
	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/invoicing"
	it "github.com/npmulder/ledgerly/internal/it"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
	"github.com/npmulder/ledgerly/internal/reports"
)

const (
	dividendFlowTaxYear       = "2025-26"
	dividendFlowCurrentDate   = "2025-07-01"
	dividendFlowInvoiceDate   = "2025-05-10"
	dividendFlowSettlementDay = "2025-05-20"
	dividendFlowCashAccount   = ledger.AccountCode("1000-cash-gbp")
)

func TestDividendFlow(t *testing.T) {
	t.Run("headroom hand-check", testDividendFlowHeadroomHandCheck)
	t.Run("declare happy path", testDividendFlowDeclareHappyPath)
	t.Run("illegal distribution", testDividendFlowIllegalDistribution)
	t.Run("TOCTOU race", testDividendFlowTOCTOURace)
	t.Run("clear with dividend", testDividendFlowClearWithDividend)
	t.Run("personal tax estimate", testDividendFlowPersonalTaxEstimate)
	t.Run("documents and immutability", testDividendFlowDocumentsAndImmutability)
}

func testDividendFlowHeadroomHandCheck(t *testing.T) {
	f := newDividendFlowFixture(t)
	f.postRetainedEarnings(t, day(2025, time.March, 31), 1_200_000)
	f.seedSettledInvoiceProfit(t, 516_000)
	f.insertDeclaration(t, "dividend-prior-handcheck", day(2025, time.June, 1), 300_000)

	breakdown, err := f.dividends().Headroom(f.ctx)
	if err != nil {
		t.Fatalf("Headroom() error = %v", err)
	}

	// Hand computation: retained b/fwd 1,200,000 + settled-invoice YTD profit
	// 516,000 - pack CIT at 0% (0) - declared dividends 300,000 = 1,416,000.
	assertDividendFlowHeadroom(t, breakdown, []dividends.MoneyLine{
		{Label: "Retained earnings b/fwd", Amount: gbp(1_200_000)},
		{Label: "Profit YTD (after expenses)", Amount: gbp(516_000)},
		{Label: "Corporation tax provision at 0%", Amount: gbp(0)},
		{Label: "Dividends already declared YTD", Amount: gbp(-300_000)},
		{Label: "Available to distribute", Amount: gbp(1_416_000)},
	})
}

func testDividendFlowDeclareHappyPath(t *testing.T) {
	f := newDividendFlowFixture(t)
	f.postRetainedEarnings(t, day(2025, time.March, 31), 1_200_000)
	f.seedSettledInvoiceProfit(t, 516_000)
	events := subscribeDividendDeclared(t, f.h)

	declaration, err := f.dividends().Declare(f.ctx, gbp(300_000))
	if err != nil {
		t.Fatalf("Declare() error = %v", err)
	}

	if declaration.Amount != gbp(300_000) {
		t.Fatalf("declaration amount = %+v, want GBP 3000.00", declaration.Amount)
	}
	if declaration.PerShare.Amount*declaration.Shares != declaration.Amount.Amount {
		t.Fatalf("per-share total = %d * %d, want %d", declaration.PerShare.Amount, declaration.Shares, declaration.Amount.Amount)
	}

	history, err := f.dividends().History(f.ctx)
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history) != 1 || history[0].ID != declaration.ID {
		t.Fatalf("History() = %#v, want declaration %s", history, declaration.ID)
	}

	sourceRef := dividendSourceRef(declaration.ID)
	assertDividendFlowLedgerPostings(t, f, dividends.ModuleName, sourceRef, []wantPosting{
		{account: dividends.RetainedEarningsAccountCode, amount: 300_000},
		{account: dla.DLAAccountCode, amount: -300_000},
	})
	assertDividendFlowDLAEntry(t, f, sourceRef, 300_000, gbp(300_000), dla.BalanceSideCredit)
	assertDividendDeclaredEvent(t, events, declaration.ID, gbp(300_000))
	assertDLAConsistent(t, f)
	it.AssertLedgerBalanced(t, f.h)
}

func testDividendFlowIllegalDistribution(t *testing.T) {
	f := newDividendFlowFixture(t)
	f.postRetainedEarnings(t, day(2025, time.March, 31), 100_000)
	engine := &dividendFlowDocumentEngine{}
	assets := &dividendFlowDocumentAssetStore{}

	_, err := f.dividends(
		dividends.WithDocumentPDFEngine(engine),
		dividends.WithDocumentAssetStore(assets),
		dividends.WithDocumentRetryBackoff(0),
	).Declare(f.ctx, gbp(100_100))
	if !errors.Is(err, dividends.ErrOverHeadroom) {
		t.Fatalf("Declare(over headroom) error = %v, want ErrOverHeadroom", err)
	}
	var over *dividends.OverHeadroomError
	if !errors.As(err, &over) {
		t.Fatalf("Declare(over headroom) error type = %T, want *OverHeadroomError", err)
	}
	if over.Distributable != gbp(100_000) {
		t.Fatalf("OverHeadroomError.Distributable = %+v, want GBP 1000.00", over.Distributable)
	}

	assertCountWhere(t, f.ctx, f.h.DB, "dividends.declarations", "true", 0)
	assertCountWhere(t, f.ctx, f.h.DB, "ledger.journal_entries", "source_module = 'dividends'", 0)
	assertCountWhere(t, f.ctx, f.h.DB, "dla.dla_entries", "source LIKE 'dividends:%'", 0)
	if assets.callCount() != 0 || engine.callCount() != 0 {
		t.Fatalf("document render/store calls = %d/%d, want zero", engine.callCount(), assets.callCount())
	}
	it.AssertLedgerBalanced(t, f.h)
}

func testDividendFlowTOCTOURace(t *testing.T) {
	f := newDividendFlowFixture(t)
	f.postRetainedEarnings(t, day(2025, time.March, 31), 100_000)
	service := f.dividends()

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := service.Declare(f.ctx, gbp(70_000))
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	successes := 0
	overHeadroom := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, dividends.ErrOverHeadroom):
			overHeadroom++
		default:
			t.Fatalf("concurrent Declare() unexpected error = %v", err)
		}
	}
	if successes != 1 || overHeadroom != 1 {
		t.Fatalf("concurrent Declare() successes=%d overHeadroom=%d, want 1/1", successes, overHeadroom)
	}
	assertCountWhere(t, f.ctx, f.h.DB, "dividends.declarations", "true", 1)
	assertCountWhere(t, f.ctx, f.h.DB, "ledger.journal_entries", "source_module = 'dividends'", 1)
	assertCountWhere(t, f.ctx, f.h.DB, "dla.dla_entries", "source LIKE 'dividends:%'", 1)
	assertDLAConsistent(t, f)
	it.AssertLedgerBalanced(t, f.h)
}

func testDividendFlowClearWithDividend(t *testing.T) {
	f := newDividendFlowFixture(t)
	f.postRetainedEarnings(t, day(2025, time.March, 31), 500_000)
	backInCredit := subscribeBackInCredit(t, f.h)

	f.fileDLADrawing(t, "banking:clear-with-dividend-drawing", gbp(150_000))
	status, err := f.dla.CurrentStatus(f.ctx)
	if err != nil {
		t.Fatalf("CurrentStatus() overdrawn error = %v", err)
	}
	if status.Status != dla.StatusOverdrawn || status.SuggestedClearanceAmount != gbp(150_000) {
		t.Fatalf("overdrawn status = %#v, want suggested clearance GBP 1500.00", status)
	}

	declaration, err := f.dividends().Declare(f.ctx, status.SuggestedClearanceAmount)
	if err != nil {
		t.Fatalf("Declare(clearance amount) error = %v", err)
	}
	assertDividendFlowDLAEntry(t, f, dividendSourceRef(declaration.ID), 150_000, gbp(0), dla.BalanceSideZero)

	balance, currentStatus, err := f.dla.CurrentBalance(f.ctx)
	if err != nil {
		t.Fatalf("CurrentBalance() after clearance error = %v", err)
	}
	if balance != gbp(0) || currentStatus != dla.StatusCredit {
		t.Fatalf("DLA after clearance = %+v/%s, want zero credit", balance, currentStatus)
	}
	assertBackInCreditOnce(t, backInCredit, gbp(0))
	assertDLAConsistent(t, f)
	it.AssertLedgerBalanced(t, f.h)
}

func testDividendFlowPersonalTaxEstimate(t *testing.T) {
	f := newDividendFlowFixture(t)
	f.postRetainedEarnings(t, day(2025, time.March, 31), 2_000_000)
	service := f.dividends()

	packMeta := jurisdiction.ActivePack()
	personalIncome, err := jurisdiction.PersonalIncomeTax(dividendFlowTaxYear)
	if err != nil {
		t.Fatalf("PersonalIncomeTax(%s) error = %v", dividendFlowTaxYear, err)
	}
	if len(personalIncome.Bands) == 0 {
		t.Fatalf("PersonalIncomeTax(%s) bands empty", dividendFlowTaxYear)
	}
	firstRate, ok := new(big.Rat).SetString(string(personalIncome.Bands[0].Rate))
	if !ok {
		t.Fatalf("parse first personal tax rate %q", personalIncome.Bands[0].Rate)
	}

	allowance := money.Money{Amount: personalIncome.PersonalAllowanceMinorUnits, Currency: packMeta.Currency}
	priorYTD := money.Money{Amount: allowance.Amount - 5_000, Currency: packMeta.Currency}
	candidate := money.Money{Amount: 20_000, Currency: packMeta.Currency}
	crossingTaxable := money.Money{Amount: priorYTD.Amount + candidate.Amount - allowance.Amount, Currency: packMeta.Currency}
	wantMarginal := crossingTaxable.MulRat(firstRate)
	f.insertDeclaration(t, "dividend-prior-personal-ytd", day(2025, time.June, 1), priorYTD.Amount)

	result, err := service.Validate(f.ctx, candidate)
	if err != nil {
		t.Fatalf("Validate(personal tax boundary) error = %v", err)
	}

	// Pack-derived hand computation:
	// allowance 1,475,000; prior YTD 1,470,000 leaves 5,000 allowance.
	// Candidate dividend 20,000 crosses the allowance by 15,000.
	// First pack band rate 0.10 gives marginal tax of 1,500.
	if result.PersonalTax.PriorYTD != priorYTD {
		t.Fatalf("PriorYTD = %+v, want %+v", result.PersonalTax.PriorYTD, priorYTD)
	}
	withDividend, err := priorYTD.Add(candidate)
	if err != nil {
		t.Fatalf("add candidate for expectation: %v", err)
	}
	if result.PersonalTax.WithDividend != withDividend {
		t.Fatalf("WithDividend = %+v, want %+v", result.PersonalTax.WithDividend, withDividend)
	}
	if result.PersonalTax.PriorEstimate.Total != gbp(0) {
		t.Fatalf("PriorEstimate.Total = %+v, want zero", result.PersonalTax.PriorEstimate.Total)
	}
	if result.PersonalTax.TotalEstimate.Taxable != crossingTaxable {
		t.Fatalf("TotalEstimate.Taxable = %+v, want %+v", result.PersonalTax.TotalEstimate.Taxable, crossingTaxable)
	}
	if len(result.PersonalTax.TotalEstimate.PerBand) == 0 || result.PersonalTax.TotalEstimate.PerBand[0].Amount != wantMarginal {
		t.Fatalf("TotalEstimate.PerBand = %#v, want first band amount %+v", result.PersonalTax.TotalEstimate.PerBand, wantMarginal)
	}
	if result.PersonalTax.Marginal != wantMarginal {
		t.Fatalf("PersonalTax.Marginal = %+v, want %+v", result.PersonalTax.Marginal, wantMarginal)
	}
	if !strings.Contains(result.PersonalTax.Message, wantMarginal.Format()) {
		t.Fatalf("PersonalTax.Message = %q, want set-aside %s", result.PersonalTax.Message, wantMarginal.Format())
	}
}

func testDividendFlowDocumentsAndImmutability(t *testing.T) {
	f := newDividendFlowFixture(t)
	f.postRetainedEarnings(t, day(2025, time.March, 31), 1_200_000)
	f.seedSettledInvoiceProfit(t, 516_000)

	declaration, err := f.dividends().Declare(f.ctx, gbp(300_000))
	if err != nil {
		t.Fatalf("Declare(document fixture) error = %v", err)
	}

	engine := &dividendFlowDocumentEngine{}
	assets := &dividendFlowDocumentAssetStore{}
	documentService := f.dividends(
		dividends.WithDocumentPDFEngine(engine),
		dividends.WithDocumentAssetStore(assets),
		dividends.WithDocumentRetryBackoff(0),
	)
	rendered, err := documentService.RenderDeclarationDocumentsNow(f.ctx, declaration.ID)
	if err != nil {
		t.Fatalf("RenderDeclarationDocumentsNow() error = %v", err)
	}
	if rendered.VoucherAsset == nil || rendered.MinutesAsset == nil {
		t.Fatalf("rendered assets = voucher %v minutes %v, want both", rendered.VoucherAsset, rendered.MinutesAsset)
	}
	if assets.callCount() != 2 {
		t.Fatalf("document asset calls = %d, want voucher and minutes", assets.callCount())
	}

	payload, err := documentService.DeclarationDocumentPayload(f.ctx, declaration.ID)
	if err != nil {
		t.Fatalf("DeclarationDocumentPayload() error = %v", err)
	}
	assertDividendDocumentPayload(t, payload)

	voucherBytes := assets.bytesAt(0)
	minutesBytes := assets.bytesAt(1)
	assertVoucherGoldenText(t, string(voucherBytes))
	assertMinutesGoldenText(t, string(minutesBytes), payload.Declaration.HeadroomSnapshot)

	renamedLegalName := "Renamed Dividend Flow Limited"
	renamedTradingName := "Renamed Dividend Flow"
	renamedShareholders := []identity.Shareholder{{Name: "Changed Shareholder", Shares: 100, Class: "ordinary \u00a31"}}
	if err := f.identity.UpdateProfile(f.ctx, identity.UpdateProfilePatch{
		LegalName:    &renamedLegalName,
		TradingName:  &renamedTradingName,
		Shareholders: &renamedShareholders,
	}); err != nil {
		t.Fatalf("UpdateProfile(rename after declaration) error = %v", err)
	}

	afterPayload, err := documentService.DeclarationDocumentPayload(f.ctx, declaration.ID)
	if err != nil {
		t.Fatalf("DeclarationDocumentPayload() after identity change error = %v", err)
	}
	if afterPayload.Declaration.CompanySnapshot == nil || afterPayload.Declaration.CompanySnapshot.LegalName != "NPM Limited" {
		t.Fatalf("company snapshot after rename = %#v, want original NPM Limited", afterPayload.Declaration.CompanySnapshot)
	}
	if afterPayload.Declaration.ShareholderSnapshot == nil || afterPayload.Declaration.ShareholderSnapshot.Name != "N. Meyer" {
		t.Fatalf("shareholder snapshot after rename = %#v, want original N. Meyer", afterPayload.Declaration.ShareholderSnapshot)
	}

	afterRendered, err := documentService.RenderDeclarationDocumentsNow(f.ctx, declaration.ID)
	if err != nil {
		t.Fatalf("RenderDeclarationDocumentsNow() after identity change error = %v", err)
	}
	if afterRendered.VoucherAsset == nil || *afterRendered.VoucherAsset != *rendered.VoucherAsset ||
		afterRendered.MinutesAsset == nil || *afterRendered.MinutesAsset != *rendered.MinutesAsset {
		t.Fatalf("assets changed after identity rename: before %v/%v after %v/%v",
			rendered.VoucherAsset,
			rendered.MinutesAsset,
			afterRendered.VoucherAsset,
			afterRendered.MinutesAsset,
		)
	}
	if assets.callCount() != 2 {
		t.Fatalf("document asset calls after immutable rerender = %d, want unchanged 2", assets.callCount())
	}
	if !bytes.Equal(assets.bytesAt(0), voucherBytes) || !bytes.Equal(assets.bytesAt(1), minutesBytes) {
		t.Fatal("stored dividend document bytes changed after identity rename")
	}
}

type dividendFlowFixture struct {
	tb       testing.TB
	ctx      context.Context
	h        *harness.Harness
	client   invoicing.Client
	ledger   *ledger.Service
	identity *identity.TransactionalProfileService
	dla      *dla.Service
	invoice  *invoicing.Service
	reports  *reports.Service
}

func newDividendFlowFixture(t *testing.T) dividendFlowFixture {
	t.Helper()

	if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("LoadActive(%q) error = %v", jurisdiction.DefaultSelector, err)
	}
	t.Cleanup(func() {
		if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
			t.Fatalf("restore active jurisdiction pack: %v", err)
		}
	})

	h := harness.New(t, harness.Options{
		ClockStart: dividendFlowDate(t, dividendFlowCurrentDate).Add(9 * time.Hour),
	})
	fixtures.Company(t, h, fixtures.CompanyYearEnd(time.March, 31))
	fixtures.Rates(t, h)
	client := fixtures.Fabrikam(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	ledgerService := ledger.New(h.LedgerPool, h.Bus)
	identityService := identity.NewTransactionalProfileService(testdb.AsModule(t, "identity"), h.Bus, identity.WithDataDir(h.IdentityDataDir))
	dlaService := dla.NewWithBusAndClock(h.DLAPool, h.Bus, h.Clock, ledgerService)
	invoiceService := newDividendFlowInvoiceService(t, h)
	reportsService := reports.New(ledgerService, identityService, invoiceService, reports.WithClock(h.Clock))

	return dividendFlowFixture{
		ctx:      ctx,
		tb:       t,
		h:        h,
		client:   client,
		ledger:   ledgerService,
		identity: identityService,
		dla:      dlaService,
		invoice:  invoiceService,
		reports:  reportsService,
	}
}

func (f dividendFlowFixture) dividends(opts ...dividends.Option) *dividends.Service {
	serviceOpts := []dividends.Option{
		dividends.WithClock(f.h.Clock),
		dividends.WithDLA(f.dla),
		dividends.WithBus(f.h.Bus),
	}
	serviceOpts = append(serviceOpts, opts...)
	return dividends.New(
		testdb.AsModule(f.tb, dividends.ModuleName),
		f.ledger,
		f.reports,
		f.identity,
		serviceOpts...,
	)
}

func (f dividendFlowFixture) postRetainedEarnings(t *testing.T, date time.Time, amount int64) {
	t.Helper()

	f.postLedgerEntry(t, date, "dividend flow opening retained earnings", "dividend-flow:retained", []ledger.NewPosting{
		{AccountCode: dividendFlowCashAccount, Amount: gbp(amount), AmountGBP: gbp(amount)},
		{AccountCode: dividends.RetainedEarningsAccountCode, Amount: gbp(-amount), AmountGBP: gbp(-amount)},
	})
}

func (f dividendFlowFixture) postLedgerEntry(t *testing.T, date time.Time, description string, sourceRef string, postings []ledger.NewPosting) {
	t.Helper()

	tx, err := f.h.LedgerPool.Begin(f.ctx)
	if err != nil {
		t.Fatalf("begin ledger tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	ensureDividendFlowCashAccount(t, f.ctx, f.ledger, tx)
	if _, err := f.ledger.Post(f.ctx, tx, ledger.NewJournalEntry{
		Date:         date,
		Description:  description,
		SourceModule: "dividend-flow-test",
		SourceRef:    sourceRef,
		Postings:     postings,
	}); err != nil {
		t.Fatalf("post dividend flow ledger entry: %v", err)
	}
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatalf("commit dividend flow ledger entry: %v", err)
	}
	committed = true
}

func (f dividendFlowFixture) seedSettledInvoiceProfit(t *testing.T, amount int64) invoicing.Invoice {
	t.Helper()

	originalNow := f.h.Clock.Now()
	f.h.Clock.Set(dividendFlowDate(t, dividendFlowInvoiceDate).Add(9 * time.Hour))
	defer f.h.Clock.Set(originalNow)

	draft, err := f.invoice.CreateDraft(f.ctx, f.client.ID)
	if err != nil {
		t.Fatalf("CreateDraft(profit invoice) error = %v", err)
	}
	lines := []invoicing.InvoiceLineInput{{
		Description: "Dividend flow settled profit fixture",
		Qty:         invoicing.MustQuantity("1"),
		UnitPrice:   invoicing.Money{Amount: amount, Currency: string(invoicing.CurrencyGBP)},
	}}
	updated, err := f.invoice.UpdateDraft(f.ctx, draft.ID, invoicing.DraftPatch{Lines: &lines})
	if err != nil {
		t.Fatalf("UpdateDraft(profit invoice) error = %v", err)
	}
	sent, err := f.invoice.Send(f.ctx, updated.ID)
	if err != nil {
		t.Fatalf("Send(profit invoice) error = %v", err)
	}
	if sent.Totals.Subtotal.Amount != amount {
		t.Fatalf("profit invoice subtotal = %+v, want amount %d", sent.Totals.Subtotal, amount)
	}
	settled, err := f.markInvoiceSettled(t, sent.ID, "banking:settle:"+sent.ID, dividendFlowDate(t, dividendFlowSettlementDay), sent.Totals.Total)
	if err != nil {
		t.Fatalf("MarkSettled(profit invoice) error = %v", err)
	}
	if settled.Status != invoicing.InvoiceStatusPaid {
		t.Fatalf("settled invoice status = %q, want paid", settled.Status)
	}
	return settled
}

func (f dividendFlowFixture) markInvoiceSettled(
	t *testing.T,
	id string,
	txnRef string,
	date time.Time,
	amount invoicing.Money,
) (_ invoicing.Invoice, err error) {
	t.Helper()

	bankingPool := testdb.AsModule(t, banking.ModuleName)
	tx, err := bankingPool.Begin(f.ctx)
	if err != nil {
		t.Fatalf("begin banking settlement tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	settled, err := f.invoice.MarkSettled(f.ctx, tx, id, txnRef, date, amount)
	if err != nil {
		return invoicing.Invoice{}, err
	}
	if err := tx.Commit(f.ctx); err != nil {
		return invoicing.Invoice{}, fmt.Errorf("commit banking settlement tx: %w", err)
	}
	committed = true
	return settled, nil
}

func (f dividendFlowFixture) insertDeclaration(t *testing.T, id string, date time.Time, amount int64) dividends.Declaration {
	t.Helper()

	stored, err := dividends.Store{}.InsertDeclaration(f.ctx, testdb.AsModule(t, dividends.ModuleName), dividends.Declaration{
		ID:              dividends.DeclarationID(id),
		DeclaredDate:    date,
		Amount:          gbp(amount),
		PerShare:        gbp(amount / 100),
		Shares:          100,
		ShareholderName: "N. Meyer",
	})
	if err != nil {
		t.Fatalf("insert dividend declaration fixture: %v", err)
	}
	return stored
}

func (f dividendFlowFixture) fileDLADrawing(t *testing.T, source string, amount money.Money) {
	t.Helper()

	bankingPool := testdb.AsModule(t, banking.ModuleName)
	tx, err := bankingPool.Begin(f.ctx)
	if err != nil {
		t.Fatalf("begin DLA drawing tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	ensureDividendFlowCashAccount(t, f.ctx, f.ledger, tx)
	if err := f.dla.FileDrawing(f.ctx, tx, dla.TxnRef{
		Ref:             source,
		Date:            dividendFlowDate(t, dividendFlowCurrentDate),
		Amount:          amount,
		CashAccountCode: dividendFlowCashAccount,
		Description:     "Dividend flow director drawing",
	}); err != nil {
		t.Fatalf("FileDrawing(%s) error = %v", source, err)
	}
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatalf("commit DLA drawing tx: %v", err)
	}
	committed = true
}

func newDividendFlowInvoiceService(t *testing.T, h *harness.Harness) *invoicing.Service {
	t.Helper()

	moneyFXPool := testdb.AsModule(t, moneyfx.ModuleName)
	rateLocks := dividendFlowRateLocker{service: moneyfx.NewService(moneyfx.NewStore(moneyFXPool), h.Clock)}
	return invoicing.NewService(
		testdb.AsModule(t, invoicing.ModuleName),
		invoicing.Store{},
		invoicing.WithClock(h.Clock),
		invoicing.WithTodayRate(dividendFlowTodayRate),
		invoicing.WithRateLocker(rateLocks),
		invoicing.WithRateLockReader(rateLocks),
		invoicing.WithLedger(ledger.New(h.LedgerPool, h.Bus)),
		invoicing.WithEventBus(h.Bus),
	)
}

type dividendFlowRateLocker struct {
	service *moneyfx.Service
}

func (l dividendFlowRateLocker) LockRate(ctx context.Context, tx db.Tx, ref invoicing.RateLockRef, from string, to string, date time.Time) (invoicing.RateLock, error) {
	lock, err := l.service.Lock(ctx, tx, moneyfx.LockRef{Module: ref.Module, Ref: ref.Ref}, from, to, date)
	if err != nil {
		return invoicing.RateLock{}, err
	}
	return invoicing.RateLock{
		ID:       int64(lock.ID),
		From:     lock.From,
		To:       lock.To,
		Rate:     lock.Rate,
		RateDate: lock.RateDate,
		Source:   lock.Source,
	}, nil
}

func (l dividendFlowRateLocker) RateLock(ctx context.Context, id int64) (invoicing.RateLock, error) {
	lock, err := l.service.GetLock(ctx, moneyfx.LockID(id))
	if err != nil {
		return invoicing.RateLock{}, err
	}
	return invoicing.RateLock{
		ID:       int64(lock.ID),
		From:     lock.From,
		To:       lock.To,
		Rate:     lock.Rate,
		RateDate: lock.RateDate,
		Source:   lock.Source,
	}, nil
}

func dividendFlowTodayRate(_ context.Context, from string, to string) (invoicing.FXRate, time.Time, error) {
	value := "0.85"
	if from == to {
		value = "1"
	}
	return invoicing.FXRate{
		From:   from,
		To:     to,
		Value:  value,
		Source: "dividend-flow-test",
	}, day(2025, time.May, 1), nil
}

func ensureDividendFlowCashAccount(t *testing.T, ctx context.Context, service *ledger.Service, tx db.Tx) {
	t.Helper()

	currency := "GBP"
	if _, err := service.EnsureAccount(ctx, tx, ledger.AccountSpec{
		Code:     dividendFlowCashAccount,
		Name:     "Dividend flow fixture cash GBP",
		Type:     ledger.AccountTypeAsset,
		Currency: &currency,
	}); err != nil {
		t.Fatalf("ensure dividend flow cash account: %v", err)
	}
}

func assertDividendFlowHeadroom(t *testing.T, got dividends.HeadroomBreakdown, want []dividends.MoneyLine) {
	t.Helper()

	if got.FinancialYear != dividendFlowTaxYear {
		t.Fatalf("FinancialYear = %q, want %s", got.FinancialYear, dividendFlowTaxYear)
	}
	if got.AsOf.Format(time.DateOnly) != dividendFlowCurrentDate {
		t.Fatalf("AsOf = %s, want %s", got.AsOf.Format(time.DateOnly), dividendFlowCurrentDate)
	}
	if !reflect.DeepEqual(got.Lines, want) {
		t.Fatalf("headroom lines = %#v, want %#v", got.Lines, want)
	}
	if got.Available != want[len(want)-1].Amount {
		t.Fatalf("Available = %+v, want %+v", got.Available, want[len(want)-1].Amount)
	}
	if !got.Distributable {
		t.Fatalf("Distributable = false, want true for %+v", got.Available)
	}
}

func assertDividendFlowLedgerPostings(t *testing.T, f dividendFlowFixture, module string, sourceRef string, want []wantPosting) {
	t.Helper()

	rows, err := f.h.DB.Query(f.ctx, `
SELECT p.account_code, p.amount, p.currency, p.amount_gbp
FROM ledger.journal_entries AS je
JOIN ledger.postings AS p ON p.entry_id = je.id
WHERE je.source_module = $1
	AND je.source_ref = $2
ORDER BY p.id`, module, sourceRef)
	if err != nil {
		t.Fatalf("query ledger postings for %s/%s: %v", module, sourceRef, err)
	}
	defer rows.Close()

	got := []wantPosting{}
	for rows.Next() {
		var posting wantPosting
		var currency string
		var amountGBP int64
		if err := rows.Scan(&posting.account, &posting.amount, &currency, &amountGBP); err != nil {
			t.Fatalf("scan ledger posting for %s/%s: %v", module, sourceRef, err)
		}
		if currency != "GBP" || amountGBP != posting.amount {
			t.Fatalf("posting for %s/%s has currency=%s amount_gbp=%d amount=%d, want GBP and matching GBP amount",
				module,
				sourceRef,
				currency,
				amountGBP,
				posting.amount,
			)
		}
		got = append(got, posting)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("collect ledger postings for %s/%s: %v", module, sourceRef, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ledger postings for %s/%s = %#v, want %#v", module, sourceRef, got, want)
	}
}

func assertDividendFlowDLAEntry(
	t *testing.T,
	f dividendFlowFixture,
	sourceRef string,
	amount int64,
	wantRunning money.Money,
	wantSide dla.BalanceSide,
) {
	t.Helper()

	entries, err := f.dla.Ledger(f.ctx, dla.LedgerFilter{Limit: 20})
	if err != nil {
		t.Fatalf("DLA Ledger() error = %v", err)
	}
	for _, entry := range entries {
		if entry.Source != sourceRef {
			continue
		}
		if entry.Kind != dla.EntryKindExpenseOwed || entry.Description != "Dividend declared" {
			t.Fatalf("DLA dividend entry = %#v, want external dividend credit", entry)
		}
		if entry.Amount != gbp(amount) || entry.RunningBalance != wantRunning || entry.BalanceSide != wantSide {
			t.Fatalf("DLA dividend entry money = amount %+v running %+v side %s, want %+v/%+v/%s",
				entry.Amount,
				entry.RunningBalance,
				entry.BalanceSide,
				gbp(amount),
				wantRunning,
				wantSide,
			)
		}
		return
	}
	t.Fatalf("DLA entry source %q not found in %#v", sourceRef, entries)
}

func assertDLAConsistent(t *testing.T, f dividendFlowFixture) {
	t.Helper()

	report, err := f.dla.CheckConsistency(f.ctx, f.h.Clock.Now())
	if err != nil || !report.Consistent {
		t.Fatalf("DLA CheckConsistency() report=%+v error=%v, want consistent", report, err)
	}
}

func subscribeDividendDeclared(t *testing.T, h *harness.Harness) <-chan dividends.Declared {
	t.Helper()

	events := make(chan dividends.Declared, 4)
	h.Bus.Subscribe(dividends.DeclaredName, func(_ context.Context, _ db.Tx, event bus.Event) error {
		declared, ok := event.(dividends.Declared)
		if !ok {
			return fmt.Errorf("got %T, want dividends.Declared", event)
		}
		events <- declared
		return nil
	})
	return events
}

func assertDividendDeclaredEvent(t *testing.T, events <-chan dividends.Declared, wantID dividends.DeclarationID, wantAmount money.Money) {
	t.Helper()

	select {
	case event := <-events:
		if event.DeclarationID != wantID || event.Amount != wantAmount {
			t.Fatalf("Declared event = %#v, want id %s amount %+v", event, wantID, wantAmount)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dividends.Declared event")
	}
}

func subscribeBackInCredit(t *testing.T, h *harness.Harness) <-chan dla.BackInCredit {
	t.Helper()

	events := make(chan dla.BackInCredit, 4)
	h.Bus.Subscribe(dla.BackInCreditName, func(_ context.Context, _ db.Tx, event bus.Event) error {
		back, ok := event.(dla.BackInCredit)
		if !ok {
			return fmt.Errorf("got %T, want dla.BackInCredit", event)
		}
		events <- back
		return nil
	})
	return events
}

func assertBackInCreditOnce(t *testing.T, events <-chan dla.BackInCredit, wantBalance money.Money) {
	t.Helper()

	select {
	case event := <-events:
		if event.Balance != wantBalance {
			t.Fatalf("BackInCredit event = %#v, want balance %+v", event, wantBalance)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dla.BackInCredit event")
	}
	select {
	case event := <-events:
		t.Fatalf("unexpected extra BackInCredit event: %#v", event)
	default:
	}
}

func assertDividendDocumentPayload(t *testing.T, payload dividends.DividendDocumentPayload) {
	t.Helper()

	declaration := payload.Declaration
	if declaration.CompanySnapshot == nil {
		t.Fatal("document payload CompanySnapshot = nil")
	}
	if declaration.CompanySnapshot.CompanyNumber != "137792C" {
		t.Fatalf("CompanyNumber = %q, want 137792C", declaration.CompanySnapshot.CompanyNumber)
	}
	if declaration.ShareholderSnapshot == nil {
		t.Fatal("document payload ShareholderSnapshot = nil")
	}
	if declaration.ShareholderSnapshot.Shares != 100 || declaration.ShareholderSnapshot.Class != "ordinary \u00a31" {
		t.Fatalf("ShareholderSnapshot = %#v, want 100 ordinary GBP 1 shares", declaration.ShareholderSnapshot)
	}
	if declaration.PerShare.Amount*declaration.Shares != declaration.Amount.Amount {
		t.Fatalf("per-share total = %d * %d, want %d exactly", declaration.PerShare.Amount, declaration.Shares, declaration.Amount.Amount)
	}
	if declaration.WithholdingSnapshot == nil || declaration.WithholdingSnapshot.Policy != "none" ||
		!strings.Contains(declaration.WithholdingSnapshot.Note, "No dividend withholding tax") {
		t.Fatalf("WithholdingSnapshot = %#v, want no-WHT wording", declaration.WithholdingSnapshot)
	}
	if declaration.HeadroomSnapshot == nil {
		t.Fatal("document payload HeadroomSnapshot = nil")
	}
	assertDividendFlowHeadroom(t, *declaration.HeadroomSnapshot, []dividends.MoneyLine{
		{Label: "Retained earnings b/fwd", Amount: gbp(1_200_000)},
		{Label: "Profit YTD (after expenses)", Amount: gbp(516_000)},
		{Label: "Corporation tax provision at 0%", Amount: gbp(0)},
		{Label: "Dividends already declared YTD", Amount: gbp(0)},
		{Label: "Available to distribute", Amount: gbp(1_716_000)},
	})
}

func assertVoucherGoldenText(t *testing.T, text string) {
	t.Helper()

	for _, want := range []string{
		"DIVIDEND VOUCHER",
		"Company no. 137792C",
		"Dividend per share \u00a330.00",
		"Total dividend \u00a33,000.00",
		"No dividend withholding tax is deducted under the active jurisdiction pack (withholding: none).",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("voucher text missing %q:\n%s", want, text)
		}
	}
}

func assertMinutesGoldenText(t *testing.T, text string, snapshot *dividends.HeadroomBreakdown) {
	t.Helper()

	if snapshot == nil {
		t.Fatal("headroom snapshot = nil")
	}
	recital := dividendFlowHeadroomRecital(*snapshot)
	for _, want := range []string{
		"BOARD MINUTES",
		"Dividend total \u00a33,000.00",
		recital,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("minutes text missing %q:\n%s", want, text)
		}
	}
}

type dividendFlowDocumentEngine struct {
	mu     sync.Mutex
	called int
}

func (e *dividendFlowDocumentEngine) RenderDividendVoucherPDF(_ context.Context, payload dividends.DividendDocumentPayload) ([]byte, error) {
	e.mu.Lock()
	e.called++
	e.mu.Unlock()
	return []byte(dividendFlowVoucherText(payload)), nil
}

func (e *dividendFlowDocumentEngine) RenderBoardMinutesPDF(_ context.Context, payload dividends.DividendDocumentPayload) ([]byte, error) {
	e.mu.Lock()
	e.called++
	e.mu.Unlock()
	return []byte(dividendFlowMinutesText(payload)), nil
}

func (e *dividendFlowDocumentEngine) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.called
}

type dividendFlowDocumentAssetStore struct {
	mu    sync.Mutex
	bytes [][]byte
}

func (s *dividendFlowDocumentAssetStore) StoreDividendDocumentPDF(_ context.Context, pdf []byte) (identity.AssetID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.bytes = append(s.bytes, append([]byte{}, pdf...))
	return identity.AssetID(fmt.Sprintf("dividend-flow-doc-%d", len(s.bytes))), nil
}

func (s *dividendFlowDocumentAssetStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bytes)
}

func (s *dividendFlowDocumentAssetStore) bytesAt(index int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte{}, s.bytes[index]...)
}

func dividendFlowVoucherText(payload dividends.DividendDocumentPayload) string {
	declaration := payload.Declaration
	return fmt.Sprintf(
		"DIVIDEND VOUCHER\nCompany no. %s\nShareholder %s\nShares %d %s\nDividend per share %s\nTotal dividend %s\n%s\n",
		declaration.CompanySnapshot.CompanyNumber,
		declaration.ShareholderSnapshot.Name,
		declaration.ShareholderSnapshot.Shares,
		declaration.ShareholderSnapshot.Class,
		declaration.PerShare.Format(),
		declaration.Amount.Format(),
		declaration.WithholdingSnapshot.Note,
	)
}

func dividendFlowMinutesText(payload dividends.DividendDocumentPayload) string {
	declaration := payload.Declaration
	return fmt.Sprintf(
		"BOARD MINUTES\nDividend total %s\nHeadroom recital\n%s\n",
		declaration.Amount.Format(),
		dividendFlowHeadroomRecital(*declaration.HeadroomSnapshot),
	)
}

func dividendFlowHeadroomRecital(snapshot dividends.HeadroomBreakdown) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Financial year %s as of %s", snapshot.FinancialYear, snapshot.AsOf.Format(time.DateOnly))
	for _, line := range snapshot.Lines {
		fmt.Fprintf(&b, "\n%s %s", line.Label, line.Amount.Format())
	}
	return b.String()
}

func dividendSourceRef(id dividends.DeclarationID) string {
	return "dividends:" + string(id)
}

func dividendFlowDate(t *testing.T, value string) time.Time {
	t.Helper()

	parsed, err := time.ParseInLocation(time.DateOnly, value, time.UTC)
	if err != nil {
		t.Fatalf("parse date %q: %v", value, err)
	}
	return parsed
}
