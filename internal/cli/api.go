package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/npmulder/ledgerly/internal/cli/gen"
)

type apiClient struct {
	client *gen.ClientWithResponses
}

func newAPIClient(baseURL, token string, httpClient *http.Client) (*apiClient, error) {
	url := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if url == "" {
		return nil, fmt.Errorf("url is required")
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("token is required")
	}

	options := []gen.ClientOption{
		gen.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
			return nil
		}),
	}
	if httpClient != nil {
		options = append(options, gen.WithHTTPClient(httpClient))
	}
	client, err := gen.NewClientWithResponses(url, options...)
	if err != nil {
		return nil, err
	}
	return &apiClient{client: client}, nil
}

func newConfiguredAPIClient(runtime *Runtime) (*apiClient, error) {
	cfg, err := loadConfig(runtime.configPath)
	if err != nil {
		return nil, err
	}
	return newAPIClient(cfg.URL, cfg.Token, runtime.httpClient)
}

func (c *apiClient) currentUser(ctx context.Context, jsonOutput bool) (*gen.IdentityUser, error) {
	response, err := c.client.IdentityCurrentUserWithResponse(ctx)
	if err != nil {
		return nil, newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 != nil {
		return response.JSON200, nil
	}
	if err := responseProblem(response.StatusCode(), response.Status(), response.Body, jsonOutput, response.ApplicationproblemJSON401); err != nil {
		return nil, err
	}
	return nil, newDomainError(fmt.Sprintf("unexpected response from Ledgerly API: %s", response.Status()))
}

func responseProblem(statusCode int, status string, body []byte, jsonOutput bool, problems ...*gen.Problem) error {
	if statusCode < 400 {
		return nil
	}
	for _, problem := range problems {
		if problem == nil {
			continue
		}
		return &problemError{
			code:    problemExitCode(statusCode),
			problem: *problem,
			raw:     body,
			json:    jsonOutput,
		}
	}
	var problem gen.Problem
	if len(body) > 0 && json.Unmarshal(body, &problem) == nil && strings.TrimSpace(problem.Title) != "" {
		if problem.Status == 0 {
			problem.Status = int32(statusCode)
		}
		return &problemError{
			code:    problemExitCode(statusCode),
			problem: problem,
			raw:     body,
			json:    jsonOutput,
		}
	}
	if strings.TrimSpace(status) == "" {
		status = fmt.Sprintf("HTTP %d", statusCode)
	}
	return newDomainError(fmt.Sprintf("unexpected response from Ledgerly API: %s", status))
}

func unexpectedAPIResponse(status string) error {
	if strings.TrimSpace(status) == "" {
		status = "unknown status"
	}
	return newDomainError(fmt.Sprintf("unexpected response from Ledgerly API: %s", status))
}
