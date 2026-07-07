package db

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWithModulePinsSearchPath(t *testing.T) {
	cfg, err := pgxpool.ParseConfig(DefaultDevDatabaseURL)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	if err := WithModule("invoicing")(cfg); err != nil {
		t.Fatalf("WithModule() error = %v", err)
	}

	if got := cfg.ConnConfig.RuntimeParams["search_path"]; got != "invoicing" {
		t.Fatalf("search_path = %q, want invoicing", got)
	}
	if cfg.AfterConnect == nil {
		t.Fatal("AfterConnect = nil, want module role setup hook")
	}
}

func TestWithModuleRejectsUnknownModule(t *testing.T) {
	cfg, err := pgxpool.ParseConfig(DefaultDevDatabaseURL)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	if err := WithModule("billing")(cfg); err == nil {
		t.Fatal("WithModule() error = nil, want unknown module error")
	}
}

func TestModulesAndRoles(t *testing.T) {
	modules := Modules()
	if len(modules) != 10 {
		t.Fatalf("len(Modules()) = %d, want 10", len(modules))
	}

	for _, module := range modules {
		role, err := RoleForModule(module)
		if err != nil {
			t.Fatalf("RoleForModule(%q) error = %v", module, err)
		}
		if want := "ledgerly_" + module; role != want {
			t.Fatalf("RoleForModule(%q) = %q, want %q", module, role, want)
		}
	}
}

func TestHistoricalMigrationChecksumCompatibility(t *testing.T) {
	migration := migrationFile{
		AppliedMigration: AppliedMigration{
			Module:   "identity",
			Filename: "003_company_profile.sql",
			Checksum: "61daeee584c6c6e407bfae57d02338d561ae56674d71fd7f2b141bbb9e59df68",
		},
	}

	if !isHistoricalMigrationChecksum(migration, "8feff29291685754dc96a2955b89167fe25d73b198a091fbbcb5a11a0fa3af6a") {
		t.Fatal("old identity/003 checksum was rejected, want compatibility")
	}
	if isHistoricalMigrationChecksum(migration, "not-a-real-checksum") {
		t.Fatal("unknown checksum was accepted, want mismatch rejection")
	}

	migration.Module = "ledger"
	if isHistoricalMigrationChecksum(migration, "8feff29291685754dc96a2955b89167fe25d73b198a091fbbcb5a11a0fa3af6a") {
		t.Fatal("checksum was accepted for wrong module")
	}
}
