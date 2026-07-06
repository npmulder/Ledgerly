package advisor

import httpserver "github.com/npmulder/ledgerly/internal/platform/http"

// OpenAPIFragment returns the advisor module's OpenAPI contribution.
func OpenAPIFragment() httpserver.OpenAPIFragment {
	return httpserver.OpenAPIFragment{
		Tags: []map[string]any{
			{
				"name":        "advisor",
				"description": "Advisor insight read, dismiss, and manual refresh endpoints.",
			},
		},
		Paths: map[string]any{
			"/api/advisor/insights": map[string]any{
				"get": map[string]any{
					"tags":        []string{"advisor"},
					"summary":     "List active advisor insights",
					"description": "Returns active, undismissed advisor insights for an optional surface, ordered amber first, then teal, then newest first.",
					"operationId": "advisorListInsights",
					"security":    advisorSessionSecurity(),
					"parameters": []map[string]any{
						{
							"name":        "surface",
							"in":          "query",
							"required":    false,
							"description": "Advisor display surface.",
							"schema":      advisorSurfaceSchema(),
						},
					},
					"responses": map[string]any{
						"200": advisorJSONResponseRef("Active advisor insights", "AdvisorInsightsResponse"),
						"400": advisorProblemResponse("Invalid advisor query"),
						"401": advisorProblemResponse("Authentication required"),
					},
				},
			},
			"/api/advisor/insights/{key}/dismiss": map[string]any{
				"post": map[string]any{
					"tags":        []string{"advisor"},
					"summary":     "Dismiss an advisor insight",
					"description": "Suppresses an active advisor insight until its underlying facts change and produce a different key.",
					"operationId": "advisorDismissInsight",
					"security":    advisorSessionSecurity(),
					"parameters": []map[string]any{
						{
							"name":        "key",
							"in":          "path",
							"required":    true,
							"description": "Advisor insight key.",
							"schema":      map[string]any{"type": "string"},
						},
					},
					"responses": map[string]any{
						"204": map[string]any{"description": "Insight dismissed"},
						"400": advisorProblemResponse("Invalid advisor dismiss request"),
						"401": advisorProblemResponse("Authentication required"),
						"404": advisorProblemResponse("Insight was not found"),
					},
				},
			},
			"/api/advisor/refresh": map[string]any{
				"post": map[string]any{
					"tags":        []string{"advisor"},
					"summary":     "Refresh advisor insights now",
					"description": "Runs the advisor RefreshNow evaluator synchronously and returns the recorded evaluation run.",
					"operationId": "advisorRefresh",
					"security":    advisorSessionSecurity(),
					"responses": map[string]any{
						"200": advisorJSONResponseRef("Advisor evaluation run", "AdvisorRefreshResponse"),
						"401": advisorProblemResponse("Authentication required"),
					},
				},
			},
		},
		Components: advisorComponents(),
	}
}

func advisorSessionSecurity() []map[string][]string {
	return []map[string][]string{
		{"sessionCookie": []string{}},
	}
}

func advisorJSONResponseRef(description string, schema string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/" + schema},
			},
		},
	}
}

func advisorProblemResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			httpserver.ProblemContentType: map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/Problem"},
			},
		},
	}
}

func advisorComponents() map[string]any {
	return map[string]any{
		"schemas": map[string]any{
			"AdvisorSeverity": map[string]any{
				"type": "string",
				"enum": []string{string(SeverityAmber), string(SeverityTeal)},
			},
			"AdvisorSurface": advisorSurfaceSchema(),
			"AdvisorCTA": map[string]any{
				"type":     "object",
				"required": []string{"label", "action"},
				"properties": map[string]any{
					"label":  map[string]any{"type": "string"},
					"action": map[string]any{"type": "string"},
					"params": map[string]any{
						"type":                 "object",
						"additionalProperties": true,
					},
				},
				"additionalProperties": false,
			},
			"AdvisorInsight": map[string]any{
				"type": "object",
				"required": []string{
					"key",
					"rule_id",
					"severity",
					"surfaces",
					"rendered_text",
					"bindings",
					"cta",
					"created_at",
				},
				"properties": map[string]any{
					"key":           map[string]any{"type": "string"},
					"rule_id":       map[string]any{"type": "string"},
					"severity":      map[string]any{"$ref": "#/components/schemas/AdvisorSeverity"},
					"surfaces":      map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/AdvisorSurface"}},
					"rendered_text": map[string]any{"type": "string"},
					"bindings": map[string]any{
						"type":                 "object",
						"additionalProperties": true,
					},
					"cta":        map[string]any{"$ref": "#/components/schemas/AdvisorCTA"},
					"created_at": map[string]any{"type": "string", "format": "date-time"},
				},
				"additionalProperties": false,
			},
			"AdvisorInsightsResponse": map[string]any{
				"type":     "object",
				"required": []string{"insights"},
				"properties": map[string]any{
					"insights": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/AdvisorInsight"},
					},
				},
				"additionalProperties": false,
			},
			"AdvisorWarning": map[string]any{
				"type":     "object",
				"required": []string{"rule_id", "message"},
				"properties": map[string]any{
					"rule_id": map[string]any{"type": "string"},
					"message": map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"AdvisorEvaluationRun": map[string]any{
				"type": "object",
				"required": []string{
					"id",
					"trigger",
					"started_at",
					"finished_at",
					"duration_ms",
					"insights_created",
					"insights_superseded",
					"insights_resolved",
					"warnings",
				},
				"properties": map[string]any{
					"id":                  map[string]any{"type": "integer", "format": "int64"},
					"trigger":             map[string]any{"type": "string"},
					"started_at":          map[string]any{"type": "string", "format": "date-time"},
					"finished_at":         map[string]any{"type": "string", "format": "date-time"},
					"duration_ms":         map[string]any{"type": "integer", "format": "int64"},
					"insights_created":    map[string]any{"type": "integer"},
					"insights_superseded": map[string]any{"type": "integer"},
					"insights_resolved":   map[string]any{"type": "integer"},
					"error":               map[string]any{"type": "string"},
					"warnings":            map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/AdvisorWarning"}},
				},
				"additionalProperties": false,
			},
			"AdvisorRefreshResponse": map[string]any{
				"type":     "object",
				"required": []string{"run"},
				"properties": map[string]any{
					"run": map[string]any{"$ref": "#/components/schemas/AdvisorEvaluationRun"},
				},
				"additionalProperties": false,
			},
		},
	}
}

func advisorSurfaceSchema() map[string]any {
	return map[string]any{
		"type": "string",
		"enum": []string{
			string(SurfaceDashboard),
			string(SurfaceInvoices),
			string(SurfaceBanking),
			string(SurfaceDLA),
			string(SurfaceDividends),
			string(SurfaceReports),
		},
	}
}
