// Package clock provides deterministic time abstractions for application code and tests.
package clock

import (
	"sync"
	"time"
)

// Clock is the time source used by application services.
type Clock interface {
	Now() time.Time
}

// RealClock reads from the system clock.
type RealClock struct{}

// New returns a real system clock.
func New() RealClock {
	return RealClock{}
}

// Now returns the current wall-clock time.
func (RealClock) Now() time.Time {
	return time.Now()
}

// FakeClock is a manually controlled clock for deterministic tests.
type FakeClock struct {
	mu  sync.RWMutex
	now time.Time
}

// NewFake returns a fake clock initialized to start.
func NewFake(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

// Now returns the fake clock's current time.
func (c *FakeClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.now
}

// Set updates the fake clock's current time.
func (c *FakeClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

// Advance moves the fake clock forward by d and returns the resulting time.
func (c *FakeClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
	return c.now
}
