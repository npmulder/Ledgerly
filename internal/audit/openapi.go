package audit

import httpserver "github.com/npmulder/ledgerly/internal/platform/http"

// OpenAPIFragment returns the audit module's OpenAPI contribution.
func OpenAPIFragment() httpserver.OpenAPIFragment {
	return httpserver.OpenAPIFragment{
		Tags: []map[string]any{
			{
				"name":        "audit",
				"description": "Scoped mutation history for non-ledger entities.",
			},
		},
		Paths: map[string]any{
			"/api/audit/history/{module}/{entity}/{entity_id}": map[string]any{
				"get": map[string]any{
					"tags":        []string{"audit"},
					"summary":     "Read entity audit history",
					"description": "Returns newest-first mutation history for one non-ledger entity.",
					"operationId": "auditGetEntityHistory",
					"security":    auditSessionSecurity(),
					"parameters": []map[string]any{
						auditPathParameter("module", "Owning module name."),
						auditPathParameter("entity", "Entity kind."),
						auditPathParameter("entity_id", "Entity identifier."),
						{
							"name":        "limit",
							"in":          "query",
							"description": "Maximum history rows.",
							"required":    false,
							"schema": map[string]any{
								"type":    "integer",
								"minimum": 1,
								"maximum": MaxHistoryLimit,
								"default": DefaultHistoryLimit,
							},
						},
					},
					"responses": map[string]any{
						"200": auditJSONResponseRef("Entity audit history", "AuditHistoryResponse"),
						"400": auditProblemResponse("Invalid audit history query"),
						"401": auditProblemResponse("Authentication required"),
					},
				},
			},
		},
		Components: map[string]any{
			"schemas": map[string]any{
				"AuditChange": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"before", "after"},
					"properties": map[string]any{
						"before": map[string]any{"nullable": true},
						"after":  map[string]any{"nullable": true},
					},
				},
				"AuditEntry": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []string{
						"id",
						"module",
						"entity",
						"entity_id",
						"actor",
						"occurred_at",
						"diff",
					},
					"properties": map[string]any{
						"id":          map[string]any{"type": "integer", "format": "int64"},
						"module":      map[string]any{"type": "string"},
						"entity":      map[string]any{"type": "string"},
						"entity_id":   map[string]any{"type": "string"},
						"actor":       map[string]any{"type": "string"},
						"occurred_at": map[string]any{"type": "string", "format": "date-time"},
						"diff": map[string]any{
							"type": "object",
							"additionalProperties": map[string]any{
								"$ref": "#/components/schemas/AuditChange",
							},
						},
					},
				},
				"AuditHistoryResponse": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"entries"},
					"properties": map[string]any{
						"entries": map[string]any{
							"type": "array",
							"items": map[string]any{
								"$ref": "#/components/schemas/AuditEntry",
							},
						},
					},
				},
			},
		},
	}
}

func auditPathParameter(name string, description string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "path",
		"description": description,
		"required":    true,
		"schema":      map[string]any{"type": "string"},
	}
}

func auditSessionSecurity() []map[string][]string {
	return []map[string][]string{{"sessionCookie": []string{}}}
}

func auditJSONResponseRef(description string, schema string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/" + schema},
			},
		},
	}
}

func auditProblemResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			httpserver.ProblemContentType: map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/Problem"},
			},
		},
	}
}
