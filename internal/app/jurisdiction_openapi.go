package app

import httpserver "github.com/npmulder/ledgerly/internal/platform/http"

func jurisdictionOpenAPIFragment() httpserver.OpenAPIFragment {
	return httpserver.OpenAPIFragment{
		Tags: []map[string]any{
			{"name": "jurisdiction", "description": "Active jurisdiction rules pack and resolved filing dates"},
		},
		Paths: map[string]any{
			"/api/jurisdiction/pack": map[string]any{
				"get": map[string]any{
					"tags":        []string{"jurisdiction"},
					"summary":     "Return the active jurisdiction rules pack overview",
					"operationId": "jurisdictionGetPack",
					"security":    sessionSecurity(),
					"responses": map[string]any{
						"200": jsonResponseRef("Active jurisdiction pack overview", "JurisdictionPack"),
						"401": problemResponse("Authentication required"),
					},
				},
			},
			"/api/jurisdiction/deadlines": map[string]any{
				"get": map[string]any{
					"tags":        []string{"jurisdiction"},
					"summary":     "Return resolved filing deadlines for the company",
					"operationId": "jurisdictionGetDeadlines",
					"security":    sessionSecurity(),
					"responses": map[string]any{
						"200": jsonResponseRef("Resolved filing deadlines", "JurisdictionFilingDeadlines"),
						"401": problemResponse("Authentication required"),
					},
				},
			},
		},
		Components: jurisdictionComponents(),
	}
}

func jsonResponseRef(description string, schema string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/" + schema},
			},
		},
	}
}

func problemResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			httpserver.ProblemContentType: map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/Problem"},
			},
		},
	}
}

func sessionSecurity() []map[string][]string {
	return []map[string][]string{
		{"sessionCookie": []string{}},
	}
}

func jurisdictionComponents() map[string]any {
	return map[string]any{
		"schemas": map[string]any{
			"JurisdictionPack": map[string]any{
				"type":     "object",
				"required": []string{"meta", "rule_summaries"},
				"properties": map[string]any{
					"meta": map[string]any{"$ref": "#/components/schemas/JurisdictionPackMeta"},
					"rule_summaries": map[string]any{
						"type": "array",
						"items": map[string]any{
							"$ref": "#/components/schemas/JurisdictionRuleSummary",
						},
					},
				},
			},
			"JurisdictionPackMeta": map[string]any{
				"type":     "object",
				"required": []string{"id", "version", "name"},
				"properties": map[string]any{
					"id":      map[string]any{"type": "string"},
					"version": map[string]any{"type": "string"},
					"name":    map[string]any{"type": "string"},
				},
			},
			"JurisdictionRuleSummary": map[string]any{
				"type":     "object",
				"required": []string{"id", "label", "summary"},
				"properties": map[string]any{
					"id":      map[string]any{"type": "string"},
					"label":   map[string]any{"type": "string"},
					"summary": map[string]any{"type": "string"},
				},
			},
			"JurisdictionFilingDeadlines": map[string]any{
				"type":     "object",
				"required": []string{"deadlines"},
				"properties": map[string]any{
					"deadlines": map[string]any{
						"type": "array",
						"items": map[string]any{
							"$ref": "#/components/schemas/JurisdictionFilingDeadline",
						},
					},
				},
			},
			"JurisdictionFilingDeadline": map[string]any{
				"type":     "object",
				"required": []string{"key", "label", "authority", "due_date", "recurrence"},
				"properties": map[string]any{
					"key":        map[string]any{"type": "string"},
					"label":      map[string]any{"type": "string"},
					"authority":  map[string]any{"type": "string"},
					"due_date":   map[string]any{"type": "string", "format": "date"},
					"recurrence": map[string]any{"type": "string"},
				},
			},
		},
	}
}
