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

	paths := map[string]any{}
	components := map[string]any{
		"schemas": map[string]any{
			"Problem": map[string]any{
				"type":     "object",
				"required": []string{"type", "title", "status"},
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
