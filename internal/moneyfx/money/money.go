package money

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

const (
	defaultDecimals = 2
	maxInt64        = int64(^uint64(0) >> 1)
	minInt64        = -maxInt64 - 1
)

var (
	// ErrCurrencyMismatch is returned when an operation requires both operands
	// to be in the same currency.
	ErrCurrencyMismatch = errors.New("money: currency mismatch")

	// ErrOverflow is returned when integer minor-unit arithmetic would overflow.
	ErrOverflow = errors.New("money: overflow")
)

// Money stores an exact amount in minor units for a currency.
type Money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

type currencyInfo struct {
	symbol   string
	decimals int
}

type allocationRemainder struct {
	index     int
	remainder *big.Int
}

// Zero returns a zero amount in currency.
func Zero(currency string) Money {
	return Money{Currency: currency}
}

// IsZero reports whether m is zero minor units.
func (m Money) IsZero() bool {
	return m.Amount == 0
}

// Add returns m + other, enforcing matching currencies and int64 overflow.
func (m Money) Add(other Money) (Money, error) {
	if err := m.checkCurrency(other); err != nil {
		return Money{}, err
	}
	amount, ok := addInt64(m.Amount, other.Amount)
	if !ok {
		return Money{}, ErrOverflow
	}
	return Money{Amount: amount, Currency: m.Currency}, nil
}

// Sub returns m - other, enforcing matching currencies and int64 overflow.
func (m Money) Sub(other Money) (Money, error) {
	if err := m.checkCurrency(other); err != nil {
		return Money{}, err
	}
	amount, ok := subInt64(m.Amount, other.Amount)
	if !ok {
		return Money{}, ErrOverflow
	}
	return Money{Amount: amount, Currency: m.Currency}, nil
}

// Negate returns -m, detecting the int64 minimum-value overflow case.
func (m Money) Negate() (Money, error) {
	if m.Amount == minInt64 {
		return Money{}, ErrOverflow
	}
	return Money{Amount: -m.Amount, Currency: m.Currency}, nil
}

// Cmp compares m and other by amount after enforcing matching currencies.
func (m Money) Cmp(other Money) (int, error) {
	if err := m.checkCurrency(other); err != nil {
		return 0, err
	}
	if m.Amount < other.Amount {
		return -1, nil
	}
	if m.Amount > other.Amount {
		return 1, nil
	}
	return 0, nil
}

// MulRat multiplies m by r using exact rational arithmetic and rounds the
// result to minor units with round-half-even, also known as banker's rounding.
//
// Worked examples:
//   - GBP 1.00 multiplied by 1/2 is GBP 0.50.
//   - GBP 0.05 multiplied by 1/2 is GBP 0.02, because 2.5 pence ties to the
//     even minor unit 2.
//   - GBP 0.07 multiplied by 1/2 is GBP 0.04, because 3.5 pence ties to the
//     even minor unit 4.
//
// MulRat panics with ErrOverflow if the rounded result cannot fit in int64.
func (m Money) MulRat(r *big.Rat) Money {
	product := new(big.Rat).Mul(big.NewRat(m.Amount, 1), r)
	amount := roundHalfEvenInt64(product)
	return Money{Amount: amount, Currency: m.Currency}
}

