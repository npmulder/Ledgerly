package testdb

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(Main(m))
}
