package dividends

import (
	"github.com/npmulder/ledgerly/internal/identity"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

// OpenAPIFragment returns the dividends module's OpenAPI contribution without
// requiring database-backed module construction.
func OpenAPIFragment() httpserver.OpenAPIFragment {
	return httpserver.OpenAPIFragment{
		Tags: []map[string]any{
			{"name": "dividends"},
		},
		Paths: map[string]any{
			"/api/dividends/headroom": map[string]any{
				"get": map[string]any{
					"tags":        []string{"dividends"},
					"summary":     "Return live dividend headroom",
					"description": "Returns the live distributable-reserves calculation lines rendered by the dividends screen.",
					"operationId": "dividendsGetHeadroom",
					"security":    dividendSessionSecurity(),
					"responses": map[string]any{
						"200": dividendsJSONResponseRef("Dividend headroom breakdown", "DividendsHeadroomBreakdown"),
						"401": dividendsProblemResponse("Authentication required"),
					},
				},
			},
			"/api/dividends/validate": map[string]any{
				"post": map[string]any{
					"tags":        []string{"dividends"},
					"summary":     "Validate a candidate dividend amount",
					"description": "Returns the validation-strip payload for a candidate dividend. Over-headroom candidates still return 200 with within_headroom=false so clients can render a blocking strip.",
					"operationId": "dividendsValidateAmount",
					"security":    dividendSessionSecurity(),
					"requestBody": dividendsJSONRequestBodyRef("DividendsAmountRequest"),
					"responses": map[string]any{
						"200": dividendsJSONResponseRef("Dividend validation strip payload", "DividendsValidationResult"),
						"400": dividendsProblemResponse("Malformed dividend amount request"),
						"401": dividendsProblemResponse("Authentication required"),
						"413": dividendsProblemResponse("Dividend amount request body is too large"),
						"422": dividendsValidationProblemResponse("Dividend amount validation failed"),
					},
				},
			},
			"/api/dividends/declare": map[string]any{
				"post": map[string]any{
					"tags":        []string{"dividends"},
					"summary":     "Declare a dividend",
					"description": "Creates an immutable dividend declaration, posts the retained-earnings and DLA entries, publishes declaration facts, and schedules voucher/minutes rendering.",
					"operationId": "dividendsDeclareAmount",
					"security":    dividendSessionSecurity(),
					"requestBody": dividendsJSONRequestBodyRef("DividendsAmountRequest"),
					"responses": map[string]any{
						"201": dividendsJSONResponseRef("Dividend declaration created", "DividendsDeclaration"),
						"400": dividendsProblemResponse("Malformed dividend amount request"),
						"401": dividendsProblemResponse("Authentication required"),
						"413": dividendsProblemResponse("Dividend amount request body is too large"),
						"422": dividendsValidationProblemResponse("Dividend declaration failed validation"),
					},
				},
			},
			"/api/dividends/history": map[string]any{
				"get": map[string]any{
					"tags":        []string{"dividends"},
					"summary":     "List dividend declaration history",
					"description": "Returns dividend declarations newest first.",
					"operationId": "dividendsGetHistory",
					"security":    dividendSessionSecurity(),
					"responses": map[string]any{
						"200": dividendsJSONResponseRef("Dividend declaration history", "DividendsHistoryResponse"),
						"401": dividendsProblemResponse("Authentication required"),
					},
				},
			},
			"/api/dividends/{id}/voucher": map[string]any{
				"get": map[string]any{
					"tags":        []string{"dividends"},
					"summary":     "Redirect to a stored dividend voucher PDF asset",
					"description": "Redirects to the immutable stored dividend voucher PDF asset.",
					"operationId": "dividendsGetVoucherPDFByID",
					"security":    dividendSessionSecurity(),
					"parameters":  declarationIDParameter(),
					"responses": map[string]any{
						"302": dividendRedirectResponse("Redirect to stored voucher PDF asset"),
						"401": dividendsProblemResponse("Authentication required"),
						"404": dividendsValidationProblemResponse("Voucher PDF asset was not found"),
					},
				},
			},
			"/api/dividends/{id}/minutes": map[string]any{
				"get": map[string]any{
					"tags":        []string{"dividends"},
					"summary":     "Redirect to a stored board minutes PDF asset",
					"description": "Redirects to the immutable stored board minutes PDF asset.",
					"operationId": "dividendsGetMinutesPDFByID",
					"security":    dividendSessionSecurity(),
					"parameters":  declarationIDParameter(),
					"responses": map[string]any{
						"302": dividendRedirectResponse("Redirect to stored board minutes PDF asset"),
						"401": dividendsProblemResponse("Authentication required"),
						"404": dividendsValidationProblemResponse("Board minutes PDF asset was not found"),
					},
				},
			},
			"/api/dividends/declarations/{id}/print": map[string]any{
				"get": map[string]any{
					"tags":        []string{"dividends"},
					"summary":     "Return dividend document print route payload",
					"description": "Returns the declaration-time snapshot payload consumed by the React dividend voucher and board-minutes print routes.",
					"operationId": "dividendsGetDeclarationDocumentPayload",
					"security":    dividendSessionSecurity(),
					"parameters":  declarationIDParameter(),
					"responses": map[string]any{
						"200": dividendsJSONResponseRef("Dividend document payload", "DividendsDocumentPayload"),
						"401": dividendsProblemResponse("Authentication required"),
						"404": dividendsProblemResponse("Declaration was not found"),
						"422": dividendsValidationProblemResponse("Declaration is missing document snapshots"),
					},
				},
			},
			"/api/dividends/declarations/{id}/documents/render": map[string]any{
				"post": map[string]any{
					"tags":        []string{"dividends"},
					"summary":     "Render and store dividend documents",
					"description": "Explicit recovery action for render failures. If immutable voucher and minutes assets are already stored, they are returned unchanged.",
					"operationId": "dividendsRenderDeclarationDocuments",
					"security":    dividendSessionSecurity(),
					"parameters":  declarationIDParameter(),
					"responses": map[string]any{
						"200": dividendsJSONResponseRef("Declaration with document assets", "DividendsDeclaration"),
						"401": dividendsProblemResponse("Authentication required"),
						"404": dividendsProblemResponse("Declaration was not found"),
						"422": dividendsValidationProblemResponse("Declaration cannot be rendered"),
					},
				},
			},
			"/api/dividends/declarations/{id}/voucher": map[string]any{
				"get": map[string]any{
					"tags":        []string{"dividends"},
					"summary":     "Redirect to a stored dividend voucher PDF asset",
					"description": "Redirects to the immutable stored dividend voucher PDF asset.",
					"operationId": "dividendsGetVoucherPDF",
					"security":    dividendSessionSecurity(),
					"parameters":  declarationIDParameter(),
					"responses": map[string]any{
						"302": dividendRedirectResponse("Redirect to stored voucher PDF asset"),
						"401": dividendsProblemResponse("Authentication required"),
						"404": dividendsValidationProblemResponse("Voucher PDF asset was not found"),
					},
				},
			},
			"/api/dividends/declarations/{id}/minutes": map[string]any{
				"get": map[string]any{
					"tags":        []string{"dividends"},
					"summary":     "Redirect to a stored board minutes PDF asset",
					"description": "Redirects to the immutable stored board minutes PDF asset.",
					"operationId": "dividendsGetMinutesPDF",
					"security":    dividendSessionSecurity(),
					"parameters":  declarationIDParameter(),
					"responses": map[string]any{
						"302": dividendRedirectResponse("Redirect to stored board minutes PDF asset"),
						"401": dividendsProblemResponse("Authentication required"),
						"404": dividendsValidationProblemResponse("Board minutes PDF asset was not found"),
					},
				},
			},
		},
		Components: dividendsComponents(),
	}
}

func declarationIDParameter() []map[string]any {
	return []map[string]any{
		{
			"name":     "id",
			"in":       "path",
			"required": true,
			"schema":   map[string]any{"type": "string"},
		},
	}
}

func dividendsJSONRequestBodyRef(schema string) map[string]any {
	return map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/" + schema},
			},
		},
	}
}

