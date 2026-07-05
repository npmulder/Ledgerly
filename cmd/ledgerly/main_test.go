package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