// Allocate splits m according to ratios with the largest-remainder method.
//
// Positive ratios receive proportional floor allocations first. Remaining
// minor units are assigned to the largest fractional remainders, with ties
// resolved by input order. Non-positive ratios receive zero unless all ratios
// are non-positive, in which case the full amount is assigned to the first
// part so the returned parts still sum exactly to m. An empty ratio list
// returns no parts for a zero amount and a single original-amount part for a
// non-zero amount.
func (m Money) Allocate(ratios []int) []Money {
	if len(ratios) == 0 && m.Amount != 0 {
		return []Money{m}
	}
	parts := make([]Money, len(ratios))
	for i := range parts {
		parts[i].Currency = m.Currency
	}
	if len(ratios) == 0 || m.Amount == 0 {
		return parts
	}

	totalWeight := big.NewInt(0)
	positiveRatios := 0
	for _, ratio := range ratios {
		if ratio > 0 {
			totalWeight.Add(totalWeight, big.NewInt(int64(ratio)))
			positiveRatios++
		}
	}
	if positiveRatios == 0 {
		parts[0].Amount = m.Amount
		return parts
	}

	sign := 1
	if m.Amount < 0 {
		sign = -1
	}
	totalAmount := absInt64(m.Amount)
	allocated := big.NewInt(0)
	remainders := make([]allocationRemainder, 0, positiveRatios)
	for i, ratio := range ratios {
		if ratio <= 0 {
			continue
		}
		product := new(big.Int).Mul(totalAmount, big.NewInt(int64(ratio)))
		quotient, remainder := new(big.Int), new(big.Int)
		quotient.QuoRem(product, totalWeight, remainder)
		parts[i].Amount = signedAbsInt64(quotient, sign)
		allocated.Add(allocated, quotient)
		remainders = append(remainders, allocationRemainder{
			index:     i,
			remainder: new(big.Int).Set(remainder),
		})
	}

	leftover := new(big.Int).Sub(totalAmount, allocated)
	sort.SliceStable(remainders, func(i, j int) bool {
		cmp := remainders[i].remainder.Cmp(remainders[j].remainder)
		if cmp == 0 {
			return remainders[i].index < remainders[j].index
		}
		return cmp > 0
	})
	for i := 0; i < int(leftover.Int64()); i++ {
		if sign > 0 {
			parts[remainders[i].index].Amount++
		} else {
			parts[remainders[i].index].Amount--
		}
	}

	return parts
}

// Format returns a locale-lite currency string such as £1,234.56 or €1,234.56.
func (m Money) Format() string {
	info := currencyInfoFor(m.Currency)
	sign := ""
	if m.Amount < 0 {
		sign = "-"
	}

	scale := decimalScale(info.decimals)
	absAmount := absInt64(m.Amount)
	major, minor := new(big.Int), new(big.Int)
	major.QuoRem(absAmount, scale, minor)

	return sign + info.symbol + groupDigits(major.String()) + "." + padLeft(minor.String(), info.decimals)
}

// ParseAmount parses a locale-lite amount string into minor units for currency.
func ParseAmount(input, currency string) (Money, error) {
	currency = normalizeCurrency(currency)
	info := currencyInfoFor(currency)
	s := strings.TrimSpace(input)
	if s == "" {
		return Money{}, fmt.Errorf("money: parse amount %q: empty", input)
	}

	sign := int64(1)
	var ok bool
	s, sign, _ = consumeSign(s, sign)
	s = stripCurrencyPrefix(strings.TrimSpace(s), currency, info)
	s, sign, ok = consumeSign(strings.TrimSpace(s), sign)
	if !ok {
		return Money{}, fmt.Errorf("money: parse amount %q: repeated sign", input)
	}

	whole, fraction, hasFraction := strings.Cut(s, ".")
	if whole == "" {
		return Money{}, fmt.Errorf("money: parse amount %q: missing whole units", input)
	}
	if !validWholeDigits(whole) {
		return Money{}, fmt.Errorf("money: parse amount %q: invalid whole units", input)
	}
	if !hasFraction {
		fraction = ""
	}
	if hasFraction && fraction == "" {
		return Money{}, fmt.Errorf("money: parse amount %q: missing minor units", input)
	}
	if len(fraction) > info.decimals || !allDigits(fraction) {
		return Money{}, fmt.Errorf("money: parse amount %q: invalid minor units", input)
	}

	digits := strings.ReplaceAll(whole, ",", "") + padRight(fraction, info.decimals)
	amount, _ := new(big.Int).SetString(digits, 10)
	if sign < 0 {
		amount.Neg(amount)
	}
	if !amount.IsInt64() {
		return Money{}, ErrOverflow
	}
	return Money{Amount: amount.Int64(), Currency: currency}, nil
}

func (m Money) checkCurrency(other Money) error {
	if m.Currency != other.Currency {
		return ErrCurrencyMismatch
	}
	return nil
}

