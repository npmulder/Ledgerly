package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/npmulder/ledgerly/internal/jurisdiction"
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

func TestRunServeFailsOnMalformedJurisdictionPack(t *testing.T) {
	t.Setenv("LEDGERLY_DATABASE_URL", "postgres://ledgerly@example/ledgerly")
	t.Setenv("LEDGERLY_ENV", "dev")
	t.Setenv("LEDGERLY_LOG_LEVEL", "info")
	t.Setenv("LEDGERLY_JURISDICTION", "testland@0.1")

	restore := setJurisdictionLoaderForTest(func(selector string) error {
		return jurisdiction.LoadActiveFromFS(fstest.MapFS{
			jurisdiction.PackPath("testland", "0.1"): {
				Data: []byte(malformedJurisdictionPack),
			},
		}, selector)
	})
	defer restore()

	var stdout bytes.Buffer
	err := run(context.Background(), []string{"serve"}, &stdout)
	if err == nil {
		t.Fatal("run() error = nil, want malformed jurisdiction pack error")
	}

	message := err.Error()
	for _, want := range []string{
		"load jurisdiction pack",
		"packs/testland/0.1/pack.yaml",
		"tax.vat.reverse_charge.b2b_services_eu.invoice_wording",
		"field invoice_wording",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("run() error = %q, want text %q", message, want)
		}
	}
}

func setVersionForTest(value string) func() {
	original := version
	version = value
	return func() {
		version = original
	}
}

func setJurisdictionLoaderForTest(loader func(string) error) func() {
	original := loadActiveJurisdiction
	loadActiveJurisdiction = loader
	return func() {
		loadActiveJurisdiction = original
	}
}

const malformedJurisdictionPack = `meta:
  id: testland
  version: "0.1"
  name: Testland
  currency: TST
tax:
  corporate_income:
    "2025-26":
      standard_rate: 0.19
  personal_income:
    "2025-26":
      personal_allowance: 1234
      bands:
        - upto: 1000
          rate: 0.05
        - rate: 0.25
  dividends:
    "2025-26":
      withholding_rate: 0.04
  vat:
    regime: test-shared
    "2025-26":
      standard_rate: 0.17
    reverse_charge:
      b2b_services_eu:
        article: Test Article 42
        invoice_wording: ""
filings:
  annual_return:
    due: incorporation_anniversary + 1 month
    authority: Testland Companies Office
director_loans:
  "2025-26":
    s455_charge: true
    overdrawn:
      warn: test_director_loan_warning
      remedy: test_clear_or_repay
advisor_rules:
  - id: test-rule
    severity: amber
    fact_query: test.facts
    condition: balance > 0
    text_template: Review the test balance before filing
    cta: open_test_review
`
