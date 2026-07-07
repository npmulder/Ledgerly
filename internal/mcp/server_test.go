package mcp

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestServeStopsWhenContextCanceledWhileInputIdle(t *testing.T) {
	server, err := New(Config{BaseURL: "http://127.0.0.1", Token: "lgy_read"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, reader, io.Discard)
	}()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error after context cancellation = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Serve() did not return after context cancellation while stdin was idle")
	}
}
