package moneyfx

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

const (
	tradeDebtorsGBPAccount ledger.AccountCode = "1101-debtors-gbp"
	realisedFXAccount      ledger.AccountCode = "4900-fx-gain-loss"
)

// SubscribeEvents wires moneyfx settlement reactions onto the in-process bus.
func (m *Module) SubscribeEvents(eventBus *bus.Bus) {
	if m == nil || eventBus == nil {
		return
	}
	handler := realisedFXHandler{
		service: m.service,
		ledger:  m.ledger,
		bus:     eventBus,
	}
	eventBus.Subscribe(invoicing.InvoiceSettledName, handler.Handle)
}

type realisedFXHandler struct {
	service *Service
	ledger  ledger.Ledger
	bus     *bus.Bus
}

// Handle consumes invoicing.InvoiceSettled events.
func (h realisedFXHandler) Handle(ctx context.Context, tx db.Tx, evt bus.Event) error {
	settled, err := invoiceSettledEvent(evt)
	if err != nil {
		return err
	}
	return h.handleInvoiceSettled(ctx, tx, settled)
}

// handleInvoiceSettled posts realised FX for one settled invoice.
//
// Sign convention: AmountGBP is positive for a realised FX gain and negative
// for a loss.
//
// Posting shape, using EUR 4,500 locked at 0.8500 and settled at 0.8600:
// locked GBP is 4,500.00 x 0.8500 = GBP 3,825.00, settled GBP is
// GBP 3,870.00, so the GBP 45.00 gain posts only GBP legs:
// Dr 1101-debtors-gbp GBP 45.00 / Cr 4900-fx-gain-loss GBP 45.00. A loss
// reverses those signs. No EUR native posting is added; the realised FX entry
// is GBP-only and internally zero-balanced.
func (h realisedFXHandler) handleInvoiceSettled(ctx context.Context, tx db.Tx, evt invoicing.InvoiceSettled) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tx == nil {
		return fmt.Errorf("moneyfx: realised FX requires transaction")
	}
	if h.service == nil || h.service.realised == nil {
		return fmt.Errorf("moneyfx: realised FX store is required")
	}
	if h.ledger == nil {
		return fmt.Errorf("moneyfx: realised FX requires ledger")
	}
	if h.bus == nil {
		return fmt.Errorf("moneyfx: realised FX requires event bus")
	}

	normalized, err := normalizeInvoiceSettled(evt)
	if err != nil {
		return err
	}
	lock, err := loadRateLockByID(ctx, tx, LockID(normalized.LockID))
	if err != nil {
		return err
	}
	delta, err := h.realisedFXAmount(ctx, tx, normalized, lock)
	if err != nil {
		return err
	}

	inserted, err := h.service.realised.InsertRealisedFX(ctx, tx, newRealisedFX{
		InvoiceID:      normalized.InvoiceID,
		LockID:         LockID(normalized.LockID),
		SettlementDate: normalized.SettlementDate,
		AmountGBP:      delta,
		SourceRef:      normalized.SourceRef,
	})
	if err != nil {
		return err
	}
	if !inserted || delta.IsZero() {
		return nil
	}

	entry, err := realisedFXJournalEntry(normalized, delta)
	if err != nil {
		return err
	}
	if _, err := h.ledger.Post(ctx, tx, entry); err != nil {
		return err
	}
	if err := h.bus.Publish(ctx, tx, RealisedFX{
		InvoiceID: normalized.InvoiceID,
		AmountGBP: delta,
	}); err != nil {
		return fmt.Errorf("moneyfx: publish realised FX: %w", err)
	}
	return nil
}

func (h realisedFXHandler) realisedFXAmount(ctx context.Context, tx db.Tx, evt invoicing.InvoiceSettled, lock RateLock) (money.Money, error) {
	native := evt.NativeAmount
	if lock.From != native.Currency {
		return money.Money{}, fmt.Errorf("moneyfx: invoice %s lock currency is %s, settlement currency is %s", evt.InvoiceID, lock.From, native.Currency)
	}
	if lock.To != "GBP" {
		return money.Money{}, fmt.Errorf("moneyfx: invoice %s lock target is %s, want GBP", evt.InvoiceID, lock.To)
	}

	rateService := &Service{
		store: txRateReader{tx: tx},
		clock: h.service.clock,
	}
	settledGBP, err := rateService.ToGBP(ctx, native, evt.SettlementDate)
	if err != nil {
		return money.Money{}, err
	}
	lockedGBP, err := lockedGBPAmount(native, lock)
	if err != nil {
		return money.Money{}, err
	}
	delta, err := settledGBP.Sub(lockedGBP)
	if err != nil {
		return money.Money{}, fmt.Errorf("moneyfx: subtract locked GBP: %w", err)
	}
	return delta, nil
}

