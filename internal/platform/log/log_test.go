package log

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestConfigureUsesJSONInProdWithModule(t *testing.T) {
	var out bytes.Buffer
	Configure(Config{
		Env:    "prod",
		Level:  slog.LevelInfo,
		Writer: &out,
	})

	For("ledger").Info("posting balanced")

	var entry map[string]any
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
		t.Fatalf("prod log output is not JSON: %v; output=%q", err, out.String())
	}

	if entry["module"] != "ledger" {
		t.Fatalf("module attribute = %v, want ledger", entry["module"])
	}
	if entry["msg"] != "posting balanced" {
		t.Fatalf("msg = %v, want posting balanced", entry["msg"])
	}
}

func TestConfigureUsesTextOutsideProdWithModule(t *testing.T) {
	var out bytes.Buffer
	Configure(Config{
		Env:    "dev",
		Level:  slog.LevelInfo,
		Writer: &out,
	})

	For("ledger").Info("posting balanced")

	got := out.String()
	if !strings.Contains(got, "module=ledger") {
		t.Fatalf("dev log output %q does not contain module attribute", got)
	}
	if json.Valid(out.Bytes()) {
		t.Fatalf("dev log output is JSON, want text: %q", got)
	}
}
