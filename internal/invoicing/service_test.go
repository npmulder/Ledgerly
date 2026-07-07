package invoicing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
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

func TestComputeTotalsDomesticVATRequiresCompanyRegistration(t *testing.T) {
	if err := jurisdiction.LoadActive(jurisdiction.DefaultSelector); err != nil {
		t.Fatalf("LoadActive(%q) error = %v", jurisdiction.DefaultSelector, err)
	}
	service := &Service{
		identity: vatRegistrationIdentity{registered: false},
	}
	invoice := Invoice{
		ID:           "invoice_unregistered_domestic",
		ClientID:     "client_domestic",
		Status:       InvoiceStatusDraft,
		IssueDate:    time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC),
		DueDate:      time.Date(2025, 5, 31, 0, 0, 0, 0, time.UTC),
		Currency:     CurrencyGBP,
		VATTreatment: VATTreatmentDomestic,
		Lines: []InvoiceLine{{
			ID:          "line_1",
			InvoiceID:   "invoice_unregistered_domestic",
			Position:    1,
			Description: "Consulting",
			Qty:         MustQuantity("1"),
			UnitPrice:   Money{Amount: 100_000, Currency: string(CurrencyGBP)},
		}},
	}

	computed, err := service.computeTotals(context.Background(), invoice, false)
	if err != nil {
		t.Fatalf("computeTotals() error = %v", err)
	}
	if computed.Totals.VAT.Amount != 0 {
		t.Fatalf("VAT = %v, want zero when company is not VAT registered", computed.Totals.VAT)
	}
	if computed.Totals.Total.Amount != 100_000 {
		t.Fatalf("Total = %v, want subtotal only", computed.Totals.Total)
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

type vatRegistrationIdentity struct {
	registered bool
}

func (i vatRegistrationIdentity) Profile(context.Context) (identity.CompanyProfile, error) {
	return identity.CompanyProfile{IsVATRegistered: i.registered}, nil
}

func (vatRegistrationIdentity) UpdateProfile(context.Context, identity.UpdateProfilePatch) error {
	return nil
}

func (vatRegistrationIdentity) ReplaceLogo(context.Context, identity.LogoUpload) (identity.AssetID, error) {
	return "", nil
}

func (vatRegistrationIdentity) Asset(context.Context, identity.AssetID) (identity.Asset, error) {
	return identity.Asset{}, nil
}

func (i vatRegistrationIdentity) CompanyFacts(context.Context) (identity.CompanyFacts, error) {
	return identity.CompanyFacts{IsVATRegistered: i.registered}, nil
}

func (i vatRegistrationIdentity) IsVATRegistered(context.Context) (bool, error) {
	return i.registered, nil
}
