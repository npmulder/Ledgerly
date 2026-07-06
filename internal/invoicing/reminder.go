package invoicing

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/platform/mail"
)

// ReminderResult is returned after a manual reminder send.
type ReminderResult struct {
	Invoice  Invoice         `json:"invoice"`
	Reminder InvoiceReminder `json:"reminder"`
}

// ReminderTemplateData is the stable data contract for the plain-text reminder.
type ReminderTemplateData struct {
	InvoiceNumber string
	DaysOverdue   int
	Amount        Money
	DueDate       time.Time
	CompanyName   string
}

// SendReminder emails a plain-text overdue reminder with the stored invoice PDF
// attached, then records the send. v1 is manual-trigger only.
func (s *Service) SendReminder(ctx context.Context, id string) (_ ReminderResult, err error) {
	if s.pool == nil {
		return ReminderResult{}, fmt.Errorf("invoicing: reminder requires pool")
	}
	if s.mailer == nil {
		return ReminderResult{}, fmt.Errorf("invoicing: reminder mailer is not configured")
	}
	if s.identity == nil {
		return ReminderResult{}, fmt.Errorf("invoicing: reminder identity API is not configured")
	}

	now := s.now().UTC()
	today := dateOnly(now)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReminderResult{}, fmt.Errorf("invoicing: begin reminder transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	invoice, err := s.store.InvoiceForUpdate(ctx, tx, strings.TrimSpace(id))
	if err != nil {
		return ReminderResult{}, err
	}
	if err := validateReminderEligibleInvoice(invoice, today); err != nil {
		return ReminderResult{}, err
	}
	if alreadySent, err := s.store.ReminderSentOnDate(ctx, tx, invoice.ID, today); err != nil {
		return ReminderResult{}, err
	} else if alreadySent {
		return ReminderResult{}, ErrInvoiceReminderRateLimited
	}

	client, err := s.store.Client(ctx, tx, invoice.ClientID)
	if err != nil {
		return ReminderResult{}, err
	}
	if client.Email == nil || strings.TrimSpace(*client.Email) == "" {
		return ReminderResult{}, ErrInvoiceReminderRecipientMissing
	}
	invoice, err = s.computeTotals(ctx, invoice, false)
	if err != nil {
		return ReminderResult{}, err
	}
	profile, err := s.identity.Profile(ctx)
	if err != nil {
		return ReminderResult{}, err
	}
	attachment, err := s.invoiceReminderPDFAttachment(ctx, invoice)
	if err != nil {
		return ReminderResult{}, err
	}

	templateData := ReminderTemplateData{
		InvoiceNumber: strings.TrimSpace(*invoice.Number),
		DaysOverdue:   daysBetween(invoice.DueDate, today),
		Amount:        invoice.Totals.Total,
		DueDate:       invoice.DueDate,
		CompanyName:   reminderCompanyName(profile),
	}
	msg := mail.Message{
		To:          strings.TrimSpace(*client.Email),
		Subject:     ReminderSubject(templateData),
		TextBody:    RenderReminderText(templateData),
		Attachments: []mail.Attachment{attachment},
	}
	if err := s.mailer.Send(ctx, msg); err != nil {
		return ReminderResult{}, fmt.Errorf("invoicing: send invoice reminder email: %w", err)
	}
	reminder, err := s.store.InsertReminder(ctx, tx, invoice.ID, now)
	if err != nil {
		return ReminderResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ReminderResult{}, fmt.Errorf("invoicing: commit reminder transaction: %w", err)
	}

	refreshed, err := s.Invoice(ctx, invoice.ID)
	if err != nil {
		return ReminderResult{}, err
	}
	return ReminderResult{Invoice: refreshed, Reminder: reminder}, nil
}

// ReminderSubject returns the stable subject line for overdue reminders.
func ReminderSubject(data ReminderTemplateData) string {
	return "Payment reminder: invoice " + strings.TrimSpace(data.InvoiceNumber)
}

// RenderReminderText renders the v1 plain-text reminder email.
func RenderReminderText(data ReminderTemplateData) string {
	return fmt.Sprintf(`Hello,

This is a polite reminder that invoice %s is %d days overdue.

Amount due: %s
Due date: %s

If payment has already been arranged, thank you and please disregard this message.

Kind regards,
%s
`, strings.TrimSpace(data.InvoiceNumber), data.DaysOverdue, formatReminderMoney(data.Amount), formatReminderDate(data.DueDate), strings.TrimSpace(data.CompanyName))
}

func validateReminderEligibleInvoice(invoice Invoice, today time.Time) error {
	if invoice.Status != InvoiceStatusSent {
		return ErrInvoiceReminderNotDue
	}
	if !dateOnly(invoice.DueDate).Before(dateOnly(today)) {
		return ErrInvoiceReminderNotDue
	}
	if invoice.Number == nil || strings.TrimSpace(*invoice.Number) == "" {
		return fmt.Errorf("invoicing: sent invoice %s has no number", invoice.ID)
	}
	if invoice.PDFAsset == nil || strings.TrimSpace(*invoice.PDFAsset) == "" {
		return ErrInvoiceReminderPDFMissing
	}
	return nil
}

func (s *Service) invoiceReminderPDFAttachment(ctx context.Context, invoice Invoice) (mail.Attachment, error) {
	assetID, err := invoicePDFIdentityAssetID(invoice.PDFAsset)
	if err != nil {
		return mail.Attachment{}, err
	}
	asset, err := s.identity.Asset(ctx, assetID)
	if err != nil {
		return mail.Attachment{}, fmt.Errorf("%w: %v", ErrInvoiceReminderPDFMissing, err)
	}
	if !strings.EqualFold(asset.MIME, "application/pdf") || len(asset.Bytes) == 0 {
		return mail.Attachment{}, ErrInvoiceReminderPDFMissing
	}
	number := "invoice"
	if invoice.Number != nil && strings.TrimSpace(*invoice.Number) != "" {
		number = strings.TrimSpace(*invoice.Number)
	}
	return mail.Attachment{
		Filename:    safeAttachmentFilename(number) + ".pdf",
		ContentType: "application/pdf",
		Bytes:       asset.Bytes,
	}, nil
}

func invoicePDFIdentityAssetID(assetURL *string) (identity.AssetID, error) {
	if assetURL == nil || strings.TrimSpace(*assetURL) == "" {
		return "", ErrInvoiceReminderPDFMissing
	}
	raw := strings.TrimSpace(*assetURL)
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: invalid PDF asset URL", ErrInvoiceReminderPDFMissing)
	}
	assetPath := parsed.Path
	const prefix = "/api/identity/assets/"
	if !strings.HasPrefix(assetPath, prefix) {
		return "", fmt.Errorf("%w: unsupported PDF asset URL", ErrInvoiceReminderPDFMissing)
	}
	id := strings.TrimPrefix(assetPath, prefix)
	if id == "" || strings.Contains(id, "/") {
		return "", fmt.Errorf("%w: invalid PDF asset id", ErrInvoiceReminderPDFMissing)
	}
	return identity.AssetID(id), nil
}

