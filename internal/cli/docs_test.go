package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCLIDocsAreCurrent(t *testing.T) {
	got, err := generateCLIDocs()
	if err != nil {
		t.Fatalf("generateCLIDocs() error = %v", err)
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "docs", "cli.md"))
	if err != nil {
		t.Fatalf("read docs/cli.md: %v", err)
	}
	if got != string(want) {
		t.Fatal("docs/cli.md is stale; run `ledgerly docs generate`")
	}
}