func addInt64(a, b int64) (int64, bool) {
	if b > 0 && a > maxInt64-b {
		return 0, false
	}
	if b < 0 && a < minInt64-b {
		return 0, false
	}
	return a + b, true
}

func subInt64(a, b int64) (int64, bool) {
	if b > 0 && a < minInt64+b {
		return 0, false
	}
	if b < 0 && a > maxInt64+b {
		return 0, false
	}
	return a - b, true
}

func roundHalfEvenInt64(r *big.Rat) int64 {
	sign := r.Sign()
	if sign == 0 {
		return 0
	}
	absRat := new(big.Rat).Set(r)
	if sign < 0 {
		absRat.Neg(absRat)
	}

	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(absRat.Num(), absRat.Denom(), remainder)
	twiceRemainder := new(big.Int).Lsh(remainder, 1)
	cmp := twiceRemainder.Cmp(absRat.Denom())
	if cmp > 0 || cmp == 0 && quotient.Bit(0) == 1 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return signedAbsInt64(quotient, sign)
}

func absInt64(n int64) *big.Int {
	result := big.NewInt(n)
	if result.Sign() < 0 {
		result.Neg(result)
	}
	return result
}

func signedAbsInt64(abs *big.Int, sign int) int64 {
	result := new(big.Int).Set(abs)
	if sign < 0 {
		result.Neg(result)
	}
	if !result.IsInt64() {
		panic(ErrOverflow)
	}
	return result.Int64()
}

func currencyInfoFor(currency string) currencyInfo {
	switch normalizeCurrency(currency) {
	case "GBP":
		return currencyInfo{symbol: "£", decimals: defaultDecimals}
	case "EUR":
		return currencyInfo{symbol: "€", decimals: defaultDecimals}
	default:
		normalized := normalizeCurrency(currency)
		if normalized == "" {
			return currencyInfo{decimals: defaultDecimals}
		}
		return currencyInfo{symbol: normalized + " ", decimals: defaultDecimals}
	}
}

func normalizeCurrency(currency string) string {
	return strings.ToUpper(strings.TrimSpace(currency))
}

func decimalScale(decimals int) *big.Int {
	scale := big.NewInt(1)
	for range decimals {
		scale.Mul(scale, big.NewInt(10))
	}
	return scale
}

func groupDigits(digits string) string {
	if len(digits) <= 3 {
		return digits
	}
	var grouped strings.Builder
	prefix := len(digits) % 3
	if prefix == 0 {
		prefix = 3
	}
	grouped.WriteString(digits[:prefix])
	for i := prefix; i < len(digits); i += 3 {
		grouped.WriteByte(',')
		grouped.WriteString(digits[i : i+3])
	}
	return grouped.String()
}

func padLeft(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat("0", width-len(s)) + s
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat("0", width-len(s))
}

func consumeSign(s string, current int64) (string, int64, bool) {
	switch {
	case strings.HasPrefix(s, "+"):
		if current != 1 {
			return s, current, false
		}
		return strings.TrimSpace(strings.TrimPrefix(s, "+")), 1, true
	case strings.HasPrefix(s, "-"):
		if current != 1 {
			return s, current, false
		}
		return strings.TrimSpace(strings.TrimPrefix(s, "-")), -1, true
	default:
		return s, current, true
	}
}

func stripCurrencyPrefix(s, currency string, info currencyInfo) string {
	if info.symbol != "" && strings.HasPrefix(s, info.symbol) {
		return strings.TrimSpace(strings.TrimPrefix(s, info.symbol))
	}
	if currency == "" {
		return s
	}
	prefix := currency + " "
	if strings.HasPrefix(strings.ToUpper(s), prefix) {
		return strings.TrimSpace(s[len(prefix):])
	}
	return s
}

func validWholeDigits(s string) bool {
	if strings.Contains(s, ",") {
		return validGroupedDigits(s)
	}
	return allDigits(s)
}

func validGroupedDigits(s string) bool {
	groups := strings.Split(s, ",")
	if len(groups[0]) == 0 || len(groups[0]) > 3 || !allDigits(groups[0]) {
		return false
	}
	for _, group := range groups[1:] {
		if len(group) != 3 || !allDigits(group) {
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