func realisedFXJournalEntry(evt invoicing.InvoiceSettled, amountGBP money.Money) (ledger.NewJournalEntry, error) {
	fxAmount, err := amountGBP.Negate()
	if err != nil {
		return ledger.NewJournalEntry{}, fmt.Errorf("moneyfx: negate realised FX amount: %w", err)
	}
	descriptionKind := "gain"
	if amountGBP.Amount < 0 {
		descriptionKind = "loss"
	}
	return ledger.NewJournalEntry{
		Date:         evt.SettlementDate,
		Description:  fmt.Sprintf("Realised FX %s for invoice %s", descriptionKind, evt.InvoiceID),
		SourceModule: ModuleName,
		SourceRef:    evt.SourceRef,
		Postings: []ledger.NewPosting{
			{AccountCode: tradeDebtorsGBPAccount, Amount: amountGBP, AmountGBP: amountGBP},
			{AccountCode: realisedFXAccount, Amount: fxAmount, AmountGBP: fxAmount},
		},
	}, nil
}

func lockedGBPAmount(native money.Money, lock RateLock) (money.Money, error) {
	rat, ok := new(big.Rat).SetString(strings.TrimSpace(lock.Rate))
	if !ok {
		return money.Money{}, fmt.Errorf("moneyfx: parse lock rate %q", lock.Rate)
	}
	locked := native.MulRat(rat)
	locked.Currency = "GBP"
	return locked, nil
}

func normalizeInvoiceSettled(evt invoicing.InvoiceSettled) (invoicing.InvoiceSettled, error) {
	normalized := invoicing.InvoiceSettled{
		InvoiceID:      strings.TrimSpace(evt.InvoiceID),
		LockID:         evt.LockID,
		NativeAmount:   evt.NativeAmount,
		SettlementDate: normalizeRateDate(evt.SettlementDate),
		SourceRef:      strings.TrimSpace(evt.SourceRef),
	}
	currency, err := normalizeCurrency(normalized.NativeAmount.Currency)
	if err != nil {
		return invoicing.InvoiceSettled{}, fmt.Errorf("moneyfx: settlement amount currency: %w", err)
	}
	normalized.NativeAmount.Currency = currency
	if normalized.InvoiceID == "" {
		return invoicing.InvoiceSettled{}, fmt.Errorf("moneyfx: settlement invoice id is required")
	}
	if normalized.LockID <= 0 {
		return invoicing.InvoiceSettled{}, fmt.Errorf("moneyfx: settlement lock id %d: %w", normalized.LockID, ErrLockNotFound)
	}
	if normalized.NativeAmount.Amount <= 0 {
		return invoicing.InvoiceSettled{}, fmt.Errorf("moneyfx: settlement native amount must be positive")
	}
	if normalized.SettlementDate.IsZero() {
		return invoicing.InvoiceSettled{}, fmt.Errorf("moneyfx: settlement date is required")
	}
	if normalized.SourceRef == "" {
		return invoicing.InvoiceSettled{}, fmt.Errorf("moneyfx: settlement source ref is required")
	}
	return normalized, nil
}

func invoiceSettledEvent(evt bus.Event) (invoicing.InvoiceSettled, error) {
	switch e := evt.(type) {
	case invoicing.InvoiceSettled:
		return e, nil
	case *invoicing.InvoiceSettled:
		if e == nil {
			return invoicing.InvoiceSettled{}, errors.New("moneyfx: nil InvoiceSettled event")
		}
		return *e, nil
	default:
		return invoicing.InvoiceSettled{}, fmt.Errorf("moneyfx: got %T, want invoicing.InvoiceSettled", evt)
	}
}
