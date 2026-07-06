// Package cron wraps robfig/cron with Ledgerly's named-job conventions.
package cron

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	robfigcron "github.com/robfig/cron/v3"

	"github.com/npmulder/ledgerly/internal/platform/clock"
)

// Job is a background task that can also be driven directly by tests and CLI
// entry points.
type Job func(context.Context) error

// Config controls runner construction.
type Config struct {
	Logger   *slog.Logger
	Clock    clock.Clock
	Location *time.Location
}

// Runner owns named cron registrations and deterministic RunNow execution.
type Runner struct {
	mu        sync.RWMutex
	logger    *slog.Logger
	clock     clock.Clock
	scheduler *robfigcron.Cron
	jobs      map[string]Job
}

// New creates an empty runner.
func New(cfg Config) *Runner {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.New()
	}
	location := cfg.Location
	if location == nil {
		location = time.Local
	}

	return &Runner{
		logger: logger,
		clock:  clk,
		scheduler: robfigcron.New(
			robfigcron.WithLocation(location),
			robfigcron.WithLogger(robfigcron.DiscardLogger),
		),
		jobs: make(map[string]Job),
	}
}

// Register installs a named job on schedule. Schedule parse failures are
// returned immediately so application wiring fails fast at boot.
func (r *Runner) Register(name, schedule string, job Job) error {
	if r == nil {
		return fmt.Errorf("cron: nil runner")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("cron: job name is required")
	}
	if strings.TrimSpace(schedule) == "" {
		return fmt.Errorf("cron: schedule is required for %s", name)
	}
	if job == nil {
		return fmt.Errorf("cron: job %s is nil", name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.jobs[name]; exists {
		return fmt.Errorf("cron: job %s is already registered", name)
	}
	if _, err := r.scheduler.AddFunc(schedule, func() {
		_ = r.run(context.Background(), name, job)
	}); err != nil {
		return fmt.Errorf("cron: register %s schedule %q: %w", name, schedule, err)
	}
	r.jobs[name] = job
	return nil
}

// Start begins scheduled execution.
func (r *Runner) Start() {
	if r == nil {
		return
	}
	r.scheduler.Start()
}

// Stop stops scheduled execution and returns a context that is closed once
// active jobs have exited.
func (r *Runner) Stop() context.Context {
	if r == nil {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}
	return r.scheduler.Stop()
}

// RunNow executes a named job immediately.
func (r *Runner) RunNow(ctx context.Context, name string) error {
	if r == nil {
		return fmt.Errorf("cron: nil runner")
	}
	name = strings.TrimSpace(name)

	r.mu.RLock()
	job := r.jobs[name]
	r.mu.RUnlock()
	if job == nil {
		return fmt.Errorf("cron: unknown job %q", name)
	}
	return r.run(ctx, name, job)
}

// HasJob reports whether name is registered.
func (r *Runner) HasJob(name string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.jobs[strings.TrimSpace(name)]
	return ok
}

// Jobs returns registered job names in deterministic order.
func (r *Runner) Jobs() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.jobs))
	for name := range r.jobs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Runner) run(ctx context.Context, name string, job Job) error {
	start := r.clock.Now()
	err := job(ctx)
	duration := r.clock.Now().Sub(start)

	errText := ""
	if err != nil {
		errText = err.Error()
		r.logger.ErrorContext(ctx, "cron job run", "name", name, "duration", duration, "error", errText)
		return err
	}

	r.logger.InfoContext(ctx, "cron job run", "name", name, "duration", duration, "error", errText)
	return nil
}
