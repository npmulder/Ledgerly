package reports

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

const (
	vatReturnFilingKey      = "vat_return"
	manualInputVATSourceRef = "manual-input-vat:"
	vatControlAccount       = ledger.AccountCode("2200-vat-control")
	salesAccount            = ledger.AccountCode("4000-sales")
)

// VATReturn computes VAT return boxes 1, 4, and 6 for one VAT quarter.
func (s *Service) VATReturn(ctx context.Context, period Period) (VATFigures, error) {
	if s == nil {
		return VATFigures{}, fmt.Errorf("reports: service is nil")
	}
	if s.ledger == nil {
		return VATFigures{}, fmt.Errorf("ledger: %w", ErrMissingProvider)
	}
	if s.invoicing == nil {
		return VATFigures{}, fmt.Errorf("invoicing: %w", ErrMissingProvider)
	}
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
		reader: s.invoicing,
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
	if s == nil {
		return VATPosition{}, fmt.Errorf("reports: service is nil")
	}
	clk := s.clock
	if clk == nil {
		clk = realVATClock{}
	}
	period := VATQuarterForDate(clk.Now())
	figures, err := s.VATReturn(ctx, period)
	if err != nil {
		return VATPosition{}, err
	}

	position := VATPosition{
		Period:  period,
		Figures: figures,
	}
	facts, ok, err := s.vatPositionCompanyFacts(ctx)
	if err != nil {
		return VATPosition{}, err
	}
	if !ok {
		return position, nil
	}
	deadlines, err := jurisdiction.FilingDeadlinesWithClock(facts, fixedClock{now: period.To})
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

type realVATClock struct{}

func (realVATClock) Now() time.Time {
	return time.Now()
}

// VATQuarterForDate returns the calendar VAT quarter containing date.
func VATQuarterForDate(date time.Time) Period {
	date = dateOnly(date)
	quarterStartMonth := time.Month(((int(date.Month())-1)/3)*3 + 1)
	from := time.Date(date.Year(), quarterStartMonth, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 3, -1)
	return Period{From: from, To: to}
}

func (s *Service) vatPositionCompanyFacts(ctx context.Context) (jurisdiction.CompanyFacts, bool, error) {
	if s.identity == nil {
		return jurisdiction.CompanyFacts{}, false, nil
	}
	facts, err := s.identity.CompanyFacts(ctx)
	if err != nil {
		return jurisdiction.CompanyFacts{}, false, fmt.Errorf("reports: VAT position identity facts: %w", err)
	}
	return toJurisdictionFacts(facts), true, nil
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
				amount, err := posting.AmountGBP.Negate()
				if err != nil {
					return fmt.Errorf("reports: negate output VAT posting: %w", err)
				}
				if figures.Box1, err = figures.Box1.Add(amount); err != nil {
					return fmt.Errorf("reports: add Box 1: %w", err)
				}
				continue
			}
			if !isManualInputVATAdjustment(entry) {
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

func isManualInputVATAdjustment(entry ledger.JournalEntry) bool {
	return entry.SourceModule == ModuleName && strings.HasPrefix(strings.TrimSpace(entry.SourceRef), manualInputVATSourceRef)
}

type vatClassifier struct {
	reader Invoicing
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
			semantics, ok, inferErr := inferLegacyInvoiceVATSemantics(entry)
			if inferErr != nil {
				return jurisdiction.VATTreatmentSemantics{}, inferErr
			}
			if ok {
				c.cache[lookupID] = semantics
				return semantics, nil
			}
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

func inferLegacyInvoiceVATSemantics(entry ledger.JournalEntry) (jurisdiction.VATTreatmentSemantics, bool, error) {
	hasSalesPosting := false
	hasVATPosting := false
	for _, posting := range entry.Postings {
		switch posting.AccountCode {
		case salesAccount:
			if !posting.AmountGBP.IsZero() {
				hasSalesPosting = true
			}
		case vatControlAccount:
			if !posting.AmountGBP.IsZero() {
				hasVATPosting = true
			}
		}
	}
	if !hasSalesPosting {
		return jurisdiction.VATTreatmentSemantics{}, false, nil
	}

	treatment := invoicing.VATTreatmentReverseChargeEUB2B
	if hasVATPosting {
		treatment = invoicing.VATTreatmentDomestic
	}
	semantics, err := jurisdiction.VATSemanticsForTreatment(string(treatment))
	if err != nil {
		return jurisdiction.VATTreatmentSemantics{}, false, fmt.Errorf("reports: infer legacy VAT semantics for ledger entry %d: %w", entry.ID, err)
	}
	return semantics, true, nil
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
