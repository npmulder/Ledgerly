package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const testModulePath = "github.com/npmulder/ledgerly"

func TestBoundaryFixtureFails(t *testing.T) {
	imports, err := importsFromFile(filepath.Join("testdata", "internal", "invoicing", "deep_import.go"))
	if err != nil {
		t.Fatal(err)
	}

	findings := checkPackageBoundaries([]goPackage{
		{
			ImportPath: testModulePath + "/internal/invoicing",
			Imports:    imports,
		},
	}, testModulePath)

	if len(findings) != 1 {
		t.Fatalf("expected 1 boundary finding, got %d: %#v", len(findings), findings)
	}
	if !strings.Contains(findings[0].String(), "internal/ledger/internal") {
		t.Fatalf("expected deep ledger import diagnostic, got %q", findings[0])
	}
}

func TestBoundaryLeafImportFails(t *testing.T) {
	findings := checkPackageBoundaries([]goPackage{
		{
			ImportPath: testModulePath + "/internal/ledger",
			Imports: []string{
				testModulePath + "/internal/invoicing",
			},
		},
	}, testModulePath)

	if len(findings) != 1 {
		t.Fatalf("expected 1 boundary finding, got %d: %#v", len(findings), findings)
	}
	if !strings.Contains(findings[0].String(), "declared internal/<module> root dependencies") {
		t.Fatalf("expected declared dependency diagnostic, got %q", findings[0])
	}
}

func TestBoundaryDeclaredDependenciesPass(t *testing.T) {
	findings := checkPackageBoundaries([]goPackage{
		{
			ImportPath: testModulePath + "/internal/invoicing/internal/workflow",
			Imports: []string{
				testModulePath + "/internal/identity",
				testModulePath + "/internal/jurisdiction",
				testModulePath + "/internal/ledger",
				testModulePath + "/internal/moneyfx",
				testModulePath + "/internal/moneyfx/money",
				testModulePath + "/internal/platform/db",
			},
		},
	}, testModulePath)

	if len(findings) != 0 {
		t.Fatalf("expected declared dependencies to pass, got %#v", findings)
	}
}

func TestBoundaryMoneyFXMayImportInvoicingRootOnly(t *testing.T) {
	findings := checkPackageBoundaries([]goPackage{
		{
			ImportPath: testModulePath + "/internal/moneyfx",
			Imports: []string{
				testModulePath + "/internal/invoicing",
			},
		},
		{
			ImportPath: testModulePath + "/internal/moneyfx",
			TestImports: []string{
				testModulePath + "/internal/invoicing/internal/workflow",
			},
		},
	}, testModulePath)

	if len(findings) != 1 {
		t.Fatalf("expected 1 boundary finding, got %d: %#v", len(findings), findings)
	}
	if !strings.Contains(findings[0].String(), "internal/invoicing/internal/workflow") {
		t.Fatalf("expected deep invoicing import diagnostic, got %q", findings[0])
	}
}

func TestRateFixtureFails(t *testing.T) {
	findings, err := checkRateLiterals(filepath.Join("testdata", "internal"), rateLiteralAllowedModules, guardedLiterals)
	if err != nil {
		t.Fatal(err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 rate findings, got %d: %#v", len(findings), findings)
	}
	joined := joinFindings(findings)
	for _, want := range []string{
		filepath.Join("internal", "invoicing", "rate_literal.go"),
		filepath.Join("internal", "ledger", "rate_literal.go"),
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected rate diagnostic for %s, got:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, filepath.Join("internal", "jurisdiction", "rate_literal.go")) {
		t.Fatalf("expected jurisdiction pack literal to be allowed, got:\n%s", joined)
	}
}

func TestListPackagesKeepsStderrOutOfJSONStream(t *testing.T) {
	previous := execCommand
	execCommand = fakeGoListCommand
	t.Cleanup(func() {
		execCommand = previous
	})

	pkgs, err := listPackages([]string{"./internal/..."})
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d: %#v", len(pkgs), pkgs)
	}
	if pkgs[0].ImportPath != testModulePath+"/internal/invoicing" {
		t.Fatalf("unexpected package import path: %q", pkgs[0].ImportPath)
	}
}

func fakeGoListCommand(command string, args ...string) *exec.Cmd {
	testArgs := []string{"-test.run=TestFakeGoListCommand", "--", command}
	testArgs = append(testArgs, args...)
	cmd := exec.Command(os.Args[0], testArgs...)
	cmd.Env = append(os.Environ(), "ARCHCHECK_FAKE_GO_LIST=1")
	return cmd
}

func TestFakeGoListCommand(t *testing.T) {
	if os.Getenv("ARCHCHECK_FAKE_GO_LIST") != "1" {
		return
	}

	_, _ = fmt.Fprintln(os.Stderr, "go: downloading example.com/noisy-dependency v0.0.0")
	_, _ = fmt.Fprintln(os.Stdout, `{"ImportPath":"github.com/npmulder/ledgerly/internal/invoicing","Imports":["fmt"]}`)
	os.Exit(0)
}

func joinFindings(findings []finding) string {
	lines := make([]string, 0, len(findings))
	for _, finding := range findings {
		lines = append(lines, finding.String())
	}
	return strings.Join(lines, "\n")
}
