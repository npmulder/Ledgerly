package dla

import httpserver "github.com/npmulder/ledgerly/internal/platform/http"

// OpenAPIFragment returns the DLA module's OpenAPI contribution.
func OpenAPIFragment() httpserver.OpenAPIFragment {
	return httpserver.OpenAPIFragment{
		Tags: []map[string]any{
			{
				"name":        "dla",
				"description": "Director's loan account ledger, balance status, and manual repayment/expense entries.",
			},
		},
		Paths: map[string]any{
			"/api/dla/ledger": map[string]any{
				"get": map[string]any{
					"tags":        []string{"dla"},
					"summary":     "Browse director loan ledger entries",
					"description": "Returns DLA presentation-ledger rows in stable date/id order with derived running balance and owed-to-you/drawn columns.",
					"operationId": "dlaListLedger",
					"security":    dlaSessionSecurity(),
					"parameters": []map[string]any{
						dlaDateQueryParameter("from", "Inclusive entry date lower bound."),
						dlaDateQueryParameter("to", "Inclusive entry date upper bound."),
						{
							"name":        "cursor",
							"in":          "query",
							"required":    false,
							"description": "Opaque keyset cursor from the previous response.",
							"schema":      map[string]any{"type": "string"},
						},
					},
					"responses": map[string]any{
						"200": dlaJSONResponseRef("DLA ledger entries listed", "DLALedgerResponse"),
						"400": dlaProblemResponse("Invalid DLA ledger query"),
						"401": dlaProblemResponse("Authentication required"),
					},
				},
			},
			"/api/dla/balance": map[string]any{
				"get": map[string]any{
					"tags":        []string{"dla"},
					"summary":     "Return current DLA balance status",
					"description": "Returns the current DLA balance, credit/overdrawn status, jurisdiction policy keys, and suggested clearance amount when overdrawn.",
					"operationId": "dlaGetBalance",
					"security":    dlaSessionSecurity(),
					"responses": map[string]any{
						"200": dlaJSONResponseRef("Current DLA balance", "DLABalanceResponse"),
						"401": dlaProblemResponse("Authentication required"),
					},
				},
			},
			"/api/dla/entries": map[string]any{
				"post": map[string]any{
					"tags":        []string{"dla"},
					"summary":     "Create a manual DLA entry",
					"description": "Accepts manual repayments and personally-paid expenses owed to the director. Drawings are rejected because they must originate from banking reconciliation.",
					"operationId": "dlaCreateEntry",
					"security":    dlaSessionSecurity(),
					"requestBody": dlaJSONRequestBodyRef("DLAEntryRequest"),
					"responses": map[string]any{
						"201": dlaJSONResponseRef("DLA entry created", "DLAEntryCreatedResponse"),
						"400": dlaProblemResponse("Malformed DLA entry request"),
						"401": dlaProblemResponse("Authentication required"),
						"409": dlaProblemResponse("Duplicate source_ref"),
						"413": dlaProblemResponse("DLA entry request body is too large"),
						"422": dlaValidationProblemResponse("DLA entry validation failed"),
					},
				},
			},
		},
		Components: dlaComponents(),
	}
}

func dlaDateQueryParameter(name string, description string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "query",
		"required":    false,
		"description": description,
		"schema":      map[string]any{"type": "string", "format": "date"},
	}
}

func dlaJSONRequestBodyRef(schema string) map[string]any {
	return map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/" + schema},
			},
		},
	}
}

func dlaJSONResponseRef(description string, schema string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/" + schema},
			},
		},
	}
}

func dlaProblemResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			httpserver.ProblemContentType: map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/Problem"},
			},
		},
	}
}

func dlaValidationProblemResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			httpserver.ProblemContentType: map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/DLAValidationProblem"},
			},
		},
	}
}

func dlaSessionSecurity() []map[string][]string {
	return []map[string][]string{
		{"sessionCookie": []string{}},
	}
}

