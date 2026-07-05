package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
)

type healthResponse struct {
	Status  string                 `json:"status"`
	Version string                 `json:"version"`
	Checks  map[string]healthCheck `json:"checks"`
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
		writeJSON(w, nethttp.StatusOK, healthResponse{
			Status:  "ok",
			Version: cfg.Version,
			Checks:  checks,
		})
	}
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
