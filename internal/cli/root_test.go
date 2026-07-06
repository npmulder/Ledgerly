package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthLoginStoresConfigWith0600(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/identity/me" {
			t.Fatalf("path = %s, want /api/identity/me", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer lgy_test" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"email":"owner@example.com","name":"Owner","created_at":"2026-07-05T12:00:00Z","token_name":"CLI token","token_scope":"full"}`))
	}))
	defer server.Close()

	configPath := filepath.Join(t.TempDir(), "config.toml")
	var stdout bytes.Buffer
	err := Execute(context.Background(), []string{
		"--config", configPath,
		"auth", "login",
		"--url", server.URL + "/",
		"--token", "lgy_test",
	}, &stdout, ioDiscard{})
	if err != nil {
		t.Fatalf("auth login error = %v", err)
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != configFileMode {
		t.Fatalf("config mode = %03o, want 600", got)
	}
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(body), `url = "`+server.URL+`"`) || !strings.Contains(string(body), `token = "lgy_test"`) {
		t.Fatalf("config body = %s", string(body))
	}
	if !strings.Contains(stdout.String(), "TOKEN NAME  CLI token") {
		t.Fatalf("stdout = %q, want token status table", stdout.String())
	}
}

func TestAuthStatusAgainstHarnessServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"email":"owner@example.com","name":"Owner","created_at":"2026-07-05T12:00:00Z","token_name":"Read token","token_scope":"read-only"}`))
	}))
	defer server.Close()

	configPath := writeTestConfig(t, server.URL, "lgy_status", configFileMode)
	var stdout bytes.Buffer
	err := Execute(context.Background(), []string{"--config", configPath, "--json", "auth", "status"}, &stdout, ioDiscard{})
	if err != nil {
		t.Fatalf("auth status error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, `"token_name": "Read token"`) || !strings.Contains(got, `"scope": "read-only"`) || !strings.Contains(got, `"reachable": true`) {
		t.Fatalf("json status = %s", got)
	}
}

func TestAuthStatusBadTokenExits3(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"https://ledgerly.local/problems/unauthenticated","title":"Unauthorized","status":401,"detail":"authentication required"}`))
	}))
	defer server.Close()

	configPath := writeTestConfig(t, server.URL, "lgy_bad", configFileMode)
	var stdout bytes.Buffer
	err := Execute(context.Background(), []string{"--config", configPath, "auth", "status"}, &stdout, ioDiscard{})
	if err == nil {
		t.Fatal("auth status error = nil, want auth error")
	}
	if exitCode(err) != ExitAuth {
		t.Fatalf("exit code = %d, want %d; err=%v", exitCode(err), ExitAuth, err)
	}
	if !strings.Contains(err.Error(), "Unauthorized — authentication required") {
		t.Fatalf("error = %q, want clean problem message", err.Error())
	}
}

func TestAuthStatusRejectsLooseConfigPermissions(t *testing.T) {
	configPath := writeTestConfig(t, "http://127.0.0.1:8080", "lgy_loose", 0o644)

	err := Execute(context.Background(), []string{"--config", configPath, "auth", "status"}, ioDiscard{}, ioDiscard{})
	if err == nil {
		t.Fatal("auth status error = nil, want config permission error")
	}
	if exitCode(err) != ExitAuth {
		t.Fatalf("exit code = %d, want %d; err=%v", exitCode(err), ExitAuth, err)
	}
	if !strings.Contains(err.Error(), "config permissions") {
		t.Fatalf("error = %q, want permissions message", err.Error())
	}
}

func writeTestConfig(t *testing.T, url, token string, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.toml")
	body := "url = " + quoteForTest(url) + "\ntoken = " + quoteForTest(token) + "\n"
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod config: %v", err)
	}
	return path
}

func quoteForTest(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func exitCode(err error) int {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return ExitDomain
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}
