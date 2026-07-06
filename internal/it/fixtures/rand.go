package fixtures

import (
	"hash/fnv"
	"math/rand"
	"testing"
)

// Rand returns a deterministic random source scoped to the test name.
func Rand(t testing.TB) *rand.Rand {
	t.Helper()

	seed := randSeed(t.Name())
	t.Logf("fixtures.Rand seed=%d", seed)
	return rand.New(rand.NewSource(seed))
}

func randSeed(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64() & (1<<63 - 1))
}
