package advisor

import (
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const (
	problemTypeAdvisorBadRequest = "https://ledgerly.local/problems/advisor/bad-request"
	problemTypeAdvisorNotFound   = "https://ledgerly.local/problems/advisor/not-found"
)

type advisorHandler struct {
	service *Service
}

type insightsResponse struct {
	Insights []insightResponse `json:"insights"`
}

type insightResponse struct {
	Key          string         `json:"key"`
	RuleID       string         `json:"rule_id"`
	Severity     Severity       `json:"severity"`
	Surfaces     []Surface      `json:"surfaces"`
	RenderedText string         `json:"rendered_text"`
	Bindings     map[string]any `json:"bindings"`
	CTA          CTA            `json:"cta"`
	CreatedAt    string         `json:"created_at"`
}

type refreshResponse struct {
	Run evaluationRunResponse `json:"run"`
}

type evaluationRunResponse struct {
	ID                 int64             `json:"id"`
	Trigger            string            `json:"trigger"`
	StartedAt          string            `json:"started_at"`
	FinishedAt         string            `json:"finished_at"`
	DurationMS         int64             `json:"duration_ms"`
	InsightsCreated    int               `json:"insights_created"`
	InsightsSuperseded int               `json:"insights_superseded"`
	InsightsResolved   int               `json:"insights_resolved"`
	Error              string            `json:"error,omitempty"`
	Warnings           []warningResponse `json:"warnings"`
}

type warningResponse struct {
	RuleID  string `json:"rule_id"`
	Message string `json:"message"`
}

// RegisterRoutes mounts advisor REST endpoints.
func (m *Module) RegisterRoutes(r chi.Router) {
	h := advisorHandler{service: m.service}
	r.Get("/insights", h.listInsights)
	r.Post("/insights/{key}/dismiss", h.dismissInsight)
	r.Post("/refresh", h.refresh)
}

func (h advisorHandler) listInsights(w nethttp.ResponseWriter, r *nethttp.Request) {
	surface, err := parseSurfaceQuery(r)
	if err != nil {
		writeAdvisorBadRequest(w, r, err)
		return
	}
	if h.service == nil {
		httpserver.WriteError(w, r, fmt.Errorf("advisor: service is required"))
		return
	}

	insights, err := h.service.InsightsFor(r.Context(), surface)
	if err != nil {
		writeAdvisorError(w, r, err)
		return
	}
	response := insightsResponse{Insights: make([]insightResponse, 0, len(insights))}
	for _, insight := range insights {
		response.Insights = append(response.Insights, insightToResponse(insight))
	}
	writeAdvisorJSON(w, nethttp.StatusOK, response)
}

func (h advisorHandler) dismissInsight(w nethttp.ResponseWriter, r *nethttp.Request) {
	key := InsightKey(strings.TrimSpace(chi.URLParam(r, "key")))
	if key == "" {
		writeAdvisorBadRequest(w, r, fmt.Errorf("insight key is required"))
		return
	}
	if h.service == nil {
		httpserver.WriteError(w, r, fmt.Errorf("advisor: service is required"))
		return
	}
	if err := h.service.Dismiss(r.Context(), key); err != nil {
		writeAdvisorError(w, r, err)
		return
	}
	w.WriteHeader(nethttp.StatusNoContent)
}

func (h advisorHandler) refresh(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.service == nil {
		httpserver.WriteError(w, r, fmt.Errorf("advisor: service is required"))
		return
	}
	run, err := h.service.RefreshNow(r.Context())
	if err != nil {
		writeAdvisorError(w, r, err)
		return
	}
	writeAdvisorJSON(w, nethttp.StatusOK, refreshResponse{
		Run: evaluationRunToResponse(run),
	})
}

func parseSurfaceQuery(r *nethttp.Request) (Surface, error) {
	value := strings.TrimSpace(r.URL.Query().Get("surface"))
	if value == "" {
		return "", nil
	}
	surface, err := normalizeSurface(value)
	if err != nil {
		return "", err
	}
	return surface, nil
}

func insightToResponse(insight Insight) insightResponse {
	return insightResponse{
		Key:          string(insight.Key),
		RuleID:       insight.RuleID,
		Severity:     insight.Severity,
		Surfaces:     append([]Surface(nil), insight.Surfaces...),
		RenderedText: insight.RenderedText,
		Bindings:     bindingsForResponse(insight.Bindings),
		CTA: CTA{
			Label:  insight.CTA.Label,
			Action: insight.CTA.Action,
			Params: cloneAnyMap(insight.CTA.Params),
		},
		CreatedAt: formatAdvisorTime(insight.CreatedAt),
	}
}

func bindingsForResponse(bindings map[string]any) map[string]any {
	out := cloneAnyMap(bindings)
	if out == nil {
		return map[string]any{}
	}
	return out
}

func evaluationRunToResponse(run EvaluationRun) evaluationRunResponse {
	warnings := make([]warningResponse, 0, len(run.Warnings))
	for _, warning := range run.Warnings {
		warnings = append(warnings, warningResponse{
			RuleID:  warning.RuleID,
			Message: warning.Message,
		})
	}
	return evaluationRunResponse{
		ID:                 run.ID,
		Trigger:            run.Trigger,
		StartedAt:          formatAdvisorTime(run.StartedAt),
		FinishedAt:         formatAdvisorTime(run.FinishedAt),
		DurationMS:         run.Duration.Milliseconds(),
		InsightsCreated:    run.InsightsCreated,
		InsightsSuperseded: run.InsightsSuperseded,
		InsightsResolved:   run.InsightsResolved,
		Error:              run.Error,
		Warnings:           warnings,
	}
}

func formatAdvisorTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339)
}

func writeAdvisorBadRequest(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeAdvisorBadRequest,
		Title:  "Invalid advisor request",
		Status: nethttp.StatusBadRequest,
		Detail: err.Error(),
	})
}

func writeAdvisorError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	if errors.Is(err, ErrInsightNotFound) {
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeAdvisorNotFound,
			Title:  "Advisor insight not found",
			Status: nethttp.StatusNotFound,
			Detail: "advisor insight was not found",
		})
		return
	}
	httpserver.WriteError(w, r, err)
}

func writeAdvisorJSON(w nethttp.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
