//go:build integration

package harness_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/banking"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/invoicing"
	it "github.com/npmulder/ledgerly/internal/it"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

func TestInvoicingInvoicesDraftLifecycleAndTotals(t *testing.T) {
	h := harness.New(t, harness.Options{})
	h.Clock.Set(time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC))
	service := newInvoiceService(t, h)

	fabrikam := fixtures.Fabrikam(t, h)
	draft, err := service.CreateDraft(context.Background(), fabrikam.ID)
	if err != nil {
		t.Fatalf("CreateDraft() error = %v", err)
	}
	if draft.Number != nil {
		t.Fatalf("draft Number = %v, want nil", *draft.Number)
	}
	if draft.Status != invoicing.InvoiceStatusDraft {
		t.Fatalf("draft Status = %q, want draft", draft.Status)
	}
	if draft.Currency != invoicing.CurrencyGBP {
		t.Fatalf("draft Currency = %q, want GBP", draft.Currency)
	}
	if draft.VATTreatment != invoicing.VATTreatmentDomestic {
		t.Fatalf("draft VATTreatment = %q, want domestic", draft.VATTreatment)
	}
	assertDate(t, draft.IssueDate, "2025-05-01")
	assertDate(t, draft.DueDate, "2025-05-31")

	lines := []invoicing.InvoiceLineInput{
		{
			Description: "Fractional day",
			Qty:         invoicing.MustQuantity("1.5"),
			UnitPrice:   invoicing.Money{Amount: 1005, Currency: string(invoicing.CurrencyGBP)},
		},
		{
			Description: "Half-penny tie",
			Qty:         invoicing.MustQuantity("0.5"),
			UnitPrice:   invoicing.Money{Amount: 5, Currency: string(invoicing.CurrencyGBP)},
		},
	}
	updated, err := service.UpdateDraft(context.Background(), draft.ID, invoicing.DraftPatch{Lines: &lines})
	if err != nil {
		t.Fatalf("UpdateDraft(lines) error = %v", err)
	}
	assertMoney(t, updated.Lines[0].LineTotal, 1508, "GBP")
	assertMoney(t, updated.Lines[1].LineTotal, 2, "GBP")
	assertMoney(t, updated.Totals.Subtotal, 1510, "GBP")
	assertMoney(t, updated.Totals.VAT, 302, "GBP")
	assertMoney(t, updated.Totals.Total, 1812, "GBP")
	if updated.Totals.ApproxGBP == nil {
		t.Fatal("ApproxGBP = nil, want draft approximation")
	}
	assertMoney(t, updated.Totals.ApproxGBP.Amount, 1812, "GBP")

	fetched, err := service.Invoice(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("Invoice() error = %v", err)
	}
	assertMoney(t, fetched.Totals.Total, 1812, "GBP")

	contoso := fixtures.Contoso(t, h)
	reverseDraft, err := service.CreateDraft(context.Background(), contoso.ID)
	if err != nil {
		t.Fatalf("CreateDraft(reverse) error = %v", err)
	}
	reverseLines := []invoicing.InvoiceLineInput{
		{
			Description: "Tie down",
			Qty:         invoicing.MustQuantity("0.5"),
			UnitPrice:   invoicing.Money{Amount: 5, Currency: string(invoicing.CurrencyEUR)},
		},
		{
			Description: "Tie up",
			Qty:         invoicing.MustQuantity("0.5"),
			UnitPrice:   invoicing.Money{Amount: 7, Currency: string(invoicing.CurrencyEUR)},
		},
	}
	reverseUpdated, err := service.UpdateDraft(context.Background(), reverseDraft.ID, invoicing.DraftPatch{Lines: &reverseLines})
	if err != nil {
		t.Fatalf("UpdateDraft(reverse lines) error = %v", err)
	}
	assertMoney(t, reverseUpdated.Lines[0].LineTotal, 2, "EUR")
	assertMoney(t, reverseUpdated.Lines[1].LineTotal, 4, "EUR")
	assertMoney(t, reverseUpdated.Totals.Subtotal, 6, "EUR")
	assertMoney(t, reverseUpdated.Totals.VAT, 0, "EUR")
	assertMoney(t, reverseUpdated.Totals.Total, 6, "EUR")
	if reverseUpdated.Totals.ApproxGBP == nil {
		t.Fatal("reverse ApproxGBP = nil, want fake EUR->GBP approximation")
	}
	assertMoney(t, reverseUpdated.Totals.ApproxGBP.Amount, 5, "GBP")

	if _, err := h.DB.Exec(context.Background(), `UPDATE invoicing.invoices SET status = 'sent' WHERE id = $1`, draft.ID); err != nil {
		t.Fatalf("mark draft sent: %v", err)
	}
	newDueDate := time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC)
	_, err = service.UpdateDraft(context.Background(), draft.ID, invoicing.DraftPatch{DueDate: &newDueDate})
	if !errors.Is(err, invoicing.ErrInvoiceImmutable) {
		t.Fatalf("UpdateDraft(sent) error = %v, want ErrInvoiceImmutable", err)
	}
	if err := service.DeleteDraft(context.Background(), draft.ID); !errors.Is(err, invoicing.ErrInvoiceImmutable) {
		t.Fatalf("DeleteDraft(sent) error = %v, want ErrInvoiceImmutable", err)
	}

	modulePool := testdb.AsModule(t, invoicing.ModuleName)
	store := invoicing.Store{}
	tx, err := modulePool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin settlement tx: %v", err)
	}
	txnRef := "txn_123"
	settledDate := time.Date(2025, 5, 20, 0, 0, 0, 0, time.UTC)
	settledAmount := invoicing.Money{Amount: 1812, Currency: string(invoicing.CurrencyGBP)}
	settled, err := store.SetInvoiceSettlement(context.Background(), tx, draft.ID, invoicing.InvoiceSettlement{
		TxnRef:        &txnRef,
		SettledDate:   &settledDate,
		SettledAmount: &settledAmount,
	})
	if err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("SetInvoiceSettlement(sent) error = %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit settlement tx: %v", err)
	}
	if settled.Status != invoicing.InvoiceStatusPaid {
		t.Fatalf("settled Status = %q, want paid", settled.Status)
	}
	if settled.SettlementTxnRef == nil || *settled.SettlementTxnRef != txnRef {
		t.Fatalf("SettlementTxnRef = %v, want %q", settled.SettlementTxnRef, txnRef)
	}
	if settled.SettledAmount == nil {
		t.Fatal("SettledAmount = nil, want amount")
	}
	assertMoney(t, *settled.SettledAmount, 1812, "GBP")
}

