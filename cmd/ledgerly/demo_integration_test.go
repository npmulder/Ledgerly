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
	cleanDemoRows(t, ctx, adminPool)
	defer cleanDemoRows(t, context.Background(), adminPool)

	demoPool, err := openPoolWithRetry(ctx, databaseURL, db.WithModule(demo.ModuleName))
	if err != nil {
		t.Fatalf("open demo pool: %v", err)
	}
	defer demoPool.Close()

	rollbackBody := "force rollback from subscriber"
	router, err := buildApplicationRouter(applicationWiring{
		Version:  "test",
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		HealthDB: pgxPinger{pool: adminPool},
		DemoPool: demoPool,
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
	note := postNote(t, ctx, client, server.URL, "hello from demo", nethttp.StatusCreated)
	notes := getNotes(t, ctx, client, server.URL)
	assertListedNote(t, notes, note)
	assertDemoRowCount(t, ctx, adminPool, "successful audit row", 1, `
SELECT count(*)
FROM demo.notes
WHERE kind = 'audit' AND note_id = $1`, note.ID)

	postNote(t, ctx, client, server.URL, rollbackBody, nethttp.StatusInternalServerError)
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

func postNote(t *testing.T, ctx context.Context, client *nethttp.Client, baseURL, body string, wantStatus int) demo.Note {
	t.Helper()

	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		t.Fatalf("marshal create note request: %v", err)
	}
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, baseURL+"/api/demo/notes", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/demo/notes: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST response: %v", err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST /api/demo/notes status = %d, want %d; body=%s", resp.StatusCode, wantStatus, string(bodyBytes))
	}
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

func getNotes(t *testing.T, ctx context.Context, client *nethttp.Client, baseURL string) []demo.Note {
	t.Helper()

	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, baseURL+"/api/demo/notes", nil)
	if err != nil {
		t.Fatalf("create GET request: %v", err)
	}
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
