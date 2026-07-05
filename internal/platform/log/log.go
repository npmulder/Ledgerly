// Package log configures structured logging for Ledgerly modules.
package log

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

const (
	envProd = "prod"
)

var (
	mu     sync.RWMutex
	logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
)

// Config controls slog handler setup.
type Config struct {
	Env    string
	Level  slog.Leveler
	Writer io.Writer
}

// Configure sets the package logger. Prod uses JSON; all other environments use text.
func Configure(cfg Config) *slog.Logger {
	writer := cfg.Writer
	if writer == nil {
		writer = os.Stdout
	}

	level := cfg.Level
	if level == nil {
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if strings.EqualFold(cfg.Env, envProd) {
		handler = slog.NewJSONHandler(writer, opts)
	} else {
		handler = slog.NewTextHandler(writer, opts)
	}

	configured := slog.New(handler)

	mu.Lock()
	logger = configured
	mu.Unlock()

	return configured
}

// For returns a logger annotated with the calling module name.
func For(module string) *slog.Logger {
	mu.RLock()
	defer mu.RUnlock()

	return logger.With("module", module)
}