func TestInvoicingSendHappyPathLocksPostsAndPublishes(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	service := newInvoiceService(t, h)
	ctx := context.Background()

	var sentEvents []invoicing.InvoiceSent
	h.Bus.Subscribe(invoicing.InvoiceSentName, func(_ context.Context, _ db.Tx, evt bus.Event) error {
		sent, ok := evt.(invoicing.InvoiceSent)
		if !ok {
			t.Fatalf("InvoiceSent event = %T, want invoicing.InvoiceSent", evt)
		}
		sentEvents = append(sentEvents, sent)
		return nil
	})

	draft := createEURInvoiceDraft(t, h, service, 450_000)
	sent, err := service.Send(ctx, draft.ID)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if sent.Status != invoicing.InvoiceStatusSent {
		t.Fatalf("sent Status = %q, want sent", sent.Status)
	}
	if sent.Number == nil || *sent.Number != "INV-2025-01" {
		t.Fatalf("sent Number = %v, want INV-2025-01", sent.Number)
	}
	if sent.LockID == nil {
		t.Fatal("sent LockID = nil, want lock id")
	}
	assertMoney(t, sent.Totals.Total, 450_000, "EUR")
	assertInvoiceRateLock(t, h, *sent.LockID, "invoicing:INV-2025-01", "EUR", "GBP", "0.850000000000000000")
	assertInvoicingLedgerPostings(t, h, invoiceSendSourceRefForTest("INV-2025-01"), []wantInvoicePosting{
		{account: "1100-debtors-eur", amount: 450_000, currency: "EUR", amountGBP: 382_500},
		{account: "4000-sales", amount: -450_000, currency: "EUR", amountGBP: -382_500},
	})

	wantEvents := []invoicing.InvoiceSent{{
		InvoiceID: sent.ID,
		Number:    "INV-2025-01",
		ClientID:  sent.ClientID,
		Amount:    invoicing.Money{Amount: 450_000, Currency: "EUR"},
		DueDate:   sent.DueDate,
	}}
	if !reflect.DeepEqual(sentEvents, wantEvents) {
		t.Fatalf("InvoiceSent events = %#v, want %#v", sentEvents, wantEvents)
	}
	it.AssertLedgerBalanced(t, h)
}

func TestInvoicingSendSchedulesPDFRenderAfterCommitWithRetry(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	var logs bytes.Buffer
	engine := newRecordingPDFEngine(1, []byte("%PDF-1.4\nretry-ok\n%%EOF\n"))
	assets := &recordingPDFAssetStore{}
	profile := testIdentityProfile(t, h)
	service := newInvoiceService(
		t,
		h,
		invoicing.WithIdentity(profile),
		invoicing.WithInvoicePDFEngine(engine),
		invoicing.WithInvoicePDFAssetStore(assets),
		invoicing.WithInvoicePDFRetryBackoff(0),
		invoicing.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))),
	)

	sent, err := service.Send(context.Background(), createEURInvoiceDraft(t, h, service, 450_000).ID)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if sent.PDFAsset != nil {
		t.Fatalf("Send() returned PDFAsset = %v, want async render after response", sent.PDFAsset)
	}

	waitForPDFAsset(t, service, sent.ID, "/api/identity/assets/test-pdf-1", func() string {
		return fmt.Sprintf("attempts=%d asset_calls=%d logs=%q", engine.attemptCount(), assets.callCount(), logs.String())
	})
	if got := engine.attemptCount(); got != 2 {
		t.Fatalf("PDF render attempts = %d, want 2", got)
	}
	if got := assets.callCount(); got != 1 {
		t.Fatalf("PDF asset store calls = %d, want 1", got)
	}
	if !strings.Contains(logs.String(), "invoice PDF render failed") {
		t.Fatalf("logs = %q, want render failure log", logs.String())
	}
}

func TestInvoicingPDFAssetIsImmutableAfterIdentityChange(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	profile := testIdentityProfile(t, h)
	engine := newRecordingPDFEngine(0, []byte("%PDF-1.4\nfirst-render\n%%EOF\n"))
	assets := &recordingPDFAssetStore{}
	service := newInvoiceService(
		t,
		h,
		invoicing.WithIdentity(profile),
		invoicing.WithInvoicePDFEngine(engine),
		invoicing.WithInvoicePDFAssetStore(assets),
		invoicing.WithInvoicePDFRetryBackoff(0),
	)

	sent, err := service.Send(context.Background(), createEURInvoiceDraft(t, h, service, 450_000).ID)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	waitForPDFAsset(t, service, sent.ID, "/api/identity/assets/test-pdf-1", func() string {
		return fmt.Sprintf("attempts=%d asset_calls=%d", engine.attemptCount(), assets.callCount())
	})
	firstBytes := assets.bytesAt(0)

	tradingName := "Renamed Ledgerly Limited"
	if err := profile.UpdateProfile(context.Background(), identity.UpdateProfilePatch{TradingName: &tradingName}); err != nil {
		t.Fatalf("UpdateProfile() error = %v", err)
	}
	engine.setPDF([]byte("%PDF-1.4\nsecond-render\n%%EOF\n"))
	after, err := service.RenderInvoicePDFNow(context.Background(), sent.ID)
	if err != nil {
		t.Fatalf("RenderInvoicePDFNow() after identity change error = %v", err)
	}

	if after.PDFAsset == nil || *after.PDFAsset != "/api/identity/assets/test-pdf-1" {
		t.Fatalf("PDFAsset after re-render = %v, want original URL", after.PDFAsset)
	}
	if got := assets.callCount(); got != 1 {
		t.Fatalf("PDF asset store calls after recovery render = %d, want 1", got)
	}
	if !bytes.Equal(assets.bytesAt(0), firstBytes) {
		t.Fatal("stored PDF bytes changed after identity update and recovery render")
	}
}

func TestInvoicingInvoicePrintPayloadReverseChargeFacts(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	service := newInvoiceService(t, h, invoicing.WithIdentity(testIdentityProfile(t, h)))

	sent, err := service.Send(context.Background(), createEURInvoiceDraft(t, h, service, 450_000).ID)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	payload, err := service.InvoicePrintPayload(context.Background(), sent.ID, false)
	if err != nil {
		t.Fatalf("InvoicePrintPayload() error = %v", err)
	}

	if payload.ReverseChargeNote == nil || !strings.Contains(*payload.ReverseChargeNote, "Article 196") {
		t.Fatalf("ReverseChargeNote = %v, want Article 196 wording", payload.ReverseChargeNote)
	}
	assertMoney(t, payload.Invoice.Totals.VAT, 0, "EUR")
	if payload.LockedRate == nil || payload.LockedRate.Rate != "0.85" {
		t.Fatalf("LockedRate = %+v, want locked 0.85", payload.LockedRate)
	}
	if payload.Identity.IBAN != "GB82 WEST 1234 5698 7654 32" || payload.Identity.BIC != "WESTGB2L" {
		t.Fatalf("SEPA details = %s/%s, want fixture bank details", payload.Identity.IBAN, payload.Identity.BIC)
	}
}

func TestInvoicingInvoicePrintPayloadDomesticVATFromPack(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	service := newInvoiceService(t, h, invoicing.WithIdentity(testIdentityProfile(t, h)))

	fabrikam := fixtures.Fabrikam(t, h)
	draft, err := service.CreateDraft(context.Background(), fabrikam.ID)
	if err != nil {
		t.Fatalf("CreateDraft() error = %v", err)
	}
	lines := []invoicing.InvoiceLineInput{{
		Description: "Domestic support",
		Qty:         invoicing.MustQuantity("1"),
		UnitPrice:   invoicing.Money{Amount: 60_000, Currency: string(invoicing.CurrencyGBP)},
	}}
	updated, err := service.UpdateDraft(context.Background(), draft.ID, invoicing.DraftPatch{Lines: &lines})
	if err != nil {
		t.Fatalf("UpdateDraft(lines) error = %v", err)
	}
	sent, err := service.Send(context.Background(), updated.ID)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	payload, err := service.InvoicePrintPayload(context.Background(), sent.ID, false)
	if err != nil {
		t.Fatalf("InvoicePrintPayload() error = %v", err)
	}

	if payload.VATRate != "0.20" || payload.VATTaxYear != "2025-26" {
		t.Fatalf("VAT rate/year = %s/%s, want 0.20 from 2025-26 pack", payload.VATRate, payload.VATTaxYear)
	}
	assertMoney(t, payload.Invoice.Totals.VAT, 12_000, "GBP")
	if payload.ReverseChargeNote != nil {
		t.Fatalf("ReverseChargeNote = %q, want nil for domestic invoice", *payload.ReverseChargeNote)
	}
}

