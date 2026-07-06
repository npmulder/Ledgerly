package advisor

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// FactProvider is a typed adapter over one module's public read API.
type FactProvider interface {
	Keys() []FactKey
	Gather(context.Context) (map[FactKey]FactValue, error)
}

// RegisteredFactProvider binds a provider implementation to the name used in
// gather diagnostics and logs.
type RegisteredFactProvider struct {
	Name     string
	Provider FactProvider
}

// FactRegistry is assembled at wiring from module-specific providers.
type FactRegistry []RegisteredFactProvider

// NewFactRegistry snapshots the providers supplied by the composition root.
func NewFactRegistry(providers ...RegisteredFactProvider) FactRegistry {
	out := make(FactRegistry, len(providers))
	copy(out, providers)
	return out
}

// GatherAll gathers every registered provider in order, returning all facts
// from successful providers and recording per-provider diagnostics.
func (r FactRegistry) GatherAll(ctx context.Context, logger *slog.Logger) (Facts, GatherReport) {
	return GatherAll(ctx, []RegisteredFactProvider(r), logger)
}

// ProviderGatherResult records one provider's declared keys, duration, and
// optional error. A provider error skips only that provider's facts.
type ProviderGatherResult struct {
	Name     string
	Keys     []FactKey
	Duration time.Duration
	Err      error
}

// GatherReport is the operational trace of one fact-gather pass.
type GatherReport struct {
	Providers []ProviderGatherResult
}

// GatherAll gathers every provider in order. Provider failures are logged and
// recorded but never returned as a top-level error because advisor degradation
// must not block unrelated insights.
func GatherAll(ctx context.Context, providers []RegisteredFactProvider, logger *slog.Logger) (Facts, GatherReport) {
	facts := Facts{}
	report := GatherReport{Providers: make([]ProviderGatherResult, 0, len(providers))}

	for index, registration := range providers {
		name := providerName(index, registration.Name)
		result := ProviderGatherResult{Name: name}
		if registration.Provider != nil {
			result.Keys = sortedFactKeys(registration.Provider.Keys())
		}

		start := time.Now()
		if registration.Provider == nil {
			result.Err = fmt.Errorf("advisor: fact provider is nil")
			result.Duration = time.Since(start)
			logProviderError(logger, result)
			report.Providers = append(report.Providers, result)
			continue
		}

		providerFacts, err := registration.Provider.Gather(ctx)
		result.Duration = time.Since(start)
		if err != nil {
			result.Err = err
			logProviderError(logger, result)
			report.Providers = append(report.Providers, result)
			continue
		}
		if err := duplicateFactKey(providerFacts, facts); err != nil {
			result.Err = err
			logProviderError(logger, result)
			report.Providers = append(report.Providers, result)
			continue
		}

		for key, value := range providerFacts {
			facts[key] = value
		}
		report.Providers = append(report.Providers, result)
	}

	return facts, report
}

func providerName(index int, name string) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	return fmt.Sprintf("provider_%d", index+1)
}

func sortedFactKeys(keys []FactKey) []FactKey {
	out := append([]FactKey(nil), keys...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func duplicateFactKey(next map[FactKey]FactValue, existing Facts) error {
	for key := range next {
		if _, ok := existing[key]; ok {
			return fmt.Errorf("advisor: duplicate fact key %q", key)
		}
	}
	return nil
}

func logProviderError(logger *slog.Logger, result ProviderGatherResult) {
	if logger == nil || result.Err == nil {
		return
	}
	logger.Error("advisor fact provider failed",
		"provider", result.Name,
		"duration", result.Duration,
		"error", result.Err,
	)
}