func dlaComponents() map[string]any {
	return map[string]any{
		"schemas": map[string]any{
			"DLAMoney": map[string]any{
				"type":     "object",
				"required": []string{"amount_minor", "currency"},
				"properties": map[string]any{
					"amount_minor": map[string]any{"type": "integer", "format": "int64"},
					"currency":     map[string]any{"type": "string", "pattern": "^[A-Z]{3}$"},
				},
				"additionalProperties": false,
			},
			"DLALedgerEntry": map[string]any{
				"type": "object",
				"required": []string{
					"id",
					"date",
					"kind",
					"description",
					"source_ref",
					"amount",
					"owed_to_you",
					"drawn",
					"running_balance",
					"balance_side",
					"created_at",
				},
				"properties": map[string]any{
					"id":          map[string]any{"type": "integer", "format": "int64"},
					"date":        map[string]any{"type": "string", "format": "date"},
					"kind":        map[string]any{"type": "string", "enum": []string{string(EntryKindDrawing), string(EntryKindRepayment), string(EntryKindExpenseOwed)}},
					"description": map[string]any{"type": "string"},
					"source_ref":  map[string]any{"type": "string"},
					"amount":      map[string]any{"$ref": "#/components/schemas/DLAMoney"},
					"owed_to_you": map[string]any{"$ref": "#/components/schemas/DLAMoney"},
					"drawn":       map[string]any{"$ref": "#/components/schemas/DLAMoney"},
					"running_balance": map[string]any{
						"$ref": "#/components/schemas/DLAMoney",
					},
					"balance_side": map[string]any{"type": "string", "enum": []string{string(BalanceSideCredit), string(BalanceSideDebit), string(BalanceSideZero)}},
					"created_at":   map[string]any{"type": "string", "format": "date-time"},
				},
				"additionalProperties": false,
			},
			"DLALedgerResponse": map[string]any{
				"type":     "object",
				"required": []string{"entries", "next_cursor"},
				"properties": map[string]any{
					"entries": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/DLALedgerEntry"},
					},
					"next_cursor": map[string]any{"type": "string", "nullable": true},
				},
				"additionalProperties": false,
			},
			"DLAPolicy": map[string]any{
				"type":     "object",
				"required": []string{"s455_charge", "bik_warning_key", "remedy"},
				"properties": map[string]any{
					"s455_charge":     map[string]any{"type": "boolean"},
					"bik_warning_key": map[string]any{"type": "string"},
					"remedy":          map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"DLABalanceResponse": map[string]any{
				"type":     "object",
				"required": []string{"balance", "status", "policy"},
				"properties": map[string]any{
					"balance": map[string]any{"$ref": "#/components/schemas/DLAMoney"},
					"status":  map[string]any{"type": "string", "enum": []string{string(StatusCredit), string(StatusOverdrawn)}},
					"policy":  map[string]any{"$ref": "#/components/schemas/DLAPolicy"},
					"suggested_clearance": map[string]any{
						"allOf":    []map[string]any{{"$ref": "#/components/schemas/DLAMoney"}},
						"nullable": true,
					},
				},
				"additionalProperties": false,
			},
			"DLAEntryRequest": map[string]any{
				"type":        "object",
				"description": "Manual DLA entry request. kind=drawing is included so clients can receive a typed rejection; accepted manual kinds are repayment and expense-owed.",
				"required":    []string{"date", "kind", "description", "amount"},
				"properties": map[string]any{
					"date":              map[string]any{"type": "string", "format": "date"},
					"kind":              map[string]any{"type": "string", "enum": []string{string(EntryKindRepayment), string(EntryKindExpenseOwed), string(EntryKindDrawing)}},
					"description":       map[string]any{"type": "string", "minLength": 1},
					"amount":            map[string]any{"$ref": "#/components/schemas/DLAMoney"},
					"source_ref":        map[string]any{"type": "string", "description": "Optional idempotency/source reference. If omitted, the server generates a manual source_ref and returns it."},
					"cash_account_code": map[string]any{"type": "string", "description": "Required for repayment entries."},
					"expense_category":  map[string]any{"type": "string", "description": "Required for expense-owed entries; use the target ledger expense account code."},
				},
				"additionalProperties": false,
			},
			"DLAEntryCreatedResponse": map[string]any{
				"type":                 "object",
				"required":             []string{"source_ref"},
				"properties":           map[string]any{"source_ref": map[string]any{"type": "string"}},
				"additionalProperties": false,
			},
			"DLAFieldError": map[string]any{
				"type":     "object",
				"required": []string{"pointer", "detail"},
				"properties": map[string]any{
					"pointer": map[string]any{"type": "string"},
					"detail":  map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"DLAValidationProblem": map[string]any{
				"type":                 "object",
				"additionalProperties": true,
				"required":             []string{"type", "title", "status", "errors"},
				"properties": map[string]any{
					"type":     map[string]any{"type": "string", "format": "uri-reference"},
					"title":    map[string]any{"type": "string"},
					"status":   map[string]any{"type": "integer", "format": "int32"},
					"detail":   map[string]any{"type": "string"},
					"instance": map[string]any{"type": "string", "format": "uri-reference"},
					"errors": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/DLAFieldError"},
					},
				},
			},
		},
	}
}
