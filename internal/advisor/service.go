package advisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

const (
	// EvaluateJobName is the platform cron job name for daily whole-set advisor
	// evaluation.
	EvaluateJobName = "advisor.evaluate"

	// EvaluateSchedule runs after overnight consistency and overdue jobs.
	EvaluateSchedule = "30 2 * * *"

	// ManualRefreshTrigger records manual RefreshNow runs.
	ManualRefreshTrigger = "manual.RefreshNow"

	postCommitNotifyChannel = "advisor_evaluate"
	defaultDebounce         = 25 * time.Millisecond
	listenerReconnectDelay  = time.Second
)

// ServiceConfig supplies the dependencies needed for whole-set evaluations.
type ServiceConfig struct {
	Pool  *pgxpool.Pool
	Facts FactRegistry
	Rules []jurisdiction.AdvisorRule
}

// Service owns advisor evaluation orchestration and read/write APIs.
type Service struct {
	pool   *pgxpool.Pool
	store  Store
	facts  FactRegistry
	rules  []RuleDef
	clock  clock.Clock
	logger *slog.Logger

	runMu sync.Mutex

	stateMu        sync.Mutex
	pendingTrigger string
	timer          *time.Timer
	queuedRuns     int
	running        bool
	debounce       time.Duration

	beforeEvaluate func(string) error
}

// ServiceOption customizes advisor orchestration.
type ServiceOption func(*Service)

// WithClock injects the evaluation clock.
func WithClock(clk clock.Clock) ServiceOption {
	return func(s *Service) {
		s.clock = clk
	}
}

// WithLogger injects evaluation and post-commit telemetry.
func WithLogger(logger *slog.Logger) ServiceOption {
	return func(s *Service) {
		s.logger = logger
	}
}

// WithDebounce customizes event-trigger burst coalescing.
func WithDebounce(duration time.Duration) ServiceOption {
	return func(s *Service) {
		if duration >= 0 {
			s.debounce = duration
		}
	}
}

// WithBeforeEvaluate installs a test hook that can fail or delay a run before
// facts are gathered. Production callers should not set it.
func WithBeforeEvaluate(hook func(string) error) ServiceOption {
	return func(s *Service) {
		s.beforeEvaluate = hook
	}
}

