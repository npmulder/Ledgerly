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
			"/api/reports/expenses": map[string]any{
				"get": map[string]any{
					"tags":        []string{"reports"},
					"summary":     "Return categorized expenses for a period",
					"description": "Returns expense totals by category, top payees, and transaction-level drill-down rows for an inclusive posting-date range.",
					"operationId": "reportsGetExpenses",
					"security":    reportsSessionSecurity(),
					"parameters": []map[string]any{
						reportsDateQueryParameter("from", "Inclusive posting date lower bound."),
						reportsDateQueryParameter("to", "Inclusive posting date upper bound."),
					},
					"responses": map[string]any{
						"200": reportsJSONResponseRef("Categorized expenses report", "ReportsExpensesResponse"),
						"400": reportsProblemResponse("Invalid reports expenses query"),
						"401": reportsProblemResponse("Authentication required"),
					},
				},
			},
			"/api/reports/expenses.csv": map[string]any{
				"get": map[string]any{
					"tags":        []string{"reports"},
					"summary":     "Download categorized expense transactions as CSV",
					"description": "Returns date, payee, reference, amount, currency, and category for accountant expense drill-down.",
					"operationId": "reportsDownloadExpensesCSV",
					"security":    reportsSessionSecurity(),
					"parameters": []map[string]any{
						reportsDateQueryParameter("from", "Inclusive posting date lower bound."),
						reportsDateQueryParameter("to", "Inclusive posting date upper bound."),
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Categorized expense transactions CSV",
							"content": map[string]any{
								"text/csv": map[string]any{
									"schema": map[string]any{"type": "string", "format": "binary"},
								},
							},
						},
						"400": reportsProblemResponse("Invalid reports expenses CSV query"),
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
			"/api/reports/export": map[string]any{
				"get": map[string]any{
					"tags":        []string{"reports"},
					"summary":     "Generate and download an accountant export pack",
					"description": "Generates or reuses the immutable export-pack ZIP for an inclusive posting-date range and redirects to the stored archive asset.",
					"operationId": "reportsExportPack",
					"security":    reportsSessionSecurity(),
					"parameters": []map[string]any{
						reportsDateQueryParameter("from", "Inclusive posting date lower bound."),
						reportsDateQueryParameter("to", "Inclusive posting date upper bound."),
					},
					"responses": map[string]any{
						"302": map[string]any{
							"description": "Redirect to stored export ZIP asset",
							"headers": map[string]any{
								"Location": map[string]any{
									"description": "Immutable export ZIP asset URL.",
									"schema":      map[string]any{"type": "string", "format": "uri-reference"},
								},
							},
						},
						"400": reportsProblemResponse("Invalid export query"),
						"401": reportsProblemResponse("Authentication required"),
						"404": reportsProblemResponse("Company profile not found"),
					},
				},
			},
			"/api/reports/share": map[string]any{
				"post": map[string]any{
					"tags":        []string{"reports"},
					"summary":     "Share an export pack with an accountant",
					"description": "Emails the export-pack ZIP as an attachment when it is within the platform mail size guard; larger packs return a manual-send response.",
					"operationId": "reportsShareExportPack",
					"security":    reportsSessionSecurity(),
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/ReportsShareRequest"},
							},
						},
					},
					"responses": map[string]any{
						"200": reportsJSONResponseRef("Share result", "ReportsShareResponse"),
						"400": reportsProblemResponse("Invalid share request"),
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
			"ReportsExpenseCategory": map[string]any{
				"type":     "object",
				"required": []string{"account_code", "category", "amount", "transaction_count"},
				"properties": map[string]any{
					"account_code":      map[string]any{"type": "string"},
					"category":          map[string]any{"type": "string"},
					"amount":            map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
					"transaction_count": map[string]any{"type": "integer", "format": "int32"},
				},
				"additionalProperties": false,
			},
			"ReportsExpensePayee": map[string]any{
				"type":     "object",
				"required": []string{"payee", "amount", "transaction_count"},
				"properties": map[string]any{
					"payee":             map[string]any{"type": "string"},
					"amount":            map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
					"transaction_count": map[string]any{"type": "integer", "format": "int32"},
				},
				"additionalProperties": false,
			},
			"ReportsExpenseTransaction": map[string]any{
				"type": "object",
				"required": []string{
					"entry_id",
					"date",
					"payee",
					"reference",
					"amount",
					"account_code",
					"category",
					"source_module",
					"source_ref",
				},
				"properties": map[string]any{
					"entry_id":      map[string]any{"type": "integer", "format": "int64"},
					"date":          map[string]any{"type": "string", "format": "date"},
					"payee":         map[string]any{"type": "string"},
					"reference":     map[string]any{"type": "string"},
					"amount":        map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
					"account_code":  map[string]any{"type": "string"},
					"category":      map[string]any{"type": "string"},
					"source_module": map[string]any{"type": "string"},
					"source_ref":    map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"ReportsExpensesResponse": map[string]any{
				"type":     "object",
				"required": []string{"period", "categories", "top_payees", "transactions", "total"},
				"properties": map[string]any{
					"period":       map[string]any{"$ref": "#/components/schemas/ReportsPeriod"},
					"categories":   reportsArraySchema("ReportsExpenseCategory"),
					"top_payees":   reportsArraySchema("ReportsExpensePayee"),
					"transactions": reportsArraySchema("ReportsExpenseTransaction"),
					"total":        map[string]any{"$ref": "#/components/schemas/ReportsMoney"},
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
			"ReportsArchiveRef": map[string]any{
				"type":     "object",
				"required": []string{"url", "sha256", "size_bytes", "data_version", "generated_at"},
				"properties": map[string]any{
					"url":          map[string]any{"type": "string", "format": "uri-reference"},
					"sha256":       map[string]any{"type": "string"},
					"size_bytes":   map[string]any{"type": "integer", "format": "int64"},
					"data_version": map[string]any{"type": "string"},
					"generated_at": map[string]any{"type": "string", "format": "date-time"},
				},
				"additionalProperties": false,
			},
			"ReportsShareRequest": map[string]any{
				"type":     "object",
				"required": []string{"email", "period"},
				"properties": map[string]any{
					"email":  map[string]any{"type": "string", "format": "email"},
					"period": map[string]any{"$ref": "#/components/schemas/ReportsPeriod"},
				},
				"additionalProperties": false,
			},
			"ReportsShareResponse": map[string]any{
				"type":     "object",
				"required": []string{"status", "archive", "message"},
				"properties": map[string]any{
					"status":  map[string]any{"type": "string", "enum": []string{string(ShareStatusSent), string(ShareStatusManualSend)}},
					"archive": map[string]any{"$ref": "#/components/schemas/ReportsArchiveRef"},
					"message": map[string]any{"type": "string"},
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
