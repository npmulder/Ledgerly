// Package chrome owns headless Chromium integration helpers.
package chrome

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

const defaultSmokeTimeout = 20 * time.Second

// RenderAboutBlankPDF renders a small PDF through chromedp. It is intentionally
// narrow so the deploy image can prove Chromium works without depending on a
// domain module's PDF route.
func RenderAboutBlankPDF(ctx context.Context, outputPath string) error {
	if outputPath == "" {
		return fmt.Errorf("output path is required")
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
	)
	if chromePath := os.Getenv("CHROME_BIN"); chromePath != "" {
		opts = append(opts, chromedp.ExecPath(chromePath))
	}

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	runCtx, cancel := context.WithTimeout(browserCtx, defaultSmokeTimeout)
	defer cancel()

	var pdf []byte
	if err := chromedp.Run(runCtx,
		chromedp.Navigate("about:blank"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			pdf, _, err = page.PrintToPDF().WithPrintBackground(true).Do(ctx)
			return err
		}),
	); err != nil {
		return fmt.Errorf("render about:blank PDF with chromedp: %w", err)
	}

	if len(pdf) == 0 {
		return fmt.Errorf("render about:blank PDF with chromedp: empty PDF")
	}

	if err := os.WriteFile(outputPath, pdf, 0o600); err != nil {
		return fmt.Errorf("write PDF %q: %w", outputPath, err)
	}

	return nil
}