func reminderCompanyName(profile identity.CompanyProfile) string {
	if name := strings.TrimSpace(profile.TradingName); name != "" {
		return name
	}
	return strings.TrimSpace(profile.LegalName)
}

func daysBetween(from time.Time, to time.Time) int {
	return int(dateOnly(to).Sub(dateOnly(from)) / (24 * time.Hour))
}

func formatReminderMoney(amount Money) string {
	major := amount.Amount / 100
	minor := amount.Amount % 100
	if minor < 0 {
		minor = -minor
	}
	symbol := amount.Currency + " "
	switch amount.Currency {
	case string(CurrencyEUR):
		symbol = "€"
	case string(CurrencyGBP):
		symbol = "£"
	}
	return fmt.Sprintf("%s%s.%02d", symbol, formatReminderWholeUnits(major), minor)
}

func formatReminderWholeUnits(value int64) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	raw := fmt.Sprintf("%d", value)
	if len(raw) <= 3 {
		return sign + raw
	}
	var parts []string
	for len(raw) > 3 {
		parts = append([]string{raw[len(raw)-3:]}, parts...)
		raw = raw[:len(raw)-3]
	}
	parts = append([]string{raw}, parts...)
	return sign + strings.Join(parts, ",")
}

func formatReminderDate(date time.Time) string {
	return dateOnly(date).Format("2 January 2006")
}

func safeAttachmentFilename(name string) string {
	cleaned := path.Base(strings.TrimSpace(name))
	cleaned = strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z':
			return r
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, cleaned)
	cleaned = strings.Trim(cleaned, "-")
	if cleaned == "" || cleaned == "." {
		return "invoice"
	}
	return cleaned
}
