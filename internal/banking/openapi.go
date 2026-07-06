package banking

import httpserver "github.com/npmulder/ledgerly/internal/platform/http"

// OpenAPIFragment returns the banking module's OpenAPI contribution.
func OpenAPIFragment() httpserver.OpenAPIFragment {
	return httpserver.OpenAPIFragment{
		Tags: []map[string]any{
			{
				"name":        "banking",
				"description": "Bank account setup, CSV import, reconciliation review queue, transaction feed, and reconciliation commands.",
			},
		},
		Paths: map[string]any{
			"/api/banking/accounts": map[string]any{
				"get": map[string]any{
					"tags":        []string{"banking"},
					"summary":     "List bank accounts",
					"description": "Returns configured bank accounts with unreconciled-count badges for the banking screen and CLI.",
					"operationId": "bankingListAccounts",
					"security":    bankingSessionSecurity(),
					"responses": map[string]any{
						"200": bankingJSONResponseRef("Bank accounts", "BankingAccountsResponse"),
						"401": bankingProblemResponse("Authentication required"),
					},
				},
				"post": map[string]any{
					"tags":        []string{"banking"},
					"summary":     "Create a bank account",
					"operationId": "bankingCreateAccount",
					"security":    bankingSessionSecurity(),
					"requestBody": bankingJSONRequestBodyRef("BankingCreateAccountRequest"),
					"responses": map[string]any{
						"201": bankingJSONResponseRef("Bank account created", "BankingAccount"),
						"400": bankingProblemResponse("Malformed account request"),
						"401": bankingProblemResponse("Authentication required"),
						"413": bankingProblemResponse("Account request body is too large"),
						"422": bankingProblemResponse("Account validation failed"),
					},
				},
			},
			"/api/banking/accounts/{id}/import": map[string]any{
				"post": map[string]any{
					"tags":        []string{"banking"},
					"summary":     "Import a bank statement CSV",
					"description": "Accepts a multipart CSV upload capped at 10 MB and returns total/new/duplicate import counts. Parse errors include row numbers.",
					"operationId": "bankingImportAccountCSV",
					"security":    bankingSessionSecurity(),
					"parameters":  []map[string]any{bankingIDPathParameter("id", "Bank account ID.")},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"multipart/form-data": map[string]any{
								"schema": map[string]any{
									"type":     "object",
									"required": []string{"file"},
									"properties": map[string]any{
										"file": map[string]any{"type": "string", "format": "binary"},
									},
									"additionalProperties": false,
								},
							},
						},
					},
					"responses": map[string]any{
						"200": bankingJSONResponseRef("Import summary", "BankingBatchSummary"),
						"400": bankingProblemResponse("Malformed multipart upload"),
						"401": bankingProblemResponse("Authentication required"),
						"404": bankingProblemResponse("Bank account was not found"),
						"413": bankingProblemResponse("CSV upload is too large"),
						"415": bankingProblemResponse("multipart/form-data is required"),
						"422": bankingValidationProblemResponse("CSV parse or validation failed"),
					},
				},
			},
			"/api/banking/review": map[string]any{
				"get": map[string]any{
					"tags":        []string{"banking"},
					"summary":     "Return reconciliation review queue",
					"description": "Returns match, suggestion, and rule card groups with confidence, explanation, transaction details, and target metadata.",
					"operationId": "bankingGetReviewQueue",
					"security":    bankingSessionSecurity(),
					"responses": map[string]any{
						"200": bankingJSONResponseRef("Review queue", "BankingReviewQueue"),
						"401": bankingProblemResponse("Authentication required"),
					},
				},
			},
			"/api/banking/feed": map[string]any{
				"get": map[string]any{
					"tags":        []string{"banking"},
					"summary":     "Browse bank transaction feed",
					"operationId": "bankingGetFeed",
					"security":    bankingSessionSecurity(),
					"parameters": []map[string]any{
						bankingQueryParameter("account", "Filter by bank account ID.", map[string]any{"type": "integer", "format": "int64"}),
						bankingQueryParameter("state", "Filter by reconciliation state.", bankingTransactionStateSchema()),
						bankingQueryParameter("cursor", "Opaque keyset cursor from the previous response.", map[string]any{"type": "string"}),
					},
					"responses": map[string]any{
						"200": bankingJSONResponseRef("Transaction feed", "BankingFeedResponse"),
						"400": bankingProblemResponse("Invalid feed query"),
						"401": bankingProblemResponse("Authentication required"),
					},
				},
			},
			"/api/banking/recent": map[string]any{
				"get": map[string]any{
					"tags":        []string{"banking"},
					"summary":     "List recently reconciled transactions",
					"operationId": "bankingGetRecent",
					"security":    bankingSessionSecurity(),
					"parameters": []map[string]any{
						bankingQueryParameter("account", "Filter by bank account ID.", map[string]any{"type": "integer", "format": "int64"}),
						bankingQueryParameter("limit", "Maximum recently reconciled rows.", map[string]any{"type": "integer", "minimum": 1, "maximum": MaxRecentlyReconciledLimit}),
					},
					"responses": map[string]any{
						"200": bankingJSONResponseRef("Recently reconciled transactions", "BankingRecentResponse"),
						"400": bankingProblemResponse("Invalid recent query"),
						"401": bankingProblemResponse("Authentication required"),
					},
				},
			},
			"/api/banking/transactions/{id}/confirm":   bankingCommandPath("bankingConfirmTransaction", "Confirm an invoice match", nil, "BankingCommandResponse"),
			"/api/banking/transactions/{id}/file-dla":  bankingCommandPath("bankingFileTransactionToDLA", "File a transaction to the DLA", nil, "BankingCommandResponse"),
			"/api/banking/transactions/{id}/recode":    bankingCommandPath("bankingRecodeTransaction", "Recode a transaction to an expense account", bankingJSONRequestBodyRef("BankingRecodeRequest"), "BankingCommandResponse"),
			"/api/banking/transactions/{id}/exclude":   bankingCommandPath("bankingExcludeTransaction", "Exclude a transaction", bankingJSONRequestBodyRef("BankingReasonRequest"), "BankingCommandResponse"),
			"/api/banking/transactions/{id}/unexclude": bankingCommandPath("bankingUnexcludeTransaction", "Unexclude a transaction", bankingOptionalJSONRequestBodyRef("BankingReasonRequest"), "BankingCommandResponse"),
		},
		Components: bankingComponents(),
	}
}

