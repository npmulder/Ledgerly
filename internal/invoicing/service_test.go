package invoicing

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeClientValidationMatrix(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(Client) Client
		wantPointer string
	}{
		{
			name: "reverse charge requires VAT number",
			mutate: func(client Client) Client {
				client.VATNumber = nil
				return client
			},
			wantPointer: "/vat_number",
		},
		{
			name: "reverse charge rejects malformed VAT number",
			mutate: func(client Client) Client {
				vatNumber := "NO 123"
				client.VATNumber = &vatNumber
				return client
			},
			wantPointer: "/vat_number",
		},
		{
			name: "currency must be supported",
			mutate: func(client Client) Client {
				client.DefaultCurrency = "USD"
				return client
			},
			wantPointer: "/default_currency",
		},
		{
			name: "terms must be net 14 or net 30",
			mutate: func(client Client) Client {
				client.TermsDays = 45
				return client
			},
			wantPointer: "/terms_days",
		},
		{
			name: "retainer currency must match default currency",
			mutate: func(client Client) Client {
				client.RetainerAmount = &MoneyAmount{AmountMinor: 450000, Currency: CurrencyGBP}
				return client
			},
			wantPointer: "/retainer_amount/currency",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeClient(test.mutate(validContosoClient()))
			var validation ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("normalizeClient() error = %v, want ValidationError", err)
			}
			for _, field := range validation.Fields {
				if field.Pointer == test.wantPointer {
					return
				}
			}
			t.Fatalf("validation fields = %#v, want pointer %s", validation.Fields, test.wantPointer)
		})
	}
}

func TestNormalizeClientAcceptsContosoVATNumber(t *testing.T) {
	client, err := normalizeClient(validContosoClient())
	if err != nil {
		t.Fatalf("normalizeClient() error = %v", err)
	}
	if client.VATNumber == nil || *client.VATNumber != "DE 129 273 398" {
		t.Fatalf("VATNumber = %v, want trimmed Contoso VAT number", client.VATNumber)
	}
}

func TestNormalizeClientNormalizesOptionalEmail(t *testing.T) {
	email := " Accounts@Example.COM "
	client := validContosoClient()
	client.Email = &email

	normalized, err := normalizeClient(client)
	if err != nil {
		t.Fatalf("normalizeClient(email) error = %v", err)
	}
	if normalized.Email == nil || *normalized.Email != "accounts@example.com" {
		t.Fatalf("Email = %v, want normalized accounts@example.com", normalized.Email)
	}
}

func TestEnsureCurrencyMutableBlocksWhenInvoicesExist(t *testing.T) {
	service := &Service{
		invoiceUsage: fakeInvoiceUsageChecker{hasInvoices: true},
	}
	existing := validContosoClient()
	existing.ID = "client_contoso"
	next := existing
	next.DefaultCurrency = CurrencyGBP

	err := service.ensureCurrencyMutable(context.Background(), existing, next)
	if !errors.Is(err, ErrClientCurrencyLocked) {
		t.Fatalf("ensureCurrencyMutable() error = %v, want ErrClientCurrencyLocked", err)
	}
}

func TestEnsureCurrencyMutableAllowsNoopAndUnusedClient(t *testing.T) {
	service := &Service{
		invoiceUsage: fakeInvoiceUsageChecker{hasInvoices: true},
	}
	existing := validContosoClient()
	existing.ID = "client_contoso"

	if err := service.ensureCurrencyMutable(context.Background(), existing, existing); err != nil {
		t.Fatalf("same-currency ensureCurrencyMutable() error = %v", err)
	}

	service.invoiceUsage = fakeInvoiceUsageChecker{hasInvoices: false}
	next := existing
	next.DefaultCurrency = CurrencyGBP
	if err := service.ensureCurrencyMutable(context.Background(), existing, next); err != nil {
		t.Fatalf("unused-client ensureCurrencyMutable() error = %v", err)
	}
}

func validContosoClient() Client {
	vatNumber := " DE 129 273 398 "
	return Client{
		Name: " Contoso GmbH ",
		Address: Address{
			Line1:    "Theresienhöhe 12",
			Locality: "München",
			Country:  "de",
		},
		VATNumber:       &vatNumber,
		DefaultCurrency: CurrencyEUR,
		TermsDays:       14,
		VATTreatment:    VATTreatmentReverseChargeEUB2B,
		RetainerAmount: &MoneyAmount{
			AmountMinor: 450000,
			Currency:    CurrencyEUR,
		},
	}
}

type fakeInvoiceUsageChecker struct {
	hasInvoices bool
	err         error
}

func (f fakeInvoiceUsageChecker) ClientHasInvoices(context.Context, string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.hasInvoices, nil
}

func TestValidationErrorStringIncludesFirstField(t *testing.T) {
	err := ValidationError{Fields: []FieldError{{Pointer: "/name", Detail: "is required"}}}
	if !strings.Contains(err.Error(), "/name is required") {
		t.Fatalf("ValidationError.Error() = %q, want first field detail", err.Error())
	}
}
