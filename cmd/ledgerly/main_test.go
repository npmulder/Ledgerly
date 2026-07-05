package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunPrintsVersionWithFlag(t *testing.T) {
	restore := setVersionForTest("test-sha")
	defer restore()

	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &stdout); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	if got, want := stdout.String(), "ledgerly test-sha\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunPrintsVersionSubcommand(t *testing.T) {
	restore := setVersionForTest("test-sha")
	defer restore()

	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"version"}, &stdout); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	if got, want := stdout.String(), "ledgerly test-sha\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunRejectsUnexpectedArguments(t *testing.T) {
	var stdout bytes.Buffer
	err := run(context.Background(), []string{"migrate", "extra"}, &stdout)
	if err == nil {
		t.Fatal("run() error = nil, want migrate argument error")
	}
	if !strings.Contains(err.Error(), "migrate accepts no arguments") {
		t.Fatalf("run() error = %q, want migrate argument error", err)
	}
}

func setVersionForTest(value string) func() {
	original := version
	version = value
	return func() {
		version = original
	}
}
