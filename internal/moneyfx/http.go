package moneyfx

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
	problemTypeInvalidRateRequest = "https://ledgerly.local/problems/moneyfx/invalid-rate-request"
	problemTypeNoRate             = "https://ledgerly.local/problems/moneyfx/no-rate"
)

type rateHandler struct {
	service *Service
}

type rateResponse struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Rate      string    `json:"rate"`
	RateDate  string    `json:"rate_date"`
	Source    string    `json:"source"`
	FetchedAt time.Time `json:"fetched_at"`
}

type badRateRequestError struct {
	detail string
}

func (e badRateRequestError) Error() string {
	return e.detail
}

// RegisterRoutes mounts moneyfx REST endpoints.
func (m *Module) RegisterRoutes(r chi.Router) {
	h := rateHandler{service: m.service}
	r.Get("/rates/today", h.todayRate)
	r.Get("/rates", h.rateOn)
}

func (h rateHandler) todayRate(w nethttp.ResponseWriter, r *nethttp.Request) {
	from, to, err := parseCurrencyQuery(r)
	if err != nil {
		writeRateError(w, r, err)
		return
	}

	rate, fetchedAt, err := h.service.TodayRate(r.Context(), from, to)
	if err != nil {
		writeRateError(w, r, err)
		return
	}
	writeRateJSON(w, nethttp.StatusOK, newRateResponse(rate, fetchedAt))
}

func (h rateHandler) rateOn(w nethttp.ResponseWriter, r *nethttp.Request) {
	from, to, err := parseCurrencyQuery(r)
	if err != nil {
		writeRateError(w, r, err)
		return
	}
	date, err := parseRateDate(r)
	if err != nil {
		writeRateError(w, r, err)
		return
	}

	rate, err := h.service.RateOn(r.Context(), date, from, to)
	if err != nil {
		writeRateError(w, r, err)
		return
	}
	writeRateJSON(w, nethttp.StatusOK, newRateResponse(rate, h.service.nowUTC()))
}

func parseCurrencyQuery(r *nethttp.Request) (string, string, error) {
	from := strings.TrimSpace(r.URL.Query().Get("from"))
	to := strings.TrimSpace(r.URL.Query().Get("to"))
	if from == "" {
		return "", "", badRateRequestError{detail: "from is required"}
	}
	if to == "" {
		return "", "", badRateRequestError{detail: "to is required"}
	}
	normalizedFrom, normalizedTo, err := normalizeRatePair(from, to)
	if err != nil {
		return "", "", badRateRequestError{detail: err.Error()}
	}
	return normalizedFrom, normalizedTo, nil
}

func parseRateDate(r *nethttp.Request) (time.Time, error) {
	value := strings.TrimSpace(r.URL.Query().Get("date"))
	if value == "" {
		return time.Time{}, badRateRequestError{detail: "date is required"}
	}
	date, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return time.Time{}, badRateRequestError{detail: fmt.Sprintf("date must use %s", time.DateOnly)}
	}
	return date, nil
}

func newRateResponse(rate Rate, fetchedAt time.Time) rateResponse {
	return rateResponse{
		From:      rate.From,
		To:        rate.To,
		Rate:      rate.Value,
		RateDate:  normalizeRateDate(rate.RateDate).Format(time.DateOnly),
		Source:    rate.Source,
		FetchedAt: fetchedAt.UTC(),
	}
}

func writeRateError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	var badRequest badRateRequestError
	if errors.As(err, &badRequest) {
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeInvalidRateRequest,
			Title:  nethttp.StatusText(nethttp.StatusBadRequest),
			Status: nethttp.StatusBadRequest,
			Detail: badRequest.Error(),
		})
		return
	}
	if errors.Is(err, ErrNoRate) {
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeNoRate,
			Title:  nethttp.StatusText(nethttp.StatusNotFound),
			Status: nethttp.StatusNotFound,
			Detail: "no ECB rate is available for the requested pair and date",
		})
		return
	}
	httpserver.WriteError(w, r, err)
}

func writeRateJSON(w nethttp.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		panic(err)
	}
}
