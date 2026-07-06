package app

import httpserver "github.com/npmulder/ledgerly/internal/platform/http"

func dashboardOpenAPIFragment() httpserver.OpenAPIFragment {
	return httpserver.OpenAPIFragment{
		Tags: []map[string]any{
			{"name": "dashboard", "description": "Composed dashboard reads"},
		},
		Paths: map[string]any{
			"/api/dashboard/summary": map[string]any{
				"get": map[string]any{
					"tags":        []string{"dashboard"},
					"summary":     "Return the dashboard summary",
					"description": "Composes existing read APIs into the screen-01 dashboard payload. Individual section failures return null sections plus errors; the endpoint only fails when every section is unavailable.",
					"operationId": "dashboardGetSummary",
					"security":    sessionSecurity(),
					"responses": map[string]any{
						"200": jsonResponseRef("Dashboard summary", "DashboardSummary"),
						"401": problemResponse("Authentication required"),
						"503": problemResponse("All dashboard sections failed"),
					},
				},
			},
		},
		Components: dashboardComponents(),
	}
}

func dashboardComponents() map[string]any {
	return map[string]any{
		"schemas": map[string]any{
			"DashboardSummary": map[string]any{
				"type": "object",
				"required": []string{
					"cash",
					"outstanding",
					"dla",
					"dividendHeadroom",
					"recentInvoices",
					"toReconcile",
					"rate",
					"greeting",
					"errors",
				},
				"properties": map[string]any{
					"cash":             dashboardNullableRef("DashboardCash"),
					"outstanding":      dashboardNullableRef("DashboardOutstanding"),
					"dla":              dashboardNullableRef("DashboardDLA"),
					"dividendHeadroom": dashboardNullableRef("DashboardDividendHeadroom"),
					"recentInvoices":   dashboardNullableArrayRef("DashboardRecentInvoice"),
					"toReconcile":      dashboardNullableRef("DashboardToReconcile"),
					"rate":             dashboardNullableRef("DashboardRate"),
					"greeting":         dashboardNullableRef("DashboardGreeting"),
					"errors": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/DashboardSectionError"},
					},
				},
				"additionalProperties": false,
			},
			"DashboardSectionError": map[string]any{
				"type":                 "object",
				"required":             []string{"section", "detail"},
				"additionalProperties": false,
				"properties": map[string]any{
					"section": map[string]any{"type": "string"},
					"detail":  map[string]any{"type": "string"},
				},
			},
			"DashboardMoney": map[string]any{
				"type":                 "object",
				"required":             []string{"amount", "currency"},
				"additionalProperties": false,
				"properties": map[string]any{
					"amount":   map[string]any{"type": "integer", "format": "int64"},
					"currency": map[string]any{"type": "string"},
				},
			},
			"DashboardCash": map[string]any{
				"type":                 "object",
				"required":             []string{"accounts", "total_gbp"},
				"additionalProperties": false,
				"properties": map[string]any{
					"accounts": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/DashboardCashAccount"},
					},
					"total_gbp": map[string]any{"$ref": "#/components/schemas/DashboardMoney"},
				},
			},
			"DashboardCashAccount": map[string]any{
				"type": "object",
				"required": []string{
					"id",
					"name",
					"provider",
					"currency",
					"ledger_account_code",
					"native_balance",
					"gbp_balance",
				},
				"additionalProperties": false,
				"properties": map[string]any{
					"id":                  map[string]any{"type": "integer", "format": "int64"},
					"name":                map[string]any{"type": "string"},
					"provider":            map[string]any{"type": "string"},
					"currency":            map[string]any{"type": "string"},
					"ledger_account_code": map[string]any{"type": "string"},
					"native_balance":      map[string]any{"$ref": "#/components/schemas/DashboardMoney"},
					"gbp_balance":         map[string]any{"$ref": "#/components/schemas/DashboardMoney"},
				},
			},
			"DashboardOutstanding": map[string]any{
				"type":                 "object",
				"required":             []string{"totals", "total_gbp", "earliest_due_date"},
				"additionalProperties": false,
				"properties": map[string]any{
					"totals": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/DashboardMoney"},
					},
					"total_gbp":         map[string]any{"$ref": "#/components/schemas/DashboardMoney"},
					"earliest_due_date": map[string]any{"type": "string", "format": "date", "nullable": true},
				},
			},
			"DashboardDLA": map[string]any{
				"type":                 "object",
				"required":             []string{"balance", "status"},
				"additionalProperties": false,
				"properties": map[string]any{
					"balance": map[string]any{"$ref": "#/components/schemas/DashboardMoney"},
					"status":  map[string]any{"type": "string"},
				},
			},
			"DashboardDividendHeadroom": map[string]any{
				"type":                 "object",
				"required":             []string{"available", "distributable"},
				"additionalProperties": false,
				"properties": map[string]any{
					"available":     map[string]any{"$ref": "#/components/schemas/DashboardMoney"},
					"distributable": map[string]any{"type": "boolean"},
				},
			},
			"DashboardRecentInvoice": map[string]any{
				"type":                 "object",
				"required":             []string{"id", "number", "client", "amount", "status"},
				"additionalProperties": false,
				"properties": map[string]any{
					"id":           map[string]any{"type": "string"},
					"number":       map[string]any{"type": "string", "nullable": true},
					"client":       map[string]any{"type": "string"},
					"amount":       map[string]any{"$ref": "#/components/schemas/DashboardMoney"},
					"status":       map[string]any{"type": "string"},
					"days_overdue": map[string]any{"type": "integer", "nullable": true},
				},
			},
			"DashboardToReconcile": map[string]any{
				"type":                 "object",
				"required":             []string{"accounts", "review_queue"},
				"additionalProperties": false,
				"properties": map[string]any{
					"accounts": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/DashboardReconcileAccount"},
					},
					"review_queue": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/DashboardReviewQueueItem"},
					},
				},
			},
			"DashboardReconcileAccount": map[string]any{
				"type": "object",
				"required": []string{
					"id",
					"name",
					"currency",
					"ledger_account_code",
					"unreconciled_count",
				},
				"additionalProperties": false,
				"properties": map[string]any{
					"id":                  map[string]any{"type": "integer", "format": "int64"},
					"name":                map[string]any{"type": "string"},
					"currency":            map[string]any{"type": "string"},
					"ledger_account_code": map[string]any{"type": "string"},
					"unreconciled_count":  map[string]any{"type": "integer", "minimum": 0},
				},
			},
			"DashboardReviewQueueItem": map[string]any{
				"type":                 "object",
				"required":             []string{"kind", "payee", "amount", "confidence"},
				"additionalProperties": false,
				"properties": map[string]any{
					"kind":       map[string]any{"type": "string"},
					"payee":      map[string]any{"type": "string"},
					"amount":     map[string]any{"$ref": "#/components/schemas/DashboardMoney"},
					"confidence": map[string]any{"type": "number", "format": "double"},
				},
			},
			"DashboardRate": map[string]any{
				"type":                 "object",
				"required":             []string{"from", "to", "rate", "rate_date", "fetched_at", "source"},
				"additionalProperties": false,
				"properties": map[string]any{
					"from":       map[string]any{"type": "string"},
					"to":         map[string]any{"type": "string"},
					"rate":       map[string]any{"type": "string"},
					"rate_date":  map[string]any{"type": "string", "format": "date"},
					"fetched_at": map[string]any{"type": "string", "format": "date-time"},
					"source":     map[string]any{"type": "string"},
				},
			},
			"DashboardGreeting": map[string]any{
				"type":                 "object",
				"required":             []string{"user_name", "trading_name"},
				"additionalProperties": false,
				"properties": map[string]any{
					"user_name":    map[string]any{"type": "string"},
					"trading_name": map[string]any{"type": "string"},
				},
			},
		},
	}
}

func dashboardNullableRef(schema string) map[string]any {
	return map[string]any{
		"allOf":    []map[string]any{{"$ref": "#/components/schemas/" + schema}},
		"nullable": true,
	}
}

func dashboardNullableArrayRef(itemSchema string) map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"$ref": "#/components/schemas/" + itemSchema,
		},
		"nullable": true,
	}
}