func TestInvoicingDomesticVATUsesCompanyRegistrationSnapshot(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	profile := testIdentityProfile(t, h)
	service := newInvoiceService(t, h, invoicing.WithIdentity(profile))
	ctx := context.Background()
	fabrikam := fixtures.Fabrikam(t, h)

	setTestVATRegistered(t, profile, false)
	draftedWhileUnregistered, err := service.CreateDraft(ctx, fabrikam.ID)
	if err != nil {
		t.Fatalf("CreateDraft(unregistered) error = %v", err)
	}
	unregisteredLines := []invoicing.InvoiceLineInput{{
		Description: "Unregistered domestic support",
		Qty:         invoicing.MustQuantity("1"),
		UnitPrice:   invoicing.Money{Amount: 100_000, Currency: string(invoicing.CurrencyGBP)},
	}}
	draftedWhileUnregistered, err = service.UpdateDraft(ctx, draftedWhileUnregistered.ID, invoicing.DraftPatch{Lines: &unregisteredLines})
	if err != nil {
		t.Fatalf("UpdateDraft(unregistered lines) error = %v", err)
	}
	assertMoney(t, draftedWhileUnregistered.Totals.VAT, 0, "GBP")
	assertMoney(t, draftedWhileUnregistered.Totals.Total, 100_000, "GBP")
	if draftedWhileUnregistered.VATTreatment != invoicing.VATTreatmentDomestic {
		t.Fatalf("unregistered draft VATTreatment = %q, want domestic", draftedWhileUnregistered.VATTreatment)
	}
	unregisteredDraftPayload, err := service.InvoicePrintPayload(ctx, draftedWhileUnregistered.ID, true)
	if err != nil {
		t.Fatalf("InvoicePrintPayload(unregistered draft) error = %v", err)
	}
	if unregisteredDraftPayload.VATRegistered {
		t.Fatal("unregistered draft payload VATRegistered = true, want false")
	}
	if unregisteredDraftPayload.Identity.VATNumber != nil {
		t.Fatalf("unregistered draft payload VATNumber = %v, want nil", *unregisteredDraftPayload.Identity.VATNumber)
	}

	setTestVATRegistered(t, profile, true)
	recomputedBeforeSend, err := service.Invoice(ctx, draftedWhileUnregistered.ID)
	if err != nil {
		t.Fatalf("Invoice(recomputed before send) error = %v", err)
	}
	assertMoney(t, recomputedBeforeSend.Totals.VAT, 20_000, "GBP")
	sentAfterRegistration, err := service.Send(ctx, draftedWhileUnregistered.ID)
	if err != nil {
		t.Fatalf("Send(after registration) error = %v", err)
	}
	assertMoney(t, sentAfterRegistration.Totals.VAT, 20_000, "GBP")

	setTestVATRegistered(t, profile, false)
	stillVATInvoice, err := service.Invoice(ctx, sentAfterRegistration.ID)
	if err != nil {
		t.Fatalf("Invoice(sent after registration) error = %v", err)
	}
	assertMoney(t, stillVATInvoice.Totals.VAT, 20_000, "GBP")
	registeredSnapshotPayload, err := service.InvoicePrintPayload(ctx, sentAfterRegistration.ID, false)
	if err != nil {
		t.Fatalf("InvoicePrintPayload(registered snapshot) error = %v", err)
	}
	if !registeredSnapshotPayload.VATRegistered {
		t.Fatal("registered snapshot payload VATRegistered = false, want true")
	}

	setTestVATRegistered(t, profile, true)
	draftedWhileRegistered, err := service.CreateDraft(ctx, fabrikam.ID)
	if err != nil {
		t.Fatalf("CreateDraft(registered) error = %v", err)
	}
	registeredLines := []invoicing.InvoiceLineInput{{
		Description: "Registered domestic support",
		Qty:         invoicing.MustQuantity("1"),
		UnitPrice:   invoicing.Money{Amount: 200_000, Currency: string(invoicing.CurrencyGBP)},
	}}
	draftedWhileRegistered, err = service.UpdateDraft(ctx, draftedWhileRegistered.ID, invoicing.DraftPatch{Lines: &registeredLines})
	if err != nil {
		t.Fatalf("UpdateDraft(registered lines) error = %v", err)
	}
	assertMoney(t, draftedWhileRegistered.Totals.VAT, 40_000, "GBP")

	setTestVATRegistered(t, profile, false)
	sentAfterDeregistration, err := service.Send(ctx, draftedWhileRegistered.ID)
	if err != nil {
		t.Fatalf("Send(after deregistration) error = %v", err)
	}
	assertMoney(t, sentAfterDeregistration.Totals.VAT, 0, "GBP")
	assertMoney(t, sentAfterDeregistration.Totals.Total, 200_000, "GBP")

	setTestVATRegistered(t, profile, true)
	stillZeroVATInvoice, err := service.Invoice(ctx, sentAfterDeregistration.ID)
	if err != nil {
		t.Fatalf("Invoice(sent after deregistration) error = %v", err)
	}
	assertMoney(t, stillZeroVATInvoice.Totals.VAT, 0, "GBP")
	zeroSnapshotPayload, err := service.InvoicePrintPayload(ctx, sentAfterDeregistration.ID, false)
	if err != nil {
		t.Fatalf("InvoicePrintPayload(unregistered snapshot) error = %v", err)
	}
	if zeroSnapshotPayload.VATRegistered {
		t.Fatal("unregistered snapshot payload VATRegistered = true, want false")
	}
	if zeroSnapshotPayload.Identity.VATNumber != nil {
		t.Fatalf("unregistered snapshot payload VATNumber = %v, want nil", *zeroSnapshotPayload.Identity.VATNumber)
	}
}

