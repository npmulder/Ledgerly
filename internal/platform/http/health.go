package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"
	"time"
)

type healthResponse struct {
	Status    string                 `json:"status"`
	Version   string                 `json:"version"`
	CheckedAt string                 `json:"checked_at"`
	Checks    map[string]healthCheck `json:"checks"`
}

type healthCheck struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func healthHandler(cfg Config) nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		checks := map[string]healthCheck{}
		dbErr := pingDB(r.Context(), cfg)
		if dbErr != nil {
			checks["db"] = healthCheck{Status: "down", Error: dbErr.Error()}
			WriteProblem(w, r, Problem{
				Type:   problemTypeServiceUnavailable,
				Title:  nethttp.StatusText(nethttp.StatusServiceUnavailable),
				Status: nethttp.StatusServiceUnavailable,
				Detail: "database ping failed",
				Extensions: map[string]any{
					"version": cfg.Version,
					"checks":  checks,
				},
			})
			return
		}

		checks["db"] = healthCheck{Status: "ok"}
		if detail := runAdditionalHealthChecks(r.Context(), cfg, checks); detail != "" {
			WriteProblem(w, r, Problem{
				Type:   problemTypeServiceUnavailable,
				Title:  nethttp.StatusText(nethttp.StatusServiceUnavailable),
				Status: nethttp.StatusServiceUnavailable,
				Detail: detail,
				Extensions: map[string]any{
					"version": cfg.Version,
					"checks":  checks,
				},
			})
			return
		}

		writeJSON(w, nethttp.StatusOK, healthResponse{
			Status:    "ok",
			Version:   cfg.Version,
			CheckedAt: cfg.Clock.Now().UTC().Format(time.RFC3339Nano),
			Checks:    checks,
		})
	}
}

func runAdditionalHealthChecks(ctx context.Context, cfg Config, checks map[string]healthCheck) string {
	var detail string
	for _, check := range cfg.HealthChecks {
		name := strings.TrimSpace(check.Name)
		if name == "" || check.Check == nil {
			continue
		}
		if err := check.Check(ctx); err != nil {
			checks[name] = healthCheck{Status: "down", Error: err.Error()}
			if detail == "" {
				detail = fmt.Sprintf("%s failed", name)
			}
			continue
		}
		checks[name] = healthCheck{Status: "ok"}
	}
	return detail
}

func pingDB(ctx context.Context, cfg Config) error {
	if cfg.DB == nil {
		return errors.New("database is not configured")
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.HealthTimeout)
	defer cancel()

	return cfg.DB.PingContext(ctx)
}

func writeJSON(w nethttp.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		panic(err)
	}
}
