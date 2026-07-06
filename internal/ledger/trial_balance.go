package ledger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// TrialBalanceJobName is the canonical platform cron job name.
const TrialBalanceJobName = "ledger.trial-balance"

const trialBalanceOffendingLimit = 5

// Report summarizes the ledger trial balance at an as-of date.
type Report struct {
	AsOf             time.Time        `json:"as_of"`
	Balanced         bool             `json:"balanced"`
	GBPTotal         int64            `json:"gbp_total"`
	CurrencySums     []CurrencySum    `json:"currency_sums"`
	OffendingEntries []OffendingEntry `json:"offending_entries,omitempty"`
}

// CurrencySum is a native-currency total in minor units.
type CurrencySum struct {
	Currency string `json:"currency"`
	Amount   int64  `json:"amount"`
}

// OffendingEntry identifies a stored journal entry whose postings do not
// satisfy per-entry invariants during a full-table sweep.
type OffendingEntry struct {
	ID           EntryID       `json:"id"`
	Date         time.Time     `json:"date"`
	Description  string        `json:"description"`
	SourceModule string        `json:"source_module"`
	SourceRef    string        `json:"source_ref"`
	PostingCount int           `json:"posting_count"`
	GBPTotal     int64         `json:"gbp_total"`
	NativeSums   []CurrencySum `json:"native_sums,omitempty"`
}

// TrialBalanceViolationError carries the unbalanced report while unwrapping to
// ErrTrialBalanceViolation.
type TrialBalanceViolationError struct {
	Report Report
}

func (e *TrialBalanceViolationError) Error() string {
	return e.Report.ViolationReason()
}

func (e *TrialBalanceViolationError) Unwrap() error {
	return ErrTrialBalanceViolation
}

// TrialBalance checks all postings up to asOf.
func (s *Service) TrialBalance(ctx context.Context, asOf time.Time) (Report, error) {
	if s.pool == nil {
		return Report{}, fmt.Errorf("ledger: trial balance requires pool")
	}
	date, err := normalizeEntryDate(asOf)
	if err != nil {
		return Report{}, err
	}

	report, err := s.store.TrialBalance(ctx, s.pool, date, trialBalanceOffendingLimit)
	if err != nil {
		return Report{}, err
	}
	if !report.Balanced {
		return report, &TrialBalanceViolationError{Report: report}
	}
	return report, nil
}

// RunTrialBalanceInvariant executes TrialBalance, logs page-worthy violations,
// and updates health status when a clean run passes.
func (s *Service) RunTrialBalanceInvariant(
	ctx context.Context,
	asOf time.Time,
	logger *slog.Logger,
	status *TrialBalanceStatus,
) (Report, error) {
	report, err := s.TrialBalance(ctx, asOf)
	if err == nil {
		if status != nil {
			status.MarkClean()
		}
		return report, nil
	}

	var violation *TrialBalanceViolationError
	if errors.As(err, &violation) {
		if status != nil {
			status.MarkViolation(report)
		}
		if logger != nil {
			logger.ErrorContext(
				ctx,
				"ledger trial balance invariant violated",
				"invariant", "violated",
				"job", TrialBalanceJobName,
				"as_of", report.AsOf.Format("2006-01-02"),
				"gbp_total", report.GBPTotal,
				"currency_sums", report.CurrencySums,
				"offending_entries", report.OffendingEntries,
			)
		}
	}
	return report, err
}

// ViolationReason returns a compact reason suitable for healthz.
func (r Report) ViolationReason() string {
	if r.Balanced {
		return "ledger trial balance is balanced"
	}

	parts := []string{fmt.Sprintf("ledger trial balance unbalanced as of %s", r.AsOf.Format("2006-01-02"))}
	if r.GBPTotal != 0 {
		parts = append(parts, fmt.Sprintf("GBP total=%d", r.GBPTotal))
	}
	for _, sum := range r.CurrencySums {
		if sum.Amount != 0 {
			parts = append(parts, fmt.Sprintf("%s native total=%d", sum.Currency, sum.Amount))
		}
	}
	if len(r.OffendingEntries) > 0 {
		entry := r.OffendingEntries[0]
		parts = append(parts, fmt.Sprintf(
			"first offending entry id=%d posting_count=%d GBP total=%d",
			entry.ID,
			entry.PostingCount,
			entry.GBPTotal,
		))
	}
	return strings.Join(parts, "; ")
}

// TrialBalanceStatus tracks the last known invariant violation for healthz.
type TrialBalanceStatus struct {
	mu     sync.RWMutex
	reason string
}

// NewTrialBalanceStatus creates an initially healthy trial-balance status.
func NewTrialBalanceStatus() *TrialBalanceStatus {
	return &TrialBalanceStatus{}
}

// MarkViolation degrades health until a later MarkClean call.
func (s *TrialBalanceStatus) MarkViolation(report Report) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.reason = report.ViolationReason()
	s.mu.Unlock()
}

// MarkClean clears any previous violation.
func (s *TrialBalanceStatus) MarkClean() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.reason = ""
	s.mu.Unlock()
}

// Check implements platform HTTP health checks.
func (s *TrialBalanceStatus) Check(context.Context) error {
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
