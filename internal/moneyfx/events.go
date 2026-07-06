package moneyfx

import "github.com/npmulder/ledgerly/internal/moneyfx/money"

// RealisedFXName is the bus event name emitted after realised FX is posted.
const RealisedFXName = "moneyfx.RealisedFX"

// RealisedFX reports a settled invoice's realised FX gain or loss in GBP.
//
// AmountGBP uses the moneyfx sign convention: positive is a gain and negative
// is a loss.
type RealisedFX struct {
	InvoiceID string
	AmountGBP money.Money
}

// Name implements bus.Event.
func (RealisedFX) Name() string {
	return RealisedFXName
}
