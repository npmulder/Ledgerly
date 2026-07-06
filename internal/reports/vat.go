package reports

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

const (
	vatReturnFilingKey = "vat_return"
	vatControlAccount  = ledger.AccountCode("2200-vat-control")
	salesAccount       = ledger.AccountCode("4000-sales")
)

// VATReturn computes VAT return boxes 1, 4, and 6 for one VAT quarter.
func (s *Service) VATReturn(ctx context.Context, period Period) (VATFigures, error) {
	period, err := normalizeVATQuarter(period)
	if err != nil {
		return VATFigures{}, err
	}

	figures := VATFigures{
		Period: period,
		Box1:   money.Zero("GBP"),
		Box4:   money.Zero("GBP"),
		Box6:   money.Zero("GBP"),
	}
	classifier := vatClassifier{
		reader: s.invoiceVATReader,
		cache:  make(map[ledger.EntryID]jurisdiction.VATTreatmentSemantics),
	}

	filter := ledger.EntryFilter{
		From:  &period.From,
		To:    &period.To,
		Limit: ledger.MaxEntriesLimit,
	}
	for {
		entries, err := s.ledger.Entries(ctx, filter)
		if err != nil {
			return VATFigures{}, fmt.Errorf("reports: VAT return ledger entries: %w", err)
		}
		for _, entry := range entries {
			if err := addEntryToVATFigures(ctx, &figures, &classifier, entry); err != nil {
				return VATFigures{}, err
			}
		}
		if len(entries) < filter.Limit {
			break
		}
		last := entries[len(entries)-1]
		filter.After = &ledger.EntryCursor{Date: last.Date, ID: last.ID}
	}

	figures.NetPosition, err = figures.Box1.Sub(figures.Box4)
	if err != nil {
		return VATFigures{}, fmt.Errorf("reports: VAT net position: %w", err)
	}
	return figures, nil
}

// VATPosition returns the current-quarter VAT return advisor fact.
func (s *Service) VATPosition(ctx context.Context) (VATPosition, error) {
	period := VATQuarterForDate(s.clock.Now())
	figures, err := s.VATReturn(ctx, period)
	if err != nil {
		return VATPosition{}, err
	}

	position := VATPosition{
		Period:  period,
		Figures: figures,
	}
	if s.companyFacts == nil {
		return position, nil
	}
	facts, err := s.companyFacts(ctx)
	if err != nil {
		return VATPosition{}, fmt.Errorf("reports: VAT position company facts: %w", err)
	}
	deadlines, err := jurisdiction.FilingDeadlinesWithClock(facts, s.clock)
	if err != nil {
		return VATPosition{}, fmt.Errorf("reports: VAT position filing deadlines: %w", err)
	}
	for _, deadline := range deadlines {
		if deadline.Key == vatReturnFilingKey {
			dueDate := deadline.DueDate
			position.DueDate = &dueDate
			break
		}
	}
	return position, nil
}

func addEntryToVATFigures(ctx context.Context, figures *VATFigures, classifier *vatClassifier, entry ledger.JournalEntry) error {
	var semantics jurisdiction.VATTreatmentSemantics
	var hasInvoicingSemantics bool
	if entry.SourceModule == invoicing.ModuleName && entryTouchesVATReturnAccounts(entry) {
		var err error
		semantics, err = classifier.semanticsForEntry(ctx, entry)
		if err != nil {
			return err
		}
		hasInvoicingSemantics = true
	}

	for _, posting := range entry.Postings {
		switch posting.AccountCode {
		case vatControlAccount:
			if hasInvoicingSemantics {
				if !semantics.OutputVAT {
					continue
				}
				if _, _, err := jurisdiction.VATStandardRateForDate(entry.Date); err != nil {
					return fmt.Errorf("reports: VAT standard rate for %s: %w", entry.Date.Format(time.DateOnly), err)
				}
				amount, err := posting.AmountGBP.Negate()
				if err != nil {
					return fmt.Errorf("reports: negate output VAT posting: %w", err)
				}
				if figures.Box1, err = figures.Box1.Add(amount); err != nil {
					return fmt.Errorf("reports: add Box 1: %w", err)
				}
				continue
			}
			if posting.AmountGBP.Amount <= 0 {
				continue
			}
			var err error
			if figures.Box4, err = figures.Box4.Add(posting.AmountGBP); err != nil {
				return fmt.Errorf("reports: add Box 4: %w", err)
			}
		case salesAccount:
			if !hasInvoicingSemantics || !semantics.VATReturnNetSales {
				continue
			}
			amount, err := posting.AmountGBP.Negate()
			if err != nil {
				return fmt.Errorf("reports: negate net sales posting: %w", err)
			}
			if figures.Box6, err = figures.Box6.Add(amount); err != nil {
				return fmt.Errorf("reports: add Box 6: %w", err)
			}
		}
	}
	return nil
}

type vatClassifier struct {
	reader InvoiceVATReader
	cache  map[ledger.EntryID]jurisdiction.VATTreatmentSemantics
}

func (c *vatClassifier) semanticsForEntry(ctx context.Context, entry ledger.JournalEntry) (jurisdiction.VATTreatmentSemantics, error) {
	lookupID := entry.ID
	if entry.ReversalOf != nil {
		lookupID = *entry.ReversalOf
	}
	if semantics, ok := c.cache[lookupID]; ok {
		return semantics, nil
	}

	invoiceContext, err := c.reader.InvoiceVATContextBySendEntryID(ctx, lookupID)
	if err != nil {
		if errors.Is(err, invoicing.ErrInvoiceNotFound) {
			return jurisdiction.VATTreatmentSemantics{}, fmt.Errorf("reports: invoicing VAT context for ledger entry %d: %w", lookupID, err)
		}
		return jurisdiction.VATTreatmentSemantics{}, err
	}
	semantics, err := jurisdiction.VATSemanticsForTreatment(string(invoiceContext.VATTreatment))
	if err != nil {
		return jurisdiction.VATTreatmentSemantics{}, fmt.Errorf("reports: VAT semantics for treatment %q: %w", invoiceContext.VATTreatment, err)
	}
	c.cache[lookupID] = semantics
	return semantics, nil
}

func entryTouchesVATReturnAccounts(entry ledger.JournalEntry) bool {
	for _, posting := range entry.Postings {
		if posting.AccountCode == vatControlAccount || posting.AccountCode == salesAccount {
			return true
		}
	}
	return false
}

func normalizeVATQuarter(period Period) (Period, error) {
	if period.From.IsZero() || period.To.IsZero() {
		return Period{}, fmt.Errorf("reports: VAT period requires from and to dates: %w", ErrInvalidPeriod)
	}
	normalized := Period{
		From: dateOnly(period.From),
		To:   dateOnly(period.To),
	}
	if normalized.From.After(normalized.To) {
		return Period{}, fmt.Errorf("reports: VAT period from %s after to %s: %w", normalized.From.Format(time.DateOnly), normalized.To.Format(time.DateOnly), ErrInvalidPeriod)
	}
	expected := VATQuarterForDate(normalized.From)
	if !normalized.From.Equal(expected.From) || !normalized.To.Equal(expected.To) {
		return Period{}, fmt.Errorf("reports: VAT period %s to %s is not a calendar quarter: %w", normalized.From.Format(time.DateOnly), normalized.To.Format(time.DateOnly), ErrInvalidPeriod)
	}
	return normalized, nil
}

func dateOnly(value time.Time) time.Time {
	year, month, day := value.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
