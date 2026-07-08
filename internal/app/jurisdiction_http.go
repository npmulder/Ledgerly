package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const jurisdictionModuleName = "jurisdiction"

const jurisdictionProblemTypeNotFound = "https://ledgerly.local/problems/not-found"

type jurisdictionCompanyFactsFunc func(context.Context) (jurisdiction.CompanyFacts, error)

type jurisdictionHTTPHandler struct {
	companyFacts jurisdictionCompanyFactsFunc
	clock        jurisdiction.Clock
}

func jurisdictionHTTPModule(companyFacts jurisdictionCompanyFactsFunc, clock jurisdiction.Clock) httpserver.Module {
	handler := jurisdictionHTTPHandler{
		companyFacts: companyFacts,
		clock:        clock,
	}
	return httpserver.Module{
		Name:           jurisdictionModuleName,
		RegisterRoutes: handler.registerRoutes,
	}
}

func (h jurisdictionHTTPHandler) registerRoutes(r chi.Router) {
	r.Get("/pack", h.getPack)
	r.Get("/deadlines", h.getDeadlines)
}

type jurisdictionPackResponse struct {
	Meta          jurisdictionPackMetaResponse      `json:"meta"`
	CompanyActs   []jurisdictionCompanyActResponse  `json:"company_acts"`
	RuleSummaries []jurisdictionRuleSummaryResponse `json:"rule_summaries"`
}

type jurisdictionPackMetaResponse struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Name    string `json:"name"`
}

type jurisdictionRuleSummaryResponse struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Summary string `json:"summary"`
}

type jurisdictionCompanyActResponse struct {
	ActType               string   `json:"act_type"`
	Label                 string   `json:"label"`
	MinimumDirectors      int      `json:"minimum_directors"`
	CorporateDirectors    *bool    `json:"corporate_directors"`
	CompanyNumberSuffixes []string `json:"company_number_suffixes"`
}

type jurisdictionFilingDeadlinesResponse struct {
	Deadlines []jurisdictionFilingDeadlineResponse `json:"deadlines"`
}

type jurisdictionFilingDeadlineResponse struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Authority  string `json:"authority"`
	DueDate    string `json:"due_date"`
	Recurrence string `json:"recurrence"`
}

func (h jurisdictionHTTPHandler) getPack(w nethttp.ResponseWriter, r *nethttp.Request) {
	overview, err := jurisdiction.ActivePackOverview()
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, jurisdictionPackOverviewToResponse(overview))
}

func (h jurisdictionHTTPHandler) getDeadlines(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.companyFacts == nil {
		httpserver.WriteError(w, r, fmt.Errorf("jurisdiction: company facts provider is required"))
		return
	}

	facts, err := h.companyFacts(r.Context())
	if err != nil {
		if errors.Is(err, identity.ErrProfileNotFound) {
			httpserver.WriteProblem(w, r, httpserver.Problem{
				Type:   jurisdictionProblemTypeNotFound,
				Title:  nethttp.StatusText(nethttp.StatusNotFound),
				Status: nethttp.StatusNotFound,
				Detail: "company profile was not found",
			})
			return
		}
		httpserver.WriteError(w, r, err)
		return
	}
	deadlines, err := jurisdiction.FilingDeadlinesWithClock(facts, h.clock)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, jurisdictionFilingDeadlinesToResponse(deadlines))
}

func jurisdictionPackOverviewToResponse(overview jurisdiction.PackOverview) jurisdictionPackResponse {
	response := jurisdictionPackResponse{
		Meta: jurisdictionPackMetaResponse{
			ID:      overview.Meta.ID,
			Version: overview.Meta.Version,
			Name:    overview.Meta.Name,
		},
		CompanyActs:   make([]jurisdictionCompanyActResponse, 0, len(overview.CompanyActs)),
		RuleSummaries: make([]jurisdictionRuleSummaryResponse, 0, len(overview.RuleSummaries)),
	}
	for _, act := range overview.CompanyActs {
		response.CompanyActs = append(response.CompanyActs, jurisdictionCompanyActResponse{
			ActType:               act.ActType,
			Label:                 act.Label,
			MinimumDirectors:      act.MinimumDirectors,
			CorporateDirectors:    cloneBoolPointer(act.CorporateDirectors),
			CompanyNumberSuffixes: append([]string{}, act.CompanyNumberSuffixes...),
		})
	}
	for _, summary := range overview.RuleSummaries {
		response.RuleSummaries = append(response.RuleSummaries, jurisdictionRuleSummaryResponse{
			ID:      summary.ID,
			Label:   summary.Label,
			Summary: summary.Summary,
		})
	}
	return response
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func jurisdictionFilingDeadlinesToResponse(deadlines []jurisdiction.Deadline) jurisdictionFilingDeadlinesResponse {
	response := jurisdictionFilingDeadlinesResponse{
		Deadlines: make([]jurisdictionFilingDeadlineResponse, 0, len(deadlines)),
	}
	for _, deadline := range deadlines {
		response.Deadlines = append(response.Deadlines, jurisdictionFilingDeadlineResponse{
			Key:        deadline.Key,
			Label:      deadline.Label,
			Authority:  deadline.Authority,
			DueDate:    deadline.DueDate.UTC().Format(time.DateOnly),
			Recurrence: deadline.Recurrence,
		})
	}
	return response
}

func writeJSON(w nethttp.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		panic(err)
	}
}
