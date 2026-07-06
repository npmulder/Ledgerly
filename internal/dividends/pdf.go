package dividends

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
	dividendDocumentPDFMIME          = "application/pdf"
	dividendDocumentPDFRenderTimeout = 30 * time.Second
	dividendDocumentStoragePrefix    = "ledgerly.print.dividend."
)

// ChromeDocumentPDFEngine renders the embedded SPA dividend print routes with
// headless Chrome.
type ChromeDocumentPDFEngine struct {
	baseURL string
	timeout time.Duration
}

// NewChromeDocumentPDFEngine returns a chromedp-backed dividend document
// renderer.
func NewChromeDocumentPDFEngine(baseURL string) *ChromeDocumentPDFEngine {
	return &ChromeDocumentPDFEngine{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		timeout: dividendDocumentPDFRenderTimeout,
	}
}

// RenderDividendVoucherPDF renders the dividend voucher print route to A4 PDF.
func (e *ChromeDocumentPDFEngine) RenderDividendVoucherPDF(ctx context.Context, payload DividendDocumentPayload) ([]byte, error) {
	return e.render(ctx, "dividend-voucher", payload)
}

// RenderBoardMinutesPDF renders the board minutes print route to A4 PDF.
func (e *ChromeDocumentPDFEngine) RenderBoardMinutesPDF(ctx context.Context, payload DividendDocumentPayload) ([]byte, error) {
	return e.render(ctx, "board-minutes", payload)
}

func (e *ChromeDocumentPDFEngine) render(ctx context.Context, kind string, payload DividendDocumentPayload) ([]byte, error) {
	if e == nil || strings.TrimSpace(e.baseURL) == "" {
		return nil, fmt.Errorf("dividends: document PDF base URL is required")
	}
	declarationID := strings.TrimSpace(string(payload.Declaration.ID))
	if declarationID == "" {
		return nil, fmt.Errorf("dividends: declaration id is required for PDF render")
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("dividends: marshal document print payload: %w", err)
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
		timeout = dividendDocumentPDFRenderTimeout
	}
	runCtx, cancel := context.WithTimeout(browserCtx, timeout)
	defer cancel()

	var pdf []byte
	keyLiteral, valueLiteral := dividendDocumentStorageScriptLiterals(kind, declarationID, payloadJSON)
	printURL := e.baseURL + "/print/" + kind + "/" + url.PathEscape(declarationID)
	if err := chromedp.Run(runCtx,
		chromedp.Navigate(e.baseURL+"/"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Evaluate(fmt.Sprintf("localStorage.setItem(%s, %s)", keyLiteral, valueLiteral), nil),
		chromedp.Navigate(printURL),
		chromedp.WaitReady(`[data-ledgerly-print-ready="true"]`, chromedp.ByQuery),
		chromedp.Evaluate(`document.fonts ? document.fonts.ready.then(() => true) : true`, nil),
		chromedp.Evaluate(waitForDividendPrintImagesScript, nil),
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
		return nil, fmt.Errorf("dividends: render %s PDF with chromedp: %w", kind, err)
	}
	if len(pdf) == 0 {
		return nil, fmt.Errorf("dividends: rendered %s PDF is empty", kind)
	}
	return pdf, nil
}

func dividendDocumentStorageScriptLiterals(kind string, declarationID string, payloadJSON []byte) (string, string) {
	key, _ := json.Marshal(dividendDocumentStorageKey(kind, declarationID))
	value, _ := json.Marshal(string(payloadJSON))
	return string(key), string(value)
}

func dividendDocumentStorageKey(kind string, declarationID string) string {
	return dividendDocumentStoragePrefix + strings.TrimSpace(kind) + "." + strings.TrimSpace(declarationID)
}

const waitForDividendPrintImagesScript = `Promise.all(Array.from(document.images).map((img) => {
  if (img.complete) {
    return true;
  }
  return new Promise((resolve) => {
    img.addEventListener("load", resolve, { once: true });
    img.addEventListener("error", resolve, { once: true });
  });
})).then(() => true)`
