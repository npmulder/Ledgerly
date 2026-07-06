package fixtures

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/npmulder/ledgerly/internal/it/harness"
)

// ErrAlreadySeeded reports an accidental duplicate fixture seed in one harness.
var ErrAlreadySeeded = errors.New("fixtures: already seeded")

type alreadySeededError struct {
	name string
}

func (e alreadySeededError) Error() string {
	return fmt.Sprintf("fixtures: %s already seeded", e.name)
}

func (e alreadySeededError) Unwrap() error {
	return ErrAlreadySeeded
}

var seedGuards = struct {
	sync.Mutex
	byHarness map[*harness.Harness]map[string]struct{}
}{
	byHarness: make(map[*harness.Harness]map[string]struct{}),
}

func claimSeed(t testing.TB, h *harness.Harness, name string) (func(success bool), error) {
	t.Helper()

	if h == nil {
		return nil, errors.New("fixtures: nil harness")
	}

	seedGuards.Lock()
	defer seedGuards.Unlock()

	claimed, ok := seedGuards.byHarness[h]
	if !ok {
		claimed = make(map[string]struct{})
		seedGuards.byHarness[h] = claimed
		t.Cleanup(func() {
			seedGuards.Lock()
			defer seedGuards.Unlock()
			delete(seedGuards.byHarness, h)
		})
	}
	if _, exists := claimed[name]; exists {
		return nil, alreadySeededError{name: name}
	}
	claimed[name] = struct{}{}

	return func(success bool) {
		if success {
			return
		}
		seedGuards.Lock()
		defer seedGuards.Unlock()
		if claimed := seedGuards.byHarness[h]; claimed != nil {
			delete(claimed, name)
		}
	}, nil
}