func dividendsJSONResponseRef(description string, schema string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/" + schema},
			},
		},
	}
}

func dividendsProblemResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			httpserver.ProblemContentType: map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/Problem"},
			},
		},
	}
}

func dividendsValidationProblemResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			httpserver.ProblemContentType: map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/DividendsValidationProblem"},
			},
		},
	}
}

func dividendRedirectResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"headers": map[string]any{
			"Location": map[string]any{
				"schema": map[string]any{"type": "string", "format": "uri-reference"},
			},
		},
	}
}

func dividendSessionSecurity() []map[string][]string {
	return []map[string][]string{
		{"sessionCookie": []string{}},
	}
}

func dividendsComponents() map[string]any {
	return map[string]any{
		"securitySchemes": map[string]any{
			"sessionCookie": map[string]any{
				"type": "apiKey",
				"in":   "cookie",
				"name": identity.SessionCookieName,
			},
		},
		"schemas": map[string]any{
			"DividendsMoney": map[string]any{
				"type":     "object",
				"required": []string{"amount", "currency"},
				"properties": map[string]any{
					"amount":   map[string]any{"type": "integer", "format": "int64"},
					"currency": map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"DividendsAddress": map[string]any{
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
			"DividendsMoneyLine": map[string]any{
				"type":     "object",
				"required": []string{"label", "amount"},
				"properties": map[string]any{
					"label":  map[string]any{"type": "string"},
					"amount": map[string]any{"$ref": "#/components/schemas/DividendsMoney"},
				},
				"additionalProperties": false,
			},
			"DividendsHeadroomBreakdown": map[string]any{
				"type":     "object",
				"required": []string{"as_of", "financial_year", "lines", "available", "distributable"},
				"properties": map[string]any{
					"as_of":          map[string]any{"type": "string", "format": "date-time"},
					"financial_year": map[string]any{"type": "string"},
					"lines": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/DividendsMoneyLine"},
					},
					"available":     map[string]any{"$ref": "#/components/schemas/DividendsMoney"},
					"distributable": map[string]any{"type": "boolean"},
				},
				"additionalProperties": false,
			},
			"DividendsAmountRequest": map[string]any{
				"type":     "object",
				"required": []string{"amount"},
				"properties": map[string]any{
					"amount": map[string]any{"$ref": "#/components/schemas/DividendsMoney"},
				},
				"additionalProperties": false,
			},
			"DividendsWithholdingValidation": map[string]any{
				"type":     "object",
				"required": []string{"tax_year", "policy", "applies", "informational"},
				"properties": map[string]any{
					"tax_year":      map[string]any{"type": "string"},
					"policy":        map[string]any{"type": "string"},
					"applies":       map[string]any{"type": "boolean"},
					"informational": map[string]any{"type": "boolean"},
				},
				"additionalProperties": false,
			},
			"DividendsPersonalTaxValidation": map[string]any{
				"type":     "object",
				"required": []string{"tax_year", "prior_ytd", "with_dividend", "marginal", "message"},
				"properties": map[string]any{
					"tax_year":      map[string]any{"type": "string"},
					"prior_ytd":     map[string]any{"$ref": "#/components/schemas/DividendsMoney"},
					"with_dividend": map[string]any{"$ref": "#/components/schemas/DividendsMoney"},
					"marginal":      map[string]any{"$ref": "#/components/schemas/DividendsMoney"},
					"message":       map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"DividendsValidationResult": map[string]any{
				"type": "object",
				"required": []string{
					"amount",
					"headroom",
					"within_headroom",
					"distributable",
					"distributable_total",
					"withholding",
					"personal_tax",
				},
				"properties": map[string]any{
					"amount":              map[string]any{"$ref": "#/components/schemas/DividendsMoney"},
					"headroom":            map[string]any{"$ref": "#/components/schemas/DividendsHeadroomBreakdown"},
					"within_headroom":     map[string]any{"type": "boolean"},
					"distributable":       map[string]any{"type": "boolean"},
					"distributable_total": map[string]any{"$ref": "#/components/schemas/DividendsMoney"},
					"withholding":         map[string]any{"$ref": "#/components/schemas/DividendsWithholdingValidation"},
					"personal_tax":        map[string]any{"$ref": "#/components/schemas/DividendsPersonalTaxValidation"},
				},
				"additionalProperties": false,
			},
			"DividendsHistoryResponse": map[string]any{
				"type":     "object",
				"required": []string{"declarations"},
				"properties": map[string]any{
					"declarations": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/DividendsDeclaration"},
					},
				},
				"additionalProperties": false,
			},
			"DividendsFieldError": map[string]any{
				"type":     "object",
				"required": []string{"pointer", "detail"},
				"properties": map[string]any{
					"pointer": map[string]any{"type": "string"},
					"detail":  map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"DividendsValidationProblem": map[string]any{
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
						"items": map[string]any{"$ref": "#/components/schemas/DividendsFieldError"},
					},
					"distributable_total": map[string]any{"$ref": "#/components/schemas/DividendsMoney"},
				},
			},
			"DividendsCompanySnapshot": map[string]any{
				"type":     "object",
				"required": []string{"trading_name", "legal_name", "company_number", "registered_office", "director_name"},
				"properties": map[string]any{
					"trading_name":      map[string]any{"type": "string"},
					"legal_name":        map[string]any{"type": "string"},
					"company_number":    map[string]any{"type": "string"},
					"registered_office": map[string]any{"$ref": "#/components/schemas/DividendsAddress"},
					"director_name":     map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"DividendsShareholderSnapshot": map[string]any{
				"type":     "object",
				"required": []string{"name", "shares", "class"},
				"properties": map[string]any{
					"name":   map[string]any{"type": "string"},
					"shares": map[string]any{"type": "integer", "format": "int64"},
					"class":  map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"DividendsWithholdingSnapshot": map[string]any{
				"type":     "object",
				"required": []string{"tax_year", "policy", "note"},
				"properties": map[string]any{
					"tax_year": map[string]any{"type": "string"},
					"policy":   map[string]any{"type": "string"},
					"note":     map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"DividendsDeclaration": map[string]any{
				"type": "object",
				"required": []string{
					"id",
					"declared_date",
					"amount",
					"per_share",
					"shares",
					"shareholder_name",
					"voucher_asset",
					"minutes_asset",
					"created_at",
				},
				"properties": map[string]any{
					"id":               map[string]any{"type": "string"},
					"declared_date":    map[string]any{"type": "string", "format": "date-time"},
					"amount":           map[string]any{"$ref": "#/components/schemas/DividendsMoney"},
					"per_share":        map[string]any{"$ref": "#/components/schemas/DividendsMoney"},
					"shares":           map[string]any{"type": "integer", "format": "int64"},
					"shareholder_name": map[string]any{"type": "string"},
					"company_snapshot": map[string]any{
						"allOf":    []map[string]any{{"$ref": "#/components/schemas/DividendsCompanySnapshot"}},
						"nullable": true,
					},
					"shareholder_snapshot": map[string]any{
						"allOf":    []map[string]any{{"$ref": "#/components/schemas/DividendsShareholderSnapshot"}},
						"nullable": true,
					},
					"headroom_snapshot": map[string]any{
						"allOf":    []map[string]any{{"$ref": "#/components/schemas/DividendsHeadroomBreakdown"}},
						"nullable": true,
					},
					"withholding_snapshot": map[string]any{
						"allOf":    []map[string]any{{"$ref": "#/components/schemas/DividendsWithholdingSnapshot"}},
						"nullable": true,
					},
					"voucher_asset": map[string]any{"type": "string", "nullable": true},
					"minutes_asset": map[string]any{"type": "string", "nullable": true},
					"created_at":    map[string]any{"type": "string", "format": "date-time"},
				},
				"additionalProperties": false,
			},
			"DividendsDocumentPayload": map[string]any{
				"type":     "object",
				"required": []string{"declaration"},
				"properties": map[string]any{
					"declaration": map[string]any{"$ref": "#/components/schemas/DividendsDeclaration"},
				},
				"additionalProperties": false,
			},
		},
	}
}
