package config

import (
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
)

func TestLoadFromReportsMissingRequiredKeys(t *testing.T) {
	_, err := LoadFrom(mapLookup(nil))
	if err == nil {
		t.Fatal("LoadFrom() error = nil, want missing keys error")
	}

	var missing MissingKeysError
	if !errors.As(err, &missing) {
		t.Fatalf("LoadFrom() error type = %T, want MissingKeysError", err)
	}

	wantKeys := []string{
		"LEDGERLY_DATABASE_URL",
		"LEDGERLY_DATA_DIR",
		"LEDGERLY_ENV",
		"LEDGERLY_LOG_LEVEL",
	}
	if !slices.Equal(missing.Keys, wantKeys) {
		t.Fatalf("missing keys = %v, want %v", missing.Keys, wantKeys)
	}

	message := err.Error()
	for _, key := range wantKeys {
		if !strings.Contains(message, key) {
			t.Fatalf("error %q does not list missing key %s", message, key)
		}
	}

	if strings.Contains(message, "LEDGERLY_HTTP_ADDR") {
		t.Fatalf("error %q lists defaulted optional key LEDGERLY_HTTP_ADDR", message)
	}
}

func TestLoadFromAppliesDefaults(t *testing.T) {
	cfg, err := LoadFrom(mapLookup(map[string]string{
		"LEDGERLY_DATABASE_URL": "postgres://ledgerly@example/ledgerly",
		"LEDGERLY_DATA_DIR":     "/var/lib/ledgerly",
		"LEDGERLY_ENV":          "dev",
		"LEDGERLY_LOG_LEVEL":    "info",
	}))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}

	if cfg.DatabaseURL != "postgres://ledgerly@example/ledgerly" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.DataDir != "/var/lib/ledgerly" {
		t.Fatalf("DataDir = %q, want /var/lib/ledgerly", cfg.DataDir)
	}
	if cfg.HTTPAddr != DefaultHTTPAddr {
		t.Fatalf("HTTPAddr = %q, want %q", cfg.HTTPAddr, DefaultHTTPAddr)
	}
	if cfg.Env != EnvDev {
		t.Fatalf("Env = %q, want %q", cfg.Env, EnvDev)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("LogLevel = %s, want %s", cfg.LogLevel, slog.LevelInfo)
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
