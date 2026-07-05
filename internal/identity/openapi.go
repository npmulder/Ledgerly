package identity

import httpserver "github.com/npmulder/ledgerly/internal/platform/http"

func OpenAPIFragment() httpserver.OpenAPIFragment {
	return httpserver.OpenAPIFragment{
		Tags: []map[string]any{
			{"name": "identity"},
		},
		Paths: map[string]any{
			"/api/identity/register": map[string]any{
				"post": map[string]any{
					"tags":        []string{"identity"},
					"summary":     "Register the first owner account",
					"description": "Allowed only while no users exist.",
					"responses": map[string]any{
						"201": map[string]any{"description": "Owner created"},
						"403": map[string]any{"description": "Registration is closed"},
					},
				},
			},
			"/api/identity/login": map[string]any{
				"post": map[string]any{
					"tags":    []string{"identity"},
					"summary": "Open a browser session",
					"responses": map[string]any{
						"200": map[string]any{"description": "Session opened"},
						"401": map[string]any{"description": "Invalid credentials"},
						"429": map[string]any{"description": "Too many login attempts"},
					},
				},
			},
			"/api/identity/logout": map[string]any{
				"post": map[string]any{
					"tags":    []string{"identity"},
					"summary": "Close the current browser session",
					"responses": map[string]any{
						"204": map[string]any{"description": "Session closed"},
						"401": map[string]any{"description": "Authentication required"},
					},
				},
			},
			"/api/identity/me": map[string]any{
				"get": map[string]any{
					"tags":    []string{"identity"},
					"summary": "Return the current user",
					"responses": map[string]any{
						"200": map[string]any{"description": "Authenticated user"},
						"401": map[string]any{"description": "Authentication required"},
					},
				},
			},
		},
	}
}
