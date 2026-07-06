//go:build integration

package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/it/testdb"
	"github.com/npmulder/ledgerly/internal/platform/mail"
)

func TestInvoicingInvoiceReminderHTTPEndpointSendsPDFAndRecordsRow(t *testing.T) {
	fakeMailer := mail.NewMemorySender()
	h := harness.New(t, harness.Options{
		ClockStart: time.Date(2025, 5, 11, 9, 0, 0, 0, time.UTC),
		MailSender: fakeMailer,
	})
	fixtures.Company(t, h)
	fixtures.Rates(t, h)
	fabrikam := fixtures.Fabrikam(t, h)

	draft := createDraftInvoiceViaHTTP(t, h, fabrikam.ID)
	patched := performInvoiceRequest(t, h, http.MethodPatch, "/api/invoicing/invoices/"+draft.ID, mustInvoiceJSON(t, map[string]any{
		"issue_date": "2025-05-01",
		"due_date":   "2025-05-02",
		"lines": []map[string]any{
			{
				"id":          "line-reminder",
				"description": "Overdue support",
				"qty":         "2",
				"unit_price": map[string]any{
					"amount":   int64(60_000),
					"currency": string(invoicing.CurrencyGBP),
				},
			},
		},
	}), true)
	if patched.StatusCode != http.StatusOK {
		t.Fatalf("patch overdue invoice status = %d, want %d; body=%s", patched.StatusCode, http.StatusOK, patched.BodyString())
	}
	send := performInvoiceRequest(t, h, http.MethodPost, "/api/invoicing/invoices/"+draft.ID+"/send", nil, true)
	if send.StatusCode != http.StatusOK {
		t.Fatalf("send overdue invoice status = %d, want %d; body=%s", send.StatusCode, http.StatusOK, send.BodyString())
	}
	sent := decodeSendInvoiceResponse(t, send)
	pdfBytes := []byte("%PDF-1.4\n% reminder fixture\n")
	storeInvoicePDFAssetForReminderTest(t, h, sent.Invoice.ID, pdfBytes)

	remind := performInvoiceRequest(t, h, http.MethodPost, "/api/invoicing/invoices/"+sent.Invoice.ID+"/remind", nil, true)
	if remind.StatusCode != http.StatusOK {
		t.Fatalf("remind invoice status = %d, want %d; body=%s", remind.StatusCode, http.StatusOK, remind.BodyString())
	}
	var result invoicing.ReminderResult
	if err := json.Unmarshal(remind.Body, &result); err != nil {
		t.Fatalf("decode reminder response: %v; body=%s", err, remind.BodyString())
	}
	if result.Reminder.InvoiceID != sent.Invoice.ID || result.Reminder.SentAt.IsZero() {
		t.Fatalf("reminder result = %+v, want invoice %s with sent_at", result.Reminder, sent.Invoice.ID)
	}
	if len(result.Invoice.Reminders) != 1 || result.Invoice.Reminders[0].InvoiceID != sent.Invoice.ID {
		t.Fatalf("invoice reminders = %+v, want one reminder for %s", result.Invoice.Reminders, sent.Invoice.ID)
	}
	if got := reminderRowCount(t, sent.Invoice.ID); got != 1 {
		t.Fatalf("reminder row count = %d, want 1", got)
	}

	messages := fakeMailer.Messages()
	if len(messages) != 1 {
		t.Fatalf("captured mail count = %d, want 1", len(messages))
	}
	msg := messages[0]
	if msg.To != "accounts@fabrikam.example" {
		t.Fatalf("mail To = %q, want accounts@fabrikam.example", msg.To)
	}
	if msg.Subject != "Payment reminder: invoice INV-2025-01" {
		t.Fatalf("mail Subject = %q", msg.Subject)
	}
	for _, want := range []string{"INV-2025-01", "9 days overdue", "£1,440.00", "2 May 2025", "NPM Limited"} {
		if !strings.Contains(msg.TextBody, want) {
			t.Fatalf("mail body missing %q:\n%s", want, msg.TextBody)
		}
	}
	if len(msg.Attachments) != 1 {
		t.Fatalf("attachment count = %d, want 1", len(msg.Attachments))
	}
	attachment := msg.Attachments[0]
	if attachment.Filename != "INV-2025-01.pdf" || attachment.ContentType != "application/pdf" || !bytes.Equal(attachment.Bytes, pdfBytes) {
		t.Fatalf("attachment = filename %q content-type %q bytes %q, want stored PDF", attachment.Filename, attachment.ContentType, string(attachment.Bytes))
	}

	second := performInvoiceRequest(t, h, http.MethodPost, "/api/invoicing/invoices/"+sent.Invoice.ID+"/remind", nil, true)
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second remind status = %d, want %d; body=%s", second.StatusCode, http.StatusConflict, second.BodyString())
	}
	if !strings.Contains(second.BodyString(), "Reminder already sent today") {
		t.Fatalf("second remind problem = %s, want rate-limit title", second.BodyString())
	}
	if got := len(fakeMailer.Messages()); got != 1 {
		t.Fatalf("mail count after rate limit = %d, want 1", got)
	}
	if got := reminderRowCount(t, sent.Invoice.ID); got != 1 {
		t.Fatalf("reminder row count after rate limit = %d, want 1", got)
	}
}

