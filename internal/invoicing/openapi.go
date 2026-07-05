package invoicing

import (
	"github.com/npmulder/ledgerly/internal/identity"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

// OpenAPIFragment returns the invoicing module's OpenAPI contribution without
// requiring database-backed module construction.
func OpenAPIFragment() httpserver.OpenAPIFragment {
	return httpserver.OpenAPIFragment{
		Tags: []map[string]any{
			{"name": "invoicing"},
		},
		Paths: map[string]any{
			"/api/invoicing/clients": map[string]any{
				"get": map[string]any{
					"tags":        []string{"invoicing"},
					"summary":     "List clients",
					"operationId": "invoicingListClients",
					"security":    sessionSecurity(),
					"parameters": []map[string]any{
						{
							"name":        "include_archived",
							"in":          "query",
							"required":    false,
							"description": "Include soft-archived clients for history views.",
							"schema":      map[string]any{"type": "boolean", "default": false},
						},
					},
					"responses": map[string]any{
						"200": jsonResponseRef("Clients listed", "InvoicingClientsResponse"),
						"400": problemResponse("Invalid query"),
						"401": problemResponse("Authentication required"),
					},
				},
				"post": map[string]any{
					"tags":        []string{"invoicing"},
					"summary":     "Create a client",
					"operationId": "invoicingCreateClient",
					"security":    sessionSecurity(),
					"requestBody": jsonRequestBodyRef("InvoicingClientRequest"),
					"responses": map[string]any{
						"201": jsonResponseRef("Client created", "InvoicingClient"),
						"400": problemResponse("Malformed client request"),
						"401": problemResponse("Authentication required"),
						"413": problemResponse("Client request body is too large"),
						"422": validationProblemResponse("Client validation failed"),
					},
				},
			},
			"/api/invoicing/clients/{id}": map[string]any{
				"get": map[string]any{
					"tags":        []string{"invoicing"},
					"summary":     "Return a client",
					"operationId": "invoicingGetClient",
					"security":    sessionSecurity(),
					"parameters":  clientIDParameter(),
					"responses": map[string]any{
						"200": jsonResponseRef("Client", "InvoicingClient"),
						"401": problemResponse("Authentication required"),
						"404": problemResponse("Client was not found"),
					},
				},
				"patch": map[string]any{
					"tags":        []string{"invoicing"},
					"summary":     "Partially update a client",
					"operationId": "invoicingPatchClient",
					"security":    sessionSecurity(),
					"parameters":  clientIDParameter(),
					"requestBody": jsonRequestBodyRef("InvoicingClientPatch"),
					"responses": map[string]any{
						"200": jsonResponseRef("Updated client", "InvoicingClient"),
						"400": problemResponse("Malformed client patch"),
						"401": problemResponse("Authentication required"),
						"404": problemResponse("Client was not found"),
						"409": problemResponse("Client currency is locked"),
						"413": problemResponse("Client patch request body is too large"),
						"422": validationProblemResponse("Client validation failed"),
					},
				},
			},
			"/api/invoicing/clients/{id}/archive": map[string]any{
				"post": map[string]any{
					"tags":        []string{"invoicing"},
					"summary":     "Archive a client",
					"operationId": "invoicingArchiveClient",
					"security":    sessionSecurity(),
					"parameters":  clientIDParameter(),
					"responses": map[string]any{
						"204": map[string]any{"description": "Client archived"},
						"401": problemResponse("Authentication required"),
						"404": problemResponse("Client was not found"),
					},
				},
			},
		},
		Components: invoicingComponents(),
	}
}

func clientIDParameter() []map[string]any {
	return []map[string]any{
		{
			"name":     "id",
			"in":       "path",
			"required": true,
			"schema":   map[string]any{"type": "string"},
		},
	}
}

func jsonRequestBody(schema map[string]any) map[string]any {
	return map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": schema,
			},
		},
	}
}