func TestInvoicingOverdueReadQueriesSweepFactsAndTotals(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	service := newInvoiceService(t, h)
	ctx := context.Background()

	overdueDraft := createEURInvoiceDraft(t, h, service, 20_000)
	overdueSent, err := service.Send(ctx, overdueDraft.ID)
	if err != nil {
		t.Fatalf("Send(overdue candidate) error = %v", err)
	}
	assertDate(t, overdueSent.DueDate, "2025-05-15")

	h.Clock.Set(time.Date(2025, 5, 10, 9, 0, 0, 0, time.UTC))
	fabrikam := fixtures.Fabrikam(t, h)
	gbpDraft, err := service.CreateDraft(ctx, fabrikam.ID)
	if err != nil {
		t.Fatalf("CreateDraft(GBP) error = %v", err)
	}
	gbpLines := []invoicing.InvoiceLineInput{{
		Description: "Current GBP delivery",
		Qty:         invoicing.MustQuantity("1"),
		UnitPrice:   invoicing.Money{Amount: 10_000, Currency: string(invoicing.CurrencyGBP)},
	}}
	gbpDraft, err = service.UpdateDraft(ctx, gbpDraft.ID, invoicing.DraftPatch{Lines: &gbpLines})
	if err != nil {
		t.Fatalf("UpdateDraft(GBP lines) error = %v", err)
	}
	currentSent, err := service.Send(ctx, gbpDraft.ID)
	if err != nil {
		t.Fatalf("Send(GBP current) error = %v", err)
	}
	assertDate(t, currentSent.DueDate, "2025-06-09")

	h.Clock.Set(overdueSent.DueDate)
	h.Clock.Advance(72 * time.Hour)

	all, err := service.List(ctx, invoicing.InvoiceListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List(all) error = %v", err)
	}
	counts := countsByStatus(all.Counts)
	if counts[invoicing.InvoiceStatusOverdue] != 1 || counts[invoicing.InvoiceStatusSent] != 1 ||
		counts[invoicing.InvoiceStatusDraft] != 0 || counts[invoicing.InvoiceStatusPaid] != 0 {
		t.Fatalf("status counts = %#v, want overdue=1 sent=1 draft=0 paid=0", counts)
	}

	overdueList, err := service.List(ctx, invoicing.InvoiceListFilter{
		Statuses: []invoicing.InvoiceStatus{invoicing.InvoiceStatusOverdue},
		Search:   "contoso",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("List(overdue search) error = %v", err)
	}
	if overdueList.TotalCount != 1 || len(overdueList.Invoices) != 1 {
		t.Fatalf("overdue list count = total %d len %d, want one row", overdueList.TotalCount, len(overdueList.Invoices))
	}
	overdueRow := overdueList.Invoices[0]
	if overdueRow.ID != overdueSent.ID || overdueRow.Status != invoicing.InvoiceStatusOverdue || overdueRow.DaysOverdue != 3 {
		t.Fatalf("overdue row = id %s status %q days %d, want %s overdue 3",
			overdueRow.ID, overdueRow.Status, overdueRow.DaysOverdue, overdueSent.ID)
	}

	if overdueSent.Number == nil {
		t.Fatal("overdue sent Number = nil")
	}
	byNumber, err := service.List(ctx, invoicing.InvoiceListFilter{Search: *overdueSent.Number, Limit: 10})
	if err != nil {
		t.Fatalf("List(number search) error = %v", err)
	}
	if byNumber.TotalCount != 1 || len(byNumber.Invoices) != 1 || byNumber.Invoices[0].ID != overdueSent.ID {
		t.Fatalf("number search result = total %d rows %#v, want overdue invoice", byNumber.TotalCount, byNumber.Invoices)
	}

	totals, err := service.Totals(ctx, invoicing.InvoiceListFilter{})
	if err != nil {
		t.Fatalf("Totals(all) error = %v", err)
	}
	if len(totals.Subtotals) != 2 {
		t.Fatalf("Totals subtotals = %#v, want EUR and GBP", totals.Subtotals)
	}
	assertMoney(t, totals.Subtotals[0], 20_000, "EUR")
	assertMoney(t, totals.Subtotals[1], 12_000, "GBP")
	assertMoney(t, totals.TotalGBP, 29_000, "GBP")

	var overdueEvents []invoicing.InvoiceOverdue
	h.Bus.Subscribe(invoicing.InvoiceOverdueName, func(_ context.Context, _ db.Tx, evt bus.Event) error {
		overdue, ok := evt.(invoicing.InvoiceOverdue)
		if !ok {
			t.Fatalf("InvoiceOverdue event = %T, want invoicing.InvoiceOverdue", evt)
		}
		overdueEvents = append(overdueEvents, overdue)
		return nil
	})
	if err := h.RunJob(invoicing.OverdueSweepJobName); err != nil {
		t.Fatalf("RunJob(%s) error = %v", invoicing.OverdueSweepJobName, err)
	}
	wantEvent := invoicing.InvoiceOverdue{InvoiceID: overdueSent.ID, DaysOverdue: 3}
	if !reflect.DeepEqual(overdueEvents, []invoicing.InvoiceOverdue{wantEvent}) {
		t.Fatalf("InvoiceOverdue events = %#v, want %#v", overdueEvents, []invoicing.InvoiceOverdue{wantEvent})
	}
	if err := h.RunJob(invoicing.OverdueSweepJobName); err != nil {
		t.Fatalf("RunJob(%s second run) error = %v", invoicing.OverdueSweepJobName, err)
	}
	if !reflect.DeepEqual(overdueEvents, []invoicing.InvoiceOverdue{wantEvent}) {
		t.Fatalf("InvoiceOverdue events after second run = %#v, want unchanged", overdueEvents)
	}

	facts, err := service.OverdueInvoices(ctx)
	if err != nil {
		t.Fatalf("OverdueInvoices() error = %v", err)
	}
	if len(facts) != 1 || facts[0].InvoiceID != overdueSent.ID || facts[0].DaysOverdue != 3 {
		t.Fatalf("OverdueInvoices() = %#v, want one fact for %s with 3 days", facts, overdueSent.ID)
	}
	assertMoney(t, facts[0].Amount, 20_000, "EUR")

	it.AssertLedgerBalanced(t, h)
}

func TestInvoicingSendGBPUsesIdentityLockAndPosting(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	service := newInvoiceService(t, h)

	fabrikam := fixtures.Fabrikam(t, h)
	draft, err := service.CreateDraft(context.Background(), fabrikam.ID)
	if err != nil {
		t.Fatalf("CreateDraft() error = %v", err)
	}
	lines := []invoicing.InvoiceLineInput{{
		Description: "GBP delivery",
		Qty:         invoicing.MustQuantity("1"),
		UnitPrice:   invoicing.Money{Amount: 10_000, Currency: string(invoicing.CurrencyGBP)},
	}}
	updated, err := service.UpdateDraft(context.Background(), draft.ID, invoicing.DraftPatch{Lines: &lines})
	if err != nil {
		t.Fatalf("UpdateDraft(lines) error = %v", err)
	}
	assertMoney(t, updated.Totals.Total, 12_000, "GBP")

	sent, err := service.Send(context.Background(), updated.ID)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if sent.Number == nil || *sent.Number != "INV-2025-01" {
		t.Fatalf("sent Number = %v, want INV-2025-01", sent.Number)
	}
	if sent.LockID == nil {
		t.Fatal("sent LockID = nil, want lock id")
	}
	assertInvoiceRateLock(t, h, *sent.LockID, "invoicing:INV-2025-01", "GBP", "GBP", "1.000000000000000000")
	assertInvoicingLedgerPostings(t, h, invoiceSendSourceRefForTest("INV-2025-01"), []wantInvoicePosting{
		{account: "1101-debtors-gbp", amount: 12_000, currency: "GBP", amountGBP: 12_000},
		{account: "4000-sales", amount: -10_000, currency: "GBP", amountGBP: -10_000},
		{account: "2200-vat-control", amount: -2_000, currency: "GBP", amountGBP: -2_000},
	})
	it.AssertLedgerBalanced(t, h)
}

func TestInvoicingRevertedDraftCanBeDeletedAfterVATContext(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	service := newInvoiceService(t, h)
	ctx := context.Background()

	fabrikam := fixtures.Fabrikam(t, h)
	draft, err := service.CreateDraft(ctx, fabrikam.ID)
	if err != nil {
		t.Fatalf("CreateDraft() error = %v", err)
	}
	lines := []invoicing.InvoiceLineInput{{
		Description: "GBP delivery",
		Qty:         invoicing.MustQuantity("1"),
		UnitPrice:   invoicing.Money{Amount: 10_000, Currency: string(invoicing.CurrencyGBP)},
	}}
	draft, err = service.UpdateDraft(ctx, draft.ID, invoicing.DraftPatch{Lines: &lines})
	if err != nil {
		t.Fatalf("UpdateDraft(lines) error = %v", err)
	}

	sent, err := service.Send(ctx, draft.ID)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if sent.SendLedgerEntryID == nil {
		t.Fatal("sent SendLedgerEntryID = nil")
	}
	reverted, err := service.RevertToDraft(ctx, sent.ID)
	if err != nil {
		t.Fatalf("RevertToDraft() error = %v", err)
	}
	if reverted.Status != invoicing.InvoiceStatusDraft {
		t.Fatalf("reverted Status = %q, want draft", reverted.Status)
	}

	if err := service.DeleteDraft(ctx, reverted.ID); err != nil {
		t.Fatalf("DeleteDraft(reverted draft) error = %v", err)
	}
	if _, err := service.Invoice(ctx, reverted.ID); !errors.Is(err, invoicing.ErrInvoiceNotFound) {
		t.Fatalf("Invoice(deleted reverted draft) error = %v, want ErrInvoiceNotFound", err)
	}

	modulePool := testdb.AsModule(t, invoicing.ModuleName)
	var contextRows int
	if err := modulePool.QueryRow(ctx, `
SELECT count(*)
FROM invoicing.invoice_send_vat_context
WHERE send_ledger_entry_id = $1`, *sent.SendLedgerEntryID).Scan(&contextRows); err != nil {
		t.Fatalf("count invoice_send_vat_context rows: %v", err)
	}
	if contextRows != 1 {
		t.Fatalf("invoice_send_vat_context rows = %d, want 1", contextRows)
	}
	it.AssertLedgerBalanced(t, h)
}

func TestInvoicingSendDomesticEURSplitsVATAtLockedRate(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	service := newInvoiceService(t, h)

	client, err := service.SaveClient(context.Background(), invoicing.Client{
		Name: "Euro Domestic Ltd",
		Address: invoicing.Address{
			Line1:      "1 Market Street",
			Locality:   "Douglas",
			PostalCode: "IM1 1AA",
			Country:    "IM",
		},
		DefaultCurrency: invoicing.CurrencyEUR,
		TermsDays:       14,
		VATTreatment:    invoicing.VATTreatmentDomestic,
	})
	if err != nil {
		t.Fatalf("SaveClient(domestic EUR) error = %v", err)
	}
	draft, err := service.CreateDraft(context.Background(), client.ID)
	if err != nil {
		t.Fatalf("CreateDraft(domestic EUR) error = %v", err)
	}
	lines := []invoicing.InvoiceLineInput{{
		Description: "EUR domestic delivery",
		Qty:         invoicing.MustQuantity("1"),
		UnitPrice:   invoicing.Money{Amount: 10_000, Currency: string(invoicing.CurrencyEUR)},
	}}
	updated, err := service.UpdateDraft(context.Background(), draft.ID, invoicing.DraftPatch{Lines: &lines})
	if err != nil {
		t.Fatalf("UpdateDraft(lines) error = %v", err)
	}
	assertMoney(t, updated.Totals.Total, 12_000, "EUR")

	sent, err := service.Send(context.Background(), updated.ID)
	if err != nil {
		t.Fatalf("Send(domestic EUR) error = %v", err)
	}
	if sent.Number == nil || *sent.Number != "INV-2025-01" {
		t.Fatalf("sent Number = %v, want INV-2025-01", sent.Number)
	}
	assertInvoicingLedgerPostings(t, h, invoiceSendSourceRefForTest("INV-2025-01"), []wantInvoicePosting{
		{account: "1100-debtors-eur", amount: 12_000, currency: "EUR", amountGBP: 10_200},
		{account: "4000-sales", amount: -10_000, currency: "EUR", amountGBP: -8_500},
		{account: "2200-vat-control", amount: -2_000, currency: "EUR", amountGBP: -1_700},
	})
	it.AssertLedgerBalanced(t, h)
}

func TestInvoicingSendRollsBackOnBusFailureAndPreservesNumberGap(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	service := newInvoiceService(t, h)
	forced := errors.New("forced invoice sent subscriber failure")
	h.Bus.Subscribe(invoicing.InvoiceSentName, func(context.Context, db.Tx, bus.Event) error {
		return nil
	})
	h.FailNextBusSubscriber(invoicing.InvoiceSentName, forced)

	draft := createEURInvoiceDraft(t, h, service, 450_000)
	_, err := service.Send(context.Background(), draft.ID)
	if !errors.Is(err, forced) {
		t.Fatalf("Send() error = %v, want forced subscriber failure", err)
	}
	fetched, err := service.Invoice(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("Invoice() after failed send error = %v", err)
	}
	if fetched.Status != invoicing.InvoiceStatusDraft || fetched.Number != nil || fetched.LockID != nil {
		t.Fatalf("invoice after failed send = status %q number %v lock %v, want draft without number/lock", fetched.Status, fetched.Number, fetched.LockID)
	}
	assertRateLockCount(t, h, "invoicing:INV-2025-%", 0)
	assertLedgerEntryCountForSource(t, h, invoicing.ModuleName, "", 0)

	sent, err := service.Send(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("Send() retry error = %v", err)
	}
	if sent.Number == nil || *sent.Number != "INV-2025-01" {
		t.Fatalf("retry Number = %v, want no gap at INV-2025-01", sent.Number)
	}
	if sent.LockID == nil {
		t.Fatal("retry LockID = nil, want new lock")
	}
	assertRateLockCount(t, h, "invoicing:INV-2025-%", 1)
	it.AssertLedgerBalanced(t, h)
}

func TestInvoicingMarkSettledPublishesRealisedFX(t *testing.T) {
	tests := []struct {
		name          string
		rates         fixtures.RateTable
		wantRealised  int64
		wantFXPosting []wantInvoicePosting
	}{
		{
			name:         "flat zero delta",
			rates:        fixtures.RatesFlat085,
			wantRealised: 0,
		},
		{
			name: "step gain",
			rates: fixtures.RatesStep(map[time.Time]string{
				time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC): "0.8500",
				time.Date(2025, 5, 2, 0, 0, 0, 0, time.UTC): "0.8600",
			}),
			wantRealised: 4_500,
			wantFXPosting: []wantInvoicePosting{
				{account: "1101-debtors-gbp", amount: 4_500, currency: "GBP", amountGBP: 4_500},
				{account: "4900-fx-gain-loss", amount: -4_500, currency: "GBP", amountGBP: -4_500},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
			fixtures.Rates(t, h, test.rates)
			service := newInvoiceService(t, h)
			sent, err := service.Send(context.Background(), createEURInvoiceDraft(t, h, service, 450_000).ID)
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			lockID := mustInvoiceLockID(t, sent)

			var realisedEvents []moneyfx.RealisedFX
			h.Bus.Subscribe(moneyfx.RealisedFXName, func(_ context.Context, _ db.Tx, evt bus.Event) error {
				realised, ok := evt.(moneyfx.RealisedFX)
				if !ok {
					t.Fatalf("RealisedFX event = %T, want moneyfx.RealisedFX", evt)
				}
				realisedEvents = append(realisedEvents, realised)
				return nil
			})

			settled, err := markSettledFromBankingTx(t, h, service, sent.ID, "bank-txn-123", time.Date(2025, 5, 2, 0, 0, 0, 0, time.UTC), invoicing.Money{Amount: 450_000, Currency: "EUR"})
			if err != nil {
				t.Fatalf("MarkSettled() error = %v", err)
			}
			if settled.Status != invoicing.InvoiceStatusPaid {
				t.Fatalf("settled Status = %q, want paid", settled.Status)
			}
			assertRealisedFXRow(t, h, sent.ID, lockID, 1, test.wantRealised)
			if test.wantRealised == 0 {
				if len(realisedEvents) != 0 {
					t.Fatalf("RealisedFX events = %#v, want none", realisedEvents)
				}
				assertLedgerEntryCountForSource(t, h, moneyfx.ModuleName, invoiceSettlementSourceRefForTest(sent.ID), 0)
			} else {
				wantEvents := []moneyfx.RealisedFX{{
					InvoiceID: sent.ID,
					AmountGBP: invoicing.Money{Amount: test.wantRealised, Currency: "GBP"},
				}}
				if !reflect.DeepEqual(realisedEvents, wantEvents) {
					t.Fatalf("RealisedFX events = %#v, want %#v", realisedEvents, wantEvents)
				}
				assertModuleLedgerPostings(t, h, moneyfx.ModuleName, invoiceSettlementSourceRefForTest(sent.ID), test.wantFXPosting)
			}
			it.AssertLedgerBalanced(t, h)
		})
	}
}

