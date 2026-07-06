package reports

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
	plPDFRenderTimeout = 30 * time.Second
	plPDFStoragePrefix = "ledgerly.print.reports.pl."
)

// ChromePLPDFEngine renders the embedded SPA reports print route to PDF.
type ChromePLPDFEngine struct {
	baseURL string
	timeout time.Duration
}

// NewChromePLPDFEngine returns a chromedp-backed reports print renderer.
func NewChromePLPDFEngine(baseURL string) *ChromePLPDFEngine {
	return &ChromePLPDFEngine{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		timeout: plPDFRenderTimeout,
	}
}

// RenderPLPDF injects payload into browser storage, navigates to the React
// reports print route, and prints an A4 PDF.
func (e *ChromePLPDFEngine) RenderPLPDF(ctx context.Context, payload PLPrintPayload) ([]byte, error) {
	if e == nil || strings.TrimSpace(e.baseURL) == "" {
		return nil, fmt.Errorf("reports: PDF base URL is required")
	}
	periodID := plPrintPeriodID(payload)
	if periodID == "" {
		return nil, fmt.Errorf("reports: P&L print period is required")
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("reports: marshal P&L print payload: %w", err)
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
		timeout = plPDFRenderTimeout
	}
	runCtx, cancel := context.WithTimeout(browserCtx, timeout)
	defer cancel()

	var pdf []byte
	keyLiteral, valueLiteral := plStorageScriptLiterals(periodID, payloadJSON)
	printURL := e.baseURL + "/print/reports/pl/" + url.PathEscape(periodID)
	if err := chromedp.Run(runCtx,
		chromedp.Navigate(e.baseURL+"/"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Evaluate(fmt.Sprintf("localStorage.setItem(%s, %s)", keyLiteral, valueLiteral), nil),
		chromedp.Navigate(printURL),
		chromedp.WaitReady(`[data-ledgerly-print-ready="true"]`, chromedp.ByQuery),
		chromedp.Evaluate(`document.fonts ? document.fonts.ready.then(() => true) : true`, nil),
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
		return nil, fmt.Errorf("reports: render P&L PDF with chromedp: %w", err)
	}
	if len(pdf) == 0 {
		return nil, fmt.Errorf("reports: rendered P&L PDF is empty")
	}
	return pdf, nil
}

func plStorageScriptLiterals(periodID string, payloadJSON []byte) (string, string) {
	key, _ := json.Marshal(plPrintStorageKey(periodID))
	value, _ := json.Marshal(string(payloadJSON))
	return string(key), string(value)
}

func plPrintStorageKey(periodID string) string {
	return plPDFStoragePrefix + strings.TrimSpace(periodID)
}

func plPrintPeriodID(payload PLPrintPayload) string {
	from := strings.TrimSpace(payload.Report.Period.From)
	to := strings.TrimSpace(payload.Report.Period.To)
	if from == "" || to == "" {
		return ""
	}
	return from + "_" + to
}
