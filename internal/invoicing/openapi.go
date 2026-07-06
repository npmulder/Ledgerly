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
			"/api/invoicing/invoices": map[string]any{
				"get": map[string]any{
					"tags":        []string{"invoicing"},
					"summary":     "List invoices",
					"description": "Returns a paginated invoice list with status counts and filtered totals for list screens and CLI exports.",
					"operationId": "invoicingListInvoices",
					"security":    sessionSecurity(),
					"parameters":  invoiceListParameters(),
					"responses": map[string]any{
						"200": jsonResponseRef("Invoices listed", "InvoicingInvoicesResponse"),
						"400": problemResponse("Invalid invoice query"),
						"401": problemResponse("Authentication required"),
					},
				},
				"post": map[string]any{
					"tags":        []string{"invoicing"},
					"summary":     "Create a draft invoice",
					"operationId": "invoicingCreateDraftInvoice",
					"security":    sessionSecurity(),
					"requestBody": jsonRequestBodyRef("InvoicingCreateDraftInvoiceRequest"),
					"responses": map[string]any{
						"201": jsonResponseRef("Draft invoice created", "InvoicingInvoice"),
						"400": problemResponse("Malformed invoice request"),
						"401": problemResponse("Authentication required"),
						"404": problemResponse("Client was not found"),
						"413": problemResponse("Invoice request body is too large"),
						"422": validationProblemResponse("Invoice validation failed"),
					},
				},
			},
			"/api/invoicing/invoices/{id}": map[string]any{
				"get": map[string]any{
					"tags":        []string{"invoicing"},
					"summary":     "Return an invoice",
					"operationId": "invoicingGetInvoice",
					"security":    sessionSecurity(),
					"parameters":  invoiceIDParameter(),
					"responses": map[string]any{
						"200": jsonResponseRef("Invoice", "InvoicingInvoice"),
						"401": problemResponse("Authentication required"),
						"404": problemResponse("Invoice was not found"),
					},
				},
				"patch": map[string]any{
					"tags":        []string{"invoicing"},
					"summary":     "Autosave a draft invoice",
					"description": "Partially updates mutable draft fields. When lines are supplied, the client-generated line-id array replaces the current line array using last-write-wins semantics.",
					"operationId": "invoicingPatchInvoice",
					"security":    sessionSecurity(),
					"parameters":  invoiceIDParameter(),
					"requestBody": jsonRequestBodyRef("InvoicingInvoicePatch"),
					"responses": map[string]any{
						"200": jsonResponseRef("Updated invoice with recomputed totals", "InvoicingInvoice"),
						"400": problemResponse("Malformed invoice patch"),
						"401": problemResponse("Authentication required"),
						"404": problemResponse("Invoice was not found"),
						"409": validationProblemResponse("Invoice cannot be edited"),
						"413": problemResponse("Invoice patch request body is too large"),
						"422": validationProblemResponse("Invoice validation failed"),
					},
				},
			},
			"/api/invoicing/invoices/{id}/send": map[string]any{
				"post": map[string]any{
					"tags":        []string{"invoicing"},
					"summary":     "Send an invoice",
					"description": "Validates a complete draft, assigns the invoice number, locks the FX rate, posts the ledger entry, and returns the locked rate.",
					"operationId": "invoicingSendInvoice",
					"security":    sessionSecurity(),
					"parameters":  invoiceIDParameter(),
					"responses": map[string]any{
						"200": jsonResponseRef("Invoice sent", "InvoicingSendInvoiceResult"),
						"401": problemResponse("Authentication required"),
						"404": problemResponse("Invoice was not found"),
						"409": validationProblemResponse("Invoice cannot be sent"),
						"422": validationProblemResponse("Invoice validation failed"),
					},
				},
			},
			"/api/invoicing/invoices/{id}/revert": map[string]any{
				"post": map[string]any{
					"tags":        []string{"invoicing"},
					"summary":     "Revert a same-day sent invoice to draft",
					"description": "Un-sends an unsettled invoice only on the same day it was sent. No settlement endpoint is exposed in REST; banking calls the Go API in-transaction.",
					"operationId": "invoicingRevertInvoice",
					"security":    sessionSecurity(),
					"parameters":  invoiceIDParameter(),
					"responses": map[string]any{
						"200": jsonResponseRef("Invoice reverted to draft", "InvoicingInvoice"),
						"401": problemResponse("Authentication required"),
						"404": problemResponse("Invoice was not found"),
						"409": validationProblemResponse("Invoice cannot be reverted"),
					},
				},
			},
			"/api/invoicing/invoices/{id}/print": map[string]any{
				"get": map[string]any{
					"tags":        []string{"invoicing"},
					"summary":     "Return invoice print route payload",
					"description": "Returns the authoritative payload consumed by the React invoice print route.",
					"operationId": "invoicingGetInvoicePrintPayload",
					"security":    sessionSecurity(),
					"parameters":  invoicePrintPayloadParameters(),
					"responses": map[string]any{
						"200": jsonResponseRef("Invoice print payload", "InvoicingInvoicePrintPayload"),
						"400": problemResponse("Invalid print query"),
						"401": problemResponse("Authentication required"),
						"404": problemResponse("Invoice was not found"),
					},
				},
			},
			"/api/invoicing/invoices/{id}/pdf/preview": map[string]any{
				"get": map[string]any{
					"tags":        []string{"invoicing"},
					"summary":     "Render a draft invoice PDF preview",
					"description": "Renders a DRAFT-watermarked PDF on demand and never stores the result.",
					"operationId": "invoicingPreviewInvoicePDF",
					"security":    sessionSecurity(),
					"parameters":  invoiceIDParameter(),
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Draft PDF preview",
							"content": map[string]any{
								"application/pdf": map[string]any{
									"schema": map[string]any{"type": "string", "format": "binary"},
								},
							},
						},
						"401": problemResponse("Authentication required"),
						"404": problemResponse("Invoice was not found"),
					},
				},
			},
			"/api/invoicing/invoices/{id}/pdf/render": map[string]any{
				"post": map[string]any{
					"tags":        []string{"invoicing"},
					"summary":     "Render and store an invoice PDF",
					"description": "Explicit recovery action for render failures. If an immutable PDF is already stored, it is returned unchanged.",
					"operationId": "invoicingRenderInvoicePDF",
					"security":    sessionSecurity(),
					"parameters":  invoiceIDParameter(),
					"responses": map[string]any{
						"200": jsonResponseRef("Invoice with PDF asset", "InvoicingInvoice"),
						"401": problemResponse("Authentication required"),
						"404": problemResponse("Invoice was not found"),
					},
				},
			},
			"/api/invoicing/invoices/{id}/pdf": map[string]any{
				"get": map[string]any{
					"tags":        []string{"invoicing"},
					"summary":     "Redirect to an invoice PDF asset",
					"description": "Redirects to the immutable stored invoice PDF asset.",
					"operationId": "invoicingGetInvoicePDF",
					"security":    sessionSecurity(),
					"parameters":  invoiceIDParameter(),
					"responses": map[string]any{
						"302": map[string]any{
							"description": "Redirect to stored invoice PDF asset",
							"headers": map[string]any{
								"Location": map[string]any{
									"schema": map[string]any{"type": "string", "format": "uri-reference"},
								},
							},
						},
						"401": problemResponse("Authentication required"),
						"404": validationProblemResponse("Invoice PDF asset was not found"),
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

func invoiceIDParameter() []map[string]any {
	return []map[string]any{
		{
			"name":     "id",
			"in":       "path",
			"required": true,
			"schema":   map[string]any{"type": "string"},
		},
	}
}

func invoicePrintPayloadParameters() []map[string]any {
	return append(invoiceIDParameter(), map[string]any{
		"name":        "draft",
		"in":          "query",
		"required":    false,
		"description": "Render the payload with the draft watermark enabled.",
		"schema":      map[string]any{"type": "boolean", "default": false},
	})
}

func invoiceListParameters() []map[string]any {
	return []map[string]any{
		{
			"name":        "status",
			"in":          "query",
			"required":    false,
			"description": "Filter by one or more invoice statuses. Repeat the parameter or pass a comma-separated value.",
			"schema": map[string]any{
				"type":  "array",
				"items": invoiceStatusSchema(),
			},
			"style":   "form",
			"explode": true,
		},
		{
			"name":        "search",
			"in":          "query",
			"required":    false,
			"description": "Search invoice numbers and client names.",
			"schema":      map[string]any{"type": "string"},
		},
		{
			"name":        "limit",
			"in":          "query",
			"required":    false,
			"description": "Page size.",
			"schema": map[string]any{
				"type":    "integer",
				"minimum": 1,
				"maximum": MaxInvoiceListLimit,
				"default": DefaultInvoiceListLimit,
			},
		},
		{
			"name":        "offset",
			"in":          "query",
			"required":    false,
			"description": "Zero-based row offset.",
			"schema":      map[string]any{"type": "integer", "minimum": 0, "default": 0},
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
			"InvoicingCreateDraftInvoiceRequest": map[string]any{
				"type":                 "object",
				"required":             []string{"client_id"},
				"properties":           map[string]any{"client_id": map[string]any{"type": "string", "minLength": 1}},
				"additionalProperties": false,
			},
			"InvoicingMoney": map[string]any{
				"type":     "object",
				"required": []string{"amount", "currency"},
				"properties": map[string]any{
					"amount":   map[string]any{"type": "integer", "format": "int64"},
					"currency": currencySchema(),
				},
				"additionalProperties": false,
			},
			"InvoicingFXRate": map[string]any{
				"type":     "object",
				"required": []string{"from", "to", "value", "rate_date", "source"},
				"properties": map[string]any{
					"from":      currencySchema(),
					"to":        currencySchema(),
					"value":     map[string]any{"type": "string"},
					"rate_date": map[string]any{"type": "string", "format": "date-time"},
					"source":    map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"InvoicingGBPApprox": map[string]any{
				"type":     "object",
				"required": []string{"amount", "rate", "as_of", "locked"},
				"properties": map[string]any{
					"amount": map[string]any{"$ref": "#/components/schemas/InvoicingMoney"},
					"rate":   map[string]any{"$ref": "#/components/schemas/InvoicingFXRate"},
					"as_of":  map[string]any{"type": "string", "format": "date-time"},
					"locked": map[string]any{"type": "boolean"},
				},
				"additionalProperties": false,
			},
			"InvoicingInvoiceTotals": map[string]any{
				"type":     "object",
				"required": []string{"subtotal", "vat", "total"},
				"properties": map[string]any{
					"subtotal": map[string]any{"$ref": "#/components/schemas/InvoicingMoney"},
					"vat":      map[string]any{"$ref": "#/components/schemas/InvoicingMoney"},
					"total":    map[string]any{"$ref": "#/components/schemas/InvoicingMoney"},
					"approx_gbp": map[string]any{
						"allOf":    []map[string]any{{"$ref": "#/components/schemas/InvoicingGBPApprox"}},
						"nullable": true,
					},
				},
				"additionalProperties": false,
			},
			"InvoicingInvoiceLine": map[string]any{
				"type":     "object",
				"required": []string{"id", "invoice_id", "position", "description", "qty", "unit_price", "line_total"},
				"properties": map[string]any{
					"id":          map[string]any{"type": "string"},
					"invoice_id":  map[string]any{"type": "string"},
					"position":    map[string]any{"type": "integer", "minimum": 1},
					"description": map[string]any{"type": "string"},
					"qty":         map[string]any{"type": "string", "pattern": "^[0-9]+(\\.[0-9]+)?$"},
					"unit_price":  map[string]any{"$ref": "#/components/schemas/InvoicingMoney"},
					"line_total":  map[string]any{"$ref": "#/components/schemas/InvoicingMoney"},
				},
				"additionalProperties": false,
			},
			"InvoicingInvoiceLineInput": map[string]any{
				"type":     "object",
				"required": []string{"id", "description", "qty", "unit_price"},
				"properties": map[string]any{
					"id":          map[string]any{"type": "string", "minLength": 1},
					"description": map[string]any{"type": "string", "minLength": 1},
					"qty":         map[string]any{"type": "string", "pattern": "^[0-9]+(\\.[0-9]+)?$"},
					"unit_price":  map[string]any{"$ref": "#/components/schemas/InvoicingMoney"},
				},
				"additionalProperties": false,
			},
			"InvoicingInvoice": map[string]any{
				"type": "object",
				"required": []string{
					"id",
					"number",
					"client_id",
					"status",
					"issue_date",
					"due_date",
					"currency",
					"lock_id",
					"vat_treatment",
					"settlement_txn_ref",
					"settled_date",
					"settled_amount",
					"pdf_asset",
					"lines",
					"totals",
					"created_at",
					"updated_at",
				},
				"properties":           invoiceProperties(),
				"additionalProperties": false,
			},
			"InvoicingInvoicePrintIdentity": map[string]any{
				"type": "object",
				"required": []string{
					"trading_name",
					"legal_name",
					"company_number",
					"address",
					"iban",
					"bic",
					"bank_name",
				},
				"properties": map[string]any{
					"trading_name":   map[string]any{"type": "string"},
					"legal_name":     map[string]any{"type": "string"},
					"company_number": map[string]any{"type": "string"},
					"address":        map[string]any{"$ref": "#/components/schemas/InvoicingAddress"},
					"vat_number":     map[string]any{"type": "string", "nullable": true},
					"iban":           map[string]any{"type": "string"},
					"bic":            map[string]any{"type": "string"},
					"bank_name":      map[string]any{"type": "string"},
					"logo_asset_url": map[string]any{"type": "string", "format": "uri-reference", "nullable": true},
					"logo_data_uri":  map[string]any{"type": "string", "nullable": true},
				},
				"additionalProperties": false,
			},
			"InvoicingInvoicePrintPayload": map[string]any{
				"type": "object",
				"required": []string{
					"invoice",
					"client",
					"identity",
					"vat_rate",
					"vat_tax_year",
					"draft_watermark",
				},
				"properties": map[string]any{
					"invoice":             map[string]any{"$ref": "#/components/schemas/InvoicingInvoice"},
					"client":              map[string]any{"$ref": "#/components/schemas/InvoicingClient"},
					"identity":            map[string]any{"$ref": "#/components/schemas/InvoicingInvoicePrintIdentity"},
					"vat_rate":            map[string]any{"type": "string"},
					"vat_tax_year":        map[string]any{"type": "string"},
					"reverse_charge_note": map[string]any{"type": "string", "nullable": true},
					"locked_rate":         nullableSchemaRef("InvoicingLockedRate"),
					"draft_watermark":     map[string]any{"type": "boolean"},
				},
				"additionalProperties": false,
			},
			"InvoicingInvoiceListItem": map[string]any{
				"type": "object",
				"required": []string{
					"id",
					"number",
					"client_id",
					"client_name",
					"status",
					"issue_date",
					"due_date",
					"days_overdue",
					"currency",
					"totals",
					"created_at",
					"updated_at",
				},
				"properties":           invoiceListItemProperties(),
				"additionalProperties": false,
			},
			"InvoicingInvoiceStatusCount": map[string]any{
				"type":     "object",
				"required": []string{"status", "count"},
				"properties": map[string]any{
					"status": invoiceStatusSchema(),
					"count":  map[string]any{"type": "integer", "minimum": 0},
				},
				"additionalProperties": false,
			},
			"InvoicingInvoiceTotalsSummary": map[string]any{
				"type":     "object",
				"required": []string{"subtotals", "total_gbp"},
				"properties": map[string]any{
					"subtotals": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/InvoicingMoney"},
					},
					"total_gbp": map[string]any{"$ref": "#/components/schemas/InvoicingMoney"},
				},
				"additionalProperties": false,
			},
			"InvoicingInvoicesResponse": map[string]any{
				"type":     "object",
				"required": []string{"invoices", "counts", "total_count", "totals", "limit", "offset"},
				"properties": map[string]any{
					"invoices": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/InvoicingInvoiceListItem"},
					},
					"counts": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/InvoicingInvoiceStatusCount"},
					},
					"total_count": map[string]any{"type": "integer", "minimum": 0},
					"totals":      map[string]any{"$ref": "#/components/schemas/InvoicingInvoiceTotalsSummary"},
					"limit":       map[string]any{"type": "integer", "minimum": 1},
					"offset":      map[string]any{"type": "integer", "minimum": 0},
				},
				"additionalProperties": false,
			},
			"InvoicingInvoicePatch": map[string]any{
				"type":                 "object",
				"properties":           invoicePatchProperties(),
				"additionalProperties": false,
			},
			"InvoicingLockedRate": map[string]any{
				"type":     "object",
				"required": []string{"id", "rate"},
				"properties": map[string]any{
					"id":   map[string]any{"type": "integer", "format": "int64"},
					"rate": map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"InvoicingSendInvoiceResult": map[string]any{
				"type":     "object",
				"required": []string{"invoice", "number", "locked_rate"},
				"properties": map[string]any{
					"invoice":     map[string]any{"$ref": "#/components/schemas/InvoicingInvoice"},
					"number":      map[string]any{"type": "string"},
					"locked_rate": map[string]any{"$ref": "#/components/schemas/InvoicingLockedRate"},
				},
				"additionalProperties": false,
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

func invoiceProperties() map[string]any {
	return map[string]any{
		"id":                 map[string]any{"type": "string"},
		"number":             map[string]any{"type": "string", "nullable": true},
		"client_id":          map[string]any{"type": "string"},
		"status":             invoiceStatusSchema(),
		"issue_date":         map[string]any{"type": "string", "format": "date-time"},
		"due_date":           map[string]any{"type": "string", "format": "date-time"},
		"currency":           currencySchema(),
		"lock_id":            map[string]any{"type": "string", "nullable": true},
		"sent_at":            map[string]any{"type": "string", "format": "date-time", "nullable": true},
		"vat_treatment":      vatTreatmentSchema(),
		"settlement_txn_ref": map[string]any{"type": "string", "nullable": true},
		"settled_date":       map[string]any{"type": "string", "format": "date-time", "nullable": true},
		"settled_amount": map[string]any{
			"allOf":    []map[string]any{{"$ref": "#/components/schemas/InvoicingMoney"}},
			"nullable": true,
		},
		"pdf_asset": map[string]any{"type": "string", "nullable": true},
		"lines": map[string]any{
			"type":  "array",
			"items": map[string]any{"$ref": "#/components/schemas/InvoicingInvoiceLine"},
		},
		"totals":     map[string]any{"$ref": "#/components/schemas/InvoicingInvoiceTotals"},
		"created_at": map[string]any{"type": "string", "format": "date-time"},
		"updated_at": map[string]any{"type": "string", "format": "date-time"},
	}
}

func invoiceListItemProperties() map[string]any {
	return map[string]any{
		"id":           map[string]any{"type": "string"},
		"number":       map[string]any{"type": "string", "nullable": true},
		"client_id":    map[string]any{"type": "string"},
		"client_name":  map[string]any{"type": "string"},
		"status":       invoiceStatusSchema(),
		"issue_date":   map[string]any{"type": "string", "format": "date-time"},
		"due_date":     map[string]any{"type": "string", "format": "date-time"},
		"days_overdue": map[string]any{"type": "integer", "minimum": 0},
		"currency":     currencySchema(),
		"totals":       map[string]any{"$ref": "#/components/schemas/InvoicingInvoiceTotals"},
		"created_at":   map[string]any{"type": "string", "format": "date-time"},
		"updated_at":   map[string]any{"type": "string", "format": "date-time"},
	}
}

func invoicePatchProperties() map[string]any {
	return map[string]any{
		"issue_date":    map[string]any{"type": "string", "format": "date"},
		"due_date":      map[string]any{"type": "string", "format": "date"},
		"currency":      currencySchema(),
		"vat_treatment": vatTreatmentSchema(),
		"lines": map[string]any{
			"type":  "array",
			"items": map[string]any{"$ref": "#/components/schemas/InvoicingInvoiceLineInput"},
		},
	}
}

func currencySchema() map[string]any {
	return map[string]any{
		"type": "string",
		"enum": []string{string(CurrencyEUR), string(CurrencyGBP)},
	}
}

func vatTreatmentSchema() map[string]any {
	return map[string]any{
		"type": "string",
		"enum": []string{string(VATTreatmentDomestic), string(VATTreatmentReverseChargeEUB2B)},
	}
}

func invoiceStatusSchema() map[string]any {
	return map[string]any{
		"type": "string",
		"enum": []string{
			string(InvoiceStatusDraft),
			string(InvoiceStatusSent),
			string(InvoiceStatusPaid),
			string(InvoiceStatusOverdue),
		},
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