func TestInvoicingMarkSettledWrongAmountDoesNotChangeInvoice(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	service := newInvoiceService(t, h)
	sent, err := service.Send(context.Background(), createEURInvoiceDraft(t, h, service, 450_000).ID)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	lockID := mustInvoiceLockID(t, sent)

	_, err = markSettledFromBankingTx(t, h, service, sent.ID, "bank-txn-wrong", time.Date(2025, 5, 2, 0, 0, 0, 0, time.UTC), invoicing.Money{Amount: 449_999, Currency: "EUR"})
	if !errors.Is(err, invoicing.ErrInvoicePartialPayment) {
		t.Fatalf("MarkSettled(wrong amount) error = %v, want ErrInvoicePartialPayment", err)
	}
	fetched, err := service.Invoice(context.Background(), sent.ID)
	if err != nil {
		t.Fatalf("Invoice() after wrong settlement error = %v", err)
	}
	if fetched.Status != invoicing.InvoiceStatusSent || fetched.SettlementTxnRef != nil || fetched.SettledDate != nil || fetched.SettledAmount != nil {
		t.Fatalf("invoice after wrong settlement = status %q txn %v date %v amount %v, want unchanged sent", fetched.Status, fetched.SettlementTxnRef, fetched.SettledDate, fetched.SettledAmount)
	}
	assertRealisedFXRow(t, h, sent.ID, lockID, 0, 0)
	assertLedgerEntryCountForSource(t, h, moneyfx.ModuleName, invoiceSettlementSourceRefForTest(sent.ID), 0)
	it.AssertLedgerBalanced(t, h)
}

