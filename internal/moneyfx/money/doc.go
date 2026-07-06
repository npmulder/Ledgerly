// Package money provides the shared Money value type for exact minor-unit
// arithmetic.
//
// Amounts are stored as integer minor units and currencies as ISO-style string
// codes. The package deliberately avoids floating point arithmetic; percentage
// and FX-rate style multiplication uses math/big rational values and rounds to
// the nearest minor unit with round-half-even semantics.
package money
