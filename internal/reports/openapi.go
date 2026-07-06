package reports

import httpserver "github.com/npmulder/ledgerly/internal/platform/http"

// OpenAPIFragment returns the reports module's OpenAPI contribution.
func OpenAPIFragment() httpserver.OpenAPIFragment {
	return httpserver.OpenAPIFragment{
		Tags: []map[string]any{
			{
				"name":        "reports",
				"description": "Read-only profit and loss, VAT return figures, filing calendar, and profit YTD endpoints.",
			},
		},
		Paths: map[string]any{
			"/api/reports/pl": map[string]any{
				"get": map[string]any{
					"tags":        []string{"reports"},
					"summary":     "Return profit and loss for a period",
					"description": "Returns income grouped by client/currency, realised FX gains, expenses by account category, corporate tax, and net profit for an inclusive posting-date range.",
					"operationId": "reportsGetProfitAndLoss",
					"security":    reportsSessionSecurity(),
					"parameters": []map[string]any{
						reportsDateQueryParameter("from", "Inclusive posting date lower bound."),
						reportsDateQueryParameter("to", "Inclusive posting date upper bound."),
					},
					"responses": map[string]any{
						"200": reportsJSONResponseRef("Profit and loss report", "ReportsPLResponse"),
						"400": reportsProblemResponse("Invalid reports P&L query"),
						"401": reportsProblemResponse("Authentication required"),
					},
				},
			},
			"/api/reports/vat": map[string]any{
				"get": map[string]any{
					"tags":        []string{"reports"},
					"summary":     "Return VAT return figures for a quarter",
					"description": "Returns Isle of Man VAT return boxes 1, 4, and 6 plus net position for a calendar quarter period.",
					"operationId": "reportsGetVATReturn",
					"security":    reportsSessionSecurity(),
					"parameters": []map[string]any{
						{
							"name":        "period",
							"in":          "query",
							"required":    true,
							"description": "Calendar VAT quarter in YYYY-QN form, for example 2026-Q2.",
							"schema":      map[string]any{"type": "string", "pattern": "^[0-9]{4}-Q[1-4]$"},
						},
					},
					"responses": map[string]any{
						"200": reportsJSONResponseRef("VAT return figures", "ReportsVATResponse"),
						"400": reportsProblemResponse("Invalid VAT return query"),
						"401": reportsProblemResponse("Authentication required"),
					},
				},
			},
			"/api/reports/calendar": map[string]any{
				"get": map[string]any{
					"tags":        []string{"reports"},
					"summary":     "Return filing calendar",
					"description": "Returns the current filing calendar enriched with due-date status colors for reports and advisor surfaces.",
					"operationId": "reportsGetFilingCalendar",
					"security":    reportsSessionSecurity(),
					"responses": map[string]any{
						"200": reportsJSONResponseRef("Filing calendar", "ReportsFilingCalendarResponse"),
						"401": reportsProblemResponse("Authentication required"),
						"404": reportsProblemResponse("Company profile not found"),
					},
				},
			},
			"/api/reports/profit-ytd": map[string]any{
				"get": map[string]any{
					"tags":        []string{"reports"},
					"summary":     "Return year-to-date profit",
					"description": "Returns net profit for the company financial year identified by taxYear.",
					"operationId": "reportsGetProfitYTD",
					"security":    reportsSessionSecurity(),
					"parameters": []map[string]any{
						{
							"name":        "taxYear",
							"in":          "query",
							"required":    true,
							"description": "Tax year in YYYY-YY form, for example 2026-27.",
							"schema":      map[string]any{"type": "string", "pattern": "^[0-9]{4}-[0-9]{2}$"},
						},
					},
					"responses": map[string]any{
						"200": reportsJSONResponseRef("Profit YTD", "ReportsProfitYTDResponse"),
						"400": reportsProblemResponse("Invalid profit YTD query"),
						"401": reportsProblemResponse("Authentication required"),
						"404": reportsProblemResponse("Company profile not found"),
					},
				},
			},
		},
		Components: reportsComponents(),
	}
}

func reportsDateQueryParameter(name string, description string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "query",
		"required":    true,
		"description": description,
		"schema":      map[string]any{"type": "string", "format": "date"},
	}
}

func reportsJSONResponseRef(description string, schema string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/" + schema},
			},
		},
	}
}

func reportsProblemResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			httpserver.ProblemContentType: map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/Problem"},
			},
		},
	}
}

func reportsSessionSecurity() []map[string][]string {
	return []map[string][]string{
		{"sessionCookie": []string{}},
	}
}

