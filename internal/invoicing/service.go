package invoicing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const (
	problemTypeClientNotFound       = "https://ledgerly.local/problems/invoicing/client-not-found"
	problemTypeClientValidation     = "https://ledgerly.local/problems/invoicing/client-validation"
	problemTypeClientCurrencyLocked = "https://ledgerly.local/problems/invoicing/client-currency-locked"
)

// Service orchestrates invoicing client commands and queries.
type Service struct {
	pool         *pgxpool.Pool
	store        Store
	invoiceUsage InvoiceUsageChecker
	idGenerator  func() (string, error)
}

type ServiceOption func(*Service)

// WithInvoiceUsageChecker installs the INV-2 currency-lock dependency. Until
// invoices exist, production uses the no-op checker.
func WithInvoiceUsageChecker(checker InvoiceUsageChecker) ServiceOption {
	return func(s *Service) {
		if checker != nil {
			s.invoiceUsage = checker
		}
	}
}

func NewService(pool *pgxpool.Pool, store Store, opts ...ServiceOption) *Service {
	service := &Service{
		pool:         pool,
		store:        store,
		invoiceUsage: noInvoiceUsageChecker{},
		idGenerator:  newClientID,
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

// Clients returns unarchived clients for picker and Settings list surfaces.
func (s *Service) Clients(ctx context.Context) ([]Client, error) {
	return s.store.ListClients(ctx, s.pool, false)
}

// ClientsIncludingArchived returns all clients for history/debug callers.
func (s *Service) ClientsIncludingArchived(ctx context.Context) ([]Client, error) {
	return s.store.ListClients(ctx, s.pool, true)
}

// Client returns a client by ID, including archived clients for historical
// invoice references.
func (s *Service) Client(ctx context.Context, id string) (Client, error) {
	return s.store.Client(ctx, s.pool, id)
}

// SaveClient creates a new client when c.ID is empty, otherwise updates the
// existing client while preserving archived history fields.
func (s *Service) SaveClient(ctx context.Context, c Client) (_ Client, err error) {
	c, err = normalizeClient(c)
	if err != nil {
		return Client{}, err
	}

	if c.ID == "" {
		c.ID, err = s.idGenerator()
		if err != nil {
			return Client{}, err
		}
		return s.store.InsertClient(ctx, s.pool, c)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Client{}, fmt.Errorf("invoicing: begin save client transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	existing, err := s.store.ClientForUpdate(ctx, tx, c.ID)
	if err != nil {
		return Client{}, err
	}
	if err := s.ensureCurrencyMutable(ctx, existing, c); err != nil {
		return Client{}, err
	}

	c.CreatedAt = existing.CreatedAt
	c.ArchivedAt = existing.ArchivedAt
	updated, err := s.store.UpdateClient(ctx, tx, c)
	if err != nil {
		return Client{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Client{}, fmt.Errorf("invoicing: commit save client transaction: %w", err)
	}
	return updated, nil
}

// ArchiveClient soft-archives a client so invoices can keep referencing it.
func (s *Service) ArchiveClient(ctx context.Context, id string) error {
	return s.store.ArchiveClient(ctx, s.pool, id)
}

func (s *Service) ensureCurrencyMutable(ctx context.Context, existing Client, next Client) error {
	if existing.DefaultCurrency == next.DefaultCurrency {
		return nil
	}
	hasInvoices, err := s.invoiceUsage.ClientHasInvoices(ctx, existing.ID)
	if err != nil {
		return fmt.Errorf("invoicing: check client invoice usage: %w", err)
	}
	if hasInvoices {
		return ErrClientCurrencyLocked
	}
	return nil
}

func newClientID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("invoicing: generate client id: %w", err)
	}
	return "client_" + hex.EncodeToString(bytes[:]), nil
}

func problemForError(err error) (httpserver.Problem, bool) {
	var validation ValidationError
	switch {
	case errors.As(err, &validation):
		return httpserver.Problem{
			Type:   problemTypeClientValidation,
			Title:  "Client validation failed",
			Status: 422,
			Detail: "client validation failed",
			Extensions: map[string]any{
				"errors": validation.Fields,
			},
		}, true
	case errors.Is(err, ErrClientNotFound):
		return httpserver.Problem{
			Type:   problemTypeClientNotFound,
			Title:  "Client not found",
			Status: 404,
			Detail: "client was not found",
		}, true
	case errors.Is(err, ErrClientCurrencyLocked):
		return httpserver.Problem{
			Type:   problemTypeClientCurrencyLocked,
			Title:  "Client currency is locked",
			Status: 409,
			Detail: "default currency cannot be changed after a client has invoices",
		}, true
	default:
		return httpserver.Problem{}, false
	}
}