func TestInvoicingInvoiceReminderHTTPEndpointRejectsNonOverdueInvoice(t *testing.T) {
	fakeMailer := mail.NewMemorySender()
	h := harness.New(t, harness.Options{
		ClockStart: time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC),
		MailSender: fakeMailer,
	})
	fixtures.Company(t, h)
	fixtures.Rates(t, h)
	fabrikam := fixtures.Fabrikam(t, h)

	draft := createDraftInvoiceViaHTTP(t, h, fabrikam.ID)
	complete := patchDraftInvoiceLinesViaHTTP(t, h, draft.ID, "line-current")
	send := performInvoiceRequest(t, h, http.MethodPost, "/api/invoicing/invoices/"+complete.ID+"/send", nil, true)
	if send.StatusCode != http.StatusOK {
		t.Fatalf("send current invoice status = %d, want %d; body=%s", send.StatusCode, http.StatusOK, send.BodyString())
	}
	sent := decodeSendInvoiceResponse(t, send)

	remind := performInvoiceRequest(t, h, http.MethodPost, "/api/invoicing/invoices/"+sent.Invoice.ID+"/remind", nil, true)
	if remind.StatusCode != http.StatusConflict {
		t.Fatalf("non-overdue remind status = %d, want %d; body=%s", remind.StatusCode, http.StatusConflict, remind.BodyString())
	}
	if !strings.Contains(remind.BodyString(), "Invoice is not overdue") {
		t.Fatalf("non-overdue problem = %s, want typed not-overdue title", remind.BodyString())
	}
	if got := len(fakeMailer.Messages()); got != 0 {
		t.Fatalf("mail count for non-overdue = %d, want 0", got)
	}
}

func storeInvoicePDFAssetForReminderTest(t testing.TB, h *harness.Harness, invoiceID string, pdfBytes []byte) string {
	t.Helper()

	writer := identity.NewAssetWriter(testdb.AsModule(t, "identity"), h.IdentityDataDir)
	assetID, err := writer.StoreAsset(context.Background(), identity.AssetUpload{
		MIME:  "application/pdf",
		Bytes: pdfBytes,
	})
	if err != nil {
		t.Fatalf("store PDF asset: %v", err)
	}
	assetURL := "/api/identity/assets/" + string(assetID)
	if _, err := (invoicing.Store{}).SetInvoicePDFAsset(context.Background(), testdb.AsModule(t, invoicing.ModuleName), invoiceID, assetURL); err != nil {
		t.Fatalf("set invoice PDF asset: %v", err)
	}
	return assetURL
}

func reminderRowCount(t testing.TB, invoiceID string) int {
	t.Helper()

	var count int
	if err := testdb.AsModule(t, invoicing.ModuleName).QueryRow(context.Background(), `
SELECT count(*)::integer
FROM invoicing.reminders
WHERE invoice_id = $1`, invoiceID).Scan(&count); err != nil {
		t.Fatalf("count reminders: %v", err)
	}
	return count
}
