// Package golden provides golden-file assertions for rendered documents.
package golden

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gen2brain/go-fitz"
	pdfreader "github.com/ledongthuc/pdf"
	"github.com/pmezard/go-difflib/difflib"
)

const (
	defaultDPI      = 144.0
	goldenDir       = "testdata/golden"
	hashVersion     = "ledgerly-pdf-raster-v1"
	defaultMaskText = "<masked>"
)

var update = flag.Bool("update", false, "rewrite golden document snapshots")

// Option customizes a golden PDF comparison.
type Option func(*config)

type config struct {
	masks       []textMask
	artifactDir string
	dpi         float64
}

type textMask struct {
	pattern     string
	replacement string
	re          *regexp.Regexp
}

// WithMasks replaces text matching each regex pattern with "<masked>" before
// text comparison and before raster hashing.
func WithMasks(patterns ...string) Option {
	return func(cfg *config) {
		for _, pattern := range patterns {
			cfg.masks = append(cfg.masks, textMask{
				pattern:     pattern,
				replacement: defaultMaskText,
			})
		}
	}
}

// WithMask replaces text matching pattern with replacement before comparison.
func WithMask(pattern, replacement string) Option {
	return func(cfg *config) {
		cfg.masks = append(cfg.masks, textMask{
			pattern:     pattern,
			replacement: replacement,
		})
	}
}

// WithArtifactDir writes visual mismatch artifacts under dir.
func WithArtifactDir(dir string) Option {
	return func(cfg *config) {
		cfg.artifactDir = dir
	}
}

// PDF compares pdfBytes against text and visual golden snapshots for name.
//
// Text goldens live at testdata/golden/<name>.txt. Raster hashes live at
// testdata/golden/<name>.hash, with a baseline PNG beside the hash so visual
// mismatches can emit both got.png and want.png artifacts.
func PDF(t testing.TB, name string, pdfBytes []byte, opts ...Option) {
	t.Helper()

	if *update && strings.EqualFold(os.Getenv("CI"), "true") {
		t.Fatalf("golden: refusing to run -update while CI=true")
	}
	if len(pdfBytes) == 0 {
		t.Fatalf("golden: %q PDF is empty", name)
	}

	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := cfg.compileMasks(); err != nil {
		t.Fatalf("golden: %v", err)
	}

	paths, err := goldenPaths(name)
	if err != nil {
		t.Fatalf("golden: %v", err)
	}

	text, err := extractPDFText(pdfBytes)
	if err != nil {
		t.Fatalf("golden: extract text for %q: %v", name, err)
	}
	text = cfg.normalizeText(text)
	compareText(t, paths.text, text)

	raster, err := renderRaster(pdfBytes, cfg.masks, cfg.dpi)
	if err != nil {
		t.Fatalf("golden: render raster for %q: %v", name, err)
	}
	compareRaster(t, name, paths, cfg, raster)
}

func defaultConfig() config {
	return config{dpi: defaultDPI}
}

func (cfg *config) compileMasks() error {
	for i := range cfg.masks {
		if cfg.masks[i].replacement == "" {
			cfg.masks[i].replacement = defaultMaskText
		}
		re, err := regexp.Compile(cfg.masks[i].pattern)
		if err != nil {
			return fmt.Errorf("compile mask %q: %w", cfg.masks[i].pattern, err)
		}
		cfg.masks[i].re = re
	}
	return nil
}

func (cfg config) normalizeText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	text = strings.Join(lines, "\n")
	text = strings.Trim(text, "\n")
	if text != "" {
		text += "\n"
	}
	for _, mask := range cfg.masks {
		text = mask.re.ReplaceAllString(text, mask.replacement)
	}
	return text
}

type paths struct {
	text string
	hash string
	png  string
}

func goldenPaths(name string) (paths, error) {
	clean, err := cleanName(name)
	if err != nil {
		return paths{}, err
	}
	base := filepath.Join(goldenDir, filepath.FromSlash(clean))
	return paths{
		text: base + ".txt",
		hash: base + ".hash",
		png:  base + ".png",
	}, nil
}

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("golden name is required")
	}
	name = filepath.ToSlash(name)
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("golden name %q must be relative", name)
	}
	clean := pathClean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("golden name %q escapes testdata/golden", name)
	}
	return clean, nil
}

func pathClean(name string) string {
	parts := strings.Split(name, "/")
	stack := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return "../" + strings.Join(parts, "/")
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, part)
		}
	}
	if len(stack) == 0 {
		return "."
	}
	return strings.Join(stack, "/")
}

func compareText(t testing.TB, wantPath, got string) {
	t.Helper()

	if *update {
		writeFile(t, wantPath, []byte(got))
		return
	}

	wantBytes, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("golden: read text golden %s: %v; run go test -run ... -update", wantPath, err)
	}
	want := string(wantBytes)
	if want == got {
		return
	}

	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(want),
		B:        difflib.SplitLines(got),
		FromFile: wantPath,
		ToFile:   "got text",
		Context:  3,
	})
	if err != nil {
		t.Fatalf("golden: create text diff: %v", err)
	}
	t.Fatalf("golden: PDF text mismatch\n%s", diff)
}

