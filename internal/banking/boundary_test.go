package banking

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestBankingPackageInternalImportBoundary(t *testing.T) {
	cmd := exec.Command("go", "list", "-json", ".")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("go list failed: %v\n%s", err, stderr.String())
	}

	var pkg struct {
		Imports []string
	}
	if err := json.Unmarshal(stdout.Bytes(), &pkg); err != nil {
		t.Fatalf("decode go list JSON: %v", err)
	}

	for _, importPath := range pkg.Imports {
		if !strings.HasPrefix(importPath, "github.com/npmulder/ledgerly/internal/") {
			continue
		}
		if bankingBoundaryAllowed(importPath) {
			continue
		}
		t.Fatalf("banking imports internal dependency %q; allowed internal modules are platform, money, moneyfx root, ledger root, invoicing root, and dla root", importPath)
	}
}

func bankingBoundaryAllowed(importPath string) bool {
	switch importPath {
	case "github.com/npmulder/ledgerly/internal/ledger",
		"github.com/npmulder/ledgerly/internal/invoicing",
		"github.com/npmulder/ledgerly/internal/dla",
		"github.com/npmulder/ledgerly/internal/moneyfx",
		"github.com/npmulder/ledgerly/internal/moneyfx/money":
		return true
	}
	return strings.HasPrefix(importPath, "github.com/npmulder/ledgerly/internal/platform/")
}
