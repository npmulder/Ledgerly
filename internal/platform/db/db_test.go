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
