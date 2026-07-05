package app

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/npmulder/ledgerly/internal/platform/config"
)

func TestBuildLoadsConfiguredJurisdictionBeforeOpeningDatabases(t *testing.T) {
	loadErr := errors.New("bad jurisdiction pack")
	var gotSelector string

	_, err := Build(context.Background(), Config{
		Runtime: config.Config{
			DatabaseURL:  "postgres://ledgerly@example.test/ledgerly",
			Jurisdiction: "testland@0.1",
		},
		Version: "test",
	}, Dependencies{
		JurisdictionLoader: func(selector string) error {
			gotSelector = selector
			return loadErr
		},
		OpenSQL: func(driverName, dataSourceName string) (*sql.DB, error) {
			t.Fatalf("OpenSQL called before jurisdiction load failed")
			return nil, nil
		},
	})
	if !errors.Is(err, loadErr) {
		t.Fatalf("Build() error = %v, want wrapped jurisdiction loader error", err)
	}
	if gotSelector != "testland@0.1" {
		t.Fatalf("jurisdiction selector = %q, want testland@0.1", gotSelector)
	}
}