func TestInvoicingRevertToDraftReversesSendAndResendConsumesNewNumber(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	service := newInvoiceService(t, h)
	sent, err := service.Send(context.Background(), createEURInvoiceDraft(t, h, service, 450_000).ID)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	firstLockID := mustInvoiceLockID(t, sent)

	reverted, err := service.RevertToDraft(context.Background(), sent.ID)
	if err != nil {
		t.Fatalf("RevertToDraft() error = %v", err)
	}
	if reverted.Status != invoicing.InvoiceStatusDraft || reverted.Number != nil || reverted.LockID != nil {
		t.Fatalf("reverted invoice = status %q number %v lock %v, want draft without number/lock", reverted.Status, reverted.Number, reverted.LockID)
	}
	assertSourceRefNetsZero(t, h, invoicing.ModuleName, invoiceSendSourceRefForTest("INV-2025-01"))

	resent, err := service.Send(context.Background(), reverted.ID)
	if err != nil {
		t.Fatalf("Send() after revert error = %v", err)
	}
	if resent.Number == nil || *resent.Number != "INV-2025-02" {
		t.Fatalf("resent Number = %v, want INV-2025-02", resent.Number)
	}
	secondLockID := mustInvoiceLockID(t, resent)
	if secondLockID == firstLockID {
		t.Fatalf("resent lock id = %d, want different from first lock %d", secondLockID, firstLockID)
	}
	assertInvoiceRateLock(t, h, *resent.LockID, "invoicing:INV-2025-02", "EUR", "GBP", "0.850000000000000000")
	it.AssertLedgerBalanced(t, h)
}

func TestInvoicingRevertToDraftAllowsBackdatedInvoiceSentToday(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2025, 5, 2, 9, 0, 0, 0, time.UTC)})
	fixtures.Rates(t, h)
	service := newInvoiceService(t, h)
	draft := createEURInvoiceDraft(t, h, service, 450_000)
	issueDate := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	backdated, err := service.UpdateDraft(context.Background(), draft.ID, invoicing.DraftPatch{IssueDate: &issueDate})
	if err != nil {
		t.Fatalf("UpdateDraft(issue date) error = %v", err)
	}
	sent, err := service.Send(context.Background(), backdated.ID)
	if err != nil {
		t.Fatalf("Send() backdated error = %v", err)
	}
	if !sent.IssueDate.Equal(issueDate) {
		t.Fatalf("sent IssueDate = %s, want %s", sent.IssueDate, issueDate)
	}

	reverted, err := service.RevertToDraft(context.Background(), sent.ID)
	if err != nil {
		t.Fatalf("RevertToDraft(backdated sent today) error = %v", err)
	}
	if reverted.Status != invoicing.InvoiceStatusDraft {
		t.Fatalf("reverted Status = %q, want draft", reverted.Status)
	}
	assertSourceRefNetsZero(t, h, invoicing.ModuleName, invoiceSendSourceRefForTest("INV-2025-01"))
	it.AssertLedgerBalanced(t, h)
}

func TestInvoicingNumberingConcurrentGapFree(t *testing.T) {
	h := harness.New(t, harness.Options{})
	_ = h
	modulePool := testdb.AsModule(t, invoicing.ModuleName)
	store := invoicing.Store{}

	const workers = 50
	start := make(chan struct{})
	results := make(chan string, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			tx, err := modulePool.Begin(ctx)
			if err != nil {
				errs <- fmt.Errorf("begin tx: %w", err)
				return
			}
			committed := false
			defer func() {
				if !committed {
					_ = tx.Rollback(context.Background())
				}
			}()
			number, err := store.NextNumber(ctx, tx, 2025)
			if err != nil {
				errs <- err
				return
			}
			if err := tx.Commit(ctx); err != nil {
				errs <- fmt.Errorf("commit tx: %w", err)
				return
			}
			committed = true
			results <- number
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent NextNumber error = %v", err)
		}
	}

	got := make([]string, 0, workers)
	for number := range results {
		got = append(got, number)
	}
	sort.Strings(got)
	if len(got) != workers {
		t.Fatalf("len(numbers) = %d, want %d; numbers=%v", len(got), workers, got)
	}
	for i, number := range got {
		want := fmt.Sprintf("INV-2025-%02d", i+1)
		if number != want {
			t.Fatalf("number[%d] = %q, want %q; all=%v", i, number, want, got)
		}
	}
}

