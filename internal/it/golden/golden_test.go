package golden

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	pdfreader "github.com/ledongthuc/pdf"
)

const (
	fixtureName      = "fixture-document"
	timestampPattern = `20[0-9]{2}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z`
	baseTimestamp    = "2030-01-02T03:04:05Z"
	altTimestamp     = "2040-02-03T04:05:06Z"
)

func TestPDFMatchesGolden(t *testing.T) {
	pdfBytes := renderFixturePDF(t, fixtureOptions{})
	PDF(t, fixtureName, pdfBytes, WithMasks(timestampPattern))
}

func TestSelfTestTextLayerCatchesWordChange(t *testing.T) {
	if *update {
		t.Skip("failure proof uses committed goldens; skipping while updating")
	}
	if os.Getenv("GOLDEN_SELFTEST_SCENARIO") == "text-change" {
		pdfBytes := renderFixturePDF(t, fixtureOptions{textWord: "Support"})
		PDF(t, fixtureName, pdfBytes, WithMasks(timestampPattern))
		return
	}
	requireChrome(t)

	output, err := runSelfTestScenario(t, "TestSelfTestTextLayerCatchesWordChange", "text-change", "")
	if err == nil {
		t.Fatalf("text-change scenario passed, want failure; output:\n%s", output)
	}
	if !strings.Contains(output, "PDF text mismatch") {
		t.Fatalf("text-change output did not include text mismatch:\n%s", output)
	}
	if !strings.Contains(output, "-Company: Ledgerly Services Limited") || !strings.Contains(output, "+Company: Ledgerly Support Limited") {
		t.Fatalf("text-change output did not include expected inline diff:\n%s", output)
	}
}

func TestSelfTestRasterLayerCatchesLayoutShift(t *testing.T) {
	if *update {
		t.Skip("failure proof uses committed goldens; skipping while updating")
	}
	if os.Getenv("GOLDEN_SELFTEST_SCENARIO") == "layout-shift" {
		pdfBytes := renderFixturePDF(t, fixtureOptions{layoutShiftPX: 2})
		PDF(t, fixtureName, pdfBytes, WithMasks(timestampPattern))
		return
	}
	requireChrome(t)

	artifactRoot := t.TempDir()
	output, err := runSelfTestScenario(t, "TestSelfTestRasterLayerCatchesLayoutShift", "layout-shift", artifactRoot)
	if err == nil {
		t.Fatalf("layout-shift scenario passed, want failure; output:\n%s", output)
	}
	if !strings.Contains(output, "PDF raster mismatch") {
		t.Fatalf("layout-shift output did not include raster mismatch:\n%s", output)
	}

	for _, name := range []string{"got.png", "want.png"} {
		path := filepath.Join(artifactRoot, fixtureName, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("expected raster artifact %s: %v\noutput:\n%s", path, err, output)
		}
		if info.Size() == 0 {
			t.Fatalf("expected raster artifact %s to be non-empty", path)
		}
	}
}

func TestSelfTestMaskedTimestampPasses(t *testing.T) {
	if *update {
		t.Skip("masked timestamp proof uses committed goldens; skipping while updating")
	}
	pdfBytes := renderFixturePDF(t, fixtureOptions{timestamp: altTimestamp})
	PDF(t, fixtureName, pdfBytes, WithMasks(timestampPattern))
}

