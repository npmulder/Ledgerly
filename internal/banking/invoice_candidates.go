package banking

import (
	"context"
	"fmt"

	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

type invoicingInvoiceCandidateSource struct {
	provider invoicingMatchCandidateProvider
}

type invoicingMatchCandidateProvider interface {
	InvoiceMatchCandidates(ctx context.Context, tx db.Tx, currency string) ([]invoicing.MatchCandidate, error)
}

func defaultInvoiceCandidateSource() InvoiceCandidateSource {
	return invoicingInvoiceCandidateSource{provider: invoicing.Store{}}
}

func (s invoicingInvoiceCandidateSource) InvoiceCandidates(ctx context.Context, tx db.Tx, currency string) ([]InvoiceMatchCandidate, error) {
	if tx == nil {
		return nil, fmt.Errorf("banking: invoice candidates require transaction")
	}
	if s.provider == nil {
		return nil, nil
	}
	var candidates []invoicing.MatchCandidate
	if err := withTransactionSearchPath(ctx, tx, invoicing.ModuleName, func() error {
		var err error
		candidates, err = s.provider.InvoiceMatchCandidates(ctx, tx, currency)
		return err
	}); err != nil {
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
