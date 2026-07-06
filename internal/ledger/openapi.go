package ledger

import httpserver "github.com/npmulder/ledgerly/internal/platform/http"

// OpenAPIFragment returns the ledger module's read-only OpenAPI contribution.
func OpenAPIFragment() httpserver.OpenAPIFragment {
	return httpserver.OpenAPIFragment{
		Tags: []map[string]any{
			{
				"name":        "ledger",
				"description": "Read-only journal, chart-of-accounts, and trial-balance APIs. Ledger write endpoints are deliberately absent; modules post entries through the Go Ledger API inside their own transactions.",
			},
		},
		Paths: map[string]any{
			"/api/ledger/entries": map[string]any{
				"get": map[string]any{
					"tags":        []string{"ledger"},
					"summary":     "Browse journal entries",
					"description": "Returns journal entries with all postings for feeds export packs and accountant review. This endpoint is read-only; ledger writes happen only through module Go APIs.",
					"operationId": "ledgerListEntries",
					"security":    ledgerSessionSecurity(),
					"parameters": []map[string]any{
						dateQueryParameter("from", "Inclusive entry date lower bound."),
						dateQueryParameter("to", "Inclusive entry date upper bound."),
						{
							"name":        "source",
							"in":          "query",
							"required":    false,
							"description": "Filter entries by source module.",
							"schema":      map[string]any{"type": "string"},
						},
						{
							"name":        "account",
							"in":          "query",
							"required":    false,
							"description": "Filter to entries touching this account code while still returning all postings on matched entries.",
							"schema":      map[string]any{"type": "string"},
						},
						{
							"name":        "cursor",
							"in":          "query",
							"required":    false,
							"description": "Opaque keyset cursor from the previous response.",
							"schema":      map[string]any{"type": "string"},
						},
					},
					"responses": map[string]any{
						"200": ledgerJSONResponseRef("Journal entries listed", "LedgerEntriesResponse"),
						"400": ledgerProblemResponse("Invalid ledger entry query"),
						"401": ledgerProblemResponse("Authentication required"),
					},
				},
			},
			"/api/ledger/accounts": map[string]any{
				"get": map[string]any{
					"tags":        []string{"ledger"},
					"summary":     "List chart of accounts",
					"operationId": "ledgerListAccounts",
					"security":    ledgerSessionSecurity(),
					"responses": map[string]any{
						"200": ledgerJSONResponseRef("Chart of accounts", "LedgerAccountsResponse"),
						"401": ledgerProblemResponse("Authentication required"),
					},
				},
			},
			"/api/ledger/trial-balance": map[string]any{
				"get": map[string]any{
					"tags":        []string{"ledger"},
					"summary":     "Return current trial-balance status",
					"description": "Returns the current full-ledger balance check as of the server date. This is a read-only report; no HTTP endpoint posts to the ledger.",
					"operationId": "ledgerGetTrialBalance",
					"security":    ledgerSessionSecurity(),
					"responses": map[string]any{
						"200": ledgerJSONResponseRef("Trial-balance status", "LedgerTrialBalance"),
						"401": ledgerProblemResponse("Authentication required"),
					},
				},
			},
		},
		Components: ledgerComponents(),
	}
}

func dateQueryParameter(name string, description string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "query",
		"required":    false,
		"description": description,
		"schema":      map[string]any{"type": "string", "format": "date"},
	}
}

func ledgerJSONResponseRef(description string, schema string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/" + schema},
			},
		},
	}
}

func ledgerProblemResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			httpserver.ProblemContentType: map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/Problem"},
			},
		},
	}
}

func ledgerSessionSecurity() []map[string][]string {
	return []map[string][]string{
		{"sessionCookie": []string{}},
	}
}

func ledgerComponents() map[string]any {
	return map[string]any{
		"schemas": map[string]any{
			"LedgerMoney": map[string]any{
				"type":     "object",
				"required": []string{"amount_minor", "currency"},
				"properties": map[string]any{
					"amount_minor": map[string]any{"type": "integer", "format": "int64"},
					"currency":     map[string]any{"type": "string", "pattern": "^[A-Z]{3}$"},
				},
				"additionalProperties": false,
			},
			"LedgerPosting": map[string]any{
				"type":     "object",
				"required": []string{"account_code", "amount", "amount_gbp"},
				"properties": map[string]any{
					"account_code": map[string]any{"type": "string"},
					"amount":       map[string]any{"$ref": "#/components/schemas/LedgerMoney"},
					"amount_gbp":   map[string]any{"$ref": "#/components/schemas/LedgerMoney"},
				},
				"additionalProperties": false,
			},
			"LedgerEntry": map[string]any{
				"type": "object",
				"required": []string{
					"id",
					"date",
					"description",
					"source_module",
					"source_ref",
					"reversal_of",
					"postings",
					"created_at",
				},
				"properties": map[string]any{
					"id":            map[string]any{"type": "integer", "format": "int64"},
					"date":          map[string]any{"type": "string", "format": "date"},
					"description":   map[string]any{"type": "string"},
					"source_module": map[string]any{"type": "string"},
					"source_ref":    map[string]any{"type": "string"},
					"reversal_of":   map[string]any{"type": "integer", "format": "int64", "nullable": true},
					"postings": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/LedgerPosting"},
					},
					"created_at": map[string]any{"type": "string", "format": "date-time"},
				},
				"additionalProperties": false,
			},
			"LedgerEntriesResponse": map[string]any{
				"type":     "object",
				"required": []string{"entries", "next_cursor"},
				"properties": map[string]any{
					"entries": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/LedgerEntry"},
					},
					"next_cursor": map[string]any{"type": "string", "nullable": true},
				},
				"additionalProperties": false,
			},
			"LedgerAccount": map[string]any{
				"type":     "object",
				"required": []string{"id", "code", "name", "type", "currency", "created_at"},
				"properties": map[string]any{
					"id":         map[string]any{"type": "integer", "format": "int64"},
					"code":       map[string]any{"type": "string"},
					"name":       map[string]any{"type": "string"},
					"type":       map[string]any{"type": "string", "enum": []string{"asset", "liability", "equity", "income", "expense"}},
					"currency":   map[string]any{"type": "string", "pattern": "^[A-Z]{3}$", "nullable": true},
					"created_at": map[string]any{"type": "string", "format": "date-time"},
				},
				"additionalProperties": false,
			},
			"LedgerAccountsResponse": map[string]any{
				"type":     "object",
				"required": []string{"accounts"},
				"properties": map[string]any{
					"accounts": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/LedgerAccount"},
					},
				},
				"additionalProperties": false,
			},
			"LedgerTrialBalance": map[string]any{
				"type":     "object",
				"required": []string{"as_of", "status", "native_totals", "amount_gbp"},
				"properties": map[string]any{
					"as_of":  map[string]any{"type": "string", "format": "date"},
					"status": map[string]any{"type": "string", "enum": []string{"balanced", "out_of_balance"}},
					"native_totals": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/LedgerMoney"},
					},
					"amount_gbp": map[string]any{"$ref": "#/components/schemas/LedgerMoney"},
				},
				"additionalProperties": false,
			},
		},
	}
}
