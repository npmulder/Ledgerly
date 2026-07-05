package jurisdiction

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestJurisdictionPackageHasNoInternalModuleImports(t *testing.T) {
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
		if strings.HasPrefix(importPath, "github.com/npmulder/ledgerly/internal/") {
			t.Fatalf("jurisdiction imports internal dependency %q; package must remain a leaf", importPath)
		}
	}
}
