package banking

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

func TestRevolutParserGolden(t *testing.T) {
	t.Parallel()

	sample := strings.NewReader(`Date started (UTC),Date completed (UTC),ID,Type,Description,Reference,Amount,Fee,Currency,State,Balance
2026-03-04 10:11:12,2026-03-04 10:11:30,rev-gbp-1,CARD_PAYMENT,"ACME & Sons / #42 (R&D)",Invoice   1001,"1,234.56",0.00,GBP,COMPLETED,"2,345.67"
2026-03-05 09:00:00,2026-03-05 09:00:05,rev-eur-1,TRANSFER,"Cafe ""Le Test""",Refund / EUR,-987.65,0.00,EUR,COMPLETED,"1,000.00"
`)

	got, err := (RevolutParser{}).Parse(sample)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(Parse) = %d, want 2", len(got))
	}

	assertRawTxn(t, got[0], RawTxn{
		Date:      time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC),
		Amount:    money.Money{Amount: 123456, Currency: "GBP"},
		Payee:     "ACME & Sons / #42 (R&D)",
		Reference: "Invoice   1001",
	})
	if got[0].ProviderMeta["ID"] != "rev-gbp-1" || got[0].ProviderMeta["Balance"] != "2,345.67" {
		t.Fatalf("first ProviderMeta = %#v, want raw Revolut fields", got[0].ProviderMeta)
	}

	assertRawTxn(t, got[1], RawTxn{
		Date:      time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
		Amount:    money.Money{Amount: -98765, Currency: "EUR"},
		Payee:     `Cafe "Le Test"`,
		Reference: "Refund / EUR",
	})
}

func TestRevolutParserBusinessStateFeeAndReferenceFallback(t *testing.T) {
	t.Parallel()

	sample := strings.NewReader(`Date completed (UTC),ID,Description,Reference,Amount,Fee,Payment currency,State
2026-06-01 10:00:00,rev-completed,Card merchant,,-10.00,-0.50,GBP,COMPLETED
,rev-pending,Pending merchant,Pending reference,not-money,0.00,GBP,PENDING
2026-06-02 10:00:00,rev-reverted,Reverted merchant,Reverted reference,-3.00,0.00,GBP,REVERTED
`)

	got, err := (RevolutParser{}).Parse(sample)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(Parse) = %d, want only completed rows", len(got))
	}
	assertRawTxn(t, got[0], RawTxn{
		Date:      time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Amount:    money.Money{Amount: -1050, Currency: "GBP"},
		Payee:     "Card merchant",
		Reference: "Card merchant",
	})
	if got[0].ProviderMeta["Payment currency"] != "GBP" || got[0].ProviderMeta["Fee"] != "-0.50" {
		t.Fatalf("ProviderMeta = %#v, want raw business currency and fee fields", got[0].ProviderMeta)
	}
}

func TestRevolutParserMalformedRowReportsRowNumber(t *testing.T) {
	t.Parallel()

	sample := strings.NewReader(`Date started (UTC),Date completed (UTC),ID,Type,Description,Reference,Amount,Fee,Currency,State,Balance
2026-03-04 10:11:12,2026-03-04 10:11:30,rev-gbp-1,CARD_PAYMENT,ACME,Invoice 1001,1.00,0.00,GBP,COMPLETED,2.00
2026-03-05 09:00:00,2026-03-05 09:00:05,rev-gbp-2,CARD_PAYMENT,ACME,Invoice 1002,not-money,0.00,GBP,COMPLETED,2.00
`)

	_, err := (RevolutParser{}).Parse(sample)
	if err == nil {
		t.Fatal("Parse() error = nil, want malformed row error")
	}
	var rowErr *ParseRowError
	if !errors.As(err, &rowErr) {
		t.Fatalf("Parse() error = %T %[1]v, want *ParseRowError", err)
	}
	if rowErr.Row != 3 {
		t.Fatalf("ParseRowError.Row = %d, want 3", rowErr.Row)
	}
	if !strings.Contains(err.Error(), "row 3") {
		t.Fatalf("Parse() error = %q, want row number", err.Error())
	}
}

func assertRawTxn(t *testing.T, got RawTxn, want RawTxn) {
	t.Helper()
	if !got.Date.Equal(want.Date) {
		t.Fatalf("Date = %s, want %s", got.Date, want.Date)
	}
	if got.Amount != want.Amount {
		t.Fatalf("Amount = %+v, want %+v", got.Amount, want.Amount)
	}
	if got.Payee != want.Payee {
		t.Fatalf("Payee = %q, want %q", got.Payee, want.Payee)
	}
	if got.Reference != want.Reference {
		t.Fatalf("Reference = %q, want %q", got.Reference, want.Reference)
	}
}
