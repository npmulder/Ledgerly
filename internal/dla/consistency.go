package dla

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	ledgerapi "github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

// ConsistencyCheckJobName is the canonical platform cron job name.
const ConsistencyCheckJobName = "dla.consistency-check"

// ConsistencyReport compares DLA's derived presentation balance with the
// authoritative ledger DLA account balance.
type ConsistencyReport struct {
	AsOf                  time.Time                   `json:"as_of"`
	Consistent            bool                        `json:"consistent"`
	DerivedBalance        money.Money                 `json:"derived_balance"`
	LedgerBalance         money.Money                 `json:"ledger_balance"`
	ExpectedLedgerBalance money.Money                 `json:"expected_ledger_balance"`
	Directors             []DirectorConsistencyReport `json:"directors"`
}

// DirectorConsistencyReport compares one director's DLA entries with the
// matching director-specific ledger account.
type DirectorConsistencyReport struct {
	DirectorID            DirectorID  `json:"director_id"`
	DirectorName          string      `json:"director_name"`
	AccountCode           string      `json:"account_code"`
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

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return ConsistencyReport{}, fmt.Errorf("dla: begin consistency check transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	report := ConsistencyReport{
		AsOf:                  date,
		Consistent:            true,
		DerivedBalance:        money.Money{Currency: "GBP"},
		LedgerBalance:         money.Money{Currency: "GBP"},
		ExpectedLedgerBalance: money.Money{Currency: "GBP"},
	}
	directors, err := s.consistencyDirectors(ctx, tx)
	if err != nil {
		return ConsistencyReport{}, err
	}
	for _, director := range directors {
		directorReport, err := s.directorConsistency(ctx, tx, director, date)
		if err != nil {
			return ConsistencyReport{}, err
		}
		report.Directors = append(report.Directors, directorReport)
		report.Consistent = report.Consistent && directorReport.Consistent
		report.DerivedBalance, err = report.DerivedBalance.Add(directorReport.DerivedBalance)
		if err != nil {
			return ConsistencyReport{}, fmt.Errorf("dla: aggregate derived balance: %w", err)
		}
		report.LedgerBalance, err = report.LedgerBalance.Add(directorReport.LedgerBalance)
		if err != nil {
			return ConsistencyReport{}, fmt.Errorf("dla: aggregate ledger balance: %w", err)
		}
		report.ExpectedLedgerBalance, err = report.ExpectedLedgerBalance.Add(directorReport.ExpectedLedgerBalance)
		if err != nil {
			return ConsistencyReport{}, fmt.Errorf("dla: aggregate expected ledger balance: %w", err)
		}
	}
	if !report.Consistent {
		return report, &ConsistencyViolationError{Report: report}
	}
	if err := tx.Commit(ctx); err != nil {
		return ConsistencyReport{}, fmt.Errorf("dla: commit consistency check transaction: %w", err)
	}
	return report, nil
}

func (s *Service) consistencyDirectors(ctx context.Context, tx db.Tx) ([]Director, error) {
	current, err := s.directors(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[DirectorID]Director, len(current))
	directors := make([]Director, 0, len(current))
	for _, director := range current {
		byID[director.ID] = director
		directors = append(directors, director)
	}
	withEntries, err := s.store.DirectorsWithEntries(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, directorID := range withEntries {
		if _, ok := byID[directorID]; ok {
			continue
		}
		director := Director{ID: directorID, Name: string(directorID)}
		byID[directorID] = director
		directors = append(directors, director)
	}
	return directors, nil
}

func (s *Service) directorConsistency(ctx context.Context, tx db.Tx, director Director, asOf time.Time) (DirectorConsistencyReport, error) {
	derivedBalance, err := s.store.CurrentBalanceAsOf(ctx, tx, director.ID, asOf)
	if err != nil {
		return DirectorConsistencyReport{}, err
	}
	accountCode, err := AccountCodeForDirector(director.ID)
	if err != nil {
		return DirectorConsistencyReport{}, err
	}
	ledgerBalance, err := s.ledger.AccountBalanceInTx(ctx, tx, accountCode, asOf)
	if err != nil {
		if errors.Is(err, ledgerapi.ErrAccountNotFound) && derivedBalance.Amount == 0 {
			ledgerBalance.AmountGBP = money.Money{Currency: "GBP"}
		} else {
			return DirectorConsistencyReport{}, err
		}
	}
	expectedLedgerBalance, err := derivedBalance.Negate()
	if err != nil {
		return DirectorConsistencyReport{}, fmt.Errorf("dla: expected ledger balance: %w", err)
	}
	return DirectorConsistencyReport{
		DirectorID:            director.ID,
		DirectorName:          director.Name,
		AccountCode:           string(accountCode),
		Consistent:            ledgerBalance.AmountGBP == expectedLedgerBalance,
		DerivedBalance:        derivedBalance,
		LedgerBalance:         ledgerBalance.AmountGBP,
		ExpectedLedgerBalance: expectedLedgerBalance,
	}, nil
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
	for _, director := range r.Directors {
		if director.Consistent {
			continue
		}
		name := director.DirectorName
		if name == "" {
			name = string(director.DirectorID)
		}
		return fmt.Sprintf(
			"DLA balance mismatch for %s as of %s; derived=%v ledger=%v expected_ledger=%v account=%s",
			name,
			r.AsOf.Format(time.DateOnly),
			director.DerivedBalance,
			director.LedgerBalance,
			director.ExpectedLedgerBalance,
			director.AccountCode,
		)
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
