//go:build integration

package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"sync"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/app"
	"github.com/npmulder/ledgerly/internal/demo"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

func TestDemoWalkingSkeletonE2E(t *testing.T) {
	t.Parallel()

	h := harness.New(t, harness.Options{})

	note := postNote(t, h, "hello from harness", nethttp.StatusCreated)
	notes := getNotes(t, h)
	assertListedNote(t, notes, note)
	var txNoteCount int
	h.Tx(func(ctx context.Context, tx db.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM demo.notes WHERE kind = 'note'").Scan(&txNoteCount)
	})
	if txNoteCount != 1 {
		t.Fatalf("h.Tx note count = %d, want 1", txNoteCount)
	}
	assertDemoRowCount(t, h, "successful audit row", 1, `
SELECT count(*)
FROM demo.notes
WHERE kind = 'audit' AND note_id = $1`, note.ID)

	rollbackBody := "force rollback from harness subscriber"
	h.FailNextBusSubscriber(demo.NoteCreatedName, errors.New("forced demo audit failure"))
	postNote(t, h, rollbackBody, nethttp.StatusInternalServerError)
	assertDemoRowCount(t, h, "rolled back note row", 0, `
SELECT count(*)
FROM demo.notes
WHERE kind = 'note' AND body = $1`, rollbackBody)
	assertDemoRowCount(t, h, "rolled back audit row", 0, `
SELECT count(*)
FROM demo.notes
WHERE kind = 'audit' AND body LIKE '%' || $1 || '%'`, rollbackBody)
}

func TestHarnessBootsUnderTwoSecondsAfterTemplateDB(t *testing.T) {
	t.Parallel()

	testdb.Raw(t)

	start := time.Now()
	h := harness.New(t, harness.Options{})
	elapsed := time.Since(start)
	if elapsed >= 2*time.Second {
		t.Fatalf("harness boot duration = %s, want < 2s", elapsed)
	}
	if h.BootDuration >= 2*time.Second {
		t.Fatalf("reported harness boot duration = %s, want < 2s", h.BootDuration)
	}
}

func TestParallelHarnessesDoNotInterfere(t *testing.T) {
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	posted := make(chan struct{}, 2)
	releaseList := make(chan struct{})
	var wait sync.Once

	waitForBoth := func() {
		wait.Do(func() {
			go func() {
				<-ready
				<-ready
				close(release)
				<-posted
				<-posted
				close(releaseList)
			}()
		})
	}

	for _, name := range []string{"suite_one", "suite_two"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			waitForBoth()

			h := harness.New(t, harness.Options{})
			ready <- struct{}{}
			<-release

			postNote(t, h, "shared parallel note", nethttp.StatusCreated)
			posted <- struct{}{}
			<-releaseList

			notes := getNotes(t, h)
			var matches int
			for _, note := range notes {
				if note.Body == "shared parallel note" {
					matches++
				}
			}
			if matches != 1 {
				t.Fatalf("shared note count = %d, want 1; notes=%+v", matches, notes)
			}
		})
	}
}

func TestClockAdvanceChangesHealthResponse(t *testing.T) {
	t.Parallel()

	start := time.Date(2040, 6, 7, 8, 9, 10, 0, time.UTC)
	h := harness.New(t, harness.Options{ClockStart: start})

	first := getHealth(t, h)
	if first.CheckedAt != start.Format(time.RFC3339Nano) {
		t.Fatalf("first checked_at = %q, want %q", first.CheckedAt, start.Format(time.RFC3339Nano))
	}

	advanced := h.Clock.Advance(3 * time.Hour)
	second := getHealth(t, h)
	if second.CheckedAt != advanced.Format(time.RFC3339Nano) {
		t.Fatalf("second checked_at = %q, want %q", second.CheckedAt, advanced.Format(time.RFC3339Nano))
	}
}

func TestRunJobExecutesRegisteredJob(t *testing.T) {
	t.Parallel()

	var ran bool
	h := harness.New(t, harness.Options{
		Jobs: map[string]app.Job{
			"probe": func(context.Context) error {
				ran = true
				return nil
			},
		},
	})

	if err := h.RunJob("probe"); err != nil {
		t.Fatalf("RunJob() error = %v", err)
	}
	if !ran {
		t.Fatal("registered job did not run")
	}
}

func postNote(t *testing.T, h *harness.Harness, body string, wantStatus int) demo.Note {
	t.Helper()

	bodyBytes, status := doJSON(t, h, nethttp.MethodPost, "/api/demo/notes", map[string]string{"body": body})
	if status != wantStatus {
		t.Fatalf("POST /api/demo/notes status = %d, want %d; body=%s", status, wantStatus, string(bodyBytes))
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

func getNotes(t *testing.T, h *harness.Harness) []demo.Note {
	t.Helper()

	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, "/api/demo/notes", nil)
	if err != nil {
		t.Fatalf("create GET /api/demo/notes request: %v", err)
	}
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("GET /api/demo/notes: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET /api/demo/notes response: %v", err)
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

type healthBody struct {
	CheckedAt string `json:"checked_at"`
}

func getHealth(t *testing.T, h *harness.Harness) healthBody {
	t.Helper()

	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, "/healthz", nil)
	if err != nil {
		t.Fatalf("create GET /healthz request: %v", err)
	}
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET /healthz response: %v", err)
	}
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d; body=%s", resp.StatusCode, nethttp.StatusOK, string(bodyBytes))
	}

	var body healthBody
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("decode health response: %v; body=%s", err, string(bodyBytes))
	}
	return body
}

func doJSON(t *testing.T, h *harness.Harness, method string, path string, requestBody any) ([]byte, int) {
	t.Helper()

	payload, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req, err := nethttp.NewRequestWithContext(context.Background(), method, path, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create %s request: %v", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return bodyBytes, resp.StatusCode
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

func assertDemoRowCount(t *testing.T, h *harness.Harness, label string, want int, query string, args ...any) {
	t.Helper()

	var got int
	if err := h.DB.QueryRow(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", label, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", label, got, want)
	}
}
