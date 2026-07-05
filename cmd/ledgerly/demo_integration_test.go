//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/demo"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

func TestDemoWalkingSkeletonE2E(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("LEDGERLY_TEST_DB"))
	if databaseURL == "" {
		t.Skip("set LEDGERLY_TEST_DB to run demo walking-skeleton E2E")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	adminPool, err := openPoolWithRetry(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()

	if _, err := db.MigrateDir(ctx, adminPool, filepath.Join(findRepoRoot(t), "db", "migrations")); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	cleanIdentityRows(t, ctx, adminPool)
	cleanDemoRows(t, ctx, adminPool)
	defer cleanDemoRows(t, context.Background(), adminPool)
	defer cleanIdentityRows(t, context.Background(), adminPool)

	identityPool, err := openPoolWithRetry(ctx, databaseURL, db.WithModule("identity"))
	if err != nil {
		t.Fatalf("open identity pool: %v", err)
	}
	defer identityPool.Close()

	demoPool, err := openPoolWithRetry(ctx, databaseURL, db.WithModule(demo.ModuleName))
	if err != nil {
		t.Fatalf("open demo pool: %v", err)
	}
	defer demoPool.Close()

	eventBus := bus.New(bus.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	identityService := identity.NewService(identity.NewPostgresStore(identityPool), clock.New())
	identityProfile := identity.NewProfileService(identityPool, eventBus, identity.WithDataDir(t.TempDir()))
	identityHandler := identity.NewHTTPHandler(identityService, identity.WithProfileAPI(identityProfile))

	rollbackBody := "force rollback from subscriber"
	router, err := buildApplicationRouter(applicationWiring{
		Version:         "test",
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		HealthDB:        pgxPinger{pool: adminPool},
		EventBus:        eventBus,
		IdentityService: identityService,
		IdentityHandler: identityHandler,
		DemoPool:        demoPool,
		DemoAuditFailure: func(_ context.Context, _ db.Tx, evt demo.NoteCreated) error {
			if evt.Body == rollbackBody {
				return errors.New("forced demo audit failure")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("build router: %v", err)
	}

	server := httptest.NewServer(router)
	defer server.Close()

	client := server.Client()
	sessionCookie := registerAndLoginOwner(t, ctx, client, server.URL)

	note := postNote(t, ctx, client, server.URL, sessionCookie, "hello from demo", nethttp.StatusCreated)
	notes := getNotes(t, ctx, client, server.URL, sessionCookie)
	assertListedNote(t, notes, note)
	assertDemoRowCount(t, ctx, adminPool, "successful audit row", 1, `
SELECT count(*)
FROM demo.notes
WHERE kind = 'audit' AND note_id = $1`, note.ID)

	postNote(t, ctx, client, server.URL, sessionCookie, rollbackBody, nethttp.StatusInternalServerError)
	assertDemoRowCount(t, ctx, adminPool, "rolled back note row", 0, `
SELECT count(*)
FROM demo.notes
WHERE kind = 'note' AND body = $1`, rollbackBody)
	assertDemoRowCount(t, ctx, adminPool, "rolled back audit row", 0, `
SELECT count(*)
FROM demo.notes
WHERE kind = 'audit' AND body LIKE '%' || $1 || '%'`, rollbackBody)
}

type pgxPinger struct {
	pool *pgxpool.Pool
}

func (p pgxPinger) PingContext(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

func registerAndLoginOwner(t *testing.T, ctx context.Context, client *nethttp.Client, baseURL string) *nethttp.Cookie {
	t.Helper()

	postJSON(t, ctx, client, baseURL+"/api/identity/register", map[string]string{
		"email":    "owner@example.com",
		"password": "correct horse battery staple",
		"name":     "Owner",
	}, nil, nethttp.StatusCreated)

	_, cookies := postJSON(t, ctx, client, baseURL+"/api/identity/login", map[string]string{
		"email":    "owner@example.com",
		"password": "correct horse battery staple",
	}, nil, nethttp.StatusOK)

	for _, cookie := range cookies {
		if cookie.Name == identity.SessionCookieName {
			return cookie
		}
	}
	t.Fatalf("login response did not set %s cookie", identity.SessionCookieName)
	return nil
}

func postJSON(t *testing.T, ctx context.Context, client *nethttp.Client, url string, requestBody any, cookie *nethttp.Cookie, wantStatus int) ([]byte, []*nethttp.Cookie) {
	t.Helper()

	payload, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST response: %v", err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST %s status = %d, want %d; body=%s", url, resp.StatusCode, wantStatus, string(bodyBytes))
	}
	return bodyBytes, resp.Cookies()
}

func postNote(t *testing.T, ctx context.Context, client *nethttp.Client, baseURL string, cookie *nethttp.Cookie, body string, wantStatus int) demo.Note {
	t.Helper()

	bodyBytes, _ := postJSON(t, ctx, client, baseURL+"/api/demo/notes", map[string]string{"body": body}, cookie, wantStatus)
	if wantStatus != nethttp.StatusCreated {
		return demo.Note{}
	}

	var note demo.Note
	if err := json.Unmarshal(bodyBytes, &note); err != nil {
		t.Fatalf("decode created note: %v; body=%s", err, string(bodyBytes))
	}
	if note.ID == "" {
		t.Fatalf("created note ID is empty: %+v", note)
	}
	if note.Body != body {
		t.Fatalf("created note body = %q, want %q", note.Body, body)
	}
	if note.CreatedAt == "" {
		t.Fatalf("created note timestamp is empty: %+v", note)
	}
	return note
}

func getNotes(t *testing.T, ctx context.Context, client *nethttp.Client, baseURL string, cookie *nethttp.Cookie) []demo.Note {
	t.Helper()

	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, baseURL+"/api/demo/notes", nil)
	if err != nil {
		t.Fatalf("create GET request: %v", err)
	}
	req.AddCookie(cookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/demo/notes: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET response: %v", err)
	}
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET /api/demo/notes status = %d, want %d; body=%s", resp.StatusCode, nethttp.StatusOK, string(bodyBytes))
	}

	var response struct {
		Notes []demo.Note `json:"notes"`
	}
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		t.Fatalf("decode list notes: %v; body=%s", err, string(bodyBytes))
	}
	return response.Notes
}

func assertListedNote(t *testing.T, notes []demo.Note, want demo.Note) {
	t.Helper()

	for _, note := range notes {
		if note.ID == want.ID && note.Body == want.Body {
			return
		}
	}
	t.Fatalf("created note %+v not found in list %+v", want, notes)
}

func assertDemoRowCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string, want int, query string, args ...any) {
	t.Helper()

	var got int
	if err := pool.QueryRow(ctx, query, args...).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", label, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", label, got, want)
	}
}

func cleanDemoRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	cleanupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(cleanupCtx, "TRUNCATE TABLE demo.notes"); err != nil {
		t.Fatalf("clean demo rows: %v", err)
	}
}

func cleanIdentityRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	cleanupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(cleanupCtx, "TRUNCATE TABLE identity.sessions, identity.users RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("clean identity rows: %v", err)
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repository root")
		}
		dir = parent
	}
}