type rasterSnapshot struct {
	Hash      string
	PNG       []byte
	PageCount int
	Width     int
	Height    int
}

type hashFile struct {
	Version   string  `json:"version"`
	DPI       float64 `json:"dpi"`
	SHA256    string  `json:"sha256"`
	PageCount int     `json:"page_count"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
}

func compareRaster(t testing.TB, name string, paths paths, cfg config, got rasterSnapshot) {
	t.Helper()

	wantHash := hashFile{
		Version:   hashVersion,
		DPI:       cfg.dpi,
		SHA256:    got.Hash,
		PageCount: got.PageCount,
		Width:     got.Width,
		Height:    got.Height,
	}

	if *update {
		body, err := json.MarshalIndent(wantHash, "", "  ")
		if err != nil {
			t.Fatalf("golden: encode raster hash: %v", err)
		}
		body = append(body, '\n')
		writeFile(t, paths.hash, body)
		writeFile(t, paths.png, got.PNG)
		return
	}

	wantBytes, err := os.ReadFile(paths.hash)
	if err != nil {
		t.Fatalf("golden: read raster hash %s: %v; run go test -run ... -update", paths.hash, err)
	}
	var want hashFile
	if err := json.Unmarshal(wantBytes, &want); err != nil {
		t.Fatalf("golden: parse raster hash %s: %v", paths.hash, err)
	}
	if want.Version != hashVersion {
		t.Fatalf("golden: raster hash version = %q, want %q", want.Version, hashVersion)
	}
	if want.DPI != cfg.dpi {
		t.Fatalf("golden: raster hash DPI = %.1f, want %.1f; run go test -run ... -update", want.DPI, cfg.dpi)
	}
	if want.SHA256 == got.Hash && want.PageCount == got.PageCount && want.Width == got.Width && want.Height == got.Height {
		return
	}

	gotPath, wantPath := writeRasterArtifacts(t, name, paths.png, cfg.artifactDir, got.PNG)
	t.Fatalf("golden: PDF raster mismatch for %q\nwant sha256: %s (%d page(s), %dx%d @ %.1f DPI)\n got sha256: %s (%d page(s), %dx%d @ %.1f DPI)\nartifacts: got=%s want=%s",
		name,
		want.SHA256, want.PageCount, want.Width, want.Height, want.DPI,
		got.Hash, got.PageCount, got.Width, got.Height, cfg.dpi,
		gotPath, wantPath,
	)
}

func writeRasterArtifacts(t testing.TB, name, wantGoldenPath, configuredDir string, gotPNG []byte) (string, string) {
	t.Helper()

	clean, err := cleanName(name)
	if err != nil {
		t.Fatalf("golden: %v", err)
	}

	dir := configuredDir
	if dir == "" {
		dir = os.Getenv("GOLDEN_ARTIFACT_DIR")
	}
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "ledgerly-golden-artifacts")
	}
	dir = filepath.Join(dir, filepath.FromSlash(clean))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("golden: create raster artifact dir %s: %v", dir, err)
	}

	gotPath := filepath.Join(dir, "got.png")
	wantPath := filepath.Join(dir, "want.png")
	if err := os.WriteFile(gotPath, gotPNG, 0o600); err != nil {
		t.Fatalf("golden: write got raster artifact %s: %v", gotPath, err)
	}

	wantPNG, err := os.ReadFile(wantGoldenPath)
	if err != nil {
		t.Fatalf("golden: read baseline raster PNG %s for artifact: %v; run go test -run ... -update", wantGoldenPath, err)
	}
	if err := os.WriteFile(wantPath, wantPNG, 0o600); err != nil {
		t.Fatalf("golden: write want raster artifact %s: %v", wantPath, err)
	}
	return gotPath, wantPath
}

func writeFile(t testing.TB, path string, body []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("golden: create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("golden: write %s: %v", path, err)
	}
}

func extractPDFText(pdfBytes []byte) (string, error) {
	reader, err := newPDFReader(pdfBytes)
	if err != nil {
		return "", err
	}

	var text strings.Builder
	for pageNum := 1; pageNum <= reader.NumPage(); pageNum++ {
		if pageNum > 1 {
			text.WriteByte('\n')
		}
		rows, err := reader.Page(pageNum).GetTextByRow()
		if err != nil {
			return "", fmt.Errorf("extract page %d rows: %w", pageNum, err)
		}
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].Position < rows[j].Position
		})
		for _, row := range rows {
			sort.Sort(row.Content)
			line := textLine(row.Content)
			if line == "" {
				continue
			}
			text.WriteString(line)
			text.WriteByte('\n')
		}
	}
	return text.String(), nil
}

func textLine(texts pdfreader.TextHorizontal) string {
	var line strings.Builder
	var prevRight float64
	for i, text := range texts {
		rawChunk := strings.ReplaceAll(text.S, "\t", " ")
		chunk := strings.TrimSpace(rawChunk)
		if chunk == "" {
			continue
		}
		if line.Len() > 0 && i > 0 && (strings.HasPrefix(rawChunk, " ") || text.X-prevRight > math.Max(1, text.FontSize*0.2)) {
			line.WriteByte(' ')
		}
		line.WriteString(chunk)
		prevRight = text.X + text.W
	}
	return strings.TrimSpace(line.String())
}

func newPDFReader(pdfBytes []byte) (*pdfreader.Reader, error) {
	return pdfreader.NewReader(bytes.NewReader(pdfBytes), int64(len(pdfBytes)))
}

func renderRaster(pdfBytes []byte, masks []textMask, dpi float64) (rasterSnapshot, error) {
	doc, err := fitz.NewFromMemory(pdfBytes)
	if err != nil {
		return rasterSnapshot{}, err
	}
	defer doc.Close()

	pageCount := doc.NumPage()
	if pageCount == 0 {
		return rasterSnapshot{}, errors.New("PDF has no pages")
	}

	images := make([]*image.RGBA, 0, pageCount)
	for page := 0; page < pageCount; page++ {
		img, err := doc.ImageDPI(page, dpi)
		if err != nil {
			return rasterSnapshot{}, fmt.Errorf("render page %d: %w", page+1, err)
		}
		images = append(images, img)
	}
	if len(masks) > 0 {
		if err := redactMaskedText(pdfBytes, images, masks, dpi); err != nil {
			return rasterSnapshot{}, err
		}
	}

	composite := compositePages(images)
	pngBytes, err := encodePNG(composite)
	if err != nil {
		return rasterSnapshot{}, err
	}
	return rasterSnapshot{
		Hash:      hashImages(images, dpi),
		PNG:       pngBytes,
		PageCount: pageCount,
		Width:     composite.Bounds().Dx(),
		Height:    composite.Bounds().Dy(),
	}, nil
}

func redactMaskedText(pdfBytes []byte, pages []*image.RGBA, masks []textMask, dpi float64) error {
	reader, err := newPDFReader(pdfBytes)
	if err != nil {
		return fmt.Errorf("open PDF for raster masks: %w", err)
	}

	for pageIndex, img := range pages {
		page := reader.Page(pageIndex + 1)
		rows, err := page.GetTextByRow()
		if err != nil {
			return fmt.Errorf("extract page %d rows for raster masks: %w", pageIndex+1, err)
		}
		for _, row := range rows {
			sort.Sort(row.Content)
			line := textLine(row.Content)
			if !matchesAnyMask(line, masks) {
				continue
			}
			for _, text := range row.Content {
				redactTextBounds(img, text, dpi)
			}
		}
	}
	return nil
}

func matchesAnyMask(text string, masks []textMask) bool {
	for _, mask := range masks {
		if mask.re.MatchString(text) {
			return true
		}
	}
	return false
}

func redactTextBounds(img *image.RGBA, text pdfreader.Text, dpi float64) {
	scale := dpi / 96.0
	pad := int(math.Ceil(2 * scale))
	x0 := int(math.Floor(text.X*scale)) - pad
	textWidth := text.W
	if textWidth <= 0 {
		textWidth = approximateTextWidth(text.S, text.FontSize)
	}
	x1 := int(math.Ceil((text.X+textWidth)*scale)) + pad

	fontSize := text.FontSize
	if fontSize <= 0 {
		fontSize = 14
	}
	baseline := text.Y * scale
	y0 := int(math.Floor(baseline-(fontSize*1.4)*scale)) - pad
	y1 := int(math.Ceil(baseline+(fontSize*0.45)*scale)) + pad

	rect := image.Rect(x0, y0, x1, y1).Intersect(img.Bounds())
	if rect.Empty() {
		return
	}
	draw.Draw(img, rect, image.NewUniform(color.White), image.Point{}, draw.Src)
}

func approximateTextWidth(text string, fontSize float64) float64 {
	if fontSize <= 0 {
		fontSize = 14
	}
	return float64(len([]rune(strings.TrimSpace(text)))) * fontSize * 0.62
}

func compositePages(pages []*image.RGBA) *image.RGBA {
	const gap = 16

	width := 0
	height := 0
	for i, page := range pages {
		if page.Bounds().Dx() > width {
			width = page.Bounds().Dx()
		}
		height += page.Bounds().Dy()
		if i > 0 {
			height += gap
		}
	}

	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)

	y := 0
	for i, page := range pages {
		if i > 0 {
			y += gap
		}
		draw.Draw(dst, image.Rect(0, y, page.Bounds().Dx(), y+page.Bounds().Dy()), page, page.Bounds().Min, draw.Src)
		y += page.Bounds().Dy()
	}
	return dst
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func hashImages(images []*image.RGBA, dpi float64) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s dpi=%.1f pages=%d\n", hashVersion, dpi, len(images))
	for i, img := range images {
		bounds := img.Bounds()
		_, _ = fmt.Fprintf(h, "page=%d width=%d height=%d stride=%d\n", i+1, bounds.Dx(), bounds.Dy(), img.Stride)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			start := (y-bounds.Min.Y)*img.Stride + (bounds.Min.X * 4)
			end := start + bounds.Dx()*4
			_, _ = h.Write(img.Pix[start:end])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
