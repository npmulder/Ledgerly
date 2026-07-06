package moneyfx

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

var ErrRateUnavailable = errors.New("moneyfx: rate unavailable")

// Rate is an exact FX multiplier from one currency into another.
type Rate struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

// Rat parses the exact rate value for use with money.MulRat.
func (r Rate) Rat() (*big.Rat, error) {
	value := strings.TrimSpace(r.Value)
	if value == "" {
		return nil, fmt.Errorf("moneyfx: rate value is required")
	}
	rat, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, fmt.Errorf("moneyfx: parse rate %q", r.Value)
	}
	return rat, nil
}

// TodayRate returns today's presentation FX rate. Full ECB-backed lookup lands
// in the later moneyfx rates work; until then, identity conversions are exact
// and non-identity pairs report a typed unavailable error.
func TodayRate(ctx context.Context, from string, to string) (Rate, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return Rate{}, time.Time{}, err
	}
	from = strings.ToUpper(strings.TrimSpace(from))
	to = strings.ToUpper(strings.TrimSpace(to))
	if from == "" || to == "" {
		return Rate{}, time.Time{}, fmt.Errorf("moneyfx: from and to currencies are required")
	}
	if from == to {
		return Rate{
			From:   from,
			To:     to,
			Value:  "1",
			Source: "identity",
		}, time.Now().UTC(), nil
	}
	return Rate{}, time.Time{}, ErrRateUnavailable
}
