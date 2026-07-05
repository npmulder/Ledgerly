//go:build integration

package harness_test

import (
	"os"
	"testing"

	"github.com/npmulder/ledgerly/internal/it/testdb"
)

func TestMain(m *testing.M) {
	os.Exit(testdb.Main(m))
}
