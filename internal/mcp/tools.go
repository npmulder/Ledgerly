package mcp

type toolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func toolDefinitions() []toolDefinition {
	return []toolDefinition{
		{
			Name:        "list_invoices",
			Description: "List invoices with optional status, search, limit, and cursor filters. Monetary fields are integer minor units with explicit currency codes; timestamps and dates are ISO 8601 strings. Totals are invoice totals and GBP approximations, not bank cash received. Prefer get_invoice when you need line-level detail for one invoice, and prefer profit_and_loss when you need accounting profit over a period.",
			InputSchema: objectSchema(map[string]any{
				"status": map[string]any{
					"type":        "array",
					"description": "Optional invoice statuses to include, such as draft, sent, overdue, or paid. Omit for all statuses.",
					"items":       map[string]any{"type": "string"},
				},
				"search": map[string]any{
					"type":        "string",
					"description": "Optional invoice number or client-name search text.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"minimum":     1,
					"description": "Optional positive page size.",
				},
				"cursor": map[string]any{
					"type":        "string",
					"description": "Optional zero-based invoice row offset encoded as a string. To fetch the next page, pass previous response offset + limit while that value is less than total_count.",
				},
			}, nil),
		},
		{
			Name:        "get_invoice",
			Description: "Get one invoice by id. Monetary fields are integer minor units with explicit currency codes; invoice dates and timestamps are ISO 8601 strings. Totals are legal invoice amounts and any GBP approximation, not proof of payment. Prefer list_invoices first when you only know filters or need to find the invoice id.",
			InputSchema: objectSchema(map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Invoice id to retrieve.",
				},
			}, []string{"id"}),
		},
		{
			Name:        "advisor_insights",
			Description: "Read deterministic advisor insights, optionally filtered by surface. Monetary fact bindings are integer minor units with currency codes when present; dates and timestamps are ISO 8601 strings. Severity, rendered text, fact bindings, and CTA explain rule output and suggested action, not model-generated advice. Prefer the underlying finance tool such as dividend_headroom, dla_balance, or vat_position when you need the source calculation behind an insight.",
			InputSchema: objectSchema(map[string]any{
				"surface": map[string]any{
					"type":        "string",
					"description": "Optional advisor surface such as dashboard, invoices, banking, dividends, dla, or reports.",
				},
			}, nil),
		},
		{
			Name:        "dividend_headroom",
			Description: "Show dividend headroom with full breakdown lines. Amounts are integer minor units with currency codes; as_of is an ISO 8601 timestamp. Headroom means legally distributable reserves; do not treat it as cash or bank balance. Prefer profit_and_loss for period profitability and dla_balance when checking whether a director loan should be cleared before declaring a dividend.",
			InputSchema: objectSchema(nil, nil),
		},
		{
			Name:        "dla_balance",
			Description: "Show the current director loan account balance and policy status. Amounts are integer minor units with currency codes; any dates in nested policy context are ISO 8601 strings. The balance describes the director/company loan position, not available cash. Prefer dla_ledger when you need the entries behind the balance, and prefer dividend_headroom when assessing whether dividends can clear an overdrawn balance.",
			InputSchema: objectSchema(nil, nil),
		},
		{
			Name:        "dla_ledger",
			Description: "List director loan account ledger entries with optional ISO date filters and cursor. Amounts are integer minor units with currency codes; entry dates are ISO YYYY-MM-DD and timestamps are ISO 8601 strings. Running balance shows the DLA position after each entry, not bank cash. Prefer dla_balance for the current summarized position.",
			InputSchema: objectSchema(map[string]any{
				"from": map[string]any{
					"type":        "string",
					"description": "Optional inclusive entry date lower bound in ISO YYYY-MM-DD form.",
				},
				"to": map[string]any{
					"type":        "string",
					"description": "Optional inclusive entry date upper bound in ISO YYYY-MM-DD form.",
				},
				"cursor": map[string]any{
					"type":        "string",
					"description": "Optional next cursor from a previous dla_ledger response.",
				},
			}, nil),
		},
		{
			Name:        "profit_and_loss",
			Description: "Get profit and loss for a required posting-date period. Amounts are integer minor units with currency codes; period.from and period.to must be ISO YYYY-MM-DD dates. Profit lines are accrual accounting results for the period, not cash movement. Prefer list_invoices for invoice-level sales detail and vat_position for VAT boxes.",
			InputSchema: objectSchema(map[string]any{
				"period": objectSchema(map[string]any{
					"from": map[string]any{
						"type":        "string",
						"description": "Inclusive posting date lower bound in ISO YYYY-MM-DD form.",
					},
					"to": map[string]any{
						"type":        "string",
						"description": "Inclusive posting date upper bound in ISO YYYY-MM-DD form.",
					},
				}, []string{"from", "to"}),
				"from": map[string]any{
					"type":        "string",
					"description": "Compatibility alias for period.from in ISO YYYY-MM-DD form.",
				},
				"to": map[string]any{
					"type":        "string",
					"description": "Compatibility alias for period.to in ISO YYYY-MM-DD form.",
				},
			}, nil),
		},
		{
			Name:        "vat_position",
			Description: "Get the VAT position for a required quarter. Amounts are integer minor units with currency codes; period is ISO-like YYYY-QN and response dates are ISO YYYY-MM-DD. Boxes are VAT return figures, not profit and not bank cash. Prefer profit_and_loss for trading profit and filing_calendar for due dates.",
			InputSchema: objectSchema(map[string]any{
				"period": map[string]any{
					"type":        "string",
					"description": "VAT quarter in YYYY-QN form, for example 2026-Q2.",
				},
			}, []string{"period"}),
		},
		{
			Name:        "filing_calendar",
			Description: "List filing obligations and due dates. Any amounts in future response extensions are integer minor units with currency codes; due_date is ISO YYYY-MM-DD and timestamps are ISO 8601 strings. days_until is calendar days until the due date, not working days or payment amount. Prefer vat_position when you need the VAT figures for a period.",
			InputSchema: objectSchema(nil, nil),
		},
		{
			Name:        "bank_review_queue",
			Description: "List banking review cards that need attention, including kind, confidence, explanation, target, and transaction. Transaction amounts are integer minor units with currency codes; transaction dates are ISO YYYY-MM-DD and timestamps are ISO 8601 strings. Confidence is match confidence from deterministic rules, not a probability of cash availability. Prefer list_invoices or dla_ledger when you need the underlying invoice or DLA context.",
			InputSchema: objectSchema(nil, nil),
		},
		{
			Name:        "create_draft_invoice",
			Description: "Create a draft invoice for an active client and populate draft lines. Line prices are integer minor units with explicit currency codes; quantities are decimal strings or numbers; response timestamps are ISO 8601 strings. This tool does not send the invoice, settle it, confirm payment, or declare dividends. Prefer the CLI or web UI for human review and any money movement after the draft is prepared.",
			InputSchema: createDraftInvoiceSchema(),
		},
		{
			Name:        "send_invoice_reminder",
			Description: "Record and send one overdue invoice reminder for an existing sent invoice. Monetary fields in the response are integer minor units with currency codes and reminder timestamps are ISO 8601 strings. This tool does not send the invoice, settle it, confirm payment, or declare dividends. Prefer get_invoice first when you need to check invoice status, due date, PDF availability, or reminder history.",
			InputSchema: objectSchema(map[string]any{
				"invoiceId": map[string]any{
					"type":        "string",
					"description": "Invoice id to remind. The invoice must already be sent, overdue, have a stored PDF, and have a client email.",
				},
			}, []string{"invoiceId"}),
		},
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
	}
	if properties == nil {
		properties = map[string]any{}
	}
	schema["properties"] = properties
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func createDraftInvoiceSchema() map[string]any {
	lineSchema := objectSchema(map[string]any{
		"description": map[string]any{
			"type":        "string",
			"description": "Line description shown on the draft invoice.",
		},
		"qty": map[string]any{
			"description": "Positive decimal quantity, as a JSON number or decimal string.",
			"oneOf": []map[string]any{
				{"type": "number", "exclusiveMinimum": 0},
				{"type": "string", "pattern": "^[0-9]+(\\.[0-9]+)?$"},
			},
		},
		"unitPriceMinor": map[string]any{
			"type":        "integer",
			"minimum":     1,
			"description": "Unit price in integer minor units.",
		},
		"currency": map[string]any{
			"type":        "string",
			"enum":        []string{"EUR", "GBP"},
			"description": "Line currency. All lines must use the same currency.",
		},
	}, []string{"description", "qty", "unitPriceMinor", "currency"})
	schema := objectSchema(map[string]any{
		"clientId": map[string]any{
			"type":        "string",
			"description": "Existing active client id. Provide either clientId or clientName, not both.",
		},
		"clientName": map[string]any{
			"type":        "string",
			"description": "Exact active client name, case-insensitive. Provide either clientId or clientName, not both.",
		},
		"lines": map[string]any{
			"type":        "array",
			"minItems":    1,
			"description": "Draft invoice lines to create.",
			"items":       lineSchema,
		},
	}, []string{"lines"})
	schema["oneOf"] = []map[string]any{
		{"required": []string{"clientId"}},
		{"required": []string{"clientName"}},
	}
	return schema
}