func TestUpdateModeGuard(t *testing.T) {
	if os.Getenv("GOLDEN_UPDATE_GUARD_SUBPROCESS") == "1" {
		PDF(t, "guard", []byte("not a pdf"))
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestUpdateModeGuard$", "-update")
	cmd.Env = append(os.Environ(),
		"CI=true",
		"GOLDEN_UPDATE_GUARD_SUBPROCESS=1",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("CI -update guard subprocess passed, want failure; output:\n%s", output)
	}
	if !strings.Contains(string(output), "refusing to run -update while CI=true") {
		t.Fatalf("CI -update guard output missing guard message:\n%s", output)
	}
}

func TestRasterMaskRedactsOnlyMatchingTextSpans(t *testing.T) {
	masks := []textMask{{
		pattern: timestampPattern,
		re:      regexp.MustCompile(timestampPattern),
	}}

	row := pdfreader.TextHorizontal{
		{X: 10, Y: 20, W: 64, S: "Generated:"},
		{X: 84, Y: 20, W: 132, S: baseTimestamp},
		{X: 230, Y: 20, W: 136, S: "Total: GBP 1,234.56"},
	}
	line, segments := textLineSegments(row)
	redactions := textRedactionsForMasks(line, segments, masks)
	if len(redactions) != 1 {
		t.Fatalf("redactions = %d, want 1 for line %q", len(redactions), line)
	}
	if redactions[0].value[redactions[0].start:redactions[0].end] != baseTimestamp {
		t.Fatalf("redacted %q, want timestamp only", redactions[0].value[redactions[0].start:redactions[0].end])
	}

	singleChunk := pdfreader.TextHorizontal{{
		X: 10,
		Y: 20,
		W: 360,
		S: "Generated: " + baseTimestamp + " Total: GBP 1,234.56",
	}}
	line, segments = textLineSegments(singleChunk)
	redactions = textRedactionsForMasks(line, segments, masks)
	if len(redactions) != 1 {
		t.Fatalf("single-chunk redactions = %d, want 1 for line %q", len(redactions), line)
	}
	got := redactions[0].value[redactions[0].start:redactions[0].end]
	if got != baseTimestamp {
		t.Fatalf("single-chunk redacted %q, want timestamp only", got)
	}
	if redactions[0].start == 0 || redactions[0].end == len(redactions[0].value) {
		t.Fatalf("single-chunk redaction covered the full text chunk: %+v", redactions[0])
	}
}

type fixtureOptions struct {
	textWord      string
	layoutShiftPX int
	timestamp     string
}

func renderFixturePDF(t *testing.T, opts fixtureOptions) []byte {
	t.Helper()
	requireChrome(t)

	html := fixtureHTML(opts)
	chromePath := findChromePath()

	allocatorOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
	)
	if chromePath != "" {
		allocatorOpts = append(allocatorOpts, chromedp.ExecPath(chromePath))
	}

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), allocatorOpts...)
	defer cancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	runCtx, cancel := context.WithTimeout(browserCtx, 20*time.Second)
	defer cancel()

	var pdfBytes []byte
	dataURL := "data:text/html;charset=utf-8," + url.PathEscape(html)
	if err := chromedp.Run(runCtx,
		chromedp.Navigate(dataURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Evaluate(`document.fonts ? document.fonts.ready.then(() => true) : true`, nil),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			pdfBytes, _, err = page.PrintToPDF().
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
		t.Fatalf("render fixture HTML to PDF: %v", err)
	}
	if len(pdfBytes) == 0 {
		t.Fatal("render fixture HTML to PDF returned empty PDF")
	}
	return pdfBytes
}

func fixtureHTML(opts fixtureOptions) string {
	textWord := opts.textWord
	if textWord == "" {
		textWord = "Services"
	}
	timestamp := opts.timestamp
	if timestamp == "" {
		timestamp = baseTimestamp
	}
	marginTop := 32 + opts.layoutShiftPX

	return fmt.Sprintf(`<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <style>
    @page { size: A4; margin: 0; }
    html, body { margin: 0; padding: 0; }
    body {
      color: #111827;
      font: 14px Arial, sans-serif;
      line-height: 1.35;
    }
    .page {
      box-sizing: border-box;
      width: 794px;
      min-height: 1123px;
      padding: 48px 56px;
    }
    h1 {
      font-size: 28px;
      font-weight: 700;
      margin: 0 0 24px;
    }
    p { margin: 0 0 10px; }
    .timestamp {
      color: #374151;
      font-size: 12px;
      margin-top: 16px;
    }
    .proof {
      border: 1px solid #111827;
      margin-top: %dpx;
      padding: 12px;
    }
  </style>
</head>
<body>
  <main class="page">
    <h1>Golden Document Snapshot</h1>
    <p>Company: Ledgerly %s Limited</p>
    <p>Amount: GBP 1,234.56</p>
    <p>Rate lock: GBP/EUR 1.1729</p>
    <p class="timestamp">%s</p>
    <div class="proof">Fixed wording for raster layout proof.</div>
  </main>
</body>
</html>`, marginTop, textWord, timestamp)
}

func runSelfTestScenario(t *testing.T, testName, scenario, artifactRoot string) (string, error) {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$", "-test.v")
	env := append(os.Environ(), "GOLDEN_SELFTEST_SCENARIO="+scenario)
	if artifactRoot != "" {
		env = append(env, "GOLDEN_ARTIFACT_DIR="+artifactRoot)
	}
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func requireChrome(t *testing.T) {
	t.Helper()

	if os.Getenv("GOLDEN_REQUIRE_CHROME") != "1" && os.Getenv("GOLDEN_RUN_CHROME") != "1" {
		t.Skip("Chrome-backed golden self-tests require deterministic Chrome; run task golden:docker")
	}
	if findChromePath() != "" {
		return
	}
	message := "Chrome/headless-shell not found; set CHROME_BIN or run task golden:docker"
	if os.Getenv("GOLDEN_REQUIRE_CHROME") == "1" {
		t.Fatal(message)
	}
	t.Skip(message)
}

func findChromePath() string {
	if chromePath := os.Getenv("CHROME_BIN"); chromePath != "" {
		if _, err := os.Stat(chromePath); err == nil {
			return chromePath
		}
		return ""
	}

	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "headless-shell"} {
		path, err := exec.LookPath(name)
		if err == nil {
			return path
		}
	}

	if runtime.GOOS == "darwin" {
		for _, path := range []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		} {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}
	return ""
}
