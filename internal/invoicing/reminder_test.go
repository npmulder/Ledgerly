package invoicing

import (
	"os"
	"testing"
	"time"
)

func TestReminderTemplateSnapshot(t *testing.T) {
	got := RenderReminderText(ReminderTemplateData{
		InvoiceNumber: "INV-2025-01",
		DaysOverdue:   9,
		Amount:        Money{Amount: 120_000, Currency: string(CurrencyGBP)},
		DueDate:       time.Date(2025, 5, 2, 0, 0, 0, 0, time.UTC),
		CompanyName:   "NPM Limited",
	})
	wantBytes, err := os.ReadFile("testdata/reminder_template.txt")
	if err != nil {
		t.Fatalf("read reminder template snapshot: %v", err)
	}
	if got != string(wantBytes) {
		t.Fatalf("RenderReminderText() mismatch\n--- got ---\n%s\n--- want ---\n%s", got, string(wantBytes))
	}
}
