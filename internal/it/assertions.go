// Package it exposes cross-suite integration-test assertions.
package it

import (
	"testing"

	"github.com/npmulder/ledgerly/internal/it/harness"
)

// AssertLedgerBalanced fails t when any stored ledger entry leaves the ledger
// out of trial balance. harness.New registers this assertion in cleanup by
// default; call this helper directly only for an early mid-test check.
func AssertLedgerBalanced(t testing.TB, h *harness.Harness) {
	t.Helper()
	harness.AssertLedgerBalanced(t, h)
}