func jsonRequestBodyRef(schema string) map[string]any {
	return jsonRequestBody(map[string]any{"$ref": "#/components/schemas/" + schema})
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

func validationProblemResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			httpserver.ProblemContentType: map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/ValidationProblem"},
			},
		},
	}
}

func sessionSecurity() []map[string][]string {
	return []map[string][]string{
		{"sessionCookie": []string{}},
	}
}

func invoicingComponents() map[string]any {
	return map[string]any{
		"securitySchemes": map[string]any{
			"sessionCookie": map[string]any{
				"type": "apiKey",
				"in":   "cookie",
				"name": identity.SessionCookieName,
			},
		},
		"schemas": map[string]any{
			"InvoicingAddress": map[string]any{
				"type":     "object",
				"required": []string{"line1", "line2", "locality", "region", "postal_code", "country"},
				"properties": map[string]any{
					"line1":       map[string]any{"type": "string"},
					"line2":       map[string]any{"type": "string"},
					"locality":    map[string]any{"type": "string"},
					"region":      map[string]any{"type": "string"},
					"postal_code": map[string]any{"type": "string"},
					"country":     map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"InvoicingMoneyAmount": map[string]any{
				"type":     "object",
				"required": []string{"amount_minor", "currency"},
				"properties": map[string]any{
					"amount_minor": map[string]any{"type": "integer", "format": "int64", "minimum": 1},
					"currency":     currencySchema(),
				},
				"additionalProperties": false,
			},
			"InvoicingClient": map[string]any{
				"type": "object",
				"required": []string{
					"id",
					"name",
					"address",
					"vat_number",
					"default_currency",
					"terms_days",
					"vat_treatment",
					"retainer_amount",
					"day_rate",
					"created_at",
					"archived_at",
				},
				"properties": clientProperties(false),
			},
			"InvoicingClientRequest": map[string]any{
				"type":                 "object",
				"required":             []string{"name", "address", "default_currency", "terms_days", "vat_treatment"},
				"properties":           clientRequestProperties(false),
				"additionalProperties": false,
			},
			"InvoicingClientPatch": map[string]any{
				"type":                 "object",
				"properties":           clientRequestProperties(true),
				"additionalProperties": false,
			},
			"InvoicingClientsResponse": map[string]any{
				"type":     "object",
				"required": []string{"clients"},
				"properties": map[string]any{
					"clients": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/InvoicingClient"},
					},
				},
			},
		},
	}
}

func clientProperties(request bool) map[string]any {
	properties := clientRequestProperties(request)
	if !request {
		properties["id"] = map[string]any{"type": "string"}
		properties["created_at"] = map[string]any{"type": "string", "format": "date-time"}
		properties["archived_at"] = map[string]any{"type": "string", "format": "date-time", "nullable": true}
	}
	return properties
}

func clientRequestProperties(_ bool) map[string]any {
	return map[string]any{
		"name":             map[string]any{"type": "string", "minLength": 1},
		"address":          map[string]any{"$ref": "#/components/schemas/InvoicingAddress"},
		"vat_number":       map[string]any{"type": "string", "nullable": true},
		"default_currency": currencySchema(),
		"terms_days":       map[string]any{"type": "integer", "enum": []int{14, 30}},
		"vat_treatment": map[string]any{
			"type": "string",
			"enum": []string{string(VATTreatmentDomestic), string(VATTreatmentReverseChargeEUB2B)},
		},
		"retainer_amount": nullableSchemaRef("InvoicingMoneyAmount"),
		"day_rate":        nullableSchemaRef("InvoicingMoneyAmount"),
	}
}

func currencySchema() map[string]any {
	return map[string]any{
		"type": "string",
		"enum": []string{string(CurrencyEUR), string(CurrencyGBP)},
	}
}

func nullableSchemaRef(schema string) map[string]any {
	return map[string]any{
		"allOf": []map[string]any{
			{"$ref": "#/components/schemas/" + schema},
		},
		"nullable": true,
	}
}