func newInvoiceService(t testing.TB, h *harness.Harness, opts ...invoicing.ServiceOption) *invoicing.Service {
	t.Helper()
	modulePool := testdb.AsModule(t, invoicing.ModuleName)
	moneyFXPool := testdb.AsModule(t, moneyfx.ModuleName)
	rateLocks := testRateLocker{service: moneyfx.NewService(moneyfx.NewStore(moneyFXPool), h.Clock)}
	serviceOpts := []invoicing.ServiceOption{
		invoicing.WithClock(h.Clock),
		invoicing.WithTodayRate(fakeTodayRate),
		invoicing.WithRateLocker(rateLocks),
		invoicing.WithRateLockReader(rateLocks),
		invoicing.WithLedger(ledger.New(h.LedgerPool, h.Bus)),
		invoicing.WithEventBus(h.Bus),
	}
	serviceOpts = append(serviceOpts, opts...)
	return invoicing.NewService(modulePool, invoicing.Store{}, serviceOpts...)
}

func testIdentityProfile(t testing.TB, h *harness.Harness) *identity.TransactionalProfileService {
	t.Helper()
	profile := identity.NewTransactionalProfileService(testdb.AsModule(t, "identity"), h.Bus, identity.WithDataDir(t.TempDir()))
	incorporationDate := "2020-07-14"
	yearEnd, err := identity.NewYearEnd(3, 31)
	if err != nil {
		t.Fatalf("NewYearEnd() error = %v", err)
	}
	office := identity.RegisteredOffice{
		Line1:      "18 Athol St",
		Line2:      "",
		Locality:   "Douglas",
		Region:     "",
		PostalCode: "IM1 1JA",
		Country:    "IM",
	}
	vatNumber := "GB 123 4567 89"
	bankDetails := identity.BankDetails{
		IBAN:     "GB82 WEST 1234 5698 7654 32",
		BIC:      "WESTGB2L",
		BankName: "Example Bank",
	}
	shareholders := []identity.Shareholder{{
		Name:   "N. Meyer",
		Shares: 100,
		Class:  "ordinary GBP 1",
	}}
	if err := profile.UpdateProfile(context.Background(), identity.UpdateProfilePatch{
		TradingName:       stringPtrForTest("NPM Limited"),
		LegalName:         stringPtrForTest("NPM Limited"),
		CompanyNumber:     stringPtrForTest("137792C"),
		RegisteredOffice:  &office,
		IncorporationDate: &incorporationDate,
		YearEnd:           &yearEnd,
		VATNumber:         &vatNumber,
		BankDetails:       &bankDetails,
		Shareholders:      &shareholders,
	}); err != nil {
		t.Fatalf("UpdateProfile(seed) error = %v", err)
	}
	return profile
}

func setTestVATRegistered(t testing.TB, profile *identity.TransactionalProfileService, registered bool) {
	t.Helper()
	if err := profile.UpdateProfile(context.Background(), identity.UpdateProfilePatch{IsVATRegistered: &registered}); err != nil {
		t.Fatalf("UpdateProfile(IsVATRegistered=%v) error = %v", registered, err)
	}
}

func stringPtrForTest(value string) *string {
	return &value
}

type recordingPDFEngine struct {
	mu       sync.Mutex
	failures int
	attempts int
	pdf      []byte
	payloads []invoicing.InvoicePrintPayload
}

func newRecordingPDFEngine(failures int, pdf []byte) *recordingPDFEngine {
	return &recordingPDFEngine{
		failures: failures,
		pdf:      append([]byte{}, pdf...),
	}
}

func (e *recordingPDFEngine) RenderInvoicePDF(_ context.Context, payload invoicing.InvoicePrintPayload) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.attempts++
	e.payloads = append(e.payloads, payload)
	if e.attempts <= e.failures {
		return nil, errors.New("forced PDF render failure")
	}
	return append([]byte{}, e.pdf...), nil
}

func (e *recordingPDFEngine) attemptCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.attempts
}

func (e *recordingPDFEngine) setPDF(pdf []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pdf = append([]byte{}, pdf...)
}

type recordingPDFAssetStore struct {
	mu    sync.Mutex
	bytes [][]byte
}

func (s *recordingPDFAssetStore) StoreInvoicePDF(_ context.Context, pdf []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bytes = append(s.bytes, append([]byte{}, pdf...))
	return fmt.Sprintf("/api/identity/assets/test-pdf-%d", len(s.bytes)), nil
}

func (s *recordingPDFAssetStore) LoadInvoicePDF(_ context.Context, assetURL string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw := strings.TrimPrefix(strings.TrimSpace(assetURL), "/api/identity/assets/test-pdf-")
	index, err := strconv.Atoi(raw)
	if err != nil || index < 1 || index > len(s.bytes) {
		return nil, fmt.Errorf("test PDF asset %q not found", assetURL)
	}
	return append([]byte{}, s.bytes[index-1]...), nil
}

func (s *recordingPDFAssetStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bytes)
}

func (s *recordingPDFAssetStore) bytesAt(i int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte{}, s.bytes[i]...)
}

func waitForPDFAsset(t testing.TB, service *invoicing.Service, id string, want string, debug func() string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		invoice, err := service.Invoice(context.Background(), id)
		if err != nil {
			t.Fatalf("Invoice(%s) while waiting for PDF: %v", id, err)
		}
		if invoice.PDFAsset != nil && *invoice.PDFAsset == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for PDF asset %q; invoice asset = %v; %s", want, invoice.PDFAsset, debug())
		case <-tick.C:
		}
	}
}

type testRateLocker struct {
	service *moneyfx.Service
}

