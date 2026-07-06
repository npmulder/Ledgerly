package dla

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

// ConsistencyCheckJobName is the canonical platform cron job name.
const ConsistencyCheckJobName = "dla.consistency-check"

// ConsistencyReport compares DLA's derived presentation balance with the
// authoritative ledger DLA account balance.
type ConsistencyReport struct {
	AsOf                  time.Time   `json:"as_of"`
	Consistent            bool        `json:"consistent"`
	DerivedBalance        money.Money `json:"derived_balance"`
	LedgerBalance         money.Money `json:"ledger_balance"`
	ExpectedLedgerBalance money.Money `json:"expected_ledger_balance"`
}

// ConsistencyViolationError carries the mismatched report while unwrapping to
// ErrConsistencyViolation.
type ConsistencyViolationError struct {
	Report ConsistencyReport
}

func (e *ConsistencyViolationError) Error() string {
	return e.Report.ViolationReason()
}

func (e *ConsistencyViolationError) Unwrap() error {
	return ErrConsistencyViolation
}

// CheckConsistency compares the DLA running balance to ledger AccountBalance.
func (s *Service) CheckConsistency(ctx context.Context, asOf time.Time) (ConsistencyReport, error) {
	if s.pool == nil {
		return ConsistencyReport{}, fmt.Errorf("dla: consistency check requires pool")
	}
	if s.ledger == nil {
		return ConsistencyReport{}, fmt.Errorf("dla: consistency check requires ledger")
	}
	date, err := normalizeDate(asOf)
	if err != nil {
		return ConsistencyReport{}, err
	}

	derivedBalance, err := s.store.CurrentBalanceAsOf(ctx, s.pool, date)
	if err != nil {
		return ConsistencyReport{}, err
	}
	ledgerBalance, err := s.ledger.AccountBalance(ctx, DLAAccountCode, date)
	if err != nil {
		return ConsistencyReport{}, err
	}
	expectedLedgerBalance, err := derivedBalance.Negate()
	if err != nil {
		return ConsistencyReport{}, fmt.Errorf("dla: expected ledger balance: %w", err)
	}

	report := ConsistencyReport{
		AsOf:                  date,
		Consistent:            ledgerBalance.AmountGBP == expectedLedgerBalance,
		DerivedBalance:        derivedBalance,
		LedgerBalance:         ledgerBalance.AmountGBP,
		ExpectedLedgerBalance: expectedLedgerBalance,
	}
	if !report.Consistent {
		return report, &ConsistencyViolationError{Report: report}
	}
	return report, nil
}

// RunConsistencyCheck executes CheckConsistency, logs invariant violations, and
// updates health status when a clean run passes.
func (s *Service) RunConsistencyCheck(
	ctx context.Context,
	asOf time.Time,
	logger *slog.Logger,
	status *ConsistencyStatus,
) (ConsistencyReport, error) {
	report, err := s.CheckConsistency(ctx, asOf)
	if err == nil {
		if status != nil {
			status.MarkClean()
		}
		return report, nil
	}

	var violation *ConsistencyViolationError
	if errors.As(err, &violation) {
		if status != nil {
			status.MarkViolation(report)
		}
		logInvariantViolation(ctx, logger, report)
	}
	return report, err
}

// ViolationReason returns a compact reason suitable for healthz.
func (r ConsistencyReport) ViolationReason() string {
	if r.Consistent {
		return "DLA balance matches ledger account balance"
	}
	return fmt.Sprintf(
		"DLA balance mismatch as of %s; derived=%v ledger=%v expected_ledger=%v",
		r.AsOf.Format(time.DateOnly),
		r.DerivedBalance,
		r.LedgerBalance,
		r.ExpectedLedgerBalance,
	)
}

func logInvariantViolation(ctx context.Context, logger *slog.Logger, report ConsistencyReport) {
	if logger == nil {
		return
	}
	logger.ErrorContext(
		ctx,
		"dla consistency invariant violated",
		"invariant", "violated",
		"job", ConsistencyCheckJobName,
		"as_of", report.AsOf.Format(time.DateOnly),
		"derived_balance", report.DerivedBalance,
		"ledger_balance", report.LedgerBalance,
		"expected_ledger_balance", report.ExpectedLedgerBalance,
	)
}

// ConsistencyStatus tracks the last known DLA consistency violation for healthz.
type ConsistencyStatus struct {
	mu     sync.RWMutex
	reason string
}

// NewConsistencyStatus creates an initially healthy DLA consistency status.
func NewConsistencyStatus() *ConsistencyStatus {
	return &ConsistencyStatus{}
}

// MarkViolation degrades health until a later MarkClean call.
func (s *ConsistencyStatus) MarkViolation(report ConsistencyReport) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.reason = report.ViolationReason()
	s.mu.Unlock()
}

// MarkClean clears any previous violation.
func (s *ConsistencyStatus) MarkClean() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.reason = ""
	s.mu.Unlock()
}

// Check implements platform HTTP health checks.
func (s *ConsistencyStatus) Check(context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.reason == "" {
		return nil
	}
	return errors.New(s.reason)
}
