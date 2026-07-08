package identity

import httpserver "github.com/npmulder/ledgerly/internal/platform/http"

func OpenAPIFragment() httpserver.OpenAPIFragment {
	return httpserver.OpenAPIFragment{
		Tags: []map[string]any{
			{"name": "identity"},
		},
		Paths: map[string]any{
			"/api/identity/register": map[string]any{
				"post": map[string]any{
					"tags":        []string{"identity"},
					"summary":     "Register the first owner account",
					"description": "Allowed only while no users exist.",
					"operationId": "identityRegister",
					"requestBody": jsonRequestBodyRef("IdentityRegisterRequest"),
					"responses": map[string]any{
						"201": jsonResponseRef("Owner created", "IdentityUser"),
						"400": problemResponse("Invalid registration request"),
						"403": problemResponse("Registration is closed"),
						"413": problemResponse("Registration request body is too large"),
					},
				},
			},
			"/api/identity/register-with-profile": map[string]any{
				"post": map[string]any{
					"tags":        []string{"identity"},
					"summary":     "Register the first owner account and company profile",
					"description": "Allowed only while no users and no company profile exist.",
					"operationId": "identityRegisterWithProfile",
					"requestBody": jsonRequestBodyRef("IdentityRegisterWithProfileRequest"),
					"responses": map[string]any{
						"201": jsonResponseRef("Owner and company profile created", "IdentityRegisterWithProfileResult"),
						"400": validationProblemResponse("Invalid first-run registration request"),
						"403": problemResponse("Registration is closed"),
						"413": problemResponse("Registration request body is too large"),
					},
				},
			},
			"/api/identity/login": map[string]any{
				"post": map[string]any{
					"tags":        []string{"identity"},
					"summary":     "Open a browser session",
					"operationId": "identityLogin",
					"requestBody": jsonRequestBodyRef("IdentityLoginRequest"),
					"responses": map[string]any{
						"200": jsonResponseRef("Session opened", "IdentityUser"),
						"400": problemResponse("Invalid login request"),
						"401": problemResponse("Invalid credentials"),
						"413": problemResponse("Login request body is too large"),
						"429": problemResponse("Too many login attempts"),
					},
				},
			},
			"/api/identity/logout": map[string]any{
				"post": map[string]any{
					"tags":        []string{"identity"},
					"summary":     "Close the current browser session",
					"operationId": "identityLogout",
					"security":    sessionSecurity(),
					"responses": map[string]any{
						"204": map[string]any{"description": "Session closed"},
						"401": problemResponse("Authentication required"),
					},
				},
			},
			"/api/identity/me": map[string]any{
				"get": map[string]any{
					"tags":        []string{"identity"},
					"summary":     "Return the current user",
					"operationId": "identityCurrentUser",
					"security":    authSecurity(),
					"responses": map[string]any{
						"200": jsonResponseRef("Authenticated user", "IdentityUser"),
						"401": problemResponse("Authentication required"),
					},
				},
			},
			"/api/identity/pats": map[string]any{
				"get": map[string]any{
					"tags":        []string{"identity"},
					"summary":     "List personal access tokens",
					"operationId": "identityListPATs",
					"security":    authSecurity(),
					"responses": map[string]any{
						"200": jsonResponseRef("Personal access tokens", "IdentityPATListResponse"),
						"401": problemResponse("Authentication required"),
					},
				},
				"post": map[string]any{
					"tags":        []string{"identity"},
					"summary":     "Create a personal access token",
					"operationId": "identityCreatePAT",
					"security":    authSecurity(),
					"requestBody": jsonRequestBodyRef("IdentityPATCreateRequest"),
					"responses": map[string]any{
						"201": jsonResponseRef("Personal access token created", "IdentityPATCreateResponse"),
						"400": problemResponse("Invalid personal access token request"),
						"401": problemResponse("Authentication required"),
						"403": problemResponse("Read-only token cannot create personal access tokens"),
						"413": problemResponse("Personal access token request body is too large"),
					},
				},
			},
			"/api/identity/pats/{id}": map[string]any{
				"delete": map[string]any{
					"tags":        []string{"identity"},
					"summary":     "Revoke a personal access token",
					"operationId": "identityRevokePAT",
					"security":    authSecurity(),
					"parameters": []map[string]any{
						{
							"name":     "id",
							"in":       "path",
							"required": true,
							"schema":   map[string]any{"type": "integer", "format": "int64"},
						},
					},
					"responses": map[string]any{
						"204": map[string]any{"description": "Personal access token revoked"},
						"400": problemResponse("Invalid personal access token id"),
						"401": problemResponse("Authentication required"),
						"403": problemResponse("Read-only token cannot revoke personal access tokens"),
					},
				},
			},
			"/api/identity/profile": map[string]any{
				"get": map[string]any{
					"tags":        []string{"identity"},
					"summary":     "Return the company identity profile",
					"operationId": "identityGetProfile",
					"security":    authSecurity(),
					"responses": map[string]any{
						"200": jsonResponseRef("Company profile", "IdentityProfile"),
						"401": problemResponse("Authentication required"),
						"404": problemResponse("Company profile was not found"),
					},
				},
				"patch": map[string]any{
					"tags":        []string{"identity"},
					"summary":     "Partially update the company identity profile",
					"operationId": "identityPatchProfile",
					"security":    authSecurity(),
					"requestBody": jsonRequestBodyRef("IdentityProfilePatch"),
					"responses": map[string]any{
						"200": jsonResponseRef("Updated company profile", "IdentityProfile"),
						"400": problemResponse("Malformed profile patch"),
						"401": problemResponse("Authentication required"),
						"404": problemResponse("Company profile was not found"),
						"413": problemResponse("Profile patch request body is too large"),
						"422": validationProblemResponse("Profile validation failed"),
					},
				},
			},
			"/api/identity/logo": map[string]any{
				"put": map[string]any{
					"tags":        []string{"identity"},
					"summary":     "Replace the company logo",
					"operationId": "identityReplaceLogo",
					"security":    authSecurity(),
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"multipart/form-data": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/IdentityLogoUploadRequest"},
							},
						},
					},
					"responses": map[string]any{
						"200": jsonResponseRef("Logo replaced", "IdentityLogoUploadResponse"),
						"400": problemResponse("Malformed logo upload"),
						"401": problemResponse("Authentication required"),
						"413": problemResponse("Logo upload is too large"),
						"415": problemResponse("Logo MIME type is not supported"),
					},
				},
			},
			"/api/identity/assets/{id}": map[string]any{
				"get": map[string]any{
					"tags":        []string{"identity"},
					"summary":     "Return a content-addressed identity asset",
					"operationId": "identityGetAsset",
					"security":    authSecurity(),
					"parameters": []map[string]any{
						{
							"name":     "id",
							"in":       "path",
							"required": true,
							"schema":   map[string]any{"type": "string", "format": "uuid"},
						},
					},
					"responses": map[string]any{
						"200": assetResponse(),
						"401": problemResponse("Authentication required"),
						"404": problemResponse("Asset was not found"),
					},
				},
			},
		},
		Components: identityComponents(),
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

