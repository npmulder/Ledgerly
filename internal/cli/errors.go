package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/npmulder/ledgerly/internal/cli/gen"
)

const (
	ExitOK     = 0
	ExitDomain = 1
	ExitUsage  = 2
	ExitAuth   = 3
)

type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string {
	return e.Message
}

func (e *Error) ExitCode() int {
	if e.Code == 0 {
		return ExitDomain
	}
	return e.Code
}

type problemError struct {
	code    int
	problem gen.Problem
	raw     []byte
	json    bool
}

func (e *problemError) Error() string {
	if e.json && len(e.raw) > 0 && json.Valid(e.raw) {
		return string(e.raw)
	}
	return renderProblem(e.problem)
}

func (e *problemError) ExitCode() int {
	if e.code == 0 {
		return ExitDomain
	}
	return e.code
}

func newAuthError(message string) *Error {
	return &Error{Code: ExitAuth, Message: message}
}

func newDomainError(message string) *Error {
	return &Error{Code: ExitDomain, Message: message}
}

func newUsageError(message string) *Error {
	return &Error{Code: ExitUsage, Message: message}
}

func problemExitCode(status int) int {
	if status == 401 {
		return ExitAuth
	}
	return ExitDomain
}

func renderProblem(problem gen.Problem) string {
	title := strings.TrimSpace(problem.Title)
	detail := ""
	if problem.Detail != nil {
		detail = strings.TrimSpace(*problem.Detail)
	}
	if title == "" {
		title = fmt.Sprintf("HTTP %d", problem.Status)
	}
	if detail == "" {
		return title
	}
	return title + " — " + detail
}
