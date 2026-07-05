package bus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/npmulder/ledgerly/internal/platform/db"
	platformlog "github.com/npmulder/ledgerly/internal/platform/log"
)

// Event is a domain fact published by a module.
//
// Event names follow the "<module>.<PascalCaseEvent>" convention, for example
// "invoicing.InvoiceSettled".
type Event interface {
	Name() string
}

// Handler reacts to an event inside the publisher's transaction.
type Handler func(context.Context, db.Tx, Event) error

// Bus dispatches domain events to in-process subscribers.
//
// Subscribe during application wiring. Publish executes matching handlers
// synchronously in registration order; it does not start goroutines, buffer
// events, retry handlers, or persist an outbox.
type Bus struct {
	mu       sync.RWMutex
	handlers map[string][]Handler
	logger   *slog.Logger
}

// Option customizes a Bus.
type Option func(*Bus)

// New creates a Bus.
func New(opts ...Option) *Bus {
	b := &Bus{}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// WithLogger sends dispatch telemetry to logger.
//
// The bus adds the event module as a "module" attribute on every dispatch log.
func WithLogger(logger *slog.Logger) Option {
	return func(b *Bus) {
		b.logger = logger
	}
}

// Subscribe registers h for events named name.
//
// Subscribe panics on invalid wiring input because callers should subscribe at
// startup, not at runtime.
func (b *Bus) Subscribe(name string, h Handler) {
	name, _, err := normalizeName(name)
	if err != nil {
		panic(err)
	}
	if h == nil {
		panic("bus: nil handler")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.handlers == nil {
		b.handlers = make(map[string][]Handler)
	}
	b.handlers[name] = append(b.handlers[name], h)
}

// Publish synchronously dispatches evt to all handlers subscribed to evt.Name().
func (b *Bus) Publish(ctx context.Context, tx db.Tx, evt Event) error {
	if evt == nil {
		return errors.New("bus: nil event")
	}

	name, module, err := normalizeName(evt.Name())
	if err != nil {
		return err
	}

	handlers := b.handlersFor(name)
	started := time.Now()

	var dispatchErr error
	for i, h := range handlers {
		if err := h(ctx, tx, evt); err != nil {
			dispatchErr = fmt.Errorf("bus: dispatch %s handler %d: %w", name, i+1, err)
			break
		}
	}

	b.logDispatch(ctx, module, name, len(handlers), time.Since(started), dispatchErr)
	return dispatchErr
}

func (b *Bus) handlersFor(name string) []Handler {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if len(b.handlers[name]) == 0 {
		return nil
	}

	handlers := make([]Handler, len(b.handlers[name]))
	copy(handlers, b.handlers[name])
	return handlers
}

func (b *Bus) logDispatch(ctx context.Context, module, name string, handlerCount int, duration time.Duration, err error) {
	logger := b.dispatchLogger(module)
	attrs := []any{
		slog.String("event", name),
		slog.Int("handler_count", handlerCount),
		slog.Duration("duration", duration),
	}
	if err != nil {
		attrs = append(attrs, slog.Any("error", err))
		logger.ErrorContext(ctx, "event dispatch failed", attrs...)
		return
	}
	logger.InfoContext(ctx, "event dispatched", attrs...)
}

func (b *Bus) dispatchLogger(module string) *slog.Logger {
	if b.logger != nil {
		return b.logger.With("module", module)
	}
	return platformlog.For(module)
}

func normalizeName(name string) (string, string, error) {
	normalized := strings.TrimSpace(name)
	module, event, ok := strings.Cut(normalized, ".")
	if !ok || module == "" || event == "" {
		return "", "", fmt.Errorf("bus: event name %q must follow <module>.<PascalCaseEvent>", normalized)
	}
	if err := db.ValidateModule(module); err != nil {
		return "", "", fmt.Errorf("bus: event name %q: %w", normalized, err)
	}
	return normalized, module, nil
}
