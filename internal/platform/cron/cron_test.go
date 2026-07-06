package cron

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/platform/clock"
)

func TestRegisterAndRunNowExecutesNamedJob(t *testing.T) {
	var logs bytes.Buffer
	fakeClock := clock.NewFake(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
	runner := New(Config{
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		Clock:  fakeClock,
	})

	var ran int
	if err := runner.Register("probe", "0 2 * * *", func(context.Context) error {
		ran++
		fakeClock.Advance(1500 * time.Millisecond)
		return nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if !runner.HasJob("probe") {
		t.Fatal("HasJob(probe) = false, want true")
	}
	if got := runner.Jobs(); len(got) != 1 || got[0] != "probe" {
		t.Fatalf("Jobs() = %v, want [probe]", got)
	}

	if err := runner.RunNow(context.Background(), "probe"); err != nil {
		t.Fatalf("RunNow() error = %v", err)
	}
	if ran != 1 {
		t.Fatalf("job ran %d time(s), want 1", ran)
	}

	logText := logs.String()
	for _, want := range []string{"name=probe", "duration=1.5s", `error=""`} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log %q missing %q", logText, want)
		}
	}
}

func TestRegisterFailsFastOnBadSchedule(t *testing.T) {
	runner := New(Config{})

	err := runner.Register("bad", "not-a-schedule", func(context.Context) error { return nil })
	if err == nil {
		t.Fatal("Register() error = nil, want schedule parse error")
	}
	if !strings.Contains(err.Error(), "register bad schedule") {
		t.Fatalf("Register() error = %q, want bad schedule context", err)
	}
	if runner.HasJob("bad") {
		t.Fatal("bad job was registered after schedule parse failure")
	}
}

func TestRunNowReturnsJobErrorAndLogsIt(t *testing.T) {
	var logs bytes.Buffer
	runner := New(Config{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	jobErr := errors.New("boom")
	if err := runner.Register("fails", "0 2 * * *", func(context.Context) error { return jobErr }); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	err := runner.RunNow(context.Background(), "fails")
	if !errors.Is(err, jobErr) {
		t.Fatalf("RunNow() error = %v, want %v", err, jobErr)
	}
	if logText := logs.String(); !strings.Contains(logText, "error=boom") {
		t.Fatalf("log %q missing error=boom", logText)
	}
}

func TestRunNowRejectsUnknownJob(t *testing.T) {
	runner := New(Config{})

	err := runner.RunNow(context.Background(), "missing")
	if err == nil {
		t.Fatal("RunNow() error = nil, want unknown job error")
	}
	if !strings.Contains(err.Error(), `unknown job "missing"`) {
		t.Fatalf("RunNow() error = %q, want unknown job context", err)
	}
}
