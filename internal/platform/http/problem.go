package httpserver

import (
	"encoding/json"
	"errors"
	nethttp "net/http"
)

const (
	// ProblemContentType is the RFC 7807 JSON media type.
	ProblemContentType = "application/problem+json"

	problemTypeInternal           = "https://ledgerly.local/problems/internal-server-error"
	problemTypeServiceUnavailable = "https://ledgerly.local/problems/service-unavailable"
)

// Problem is an RFC 7807 problem-details response body.
type Problem struct {
	Type       string         `json:"type"`
	Title      string         `json:"title"`
	Status     int            `json:"status"`
	Detail     string         `json:"detail,omitempty"`
	Instance   string         `json:"instance,omitempty"`
	Extensions map[string]any `json:"-"`
}

// MarshalJSON emits RFC 7807 standard fields plus extension members.
func (p Problem) MarshalJSON() ([]byte, error) {
	fields := map[string]any{
		"type":   p.Type,
		"title":  p.Title,
		"status": p.Status,
	}

	if p.Detail != "" {
		fields["detail"] = p.Detail
	}
	if p.Instance != "" {
		fields["instance"] = p.Instance
	}

	for key, value := range p.Extensions {
		if isReservedProblemField(key) {
			continue
		}
		fields[key] = value
	}

	return json.Marshal(fields)
}

func isReservedProblemField(key string) bool {
	switch key {
	case "type", "title", "status", "detail", "instance":
		return true
	default:
		return false
	}
}

// ProblemError is implemented by errors that can describe their HTTP problem
// response. Domain modules can return this interface without depending on
// handler internals.
type ProblemError interface {
	error
	ProblemDetail() Problem
}

// DomainError is the reusable typed error shape domain modules can map to
// problem-details JSON.
type DomainError struct {
	problem Problem
	cause   error
}

// NewDomainError creates a typed domain error with an RFC 7807 response shape.
func NewDomainError(status int, problemType, title, detail string) DomainError {
	return WrapDomainError(status, problemType, title, detail, nil)
}

// WrapDomainError creates a typed domain error that unwraps to cause.
func WrapDomainError(status int, problemType, title, detail string, cause error) DomainError {
	problem := normalizeProblem(Problem{
		Type:   problemType,
		Title:  title,
		Status: status,
		Detail: detail,
	})

	return DomainError{
		problem: problem,
		cause:   cause,
	}
}

func (e DomainError) Error() string {
	if e.problem.Detail != "" {
		return e.problem.Detail
	}
	if e.cause != nil {
		return e.cause.Error()
	}
	return e.problem.Title
}

func (e DomainError) Unwrap() error {
	return e.cause
}

func (e DomainError) ProblemDetail() Problem {
	return e.problem
}

// ProblemFromError maps typed domain errors to problem-details responses and
// hides details for untyped errors.
func ProblemFromError(err error) Problem {
	var problemErr ProblemError
	if errors.As(err, &problemErr) {
		return normalizeProblem(problemErr.ProblemDetail())
	}

	return Problem{
		Type:   problemTypeInternal,
		Title:  nethttp.StatusText(nethttp.StatusInternalServerError),
		Status: nethttp.StatusInternalServerError,
		Detail: "internal server error",
	}
}

// WriteError writes err as application/problem+json.
func WriteError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	WriteProblem(w, r, ProblemFromError(err))
}

// WriteProblem writes problem as application/problem+json.
func WriteProblem(w nethttp.ResponseWriter, r *nethttp.Request, problem Problem) {
	problem = normalizeProblem(problem)
	if problem.Instance == "" && r != nil && r.URL != nil {
		problem.Instance = r.URL.Path
	}

	w.Header().Set("Content-Type", ProblemContentType)
	w.WriteHeader(problem.Status)
	_ = json.NewEncoder(w).Encode(problem)
}

func normalizeProblem(problem Problem) Problem {
	if problem.Status < 100 || problem.Status > 599 {
		problem.Status = nethttp.StatusInternalServerError
	}
	if problem.Type == "" {
		problem.Type = "about:blank"
	}
	if problem.Title == "" {
		problem.Title = nethttp.StatusText(problem.Status)
	}
	if problem.Title == "" {
		problem.Title = "HTTP Error"
	}
	return problem
}
