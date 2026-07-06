package banking

import (
	"context"
	"fmt"

	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

type invoicingInvoiceCandidateSource struct {
	store invoicing.Store
}

func defaultInvoiceCandidateSource() InvoiceCandidateSource {
	return invoicingInvoiceCandidateSource{store: invoicing.Store{}}
}

func (s invoicingInvoiceCandidateSource) InvoiceCandidates(ctx context.Context, tx db.Tx, currency string) ([]InvoiceMatchCandidate, error) {
	if tx == nil {
		return nil, fmt.Errorf("banking: invoice candidates require transaction")
	}
	candidates, err := s.store.InvoiceMatchCandidates(ctx, tx, currency)
	if err != nil {
		return nil, err
	}
	mapped := make([]InvoiceMatchCandidate, len(candidates))
	for i, candidate := range candidates {
		mapped[i] = InvoiceMatchCandidate{
			InvoiceID:  candidate.InvoiceID,
			Number:     candidate.Number,
			ClientName: candidate.ClientName,
			IssueDate:  candidate.IssueDate,
			DueDate:    candidate.DueDate,
			TermsDays:  candidate.TermsDays,
			Amount:     candidate.Amount,
			Status:     string(candidate.Status),
			Settled:    candidate.Settled,
		}
	}
	return mapped, nil
}
