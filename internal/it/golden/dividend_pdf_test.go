package golden

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/dividends"
	"github.com/npmulder/ledgerly/internal/identity"
	moneyfxmoney "github.com/npmulder/ledgerly/internal/moneyfx/money"
)

func TestDividendPDFGoldens(t *testing.T) {
	requireChrome(t)

	server := startInvoicePrintServer(t)
	engine := dividends.NewChromeDocumentPDFEngine(server.URL)
	payload := dividendDocumentPayload(t)

	t.Run("dividend-voucher-npm", func(t *testing.T) {
		pdfBytes, err := engine.RenderDividendVoucherPDF(context.Background(), payload)
		if err != nil {
			t.Fatalf("RenderDividendVoucherPDF() error = %v", err)
		}
		text, err := extractPDFText(pdfBytes)
		if err != nil {
			t.Fatalf("extract voucher PDF text: %v", err)
		}
		for _, want := range []string{
			"137792C",
			"£30.00",
			"£3,000.00",
			"withholding: none",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("voucher PDF text missing %q:\n%s", want, text)
			}
		}
		PDF(t, "dividend-voucher-npm", pdfBytes)
	})

	t.Run("board-minutes-npm", func(t *testing.T) {
		pdfBytes, err := engine.RenderBoardMinutesPDF(context.Background(), payload)
		if err != nil {
			t.Fatalf("RenderBoardMinutesPDF() error = %v", err)
		}
		text, err := extractPDFText(pdfBytes)
		if err != nil {
			t.Fatalf("extract board minutes PDF text: %v", err)
		}
		for _, want := range []string{
			"£17,160.00",
			"£30.00",
			"£3,000.00",
			"director's loan account",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("minutes PDF text missing %q:\n%s", want, text)
			}
		}
		PDF(t, "board-minutes-npm", pdfBytes)
	})
}

func dividendDocumentPayload(t testing.TB) dividends.DividendDocumentPayload {
	t.Helper()

	declaredDate := mustDate(t, "2026-07-03")
	createdAt := time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC)
	headroom := dividends.HeadroomBreakdown{
		AsOf:          declaredDate,
		FinancialYear: "2026-27",
		Lines: []dividends.MoneyLine{
			{Label: "Retained earnings b/fwd", Amount: gbpMoney(1_200_000)},
			{Label: "Profit YTD (after expenses)", Amount: gbpMoney(516_000)},
			{Label: "Corporation tax provision at 0%", Amount: gbpMoney(0)},
			{Label: "Dividends already declared YTD", Amount: gbpMoney(0)},
			{Label: "Available to distribute", Amount: gbpMoney(1_716_000)},
		},
		Available:     gbpMoney(1_716_000),
		Distributable: true,
	}
	return dividends.DividendDocumentPayload{
		Declaration: dividends.Declaration{
			ID:              "dividend-golden-2026-07",
			DeclaredDate:    declaredDate,
			Amount:          gbpMoney(300_000),
			PerShare:        gbpMoney(3_000),
			Shares:          100,
			ShareholderName: "N. Meyer",
			CompanySnapshot: &dividends.CompanySnapshot{
				TradingName:   "NPM Limited",
				LegalName:     "NPM Limited",
				CompanyNumber: "137792C",
				RegisteredOffice: identity.RegisteredOffice{
					Line1:    "18 Athol St",
					Locality: "Douglas",
					Country:  "IM",
				},
				DirectorName: "N. Meyer",
			},
			ShareholderSnapshot: &dividends.ShareholderSnapshot{
				Name:   "N. Meyer",
				Shares: 100,
				Class:  "ordinary £1",
			},
			HeadroomSnapshot: &headroom,
			WithholdingSnapshot: &dividends.WithholdingSnapshot{
				TaxYear: "2026-27",
				Policy:  "none",
				Note:    "No dividend withholding tax is deducted under the active jurisdiction pack (withholding: none).",
			},
			CreatedAt: createdAt,
		},
	}
}

func gbpMoney(amount int64) moneyfxmoney.Money {
	return moneyfxmoney.Money{Amount: amount, Currency: "GBP"}
}
