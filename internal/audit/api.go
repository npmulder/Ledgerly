package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

// ModuleName is the database schema, HTTP route segment, and event namespace.
const ModuleName = "audit"

const (
	defaultActor        = "system"
	DefaultHistoryLimit = 50
	MaxHistoryLimit     = 100
)

// Change is the before/after value for one changed field.
type Change struct {
	Before any `json:"before"`
	After  any `json:"after"`
}

// Diff is a JSON object keyed by field name.
type Diff map[string]Change

// Entry is one durable audit record.
type Entry struct {
	ID         int64     `json:"id"`
	Module     string    `json:"module"`
	Entity     string    `json:"entity"`
	EntityID   string    `json:"entity_id"`
	Actor      string    `json:"actor"`
	OccurredAt time.Time `json:"occurred_at"`
	Diff       Diff      `json:"diff"`
}

// HistoryFilter scopes audit history to one entity.
type HistoryFilter struct {
	Module   string
	Entity   string
	EntityID string
	Limit    int
}

// ActorFunc derives the actor string for a service-layer write.
type ActorFunc func(context.Context) string

// Recorder writes audit rows inside the caller's transaction.
type Recorder struct {
	store Store
	actor ActorFunc
}

type RecorderOption func(*Recorder)

// WithActor installs the actor extractor used for new audit entries.
func WithActor(actor ActorFunc) RecorderOption {
	return func(r *Recorder) {
		if actor != nil {
			r.actor = actor
		}
	}
}

// NewRecorder returns a transaction-bound audit writer.
func NewRecorder(opts ...RecorderOption) *Recorder {
	recorder := &Recorder{
		store: Store{},
		actor: func(context.Context) string {
			return defaultActor
		},
	}
	for _, opt := range opts {
		opt(recorder)
	}
	return recorder
}

// Record computes a meaningful diff and inserts it when at least one field
// changed. before or after may be nil for create/delete style mutations.
func (r *Recorder) Record(ctx context.Context, tx db.Tx, module, entity, entityID string, before, after any) error {
	if r == nil {
		return nil
	}
	if tx == nil {
		return fmt.Errorf("audit: record requires transaction")
	}
	diff, err := DiffValues(before, after)
	if err != nil {
		return err
	}
	if len(diff) == 0 {
		return nil
	}
	actor := strings.TrimSpace(r.actor(ctx))
	if actor == "" {
		actor = defaultActor
	}
	_, err = r.store.Insert(ctx, tx, NewEntry{
		Module:   module,
		Entity:   entity,
		EntityID: entityID,
		Actor:    actor,
		Diff:     diff,
	})
	return err
}

// DiffValues returns the top-level JSON field changes between two values.
func DiffValues(before, after any) (Diff, error) {
	beforeObject, err := objectFromValue(before)
	if err != nil {
		return nil, fmt.Errorf("audit: marshal before value: %w", err)
	}
	afterObject, err := objectFromValue(after)
	if err != nil {
		return nil, fmt.Errorf("audit: marshal after value: %w", err)
	}

	keys := make(map[string]struct{}, len(beforeObject)+len(afterObject))
	for key := range beforeObject {
		keys[key] = struct{}{}
	}
	for key := range afterObject {
		keys[key] = struct{}{}
	}

	sortedKeys := make([]string, 0, len(keys))
	for key := range keys {
		sortedKeys = append(sortedKeys, key)
	}
	sort.Strings(sortedKeys)

	diff := make(Diff)
	for _, key := range sortedKeys {
		beforeValue, beforeOK := beforeObject[key]
		afterValue, afterOK := afterObject[key]
		if !beforeOK {
			beforeValue = nil
		}
		if !afterOK {
			afterValue = nil
		}
		if reflect.DeepEqual(beforeValue, afterValue) {
			continue
		}
		diff[key] = Change{Before: beforeValue, After: afterValue}
	}
	return diff, nil
}

func objectFromValue(value any) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	if object, ok := value.(map[string]any); ok {
		return object, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	return object, nil
}

func normalizeHistoryLimit(limit int) int {
	if limit <= 0 {
		return DefaultHistoryLimit
	}
	if limit > MaxHistoryLimit {
		return MaxHistoryLimit
	}
	return limit
}
