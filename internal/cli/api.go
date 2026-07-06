package cli

import (
	"context"
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

func (c *apiClient) currentUser(ctx context.Context, jsonOutput bool) (*gen.IdentityUser, error) {
	response, err := c.client.IdentityCurrentUserWithResponse(ctx)
	if err != nil {
		return nil, newDomainError(fmt.Sprintf("unable to reach Ledgerly API: %v", err))
	}
	if response.JSON200 != nil {
		return response.JSON200, nil
	}
	if response.ApplicationproblemJSON401 != nil {
		return nil, &problemError{
			code:    problemExitCode(response.StatusCode()),
			problem: *response.ApplicationproblemJSON401,
			raw:     response.Body,
			json:    jsonOutput,
		}
	}
	return nil, newDomainError(fmt.Sprintf("unexpected response from Ledgerly API: %s", response.Status()))
}