func (l testRateLocker) LockRate(ctx context.Context, tx db.Tx, ref invoicing.RateLockRef, from string, to string, date time.Time) (invoicing.RateLock, error) {
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

func (l testRateLocker) RateLock(ctx context.Context, id int64) (invoicing.RateLock, error) {
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

type wantInvoicePosting struct {
	account   string
	amount    int64
	currency  string
	amountGBP int64
}

func createEURInvoiceDraft(t testing.TB, h *harness.Harness, service *invoicing.Service, amount int64) invoicing.Invoice {
	t.Helper()

	contoso := fixtures.Contoso(t, h)
	draft, err := service.CreateDraft(context.Background(), contoso.ID)
	if err != nil {
		t.Fatalf("CreateDraft() error = %v", err)
	}
	lines := []invoicing.InvoiceLineInput{{
		Description: "Monthly retainer",
		Qty:         invoicing.MustQuantity("1"),
		UnitPrice:   invoicing.Money{Amount: amount, Currency: string(invoicing.CurrencyEUR)},
	}}
	updated, err := service.UpdateDraft(context.Background(), draft.ID, invoicing.DraftPatch{Lines: &lines})
	if err != nil {
		t.Fatalf("UpdateDraft(lines) error = %v", err)
	}
	return updated
}

func markSettledFromBankingTx(t testing.TB, h *harness.Harness, service *invoicing.Service, id string, txnRef string, date time.Time, amount invoicing.Money) (_ invoicing.Invoice, err error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bankingPool := testdb.AsModule(t, banking.ModuleName)
	tx, err := bankingPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin banking transaction: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	settled, err := service.MarkSettled(ctx, tx, id, txnRef, date, amount)
	if err != nil {
		return invoicing.Invoice{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return invoicing.Invoice{}, fmt.Errorf("commit banking settlement transaction: %w", err)
	}
	committed = true
	return settled, nil
}

func mustInvoiceLockID(t testing.TB, invoice invoicing.Invoice) int64 {
	t.Helper()
	if invoice.LockID == nil {
		t.Fatal("invoice LockID = nil")
	}
	var id int64
	if _, err := fmt.Sscan(*invoice.LockID, &id); err != nil {
		t.Fatalf("parse LockID %q: %v", *invoice.LockID, err)
	}
	return id
}

func assertInvoiceRateLock(t testing.TB, h *harness.Harness, lockID string, ref string, from string, to string, rate string) {
	t.Helper()

	var gotRef, gotFrom, gotTo, gotRate string
	if err := h.DB.QueryRow(context.Background(), `
SELECT ref, from_currency, to_currency, rate
FROM moneyfx.rate_locks
WHERE id = $1`, lockID).Scan(&gotRef, &gotFrom, &gotTo, &gotRate); err != nil {
		t.Fatalf("query rate lock %s: %v", lockID, err)
	}
	if gotRef != ref || gotFrom != from || gotTo != to || gotRate != rate {
		t.Fatalf("rate lock %s = ref=%s from=%s to=%s rate=%s, want ref=%s from=%s to=%s rate=%s",
			lockID, gotRef, gotFrom, gotTo, gotRate, ref, from, to, rate)
	}
}

func assertRateLockCount(t testing.TB, h *harness.Harness, refLike string, want int) {
	t.Helper()

	var got int
	if err := h.DB.QueryRow(context.Background(), `
SELECT count(*)
FROM moneyfx.rate_locks
WHERE ref LIKE $1`, refLike).Scan(&got); err != nil {
		t.Fatalf("count rate locks %s: %v", refLike, err)
	}
	if got != want {
		t.Fatalf("rate lock count for %s = %d, want %d", refLike, got, want)
	}
}

func assertInvoicingLedgerPostings(t testing.TB, h *harness.Harness, sourceRef string, want []wantInvoicePosting) {
	t.Helper()
	assertModuleLedgerPostings(t, h, invoicing.ModuleName, sourceRef, want)
}

func assertModuleLedgerPostings(t testing.TB, h *harness.Harness, module string, sourceRef string, want []wantInvoicePosting) {
	t.Helper()

	rows, err := h.DB.Query(context.Background(), `
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

	var got []wantInvoicePosting
	for rows.Next() {
		var posting wantInvoicePosting
		if err := rows.Scan(&posting.account, &posting.amount, &posting.currency, &posting.amountGBP); err != nil {
			t.Fatalf("scan ledger posting for %s/%s: %v", module, sourceRef, err)
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

func assertLedgerEntryCountForSource(t testing.TB, h *harness.Harness, module string, sourceRef string, want int) {
	t.Helper()

	query := `
SELECT count(*)
FROM ledger.journal_entries
WHERE source_module = $1`
	args := []any{module}
	if sourceRef != "" {
		query += ` AND source_ref = $2`
		args = append(args, sourceRef)
	}

	var got int
	if err := h.DB.QueryRow(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("count ledger entries for %s/%s: %v", module, sourceRef, err)
	}
	if got != want {
		t.Fatalf("ledger entry count for %s/%s = %d, want %d", module, sourceRef, got, want)
	}
}

func assertRealisedFXRow(t testing.TB, h *harness.Harness, invoiceID string, lockID int64, wantCount int, wantAmountGBP int64) {
	t.Helper()

	rows, err := h.DB.Query(context.Background(), `
SELECT amount_gbp
FROM moneyfx.realised_fx
WHERE invoice_id = $1
	AND lock_id = $2`, invoiceID, lockID)
	if err != nil {
		t.Fatalf("query realised FX rows: %v", err)
	}
	defer rows.Close()

	var amounts []int64
	for rows.Next() {
		var amount int64
		if err := rows.Scan(&amount); err != nil {
			t.Fatalf("scan realised FX row: %v", err)
		}
		amounts = append(amounts, amount)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("collect realised FX rows: %v", err)
	}
	if len(amounts) != wantCount {
		t.Fatalf("realised FX rows = %d (%v), want %d", len(amounts), amounts, wantCount)
	}
	if wantCount > 0 && amounts[0] != wantAmountGBP {
		t.Fatalf("realised FX amount = %d, want %d", amounts[0], wantAmountGBP)
	}
}

func assertSourceRefNetsZero(t testing.TB, h *harness.Harness, module string, sourceRef string) {
	t.Helper()

	rows, err := h.DB.Query(context.Background(), `
SELECT p.currency, COALESCE(sum(p.amount), 0)::bigint, COALESCE(sum(p.amount_gbp), 0)::bigint
FROM ledger.journal_entries AS je
JOIN ledger.postings AS p ON p.entry_id = je.id
WHERE je.source_module = $1
	AND je.source_ref = $2
GROUP BY p.currency
ORDER BY p.currency`, module, sourceRef)
	if err != nil {
		t.Fatalf("query source ref net for %s/%s: %v", module, sourceRef, err)
	}
	defer rows.Close()

	seen := false
	for rows.Next() {
		seen = true
		var currency string
		var amount, amountGBP int64
		if err := rows.Scan(&currency, &amount, &amountGBP); err != nil {
			t.Fatalf("scan source ref net for %s/%s: %v", module, sourceRef, err)
		}
		if amount != 0 || amountGBP != 0 {
			t.Fatalf("source ref %s/%s nets %d %s and %d GBP, want zero", module, sourceRef, amount, currency, amountGBP)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("collect source ref net for %s/%s: %v", module, sourceRef, err)
	}
	if !seen {
		t.Fatalf("source ref %s/%s had no postings, want reversal postings", module, sourceRef)
	}
}

func invoiceSendSourceRefForTest(number string) string {
	return "invoice:" + number + ":send"
}

func invoiceSettlementSourceRefForTest(id string) string {
	return "invoice:" + id + ":settlement"
}

func fakeTodayRate(_ context.Context, from string, to string) (invoicing.FXRate, time.Time, error) {
	value := "4/5"
	if from == to {
		value = "1"
	}
	return invoicing.FXRate{
		From:   from,
		To:     to,
		Value:  value,
		Source: "test",
	}, time.Date(2025, 5, 1, 12, 0, 0, 0, time.UTC), nil
}

func countsByStatus(counts []invoicing.InvoiceStatusCount) map[invoicing.InvoiceStatus]int {
	result := make(map[invoicing.InvoiceStatus]int, len(counts))
	for _, count := range counts {
		result[count.Status] = count.Count
	}
	return result
}

func assertMoney(t testing.TB, got invoicing.Money, wantAmount int64, wantCurrency string) {
	t.Helper()
	if got.Amount != wantAmount || got.Currency != wantCurrency {
		t.Fatalf("money = %+v, want %d %s", got, wantAmount, wantCurrency)
	}
}

func assertDate(t testing.TB, got time.Time, want string) {
	t.Helper()
	if got.Format(time.DateOnly) != want {
		t.Fatalf("date = %s, want %s", got.Format(time.DateOnly), want)
	}
}
