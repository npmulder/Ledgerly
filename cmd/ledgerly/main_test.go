package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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
		t.Fatal("run() error = nil, want migrate usage error")
	}
	if !strings.Contains(err.Error(), "usage: ledgerly migrate") {
		t.Fatalf("run() error = %q, want migrate usage error", err)
	}
}

func TestResolveMigrationsDirUsesEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(migrationsDirEnv, dir)

	got, err := resolveMigrationsDir()
	if err != nil {
		t.Fatalf("resolveMigrationsDir() error = %v", err)
	}
	if got != dir {
		t.Fatalf("resolveMigrationsDir() = %q, want %q", got, dir)
	}
}

func TestResolveMigrationsDirWalksUpFromCWD(t *testing.T) {
	t.Setenv(migrationsDirEnv, "")

	originalCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	defer func() {
		if err := os.Chdir(originalCWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	if err := os.Chdir(filepath.Join("..", "..", "internal", "platform", "db")); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}

	got, err := resolveMigrationsDir()
	if err != nil {
		t.Fatalf("resolveMigrationsDir() error = %v", err)
	}
	if filepath.Base(got) != "migrations" || filepath.Base(filepath.Dir(got)) != "db" {
		t.Fatalf("resolveMigrationsDir() = %q, want db/migrations", got)
	}
}

func setVersionForTest(value string) func() {
	original := version
	version = value
	return func() {
		version = original
	}
}
