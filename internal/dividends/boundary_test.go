package dividends

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestDividendsPackageHasAllowedInternalImports(t *testing.T) {
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

	allowed := map[string]struct{}{
		"github.com/npmulder/ledgerly/internal/dla":           {},
		"github.com/npmulder/ledgerly/internal/identity":      {},
		"github.com/npmulder/ledgerly/internal/jurisdiction":  {},
		"github.com/npmulder/ledgerly/internal/ledger":        {},
		"github.com/npmulder/ledgerly/internal/moneyfx/money": {},
		"github.com/npmulder/ledgerly/internal/reports":       {},
	}
	for _, importPath := range pkg.Imports {
		if _, ok := allowed[importPath]; ok {
			continue
		}
		if strings.HasPrefix(importPath, "github.com/npmulder/ledgerly/internal/platform/") {
			continue
		}
		if strings.HasPrefix(importPath, "github.com/npmulder/ledgerly/internal/") {
			t.Fatalf("dividends imports internal dependency %q; allowed internal dependencies are platform, money, ledger, reports, jurisdiction, identity, and dla", importPath)
		}
	}
}
