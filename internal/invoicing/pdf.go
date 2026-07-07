package invoicing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

const (
	invoicePDFMIME          = "application/pdf"
	invoicePDFRenderTimeout = 30 * time.Second
	invoicePDFStoragePrefix = "ledgerly.print.invoice."
)

// InvoicePDFEngine renders an invoice print payload into PDF bytes.
type InvoicePDFEngine interface {
	RenderInvoicePDF(context.Context, InvoicePrintPayload) ([]byte, error)
}

// InvoicePDFAssetStore persists immutable PDF bytes and reloads them by the
// asset URL stored on invoices.pdf_asset.
type InvoicePDFAssetStore interface {
	StoreInvoicePDF(context.Context, []byte) (string, error)
	LoadInvoicePDF(context.Context, string) ([]byte, error)
}

// InvoicePrintPayload is the single data contract consumed by the React print
// route and the chromedp renderer.
type InvoicePrintPayload struct {
	Invoice           Invoice                 `json:"invoice"`
	Client            Client                  `json:"client"`
	Identity          InvoicePrintIdentity    `json:"identity"`
	VATRegistered     bool                    `json:"vat_registered"`
	VATRate           string                  `json:"vat_rate"`
	VATTaxYear        string                  `json:"vat_tax_year"`
	ReverseChargeNote *string                 `json:"reverse_charge_note,omitempty"`
	LockedRate        *InvoicePrintLockedRate `json:"locked_rate,omitempty"`
	DraftWatermark    bool                    `json:"draft_watermark"`
}

// InvoicePrintIdentity snapshots identity data for one render.
type InvoicePrintIdentity struct {
	TradingName   string  `json:"trading_name"`
	LegalName     string  `json:"legal_name"`
	CompanyNumber string  `json:"company_number"`
	Address       Address `json:"address"`
	VATNumber     *string `json:"vat_number,omitempty"`
	IBAN          string  `json:"iban"`
	BIC           string  `json:"bic"`
	BankName      string  `json:"bank_name"`
	LogoAssetURL  *string `json:"logo_asset_url,omitempty"`
	LogoDataURI   *string `json:"logo_data_uri,omitempty"`
}

// InvoicePrintLockedRate is the locked FX rate displayed on sent invoices.
type InvoicePrintLockedRate struct {
	ID   int64  `json:"id"`
	Rate string `json:"rate"`
}

// ChromePDFEngine renders the embedded SPA print route with headless Chrome.
type ChromePDFEngine struct {
	baseURL string
	timeout time.Duration
}

// NewChromePDFEngine returns a chromedp-backed print route renderer.
func NewChromePDFEngine(baseURL string) *ChromePDFEngine {
	return &ChromePDFEngine{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		timeout: invoicePDFRenderTimeout,
	}
}

// RenderInvoicePDF injects payload into browser storage, navigates to the
// React print route, waits for fonts/assets, and prints an A4 PDF.
func (e *ChromePDFEngine) RenderInvoicePDF(ctx context.Context, payload InvoicePrintPayload) ([]byte, error) {
	if e == nil || strings.TrimSpace(e.baseURL) == "" {
		return nil, fmt.Errorf("invoicing: PDF base URL is required")
	}
	invoiceID := strings.TrimSpace(payload.Invoice.ID)
	if invoiceID == "" {
		return nil, fmt.Errorf("invoicing: invoice id is required for PDF render")
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("invoicing: marshal invoice print payload: %w", err)
	}

	allocatorOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
	)
	if chromePath := os.Getenv("CHROME_BIN"); strings.TrimSpace(chromePath) != "" {
		allocatorOpts = append(allocatorOpts, chromedp.ExecPath(chromePath))
	}

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, allocatorOpts...)
	defer cancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	timeout := e.timeout
	if timeout <= 0 {
		timeout = invoicePDFRenderTimeout
	}
	runCtx, cancel := context.WithTimeout(browserCtx, timeout)
	defer cancel()

	var pdf []byte
	keyLiteral, valueLiteral := storageScriptLiterals(invoiceID, payloadJSON)
	printURL := e.baseURL + "/print/invoice/" + url.PathEscape(invoiceID)
	if payload.DraftWatermark {
		printURL += "?draft=1"
	}
	if err := chromedp.Run(runCtx,
		chromedp.Navigate(e.baseURL+"/"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Evaluate(fmt.Sprintf("localStorage.setItem(%s, %s)", keyLiteral, valueLiteral), nil),
		chromedp.Navigate(printURL),
		chromedp.WaitReady(`[data-ledgerly-print-ready="true"]`, chromedp.ByQuery),
		chromedp.Evaluate(`document.fonts ? document.fonts.ready.then(() => true) : true`, nil),
		chromedp.Evaluate(waitForPrintImagesScript, nil),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			pdf, _, err = page.PrintToPDF().
				WithPrintBackground(true).
				WithPreferCSSPageSize(true).
				WithPaperWidth(8.27).
				WithPaperHeight(11.69).
				WithMarginTop(0).
				WithMarginRight(0).
				WithMarginBottom(0).
				WithMarginLeft(0).
				Do(ctx)
			return err
		}),
	); err != nil {
		return nil, fmt.Errorf("invoicing: render invoice PDF with chromedp: %w", err)
	}
	if len(pdf) == 0 {
		return nil, fmt.Errorf("invoicing: rendered invoice PDF is empty")
	}
	return pdf, nil
}

func storageScriptLiterals(invoiceID string, payloadJSON []byte) (string, string) {
	key, _ := json.Marshal(invoicePrintStorageKey(invoiceID))
	value, _ := json.Marshal(string(payloadJSON))
	return string(key), string(value)
}

func invoicePrintStorageKey(invoiceID string) string {
	return invoicePDFStoragePrefix + strings.TrimSpace(invoiceID)
}

const waitForPrintImagesScript = `Promise.all(Array.from(document.images).map((img) => {
  if (img.complete) {
    return true;
  }
  return new Promise((resolve) => {
    img.addEventListener("load", resolve, { once: true });
    img.addEventListener("error", resolve, { once: true });
  });
})).then(() => true)`