func reportsComponents() map[string]any {
	return map[string]any{
		"schemas": map[string]any{
			"ReportsMoney": reportsMoneySchema(),
			"ReportsPeriod": map[string]any{
				"type":     "object",
				"required": []string{"from", "to"},
				"properties": map[string]any{
					"from": map[string]any{"type": "string", "format": "date"},
					"to":   map[string]any{"type": "string", "format": "date"},
				},
				"additionalProperties": false,
			},
			"ReportsIncomeLine": map[string]any{
				"type":     "object",
				"required": []string{"label", "client_id", "client_name", "currency", "amount"},
				"properties": map[string]any{
					"label":       map[string]any{"type": "string"},
					"client_id":   map[string]any{"type": "string"},
					"client_name": map[string]any{"type": "string"},
					"currency":    map[string]any{"type": "string", "pattern": "^[A-Z]{3}$"},
					"amount":      map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
				},
				"additionalProperties": false,
			},
			"ReportsExpenseLine": map[string]any{
				"type":     "object",
				"required": []string{"account_code", "account_name", "amount"},
				"properties": map[string]any{
					"account_code": map[string]any{"type": "string"},
					"account_name": map[string]any{"type": "string"},
					"amount":       map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
				},
				"additionalProperties": false,
			},
			"ReportsLineItem": map[string]any{
				"type":     "object",
				"required": []string{"label", "amount"},
				"properties": map[string]any{
					"label":  map[string]any{"type": "string"},
					"amount": map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
				},
				"additionalProperties": false,
			},
			"ReportsTaxLine": map[string]any{
				"type":     "object",
				"required": []string{"label", "tax_year", "rate", "amount"},
				"properties": map[string]any{
					"label":    map[string]any{"type": "string"},
					"tax_year": map[string]any{"type": "string"},
					"rate":     map[string]any{"type": "string"},
					"amount":   map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
				},
				"additionalProperties": false,
			},
			"ReportsPLResponse": map[string]any{
				"type": "object",
				"required": []string{
					"period",
					"tax_year",
					"income",
					"income_total",
					"realised_fx_gains",
					"expenses",
					"expense_total",
					"profit_before_tax",
					"corporate_tax",
					"net_profit",
				},
				"properties": map[string]any{
					"period":            map[string]any{"$ref": "#/components/schemas/ReportsPeriod"},
					"tax_year":          map[string]any{"type": "string"},
					"income":            reportsArraySchema("ReportsIncomeLine"),
					"income_total":      map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
					"realised_fx_gains": map[string]any{"$ref": "#/components/schemas/ReportsLineItem"},
					"expenses":          reportsArraySchema("ReportsExpenseLine"),
					"expense_total":     map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
					"profit_before_tax": map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
					"corporate_tax":     map[string]any{"$ref": "#/components/schemas/ReportsTaxLine"},
					"net_profit":        map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
				},
				"additionalProperties": false,
			},
			"ReportsVATResponse": map[string]any{
				"type":     "object",
				"required": []string{"period", "box1", "box4", "box6", "net_position"},
				"properties": map[string]any{
					"period":       map[string]any{"$ref": "#/components/schemas/ReportsPeriod"},
					"box1":         map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
					"box4":         map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
					"box6":         map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
					"net_position": map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
				},
				"additionalProperties": false,
			},
			"ReportsFiling": map[string]any{
				"type":     "object",
				"required": []string{"key", "label", "authority", "due_date", "days_until", "status"},
				"properties": map[string]any{
					"key":        map[string]any{"type": "string"},
					"label":      map[string]any{"type": "string"},
					"authority":  map[string]any{"type": "string"},
					"due_date":   map[string]any{"type": "string", "format": "date"},
					"days_until": map[string]any{"type": "integer", "format": "int32"},
					"status":     map[string]any{"type": "string", "enum": []string{string(FilingStatusUpcoming), string(FilingStatusDueSoon), string(FilingStatusOverdue)}},
				},
				"additionalProperties": false,
			},
			"ReportsFilingCalendarResponse": map[string]any{
				"type":     "object",
				"required": []string{"filings"},
				"properties": map[string]any{
					"filings": reportsArraySchema("ReportsFiling"),
				},
				"additionalProperties": false,
			},
			"ReportsProfitYTDResponse": map[string]any{
				"type":     "object",
				"required": []string{"tax_year", "profit"},
				"properties": map[string]any{
					"tax_year": map[string]any{"type": "string"},
					"profit":   map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
				},
				"additionalProperties": false,
			},
		},
	}
}

func reportsMoneySchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"amount_minor", "currency"},
		"properties": map[string]any{
			"amount_minor": map[string]any{"type": "integer", "format": "int64"},
			"currency":     map[string]any{"type": "string", "pattern": "^[A-Z]{3}$"},
		},
		"additionalProperties": false,
	}
}

func reportsArraySchema(schema string) map[string]any {
	return map[string]any{
		"type":  "array",
		"items": map[string]any{"$ref": "#/components/schemas/" + schema},
	}
}