// NewService compiles the active pack rules and returns an evaluation service.
func NewService(cfg ServiceConfig, opts ...ServiceOption) (*Service, error) {
	if cfg.Pool == nil {
		return nil, fmt.Errorf("advisor: service requires pool")
	}
	rules := cfg.Rules
	if rules == nil {
		rules = jurisdiction.AdvisorRules()
	}
	compiled, err := CompileJurisdictionRules(rules)
	if err != nil {
		return nil, err
	}
	service := &Service{
		pool:     cfg.Pool,
		facts:    cfg.Facts,
		rules:    compiled,
		clock:    clock.New(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		debounce: defaultDebounce,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(service)
		}
	}
	if service.clock == nil {
		service.clock = clock.New()
	}
	if service.logger == nil {
		service.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return service, nil
}

// RunEvaluation gathers all facts, evaluates the whole active rule set, applies
// the durable insight delta, and records the run. Calls are serialized.
func (s *Service) RunEvaluation(ctx context.Context, trigger string) (EvaluationRun, error) {
	if s == nil {
		return EvaluationRun{}, fmt.Errorf("advisor: nil service")
	}
	s.runMu.Lock()
	s.setRunning(true)
	defer func() {
		s.setRunning(false)
		s.runMu.Unlock()
	}()

	return s.runEvaluationLocked(ctx, trigger)
}

// RefreshNow is the manual entry point that ADV-4 can expose through HTTP.
func (s *Service) RefreshNow(ctx context.Context) (EvaluationRun, error) {
	return s.RunEvaluation(ctx, ManualRefreshTrigger)
}

// RequestEvaluation debounces event-triggered evaluation requests. Bursts are
// coalesced into one later RunEvaluation call.
func (s *Service) RequestEvaluation(trigger string) {
	if s == nil {
		return
	}
	trigger = normalizeTrigger(trigger)

	s.stateMu.Lock()
	s.pendingTrigger = coalesceTrigger(s.pendingTrigger, trigger)
	if s.timer == nil {
		s.timer = time.AfterFunc(s.debounce, s.firePendingEvaluation)
	} else {
		s.timer.Reset(s.debounce)
	}
	s.stateMu.Unlock()
}

// TriggerAfterCommit records an event-triggered evaluation request that is only
// delivered after the source transaction commits. It deliberately never returns
// an error to the source event bus: advisor evaluation is derived and must not
// roll back financial source transactions.
func (s *Service) TriggerAfterCommit(ctx context.Context, tx db.Tx, trigger string) {
	if s == nil {
		return
	}
	trigger = normalizeTrigger(trigger)
	if tx == nil {
		s.RequestEvaluation(trigger)
		return
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, postCommitNotifyChannel, trigger); err != nil {
		s.logger.ErrorContext(ctx, "advisor post-commit trigger registration failed",
			"trigger", trigger,
			"error", err,
		)
	}
}

// StartPostCommitListener starts the in-process listener that converts
// committed NOTIFY messages into debounced evaluation requests.
func (s *Service) StartPostCommitListener(ctx context.Context) (func() error, error) {
	if s == nil {
		return nil, fmt.Errorf("advisor: nil service")
	}
	conn, err := s.openPostCommitListener(ctx)
	if err != nil {
		return nil, err
	}

	listenCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			err := s.consumePostCommitNotifications(listenCtx, conn)
			conn.Release()
			if errors.Is(err, context.Canceled) || listenCtx.Err() != nil {
				return
			}
			s.logger.ErrorContext(listenCtx, "advisor post-commit listener stopped", "error", err)
			for {
				if err := sleepContext(listenCtx, listenerReconnectDelay); err != nil {
					return
				}
				conn, err = s.openPostCommitListener(listenCtx)
				if err == nil {
					break
				}
				if errors.Is(err, context.Canceled) || listenCtx.Err() != nil {
					return
				}
				s.logger.ErrorContext(listenCtx, "advisor post-commit listener reconnect failed", "error", err)
			}
		}
	}()

	return func() error {
		cancel()
		<-done
		return nil
	}, nil
}

func (s *Service) openPostCommitListener(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("advisor: acquire post-commit listener: %w", err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{postCommitNotifyChannel}.Sanitize()); err != nil {
		conn.Release()
		return nil, fmt.Errorf("advisor: listen for post-commit triggers: %w", err)
	}
	return conn, nil
}

func (s *Service) consumePostCommitNotifications(ctx context.Context, conn *pgxpool.Conn) error {
	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		s.RequestEvaluation(notification.Payload)
	}
}