func assetResponse() map[string]any {
	binarySchema := map[string]any{"type": "string", "format": "binary"}
	return map[string]any{
		"description": "Asset bytes",
		"headers": map[string]any{
			"Cache-Control": map[string]any{
				"description": "Immutable cache directive for content-addressed assets",
				"schema":      map[string]any{"type": "string"},
			},
		},
		"content": map[string]any{
			"image/png":  map[string]any{"schema": binarySchema},
			"image/jpeg": map[string]any{"schema": binarySchema},
		},
	}
}

func sessionSecurity() []map[string][]string {
	return []map[string][]string{
		{"sessionCookie": []string{}},
	}
}

func authSecurity() []map[string][]string {
	return []map[string][]string{
		{"sessionCookie": []string{}},
		{"patBearer": []string{}},
	}
}

func identityComponents() map[string]any {
	return map[string]any{
		"securitySchemes": map[string]any{
			"sessionCookie": map[string]any{
				"type": "apiKey",
				"in":   "cookie",
				"name": SessionCookieName,
			},
			"patBearer": map[string]any{
				"type":         "http",
				"scheme":       "bearer",
				"bearerFormat": "lgy_PAT",
			},
		},
		"schemas": map[string]any{
			"IdentityPATScope": map[string]any{
				"type": "string",
				"enum": []string{"read-only", "full"},
			},
			"IdentityRegisterRequest": map[string]any{
				"type":     "object",
				"required": []string{"email", "password", "name"},
				"properties": map[string]any{
					"email":    map[string]any{"type": "string", "format": "email"},
					"password": map[string]any{"type": "string", "format": "password", "minLength": 1},
					"name":     map[string]any{"type": "string", "minLength": 1},
				},
				"additionalProperties": false,
			},
			"IdentityRegisterWithProfileRequest": map[string]any{
				"type": "object",
				"required": []string{
					"email",
					"password",
					"name",
					"trading_name",
					"legal_name",
					"company_number",
					"registered_office",
					"incorporation_date",
					"year_end_month",
					"year_end_day",
				},
				"properties": map[string]any{
					"email":              map[string]any{"type": "string", "format": "email"},
					"password":           map[string]any{"type": "string", "format": "password", "minLength": 1},
					"name":               map[string]any{"type": "string", "minLength": 1},
					"trading_name":       map[string]any{"type": "string", "minLength": 1},
					"legal_name":         map[string]any{"type": "string", "minLength": 1},
					"company_number":     map[string]any{"type": "string", "minLength": 1},
					"registered_office":  map[string]any{"$ref": "#/components/schemas/RegisteredOffice"},
					"incorporation_date": map[string]any{"type": "string", "format": "date"},
					"year_end_month":     map[string]any{"type": "integer", "minimum": 1, "maximum": 12},
					"year_end_day":       map[string]any{"type": "integer", "minimum": 1, "maximum": 31},
					"directors": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/Director"},
					},
				},
				"additionalProperties": false,
			},
			"IdentityRegisterWithProfileResult": map[string]any{
				"type":     "object",
				"required": []string{"user", "profile"},
				"properties": map[string]any{
					"user":    map[string]any{"$ref": "#/components/schemas/IdentityUser"},
					"profile": map[string]any{"$ref": "#/components/schemas/IdentityProfile"},
				},
			},
			"IdentityLoginRequest": map[string]any{
				"type":     "object",
				"required": []string{"email", "password"},
				"properties": map[string]any{
					"email":    map[string]any{"type": "string", "format": "email"},
					"password": map[string]any{"type": "string", "format": "password", "minLength": 1},
				},
				"additionalProperties": false,
			},
			"IdentityUser": map[string]any{
				"type":     "object",
				"required": []string{"id", "email", "name", "created_at"},
				"properties": map[string]any{
					"id":         map[string]any{"type": "integer", "format": "int64"},
					"email":      map[string]any{"type": "string", "format": "email"},
					"name":       map[string]any{"type": "string"},
					"created_at": map[string]any{"type": "string", "format": "date-time"},
					"token_name": map[string]any{"type": "string", "nullable": true},
					"token_scope": map[string]any{
						"allOf":    []map[string]any{{"$ref": "#/components/schemas/IdentityPATScope"}},
						"nullable": true,
					},
				},
			},
			"IdentityPAT": map[string]any{
				"type":     "object",
				"required": []string{"id", "name", "scope", "created_at", "last_used_at", "expires_at"},
				"properties": map[string]any{
					"id":           map[string]any{"type": "integer", "format": "int64"},
					"name":         map[string]any{"type": "string"},
					"scope":        map[string]any{"$ref": "#/components/schemas/IdentityPATScope"},
					"created_at":   map[string]any{"type": "string", "format": "date-time"},
					"last_used_at": map[string]any{"type": "string", "format": "date-time", "nullable": true},
					"expires_at":   map[string]any{"type": "string", "format": "date-time", "nullable": true},
				},
			},
			"IdentityPATCreateRequest": map[string]any{
				"type":     "object",
				"required": []string{"name", "scope"},
				"properties": map[string]any{
					"name":       map[string]any{"type": "string", "minLength": 1},
					"scope":      map[string]any{"$ref": "#/components/schemas/IdentityPATScope"},
					"expires_at": map[string]any{"type": "string", "format": "date-time", "nullable": true},
				},
				"additionalProperties": false,
			},
			"IdentityPATCreateResponse": map[string]any{
				"type":     "object",
				"required": []string{"personal_access_token", "token"},
				"properties": map[string]any{
					"personal_access_token": map[string]any{"$ref": "#/components/schemas/IdentityPAT"},
					"token":                 map[string]any{"type": "string"},
				},
			},
			"IdentityPATListResponse": map[string]any{
				"type":     "object",
				"required": []string{"tokens"},
				"properties": map[string]any{
					"tokens": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/IdentityPAT"},
					},
				},
			},
			"RegisteredOffice": map[string]any{
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
			"YearEnd": map[string]any{
				"type":     "object",
				"required": []string{"month", "day"},
				"properties": map[string]any{
					"month": map[string]any{"type": "integer", "minimum": 1, "maximum": 12},
					"day":   map[string]any{"type": "integer", "minimum": 1, "maximum": 31},
				},
				"additionalProperties": false,
			},
			"BankDetails": map[string]any{
				"type":     "object",
				"required": []string{"iban", "bic", "bank_name"},
				"properties": map[string]any{
					"iban":      map[string]any{"type": "string"},
					"bic":       map[string]any{"type": "string"},
					"bank_name": map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"Shareholder": map[string]any{
				"type":     "object",
				"required": []string{"name", "shares", "class"},
				"properties": map[string]any{
					"name":   map[string]any{"type": "string"},
					"shares": map[string]any{"type": "integer", "format": "int64"},
					"class":  map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
			"Director": map[string]any{
				"type":     "object",
				"required": []string{"name"},
				"properties": map[string]any{
					"name":           map[string]any{"type": "string"},
					"appointed_date": map[string]any{"type": "string", "format": "date"},
					"is_chair":       map[string]any{"type": "boolean"},
				},
				"additionalProperties": false,
			},
			"IdentityProfile": map[string]any{
				"type": "object",
				"required": []string{
					"trading_name",
					"legal_name",
					"company_number",
					"registered_office",
					"incorporation_date",
					"year_end",
					"is_vat_registered",
					"vat_number",
					"bank_details",
					"shareholders",
					"directors",
					"logo_asset_id",
					"logo_asset_url",
				},
				"properties": identityProfileProperties(false),
			},
			"IdentityProfilePatch": map[string]any{
				"type":                 "object",
				"properties":           identityProfileProperties(true),
				"additionalProperties": false,
			},
			"IdentityLogoUploadRequest": map[string]any{
				"type":     "object",
				"required": []string{"logo"},
				"properties": map[string]any{
					"logo": map[string]any{
						"type":   "string",
						"format": "binary",
					},
				},
				"additionalProperties": false,
			},
			"IdentityLogoUploadResponse": map[string]any{
				"type":     "object",
				"required": []string{"asset_id", "asset_url"},
				"properties": map[string]any{
					"asset_id":  map[string]any{"type": "string", "format": "uuid"},
					"asset_url": map[string]any{"type": "string", "format": "uri-reference"},
				},
			},
			"FieldError": map[string]any{
				"type":     "object",
				"required": []string{"pointer", "detail"},
				"properties": map[string]any{
					"pointer": map[string]any{"type": "string"},
					"detail":  map[string]any{"type": "string"},
				},
			},
			"ValidationProblem": map[string]any{
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
						"items": map[string]any{"$ref": "#/components/schemas/FieldError"},
					},
				},
			},
		},
	}
}

func identityProfileProperties(patch bool) map[string]any {
	properties := map[string]any{
		"trading_name":       map[string]any{"type": "string"},
		"legal_name":         map[string]any{"type": "string"},
		"company_number":     map[string]any{"type": "string"},
		"registered_office":  map[string]any{"$ref": "#/components/schemas/RegisteredOffice"},
		"incorporation_date": map[string]any{"type": "string", "format": "date"},
		"year_end":           map[string]any{"$ref": "#/components/schemas/YearEnd"},
		"is_vat_registered":  map[string]any{"type": "boolean"},
		"vat_number":         map[string]any{"type": "string", "nullable": true},
		"bank_details":       map[string]any{"$ref": "#/components/schemas/BankDetails"},
		"shareholders": map[string]any{
			"type":  "array",
			"items": map[string]any{"$ref": "#/components/schemas/Shareholder"},
		},
		"directors": map[string]any{
			"type":  "array",
			"items": map[string]any{"$ref": "#/components/schemas/Director"},
		},
		"logo_asset_id":  map[string]any{"type": "string", "format": "uuid", "nullable": true},
		"logo_asset_url": map[string]any{"type": "string", "format": "uri-reference", "nullable": true},
	}
	if patch {
		delete(properties, "logo_asset_url")
	}
	return properties
}
