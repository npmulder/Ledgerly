package httpserver

import nethttp "net/http"

// OpenAPIFragment is the per-module OpenAPI contribution assembled by the
// platform. Paths should be fully namespaced, for example
// /api/invoicing/invoices.
type OpenAPIFragment struct {
	Paths      map[string]any
	Components map[string]any
	Tags       []map[string]any
}

func openAPIHandler(cfg Config) nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		writeJSON(w, nethttp.StatusOK, OpenAPIDocument(cfg.Version, cfg.OpenAPIFragments...))
	}
}

// OpenAPIDocument assembles the platform skeleton and all module fragments.
func OpenAPIDocument(version string, fragments ...OpenAPIFragment) map[string]any {
	if version == "" {
		version = defaultVersion
	}

	paths := platformOpenAPIPaths()
	components := map[string]any{
		"schemas": map[string]any{
			"HealthCheck": map[string]any{
				"type":     "object",
				"required": []string{"status"},
				"properties": map[string]any{
					"status": map[string]any{"type": "string"},
					"error":  map[string]any{"type": "string"},
				},
			},
			"HealthResponse": map[string]any{
				"type":     "object",
				"required": []string{"status", "version", "checks"},
				"properties": map[string]any{
					"status":  map[string]any{"type": "string"},
					"version": map[string]any{"type": "string"},
					"checks": map[string]any{
						"type": "object",
						"additionalProperties": map[string]any{
							"$ref": "#/components/schemas/HealthCheck",
						},
					},
				},
			},
			"Problem": map[string]any{
				"type":                 "object",
				"additionalProperties": true,
				"required":             []string{"type", "title", "status"},
				"properties": map[string]any{
					"type":     map[string]any{"type": "string", "format": "uri-reference"},
					"title":    map[string]any{"type": "string"},
					"status":   map[string]any{"type": "integer", "format": "int32"},
					"detail":   map[string]any{"type": "string"},
					"instance": map[string]any{"type": "string", "format": "uri-reference"},
				},
			},
		},
	}
	var tags []map[string]any

	for _, fragment := range fragments {
		for path, operation := range fragment.Paths {
			paths[path] = operation
		}
		mergeComponents(components, fragment.Components)
		tags = append(tags, fragment.Tags...)
	}

	document := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":   "Ledgerly API",
			"version": version,
		},
		"paths":      paths,
		"components": components,
	}
	if len(tags) > 0 {
		document["tags"] = tags
	}

	return document
}

func platformOpenAPIPaths() map[string]any {
	healthOperation := map[string]any{
		"summary": "Read platform health",
		"tags":    []string{"platform"},
		"responses": map[string]any{
			"200": map[string]any{
				"description": "Platform health status",
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": map[string]any{"$ref": "#/components/schemas/HealthResponse"},
					},
				},
			},
			"503": map[string]any{
				"description": "Platform dependency is unavailable",
				"content": map[string]any{
					ProblemContentType: map[string]any{
						"schema": map[string]any{"$ref": "#/components/schemas/Problem"},
					},
				},
			},
		},
	}

	return map[string]any{
		"/healthz": map[string]any{
			"get": healthOperation,
		},
		"/readyz": map[string]any{
			"get": healthOperation,
		},
	}
}

func mergeComponents(dst, src map[string]any) {
	for group, value := range src {
		srcGroup, ok := value.(map[string]any)
		if !ok {
			dst[group] = value
			continue
		}

		dstGroup, ok := dst[group].(map[string]any)
		if !ok {
			dst[group] = srcGroup
			continue
		}

		for name, component := range srcGroup {
			dstGroup[name] = component
		}
	}
}
