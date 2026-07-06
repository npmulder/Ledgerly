package invoicing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	"github.com/npmulder/ledgerly/internal/platform/db"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

// ModuleName is the database schema, HTTP route segment, and event namespace.
const ModuleName = "invoicing"

const (
	CurrencyEUR Currency = "EUR"
	CurrencyGBP Currency = "GBP"

	VATTreatmentDomestic           VATTreatment = "domestic"
	VATTreatmentReverseChargeEUB2B VATTreatment = "reverse-charge-eu-b2b"
)

var (
	ErrClientNotFound       = errors.New("invoicing: client not found")
	ErrClientCurrencyLocked = errors.New("invoicing: client currency is locked by invoices")
)

// Currency is the limited set of invoice currencies supported in INV-1.
type Currency string

// VATTreatment determines how VAT is applied when invoices are created later.
type VATTreatment string

// Address stores the structured client billing address.
type Address struct {
	Line1      string `json:"line1"`
	Line2      string `json:"line2"`
	Locality   string `json:"locality"`
	Region     string `json:"region"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
}

// MoneyAmount stores exact minor units in the client's configured currency.
type MoneyAmount struct {
	AmountMinor int64    `json:"amount_minor"`
	Currency    Currency `json:"currency"`
}

// Config contains the platform dependencies required by invoicing.
type Config struct {
	Pool                *pgxpool.Pool
	Clock               clock.Clock
	TodayRate           TodayRateFunc
	RateLocker          RateLocker
	RateLockReader      RateLockReader
	Ledger              LedgerJournal
	Bus                 *bus.Bus
	InvoiceUsageChecker InvoiceUsageChecker
}

// Module is the invoicing module wiring surface used by the app builder.
type Module struct {
	service *Service
}

// New assembles the invoicing module without registering side effects globally.
func New(cfg Config) (*Module, error) {
	if cfg.Pool == nil {
		return nil, fmt.Errorf("invoicing: pool is required")
	}
	store := Store{}
	invoiceUsage := cfg.InvoiceUsageChecker
	if invoiceUsage == nil {
		invoiceUsage = storeInvoiceUsageChecker{pool: cfg.Pool, store: store}
	}
	rateLockReader := cfg.RateLockReader
	if rateLockReader == nil {
		if reader, ok := cfg.RateLocker.(RateLockReader); ok {
			rateLockReader = reader
		}
	}
	return &Module{
		service: NewService(
			cfg.Pool,
			store,
			WithClock(cfg.Clock),
			WithTodayRate(cfg.TodayRate),
			WithRateLocker(cfg.RateLocker),
			WithRateLockReader(rateLockReader),
			WithLedger(cfg.Ledger),
			WithEventBus(cfg.Bus),
			WithInvoiceUsageChecker(invoiceUsage),
		),
	}, nil
}

// HTTPModule returns the platform route mount for this module.
func (m *Module) HTTPModule() httpserver.Module {
	return httpserver.Module{
		Name:           ModuleName,
		RegisterRoutes: m.RegisterRoutes,
	}
}

// OpenAPIFragment returns the module's OpenAPI contribution.
func (m *Module) OpenAPIFragment() httpserver.OpenAPIFragment {
	return OpenAPIFragment()
}

// Client is invoicing's client reference-data record.
type Client struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Address         Address      `json:"address"`
	VATNumber       *string      `json:"vat_number"`
	DefaultCurrency Currency     `json:"default_currency"`
	TermsDays       int          `json:"terms_days"`
	VATTreatment    VATTreatment `json:"vat_treatment"`
	RetainerAmount  *MoneyAmount `json:"retainer_amount"`
	DayRate         *MoneyAmount `json:"day_rate"`
	CreatedAt       time.Time    `json:"created_at"`
	ArchivedAt      *time.Time   `json:"archived_at"`
}

// FieldError points to an invalid JSON field in client commands.
type FieldError struct {
	Pointer string `json:"pointer"`
	Detail  string `json:"detail"`
}

// ValidationError collects client command validation failures.
type ValidationError struct {
	Fields []FieldError
}

func (e ValidationError) Error() string {
	if len(e.Fields) == 0 {
		return "client validation failed"
	}
	return fmt.Sprintf("client validation failed: %s %s", e.Fields[0].Pointer, e.Fields[0].Detail)
}

func validationError(fields []FieldError) error {
	if len(fields) == 0 {
		return nil
	}
	return ValidationError{Fields: fields}
}

func normalizeClient(client Client) (Client, error) {
	var fields []FieldError

	client.ID = strings.TrimSpace(client.ID)
	client.Name = strings.TrimSpace(client.Name)
	if client.Name == "" {
		fields = append(fields, FieldError{Pointer: "/name", Detail: "is required"})
	}

	client.Address = normalizeAddress(client.Address)
	fields = append(fields, validateAddress(client.Address)...)

	if client.VATNumber != nil {
		vatNumber := strings.TrimSpace(*client.VATNumber)
		if vatNumber == "" {
			client.VATNumber = nil
		} else {
			client.VATNumber = &vatNumber
		}
	}

	switch client.DefaultCurrency {
	case CurrencyEUR, CurrencyGBP:
	default:
		fields = append(fields, FieldError{Pointer: "/default_currency", Detail: "must be EUR or GBP"})
	}

	switch client.TermsDays {
	case 14, 30:
	default:
		fields = append(fields, FieldError{Pointer: "/terms_days", Detail: "must be 14 or 30"})
	}

	switch client.VATTreatment {
	case VATTreatmentDomestic:
	case VATTreatmentReverseChargeEUB2B:
		if client.VATNumber == nil {
			fields = append(fields, FieldError{Pointer: "/vat_number", Detail: "is required for reverse-charge EU B2B clients"})
		} else if !validEUVATNumber(*client.VATNumber) {
			fields = append(fields, FieldError{Pointer: "/vat_number", Detail: "must be a valid EU VAT number"})
		}
	default:
		fields = append(fields, FieldError{Pointer: "/vat_treatment", Detail: "must be domestic or reverse-charge-eu-b2b"})
	}

	client.RetainerAmount = normalizeMoney(client.RetainerAmount)
	fields = append(fields, validateMoney("/retainer_amount", client.DefaultCurrency, client.RetainerAmount)...)
	client.DayRate = normalizeMoney(client.DayRate)
	fields = append(fields, validateMoney("/day_rate", client.DefaultCurrency, client.DayRate)...)

	return client, validationError(fields)
}

func normalizeAddress(address Address) Address {
	return Address{
		Line1:      strings.TrimSpace(address.Line1),
		Line2:      strings.TrimSpace(address.Line2),
		Locality:   strings.TrimSpace(address.Locality),
		Region:     strings.TrimSpace(address.Region),
		PostalCode: strings.TrimSpace(address.PostalCode),
		Country:    strings.ToUpper(strings.TrimSpace(address.Country)),
	}
}

func validateAddress(address Address) []FieldError {
	var fields []FieldError
	if address.Line1 == "" {
		fields = append(fields, FieldError{Pointer: "/address/line1", Detail: "is required"})
	}
	if address.Locality == "" {
		fields = append(fields, FieldError{Pointer: "/address/locality", Detail: "is required"})
	}
	if address.Country == "" {
		fields = append(fields, FieldError{Pointer: "/address/country", Detail: "is required"})
	}
	return fields
}

func normalizeMoney(amount *MoneyAmount) *MoneyAmount {
	if amount == nil {
		return nil
	}
	normalized := *amount
	return &normalized
}

func validateMoney(pointer string, defaultCurrency Currency, amount *MoneyAmount) []FieldError {
	if amount == nil {
		return nil
	}

	var fields []FieldError
	if amount.AmountMinor <= 0 {
		fields = append(fields, FieldError{Pointer: pointer + "/amount_minor", Detail: "must be greater than zero"})
	}
	switch amount.Currency {
	case CurrencyEUR, CurrencyGBP:
	default:
		fields = append(fields, FieldError{Pointer: pointer + "/currency", Detail: "must be EUR or GBP"})
	}
	if defaultCurrency == CurrencyEUR || defaultCurrency == CurrencyGBP {
		if amount.Currency != defaultCurrency {
			fields = append(fields, FieldError{Pointer: pointer + "/currency", Detail: "must match default_currency"})
		}
	}
	return fields
}

func validEUVATNumber(value string) bool {
	normalized := strings.NewReplacer(" ", "", ".", "", "-", "").Replace(strings.ToUpper(strings.TrimSpace(value)))
	if len(normalized) < 4 || len(normalized) > 14 {
		return false
	}
	country := normalized[:2]
	if !euVATCountryCodes[country] {
		return false
	}
	for _, r := range normalized[2:] {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

var euVATCountryCodes = map[string]bool{
	"AT": true,
	"BE": true,
	"BG": true,
	"CY": true,
	"CZ": true,
	"DE": true,
	"DK": true,
	"EE": true,
	"EL": true,
	"ES": true,
	"FI": true,
	"FR": true,
	"HR": true,
	"HU": true,
	"IE": true,
	"IT": true,
	"LT": true,
	"LU": true,
	"LV": true,
	"MT": true,
	"NL": true,
	"PL": true,
	"PT": true,
	"RO": true,
	"SE": true,
	"SI": true,
	"SK": true,
}

// InvoiceUsageChecker lets INV-2 wire invoice existence into the client
// currency immutability rule without changing INV-1's client API.
type InvoiceUsageChecker interface {
	ClientHasInvoices(context.Context, string) (bool, error)
}

// RateLockRef identifies the invoice-owned reference used for immutable FX locks.
type RateLockRef struct {
	Module string
	Ref    string
}

// RateLock is the subset of a moneyfx lock that invoicing needs for posting.
type RateLock struct {
	ID       int64
	From     string
	To       string
	Rate     string
	RateDate time.Time
	Source   string
}

// RateLocker is the moneyfx capability invoicing needs for send-time locks.
type RateLocker interface {
	LockRate(context.Context, db.Tx, RateLockRef, string, string, time.Time) (RateLock, error)
}

// RateLockReader is the moneyfx capability invoicing needs for read-side GBP
// totals on sent/paid invoices with immutable locks.
type RateLockReader interface {
	RateLock(context.Context, int64) (RateLock, error)
}

// LedgerJournal is the ledger capability invoicing needs for send and unsend.
type LedgerJournal interface {
	Post(context.Context, db.Tx, ledger.NewJournalEntry) (ledger.EntryID, error)
	Reverse(context.Context, db.Tx, ledger.EntryID, string) (ledger.EntryID, error)
}

type noInvoiceUsageChecker struct{}

func (noInvoiceUsageChecker) ClientHasInvoices(context.Context, string) (bool, error) {
	return false, nil
}