// WaitIdle blocks until no debounced or running evaluation remains. It is a
// deterministic test helper and is also useful for graceful orchestration.
func (s *Service) WaitIdle(ctx context.Context) error {
	if s == nil {
		return nil
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if s.isIdle() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// InsightsFor returns active, undismissed insights for surface.
func (s *Service) InsightsFor(ctx context.Context, surface Surface) ([]Insight, error) {
	if s == nil {
		return nil, fmt.Errorf("advisor: nil service")
	}
	return s.store.ActiveInsights(ctx, s.pool, surface)
}

// Dismiss suppresses one active insight key until its facts change.
func (s *Service) Dismiss(ctx context.Context, key InsightKey) error {
	if s == nil {
		return fmt.Errorf("advisor: nil service")
	}
	return s.store.Dismiss(ctx, s.pool, key, s.clock.Now())
}

func (s *Service) runEvaluationLocked(ctx context.Context, trigger string) (EvaluationRun, error) {
	trigger = normalizeTrigger(trigger)
	startedAt := s.clock.Now().UTC()
	wallStart := time.Now()
	run := EvaluationRun{
		Trigger:   trigger,
		StartedAt: startedAt,
	}

	if s.beforeEvaluate != nil {
		if err := s.beforeEvaluate(trigger); err != nil {
			return s.recordFailedRun(ctx, run, wallStart, err)
		}
	}

	facts, report := s.facts.GatherAll(ctx, s.logger)
	logGatherReport(s.logger, report)

	delta, err := Evaluate(s.rules, facts, s.clock.Now())
	if err != nil {
		return s.recordFailedRun(ctx, run, wallStart, err)
	}
	run.Warnings = delta.Warnings

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EvaluationRun{}, fmt.Errorf("advisor: begin evaluation transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	summary, err := s.store.ApplyWithSummary(ctx, tx, delta)
	if err != nil {
		_ = tx.Rollback(context.Background())
		committed = true
		return s.recordFailedRun(ctx, run, wallStart, err)
	}
	run.InsightsCreated = summary.InsightsCreated
	run.InsightsSuperseded = summary.InsightsSuperseded
	run.InsightsResolved = summary.InsightsResolved
	run = finishRun(run, wallStart)
	run, err = s.store.InsertEvaluationRun(ctx, tx, run)
	if err != nil {
		return EvaluationRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EvaluationRun{}, fmt.Errorf("advisor: commit evaluation transaction: %w", err)
	}
	committed = true
	return run, nil
}

func (s *Service) recordFailedRun(ctx context.Context, run EvaluationRun, wallStart time.Time, cause error) (EvaluationRun, error) {
	run = finishRun(run, wallStart)
	run.Error = cause.Error()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EvaluationRun{}, fmt.Errorf("advisor: begin failed-run log transaction after %w: %w", cause, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	recorded, insertErr := s.store.InsertEvaluationRun(ctx, tx, run)
	if insertErr != nil {
		return EvaluationRun{}, errors.Join(cause, insertErr)
	}
	if err := tx.Commit(ctx); err != nil {
		return EvaluationRun{}, errors.Join(cause, fmt.Errorf("advisor: commit failed-run log transaction: %w", err))
	}
	committed = true
	return recorded, cause
}

func finishRun(run EvaluationRun, wallStart time.Time) EvaluationRun {
	run.Duration = time.Since(wallStart)
	if run.Duration < 0 {
		run.Duration = 0
	}
	run.FinishedAt = run.StartedAt.Add(run.Duration)
	return run
}

func (s *Service) firePendingEvaluation() {
	s.stateMu.Lock()
	trigger := s.pendingTrigger
	s.pendingTrigger = ""
	s.timer = nil
	if strings.TrimSpace(trigger) == "" {
		s.stateMu.Unlock()
		return
	}
	s.queuedRuns++
	s.stateMu.Unlock()

	defer func() {
		s.stateMu.Lock()
		s.queuedRuns--
		s.stateMu.Unlock()
	}()
	if _, err := s.RunEvaluation(context.Background(), trigger); err != nil {
		s.logger.Error("advisor evaluation failed", "trigger", trigger, "error", err)
	}
}

func (s *Service) setRunning(running bool) {
	s.stateMu.Lock()
	s.running = running
	s.stateMu.Unlock()
}

func (s *Service) isIdle() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return !s.running && s.timer == nil && s.pendingTrigger == "" && s.queuedRuns == 0
}

func normalizeTrigger(trigger string) string {
	trigger = strings.TrimSpace(trigger)
	if trigger == "" {
		return "unknown"
	}
	return trigger
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func coalesceTrigger(existing string, next string) string {
	existing = strings.TrimSpace(existing)
	next = normalizeTrigger(next)
	if existing == "" || existing == next {
		return next
	}
	if strings.Contains(existing, next) {
		return existing
	}
	return existing + "," + next
}

func logGatherReport(logger *slog.Logger, report GatherReport) {
	if logger == nil {
		return
	}
	for _, result := range report.Providers {
		if result.Err != nil {
			continue
		}
		logger.Debug("advisor fact provider gathered",
			"provider", result.Name,
			"keys", result.Keys,
			"duration", result.Duration,
		)
	}
}
