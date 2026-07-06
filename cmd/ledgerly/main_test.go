package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func TestRunRejectsInvalidCheckArguments(t *testing.T) {
	var stdout bytes.Buffer
	err := run(context.Background(), []string{"check"}, &stdout)
	if err == nil {
		t.Fatal("run() error = nil, want check usage error")
	}
	if !strings.Contains(err.Error(), "usage: ledgerly check trial-balance") {
		t.Fatalf("run() error = %q, want check usage error", err)
	}

	err = run(context.Background(), []string{"check", "trial-balance", "extra"}, &stdout)
	if err == nil {
		t.Fatal("run() error = nil, want check usage error")
	}
	if !strings.Contains(err.Error(), "usage: ledgerly check trial-balance") {
		t.Fatalf("run() error = %q, want check usage error", err)
	}
}

func TestRunRejectsInvalidFetchRatesArguments(t *testing.T) {
	var stdout bytes.Buffer
	err := run(context.Background(), []string{"fetch-rates", "extra"}, &stdout)
	if err == nil {
		t.Fatal("run() error = nil, want fetch-rates usage error")
	}
	if !strings.Contains(err.Error(), "usage: ledgerly fetch-rates") {
		t.Fatalf("run() error = %q, want fetch-rates usage error", err)
	}
}

func TestRunFetchRatesDispatchesRunner(t *testing.T) {
	restore := setFetchRatesRunnerForTest(func(ctx context.Context, stdout io.Writer) error {
		_, err := fmt.Fprintln(stdout, "stub fetch")
		return err
	})
	defer restore()

	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"fetch-rates"}, &stdout); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got, want := stdout.String(), "stub fetch\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunPrintsOpenAPIDocument(t *testing.T) {
	restore := setVersionForTest("test-sha")
	defer restore()

	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"openapi"}, &stdout); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	var document map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("openapi output is not JSON: %v; body=%s", err, stdout.String())
	}
	info, ok := document["info"].(map[string]any)
	if !ok {
		t.Fatalf("openapi info missing or wrong type: %+v", document["info"])
	}
	if got := info["version"]; got != "test-sha" {
		t.Fatalf("openapi version = %v, want test-sha", got)
	}
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatalf("openapi paths missing or wrong type: %+v", document["paths"])
	}
	if _, ok := paths["/api/identity/login"]; !ok {
		t.Fatalf("openapi paths missing /api/identity/login: %+v", paths)
	}
	if _, ok := paths["/api/invoicing/clients"]; !ok {
		t.Fatalf("openapi paths missing /api/invoicing/clients: %+v", paths)
	}
	if _, ok := paths["/api/jurisdiction/pack"]; !ok {
		t.Fatalf("openapi paths missing /api/jurisdiction/pack: %+v", paths)
	}
	if _, ok := paths["/api/ledger/accounts"]; !ok {
		t.Fatalf("openapi paths missing /api/ledger/accounts: %+v", paths)
	}
	if _, ok := paths["/api/dla/balance"]; !ok {
		t.Fatalf("openapi paths missing /api/dla/balance: %+v", paths)
	}
	if _, ok := paths["/api/reports/pl"]; !ok {
		t.Fatalf("openapi paths missing /api/reports/pl: %+v", paths)
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
	t.Setenv("LEDGERLY_DATA_DIR", t.TempDir())
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

func setFetchRatesRunnerForTest(runner func(context.Context, io.Writer) error) func() {
	original := fetchRatesRunner
	fetchRatesRunner = runner
	return func() {
		fetchRatesRunner = original
	}
}

const malformedJurisdictionPack = `meta:
  id: testland
  version: "0.1"
  name: Testland
  currency: TST
tax:
  year_end:
    month: 6
    day: 30
  corporate_income:
    "2025-26":
      standard_rate: "0.19"
  personal_income:
    "2025-26":
      personal_allowance_minor_units: 1234
      bands:
        - upto_minor_units: 1000
          rate: "0.05"
        - rate: "0.25"
  dividends:
    "2025-26":
      withholding: test-withholding
  vat:
    regime: test-shared
    authority: Testland Customs
    "2025-26":
      standard_rate: "0.17"
    reverse_charge:
      b2b_services_eu:
        article: Test Article 42
        invoice_wording: ""
filings:
  annual_return:
    due: incorporation_anniversary + 1 month
    authority: Testland Companies Office
director_loans:
  s455_charge: true
  overdrawn:
    warn: test_director_loan_warning
    remedy: test_clear_or_repay
advisor_rules:
  - id: test-rule
    severity: amber
    surfaces: [dashboard, reports]
    fact_query: [balance]
    condition: balance > 0
    text_template: Review the test balance before filing
    cta:
      label: Open test review
      action: test.openReview
`
