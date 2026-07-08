package invoicing

import (
	"context"
	"fmt"
	"strings"

	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

func (s Store) InvoiceMatchCandidates(ctx context.Context, tx db.Tx, currency string) ([]MatchCandidate, error) {
	return s.invoiceMatchCandidates(ctx, tx, currency, func(invoice Invoice) (Invoice, error) {
		return invoiceMatchTotals(invoice)
	})
}

// InvoiceMatchCandidates returns match candidates with draft totals computed
// from the service's current company identity state.
func (s *Service) InvoiceMatchCandidates(ctx context.Context, tx db.Tx, currency string) ([]MatchCandidate, error) {
	if s == nil {
		return nil, fmt.Errorf("invoicing: list match candidates requires service")
	}
	return s.store.invoiceMatchCandidates(ctx, tx, currency, func(invoice Invoice) (Invoice, error) {
		return s.computeTotals(ctx, invoice, false)
	})
}

type invoiceMatchTotaler func(Invoice) (Invoice, error)

func (s Store) invoiceMatchCandidates(ctx context.Context, tx db.Tx, currency string, totals invoiceMatchTotaler) ([]MatchCandidate, error) {
	currency = strings.TrimSpace(currency)
	if currency == "" {
		return nil, nil
	}
	if totals == nil {
		totals = invoiceMatchTotals
	}
	rows, err := tx.Query(ctx, `
SELECT i.id, c.name, c.terms_days
FROM invoicing.invoices i
JOIN invoicing.clients c ON c.id = i.client_id
WHERE i.status IN ('draft', 'sent')
	AND i.currency = $1
	AND i.settled_date IS NULL
	AND i.settled_amount_minor IS NULL
ORDER BY i.issue_date, i.number NULLS LAST, i.id`, currency)
	if err != nil {
		return nil, fmt.Errorf("invoicing: list match candidates: %w", err)
	}
	defer rows.Close()

	type candidateRow struct {
		invoiceID  string
		clientName string
		termsDays  int
	}
	var rowsByInvoice []candidateRow
	for rows.Next() {
		var row candidateRow
		if err := rows.Scan(&row.invoiceID, &row.clientName, &row.termsDays); err != nil {
			return nil, fmt.Errorf("invoicing: scan match candidate row: %w", err)
		}
		rowsByInvoice = append(rowsByInvoice, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("invoicing: iterate match candidates: %w", err)
	}

	candidates := make([]MatchCandidate, 0, len(rowsByInvoice))
	for _, row := range rowsByInvoice {
		invoice, err := s.Invoice(ctx, tx, row.invoiceID)
		if err != nil {
			return nil, fmt.Errorf("invoicing: load match candidate invoice %q: %w", row.invoiceID, err)
		}
		invoice, err = totals(invoice)
		if err != nil {
			return nil, fmt.Errorf("invoicing: compute match candidate invoice %q total: %w", row.invoiceID, err)
		}
		number := ""
		if invoice.Number != nil {
			number = *invoice.Number
		}
		candidates = append(candidates, MatchCandidate{
			InvoiceID:  invoice.ID,
			Number:     number,
			ClientName: row.clientName,
			IssueDate:  invoice.IssueDate,
			DueDate:    invoice.DueDate,
			TermsDays:  row.termsDays,
			Amount:     invoice.Totals.Total,
			Status:     invoice.Status,
			Settled:    invoice.Status == InvoiceStatusPaid || invoice.SettledDate != nil || invoice.SettledAmount != nil,
		})
	}
	return candidates, nil
}

func invoiceMatchTotals(invoice Invoice) (Invoice, error) {
	subtotal := Money{Currency: string(invoice.Currency)}
	for i := range invoice.Lines {
		rat, err := invoice.Lines[i].Qty.rat()
		if err != nil {
			return Invoice{}, err
		}
		lineTotal := invoice.Lines[i].UnitPrice.MulRat(rat)
		invoice.Lines[i].LineTotal = lineTotal
		subtotal, err = subtotal.Add(lineTotal)
		if err != nil {
			return Invoice{}, fmt.Errorf("invoicing: add line total: %w", err)
		}
	}

	vat := Money{Currency: string(invoice.Currency)}
	if invoice.VATTreatment == VATTreatmentDomestic && sentInvoiceVATRegistered(invoice) && !subtotal.IsZero() {
		vatRate, _, err := jurisdiction.VATStandardRateForDate(invoice.IssueDate)
		if err != nil {
			return Invoice{}, fmt.Errorf("invoicing: VAT rate: %w", err)
		}
		rat, err := rateRat(vatRate.String())
		if err != nil {
			return Invoice{}, err
		}
		vat = subtotal.MulRat(rat)
	}
	total, err := subtotal.Add(vat)
	if err != nil {
		return Invoice{}, fmt.Errorf("invoicing: add VAT total: %w", err)
	}
	invoice.Totals = InvoiceTotals{
		Subtotal: subtotal,
		VAT:      vat,
		Total:    total,
	}
	return invoice, nil
}