func bankingCommandPath(operationID string, summary string, requestBody map[string]any, responseSchema string) map[string]any {
	post := map[string]any{
		"tags":        []string{"banking"},
		"summary":     summary,
		"operationId": operationID,
		"security":    bankingSessionSecurity(),
		"parameters":  []map[string]any{bankingIDPathParameter("id", "Bank transaction ID.")},
		"responses": map[string]any{
			"200": bankingJSONResponseRef("Command accepted", responseSchema),
			"400": bankingProblemResponse("Malformed command request"),
			"401": bankingProblemResponse("Authentication required"),
			"404": bankingProblemResponse("Transaction was not found"),
			"409": bankingProblemResponse("Transaction cannot be reconciled by this command"),
			"413": bankingProblemResponse("Command request body is too large"),
			"422": bankingProblemResponse("Command validation failed"),
		},
	}
	if requestBody != nil {
		post["requestBody"] = requestBody
	}
	return map[string]any{"post": post}
}

func bankingIDPathParameter(name string, description string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "path",
		"required":    true,
		"description": description,
		"schema":      map[string]any{"type": "integer", "format": "int64", "minimum": 1},
	}
}

func bankingQueryParameter(name string, description string, schema map[string]any) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "query",
		"required":    false,
		"description": description,
		"schema":      schema,
	}
}

func bankingJSONRequestBodyRef(schema string) map[string]any {
	return map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/" + schema},
			},
		},
	}
}

func bankingOptionalJSONRequestBodyRef(schema string) map[string]any {
	body := bankingJSONRequestBodyRef(schema)
	body["required"] = false
	return body
}

func bankingJSONResponseRef(description string, schema string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/" + schema},
			},
		},
	}
}

func bankingProblemResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			httpserver.ProblemContentType: map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/Problem"},
			},
		},
	}
}

func bankingValidationProblemResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			httpserver.ProblemContentType: map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/BankingValidationProblem"},
			},
		},
	}
}

func bankingSessionSecurity() []map[string][]string {
	return []map[string][]string{
		{"sessionCookie": []string{}},
	}
}

