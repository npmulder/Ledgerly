package main

import (
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

func TestRateFixtureFails(t *testing.T) {
	findings, err := checkRateLiterals(filepath.Join("testdata", "internal"), featureModules, guardedLiterals)
	if err != nil {
		t.Fatal(err)
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 rate finding, got %d: %#v", len(findings), findings)
	}
	if !strings.Contains(findings[0].String(), "0.20") {
		t.Fatalf("expected 0.20 diagnostic, got %q", findings[0])
	}
}
