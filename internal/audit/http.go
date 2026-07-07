package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const problemTypeAuditBadRequest = "https://ledgerly.local/problems/audit/bad-request"

type auditHandler struct {
	service *Service
}

// HTTPModule returns the platform route mount for audit.
func HTTPModule(service *Service) httpserver.Module {
	handler := auditHandler{service: service}
	return httpserver.Module{
		Name:           ModuleName,
		RegisterRoutes: handler.registerRoutes,
	}
}

func (h auditHandler) registerRoutes(r chi.Router) {
	r.Get("/history/{module}/{entity}/{entity_id}", h.history)
}

type historyResponse struct {
	Entries []Entry `json:"entries"`
}

func (h auditHandler) history(w nethttp.ResponseWriter, r *nethttp.Request) {
	limit, err := parseLimit(r)
	if err != nil {
		writeAuditBadRequest(w, r, err)
		return
	}
	entries, err := h.service.History(r.Context(), HistoryFilter{
		Module:   chi.URLParam(r, "module"),
		Entity:   chi.URLParam(r, "entity"),
		EntityID: chi.URLParam(r, "entity_id"),
		Limit:    limit,
	})
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	writeAuditJSON(w, nethttp.StatusOK, historyResponse{Entries: entries})
}

func parseLimit(r *nethttp.Request) (int, error) {
	value := strings.TrimSpace(r.URL.Query().Get("limit"))
	if value == "" {
		return DefaultHistoryLimit, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return 0, fmt.Errorf("limit must be a positive integer")
	}
	return normalizeHistoryLimit(limit), nil
}

func writeAuditBadRequest(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeAuditBadRequest,
		Title:  nethttp.StatusText(nethttp.StatusBadRequest),
		Status: nethttp.StatusBadRequest,
		Detail: err.Error(),
	})
}

func writeAuditJSON(w nethttp.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil && !errors.Is(err, nethttp.ErrHandlerTimeout) {
		panic(err)
	}
}