func bankingComponents() map[string]any {
	return map[string]any{
		"schemas": map[string]any{
			"BankingMoney":            bankingMoneySchema(),
			"BankingAccount":          bankingAccountSchema(),
			"BankingAccountsResponse": bankingAccountsResponseSchema(),
			"BankingCreateAccountRequest": map[string]any{
				"type":     "object",
				"required": []string{"name", "provider", "currency"},
				"properties": map[string]any{
					"name":     map[string]any{"type": "string"},
					"provider": map[string]any{"type": "string", "enum": []string{string(ProviderRevolut)}},
					"currency": map[string]any{"type": "string", "enum": []string{"GBP", "EUR"}},
				},
				"additionalProperties": false,
			},
			"BankingBatchSummary":   bankingBatchSummarySchema(),
			"BankingTransaction":    bankingTransactionSchema(),
			"BankingReviewTarget":   bankingReviewTargetSchema(),
			"BankingReviewCard":     bankingReviewCardSchema(),
			"BankingReviewQueue":    bankingReviewQueueSchema(),
			"BankingFeedResponse":   bankingFeedResponseSchema(),
			"BankingRecentResponse": bankingRecentResponseSchema(),
			"BankingRecentTransaction": map[string]any{
				"type":     "object",
				"required": []string{"transaction", "reconciled_at", "actor"},
				"properties": map[string]any{
					"transaction":   map[string]any{"$ref": "#/components/schemas/BankingTransaction"},
					"reconciled_at": map[string]any{"type": "string", "format": "date-time"},
					"actor":         map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"BankingCommandResponse": bankingCommandResponseSchema(),
			"BankingPayeeRule":       bankingPayeeRuleSchema(),
			"BankingStateChange":     bankingStateChangeSchema(),
			"BankingRecodeRequest": map[string]any{
				"type":     "object",
				"required": []string{"account_code"},
				"properties": map[string]any{
					"account_code": map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"BankingReasonRequest": map[string]any{
				"type":     "object",
				"required": []string{"reason"},
				"properties": map[string]any{
					"reason": map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"BankingValidationProblem": bankingValidationProblemSchema(),
		},
	}
}

func bankingMoneySchema() map[string]any {
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

func bankingAccountSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"required": []string{
			"id",
			"name",
			"provider",
			"currency",
			"ledger_account_code",
			"unreconciled_count",
			"created_at",
		},
		"properties": map[string]any{
			"id":                  map[string]any{"type": "integer", "format": "int64"},
			"name":                map[string]any{"type": "string"},
			"provider":            map[string]any{"type": "string", "enum": []string{string(ProviderRevolut)}},
			"currency":            map[string]any{"type": "string", "pattern": "^[A-Z]{3}$"},
			"ledger_account_code": map[string]any{"type": "string"},
			"unreconciled_count":  map[string]any{"type": "integer"},
			"created_at":          map[string]any{"type": "string", "format": "date-time"},
		},
		"additionalProperties": false,
	}
}

func bankingAccountsResponseSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"accounts"},
		"properties": map[string]any{
			"accounts": map[string]any{
				"type":  "array",
				"items": map[string]any{"$ref": "#/components/schemas/BankingAccount"},
			},
		},
		"additionalProperties": false,
	}
}

func bankingBatchSummarySchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"batch_id", "account_id", "filename", "imported_at", "total", "new", "duplicates"},
		"properties": map[string]any{
			"batch_id":   map[string]any{"type": "integer", "format": "int64"},
			"account_id": map[string]any{"type": "integer", "format": "int64"},
			"filename":   map[string]any{"type": "string"},
			"imported_at": map[string]any{
				"type":   "string",
				"format": "date-time",
			},
			"total":      map[string]any{"type": "integer"},
			"new":        map[string]any{"type": "integer"},
			"duplicates": map[string]any{"type": "integer"},
		},
		"additionalProperties": false,
	}
}

func bankingTransactionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"required": []string{
			"id",
			"account_id",
			"date",
			"amount",
			"payee",
			"reference",
			"provider_meta",
			"import_batch_id",
			"state",
			"created_at",
		},
		"properties": map[string]any{
			"id":              map[string]any{"type": "integer", "format": "int64"},
			"account_id":      map[string]any{"type": "integer", "format": "int64"},
			"date":            map[string]any{"type": "string", "format": "date"},
			"amount":          map[string]any{"$ref": "#/components/schemas/BankingMoney"},
			"payee":           map[string]any{"type": "string"},
			"reference":       map[string]any{"type": "string"},
			"provider_meta":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"import_batch_id": map[string]any{"type": "integer", "format": "int64"},
			"state":           bankingTransactionStateSchema(),
			"created_at":      map[string]any{"type": "string", "format": "date-time"},
		},
		"additionalProperties": false,
	}
}

func bankingTransactionStateSchema() map[string]any {
	return map[string]any{
		"type": "string",
		"enum": []string{
			string(TransactionStateUnreconciled),
			string(TransactionStateSuggested),
			string(TransactionStateReconciled),
			string(TransactionStateExcluded),
		},
	}
}

func bankingReviewTargetSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"type"},
		"properties": map[string]any{
			"type":           map[string]any{"type": "string", "enum": []string{"invoice", "dla", "account"}},
			"id":             map[string]any{"type": "string"},
			"invoice_number": map[string]any{"type": "string"},
			"client":         map[string]any{"type": "string"},
			"account_code":   map[string]any{"type": "string"},
			"times_applied":  map[string]any{"type": "integer", "nullable": true},
		},
		"additionalProperties": false,
	}
}

func bankingReviewCardSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"kind", "suggestion_id", "transaction", "confidence", "explanation", "target"},
		"properties": map[string]any{
			"kind":          map[string]any{"type": "string", "enum": []string{"match", "suggestion", "rule"}},
			"suggestion_id": map[string]any{"type": "integer", "format": "int64"},
			"transaction":   map[string]any{"$ref": "#/components/schemas/BankingTransaction"},
			"confidence":    map[string]any{"type": "number", "format": "double"},
			"explanation":   map[string]any{"type": "string"},
			"target":        map[string]any{"$ref": "#/components/schemas/BankingReviewTarget"},
		},
		"additionalProperties": false,
	}
}

func bankingReviewQueueSchema() map[string]any {
	cardArray := map[string]any{
		"type":  "array",
		"items": map[string]any{"$ref": "#/components/schemas/BankingReviewCard"},
	}
	return map[string]any{
		"type":     "object",
		"required": []string{"matches", "suggestions", "rules"},
		"properties": map[string]any{
			"matches":     cardArray,
			"suggestions": cardArray,
			"rules":       cardArray,
		},
		"additionalProperties": false,
	}
}

func bankingFeedResponseSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"transactions", "next_cursor"},
		"properties": map[string]any{
			"transactions": map[string]any{
				"type":  "array",
				"items": map[string]any{"$ref": "#/components/schemas/BankingTransaction"},
			},
			"next_cursor": map[string]any{"type": "string", "nullable": true},
		},
		"additionalProperties": false,
	}
}

func bankingRecentResponseSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"transactions"},
		"properties": map[string]any{
			"transactions": map[string]any{
				"type":  "array",
				"items": map[string]any{"$ref": "#/components/schemas/BankingRecentTransaction"},
			},
		},
		"additionalProperties": false,
	}
}

func bankingCommandResponseSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"transaction":        map[string]any{"$ref": "#/components/schemas/BankingTransaction"},
			"kind":               map[string]any{"type": "string", "enum": []string{"match", "suggestion", "rule"}},
			"realised_fx_amount": map[string]any{"$ref": "#/components/schemas/BankingMoney"},
			"amount_gbp":         map[string]any{"$ref": "#/components/schemas/BankingMoney"},
			"rule":               map[string]any{"$ref": "#/components/schemas/BankingPayeeRule"},
			"state_change":       map[string]any{"$ref": "#/components/schemas/BankingStateChange"},
		},
		"additionalProperties": false,
	}
}

func bankingPayeeRuleSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"id", "matcher", "match_mode", "account_code", "times_applied", "last_applied_at", "created_from", "created_at"},
		"properties": map[string]any{
			"id":              map[string]any{"type": "integer", "format": "int64"},
			"matcher":         map[string]any{"type": "string"},
			"match_mode":      map[string]any{"type": "string", "enum": []string{string(PayeeRuleMatchExact), string(PayeeRuleMatchContains)}},
			"account_code":    map[string]any{"type": "string"},
			"times_applied":   map[string]any{"type": "integer"},
			"last_applied_at": map[string]any{"type": "string", "format": "date-time", "nullable": true},
			"created_from":    map[string]any{"type": "string", "enum": []string{string(PayeeRuleCreatedFromRecode), string(PayeeRuleCreatedFromManual)}},
			"created_at":      map[string]any{"type": "string", "format": "date-time"},
		},
		"additionalProperties": false,
	}
}

func bankingStateChangeSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"id", "transaction_id", "from", "to", "changed_at", "actor"},
		"properties": map[string]any{
			"id":             map[string]any{"type": "integer", "format": "int64"},
			"transaction_id": map[string]any{"type": "integer", "format": "int64"},
			"from":           bankingTransactionStateSchema(),
			"to":             bankingTransactionStateSchema(),
			"changed_at":     map[string]any{"type": "string", "format": "date-time"},
			"actor":          map[string]any{"type": "string"},
		},
		"additionalProperties": false,
	}
}

func bankingValidationProblemSchema() map[string]any {
	return map[string]any{
		"allOf": []map[string]any{
			{"$ref": "#/components/schemas/Problem"},
			{
				"type": "object",
				"properties": map[string]any{
					"row_numbers": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "integer"},
					},
					"errors": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type":     "object",
							"required": []string{"pointer", "detail"},
							"properties": map[string]any{
								"pointer": map[string]any{"type": "string"},
								"detail":  map[string]any{"type": "string"},
								"row":     map[string]any{"type": "integer"},
							},
							"additionalProperties": false,
						},
					},
				},
			},
		},
	}
}
