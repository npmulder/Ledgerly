package moneyfx

import httpserver "github.com/npmulder/ledgerly/internal/platform/http"

func openAPIFragment() httpserver.OpenAPIFragment {
	return httpserver.OpenAPIFragment{
		Tags: []map[string]any{
			{"name": "moneyfx"},
		},
		Paths: map[string]any{
			"/api/moneyfx/rates/today": map[string]any{
				"get": map[string]any{
					"tags":        []string{"moneyfx"},
					"summary":     "Return the latest FX rate",
					"operationId": "moneyfxTodayRate",
					"security":    sessionSecurity(),
					"parameters":  ratePairParameters(),
					"responses": map[string]any{
						"200": jsonResponseRef("Latest FX rate", "MoneyFXRateResponse"),
						"400": problemResponse("Invalid rate query"),
						"401": problemResponse("Authentication required"),
						"404": problemResponse("Rate was not found"),
					},
				},
			},
			"/api/moneyfx/rates": map[string]any{
				"get": map[string]any{
					"tags":        []string{"moneyfx"},
					"summary":     "Return an FX rate for a date",
					"operationId": "moneyfxRateOn",
					"security":    sessionSecurity(),
					"parameters": append([]map[string]any{
						{
							"name":        "date",
							"in":          "query",
							"required":    true,
							"description": "Requested posting date. If no ECB rate exists for this date, lookup walks back up to seven calendar days.",
							"schema":      map[string]any{"type": "string", "format": "date"},
						},
					}, ratePairParameters()...),
					"responses": map[string]any{
						"200": jsonResponseRef("FX rate", "MoneyFXRateResponse"),
						"400": problemResponse("Invalid rate query"),
						"401": problemResponse("Authentication required"),
						"404": problemResponse("Rate was not found"),
					},
				},
			},
		},
		Components: map[string]any{
			"schemas": map[string]any{
				"MoneyFXRateResponse": map[string]any{
					"type":     "object",
					"required": []string{"from", "to", "rate", "rate_date", "source", "fetched_at"},
					"properties": map[string]any{
						"from":       currencySchema(),
						"to":         currencySchema(),
						"rate":       map[string]any{"type": "string", "description": "Exact FX multiplier as a decimal or rational string."},
						"rate_date":  map[string]any{"type": "string", "format": "date"},
						"source":     map[string]any{"type": "string", "enum": []string{rateSourceECB, rateSourceIdentity}},
						"fetched_at": map[string]any{"type": "string", "format": "date-time"},
					},
					"additionalProperties": false,
				},
			},
		},
	}
}

func ratePairParameters() []map[string]any {
	return []map[string]any{
		{
			"name":     "from",
			"in":       "query",
			"required": true,
			"schema":   currencySchema(),
		},
		{
			"name":     "to",
			"in":       "query",
			"required": true,
			"schema":   currencySchema(),
		},
	}
}

func currencySchema() map[string]any {
	return map[string]any{
		"type":      "string",
		"minLength": 3,
		"maxLength": 12,
		"pattern":   "^[A-Za-z0-9]+$",
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
