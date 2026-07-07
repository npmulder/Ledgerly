package reports

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/mail"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	platformmail "github.com/npmulder/ledgerly/internal/platform/mail"
)

const (
	defaultShareAttachmentLimit = 15 * 1024 * 1024
	exportArchiveContentType    = "application/zip"
)

// ExportPack assembles the accountant export pack for an inclusive period.
func (s *Service) ExportPack(ctx context.Context, period Period) (ArchiveRef, error) {
	if s == nil {
		return ArchiveRef{}, fmt.Errorf("reports: service is nil")
	}
	if s.ledger == nil {
		return ArchiveRef{}, fmt.Errorf("ledger: %w", ErrMissingProvider)
	}
	if s.identity == nil {
		return ArchiveRef{}, fmt.Errorf("identity: %w", ErrMissingProvider)
	}
	if s.invoicing == nil {
		return ArchiveRef{}, fmt.Errorf("invoicing: %w", ErrMissingProvider)
	}
	if s.dla == nil {
		return ArchiveRef{}, fmt.Errorf("dla: %w", ErrMissingProvider)
	}
	if s.archiveStore == nil {
		return ArchiveRef{}, fmt.Errorf("reports: archive store: %w", ErrMissingProvider)
	}
	if s.pdfEngine == nil {
		return ArchiveRef{}, fmt.Errorf("reports: P&L PDF engine: %w", ErrMissingProvider)
	}

	normalized, err := normalizePeriod(period)
	if err != nil {
		return ArchiveRef{}, err
	}
	data, err := s.exportPackData(ctx, normalized)
	if err != nil {
		return ArchiveRef{}, err
	}
	dataVersion, err := exportPackDataVersion(data, s.appVersion)
	if err != nil {
		return ArchiveRef{}, err
	}
	key := exportArchiveKey(normalized, dataVersion)
	if existing, ok, err := s.archiveStore.ExistingExportArchive(ctx, key); err != nil {
		return ArchiveRef{}, err
	} else if ok {
		existing.DataVersion = dataVersion
		return existing, nil
	}

	generatedAt := s.clock.Now().UTC()
	if generatedAt.IsZero() {
		generatedAt = time.Now().UTC()
	}
	data.generatedAt = generatedAt
	data.dataVersion = dataVersion
	data.appVersion = s.appVersion

	plPDF, err := s.pdfEngine.RenderPLPDF(ctx, PLPrintPayload{
		Report:      plToResponse(data.pl),
		CompanyName: data.companyName(),
		GeneratedAt: generatedAt.Format(time.RFC3339),
		AppVersion:  s.appVersion,
	})
	if err != nil {
		return ArchiveRef{}, err
	}
	data.plPDF = plPDF

	archive, err := buildExportArchive(data)
	if err != nil {
		return ArchiveRef{}, err
	}
	ref, err := s.archiveStore.StoreExportArchive(ctx, key, archive)
	if err != nil {
		return ArchiveRef{}, err
	}
	ref.DataVersion = dataVersion
	ref.GeneratedAt = generatedAt
	return ref, nil
}

// ShareExportPack sends the archive as an email attachment unless it exceeds
// the platform mail size guard.
func (s *Service) ShareExportPack(ctx context.Context, request ShareRequest) (ShareResult, error) {
	address, err := mail.ParseAddress(strings.TrimSpace(request.Email))
	if err != nil || strings.TrimSpace(address.Address) == "" {
		return ShareResult{}, fmt.Errorf("reports: accountant email must be valid: %w", ErrInvalidShare)
	}
	ref, err := s.ExportPack(ctx, request.Period)
	if err != nil {
		return ShareResult{}, err
	}
	limit := s.shareSizeLimit
	if limit <= 0 {
		limit = defaultShareAttachmentLimit
	}
	if ref.Size > limit {
		return ShareResult{
			Status:  ShareStatusManualSend,
			Archive: ref,
			Message: "Export pack is larger than 15 MB. Download the zip and send it to your accountant manually.",
		}, nil
	}
	if s.mailer == nil {
		return ShareResult{}, fmt.Errorf("reports: mailer: %w", ErrMissingProvider)
	}
	asset, err := s.archiveStore.LoadAsset(ctx, ref.URL)
	if err != nil {
		return ShareResult{}, err
	}
	filename := exportArchiveFilename(request.Period)
	if strings.TrimSpace(asset.Filename) != "" {
		filename = asset.Filename
	}
	if err := s.mailer.Send(ctx, platformmail.Message{
		To:       address.Address,
		Subject:  "Ledgerly export pack " + periodFilenamePart(request.Period),
		TextBody: shareEmailBody(request.Period),
		Attachments: []platformmail.Attachment{{
			Filename:    filename,
			ContentType: exportArchiveContentType,
			Bytes:       asset.Bytes,
		}},
	}); err != nil {
		return ShareResult{}, fmt.Errorf("reports: send export pack email: %w", err)
	}
	return ShareResult{
		Status:  ShareStatusSent,
		Archive: ref,
		Message: "Export pack sent to accountant.",
	}, nil
}

type exportPackData struct {
	period      Period
	pl          PL
	vat         *VATFigures
	journal     []ledger.JournalEntry
	dlaRows     []dla.Entry
	invoices    []StoredDocument
	dividends   []StoredDocument
	receipts    []StoredDocument
	profile     identity.CompanyProfile
	facts       identity.CompanyFacts
	plPDF       []byte
	generatedAt time.Time
	appVersion  string
	dataVersion string
}

func (s *Service) exportPackData(ctx context.Context, period Period) (exportPackData, error) {
	pl, err := s.ProfitAndLoss(ctx, period)
	if err != nil {
		return exportPackData{}, err
	}
	journal, err := s.ledgerEntries(ctx, period)
	if err != nil {
		return exportPackData{}, err
	}
	dlaRows, err := s.dlaEntries(ctx, period)
	if err != nil {
		return exportPackData{}, err
	}
	profile, err := s.identity.Profile(ctx)
	if err != nil {
		return exportPackData{}, err
	}
	facts, err := s.identity.CompanyFacts(ctx)
	if err != nil {
		return exportPackData{}, err
	}
	var vat *VATFigures
	if facts.IsVATRegistered {
		figures, err := s.VATReturn(ctx, period)
		if err != nil {
			return exportPackData{}, err
		}
		vat = &figures
	}
	invoiceDocs, err := s.invoiceDocuments(ctx, period)
	if err != nil {
		return exportPackData{}, err
	}
	dividendDocs, err := s.dividendDocuments(ctx, period)
	if err != nil {
		return exportPackData{}, err
	}
	receiptDocs, err := s.receiptDocuments(ctx, period)
	if err != nil {
		return exportPackData{}, err
	}
	return exportPackData{
		period:    period,
		pl:        pl,
		vat:       vat,
		journal:   journal,
		dlaRows:   dlaRows,
		invoices:  invoiceDocs,
		dividends: dividendDocs,
		receipts:  receiptDocs,
		profile:   profile,
		facts:     facts,
	}, nil
}

func (s *Service) ledgerEntries(ctx context.Context, period Period) ([]ledger.JournalEntry, error) {
	filter := ledger.EntryFilter{
		From:  &period.From,
		To:    &period.To,
		Limit: ledger.MaxEntriesLimit,
	}
	var entries []ledger.JournalEntry
	for {
		page, err := s.ledger.Entries(ctx, filter)
		if err != nil {
			return nil, fmt.Errorf("reports: export journal entries: %w", err)
		}
		entries = append(entries, page...)
		if len(page) < filter.Limit {
			return entries, nil
		}
		last := page[len(page)-1]
		filter.After = &ledger.EntryCursor{Date: last.Date, ID: last.ID}
	}
}

func (s *Service) dlaEntries(ctx context.Context, period Period) ([]dla.Entry, error) {
	filter := dla.LedgerFilter{
		From:  &period.From,
		To:    &period.To,
		Limit: dla.MaxLedgerLimit,
	}
	var entries []dla.Entry
	for {
		page, err := s.dla.Ledger(ctx, filter)
		if err != nil {
			return nil, fmt.Errorf("reports: export DLA entries: %w", err)
		}
		entries = append(entries, page...)
		if len(page) < filter.Limit {
			return entries, nil
		}
		last := page[len(page)-1]
		filter.After = &dla.EntryCursor{Date: last.Date, ID: last.ID}
	}
}

func (s *Service) invoiceDocuments(ctx context.Context, period Period) ([]StoredDocument, error) {
	invoices, err := s.invoicing.InvoicesIssuedBetween(ctx, period.From, period.To)
	if err != nil {
		return nil, fmt.Errorf("reports: export invoice list: %w", err)
	}
	documents := make([]StoredDocument, 0, len(invoices))
	used := map[string]int{}
	for _, invoice := range invoices {
		if invoice.PDFAsset == nil || strings.TrimSpace(*invoice.PDFAsset) == "" {
			continue
		}
		asset, err := s.archiveStore.LoadAsset(ctx, *invoice.PDFAsset)
		if err != nil {
			return nil, fmt.Errorf("reports: load invoice PDF %s: %w", invoice.ID, err)
		}
		name := invoiceDocumentName(invoice)
		name = uniqueArchivePath("invoices/"+name+".pdf", used)
		documents = append(documents, StoredDocument{
			Path:        name,
			ContentType: asset.ContentType,
			Bytes:       append([]byte{}, asset.Bytes...),
		})
	}
	sortStoredDocuments(documents)
	return documents, nil
}

func (s *Service) dividendDocuments(ctx context.Context, period Period) ([]StoredDocument, error) {
	if s.dividends == nil {
		return nil, nil
	}
	documents, err := s.dividends.DividendDocuments(ctx, period)
	if err != nil {
		return nil, fmt.Errorf("reports: export dividend documents: %w", err)
	}
	out := make([]StoredDocument, 0, len(documents))
	used := map[string]int{}
	for _, document := range documents {
		if len(document.Bytes) == 0 {
			continue
		}
		name := strings.TrimSpace(document.Path)
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "dividends/") {
			name = "dividends/" + name
		}
		name = uniqueArchivePath(safeArchivePath(name), used)
		out = append(out, StoredDocument{
			Path:        name,
			ContentType: document.ContentType,
			Bytes:       append([]byte{}, document.Bytes...),
		})
	}
	sortStoredDocuments(out)
	return out, nil
}

func (s *Service) receiptDocuments(ctx context.Context, period Period) ([]StoredDocument, error) {
	if s.receipts == nil {
		return nil, nil
	}
	documents, err := s.receipts.ReceiptDocuments(ctx, period)
	if err != nil {
		return nil, fmt.Errorf("reports: export receipt documents: %w", err)
	}
	out := make([]StoredDocument, 0, len(documents))
	used := map[string]int{}
	for _, document := range documents {
		if len(document.Bytes) == 0 {
			continue
		}
		name := strings.TrimSpace(document.Path)
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "receipts/") {
			name = "receipts/" + name
		}
		name = uniqueArchivePath(safeArchivePath(name), used)
		out = append(out, StoredDocument{
			Path:        name,
			ContentType: document.ContentType,
			Bytes:       append([]byte{}, document.Bytes...),
		})
	}
	sortStoredDocuments(out)
	return out, nil
}

func buildExportArchive(data exportPackData) ([]byte, error) {
	files := []archiveFile{
		{Name: "pl.csv", Bytes: mustBuildCSV(plCSVRows(data.pl))},
		{Name: "pl.pdf", Bytes: data.plPDF},
		{Name: "journal.csv", Bytes: mustBuildCSV(journalCSVRows(data.journal))},
		{Name: "dla.csv", Bytes: mustBuildCSV(dlaCSVRows(data.dlaRows))},
	}
	if data.vat != nil {
		files = append(files, archiveFile{Name: "vat.csv", Bytes: mustBuildCSV(vatCSVRows(*data.vat))})
	}
	files = append(files, documentsToArchiveFiles(data.invoices)...)
	files = append(files, documentsToArchiveFiles(data.dividends)...)
	files = append(files, documentsToArchiveFiles(data.receipts)...)
	manifest, err := exportManifestJSON(data, files)
	if err != nil {
		return nil, err
	}
	files = append(files, archiveFile{Name: "manifest.json", Bytes: manifest})
	sort.SliceStable(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, dir := range []string{"invoices/", "dividends/", "receipts/"} {
		header := &zip.FileHeader{Name: dir, Method: zip.Store, Modified: data.generatedAt}
		if _, err := writer.CreateHeader(header); err != nil {
			_ = writer.Close()
			return nil, fmt.Errorf("reports: create zip directory %s: %w", dir, err)
		}
	}
	for _, file := range files {
		if err := addZipFile(writer, file, data.generatedAt); err != nil {
			_ = writer.Close()
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("reports: close export zip: %w", err)
	}
	return buf.Bytes(), nil
}

type archiveFile struct {
	Name  string
	Bytes []byte
}

func addZipFile(writer *zip.Writer, file archiveFile, modTime time.Time) error {
	if strings.TrimSpace(file.Name) == "" {
		return fmt.Errorf("reports: archive filename is required")
	}
	header := &zip.FileHeader{Name: safeArchivePath(file.Name), Method: zip.Deflate, Modified: modTime}
	out, err := writer.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("reports: create zip file %s: %w", file.Name, err)
	}
	if _, err := out.Write(file.Bytes); err != nil {
		return fmt.Errorf("reports: write zip file %s: %w", file.Name, err)
	}
	return nil
}

func documentsToArchiveFiles(documents []StoredDocument) []archiveFile {
	files := make([]archiveFile, 0, len(documents))
	for _, document := range documents {
		files = append(files, archiveFile{Name: document.Path, Bytes: document.Bytes})
	}
	return files
}

func mustBuildCSV(rows [][]string) []byte {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	writer.UseCRLF = true
	if err := writer.WriteAll(rows); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func plCSVRows(pl PL) [][]string {
	rows := [][]string{{"section", "label", "amount", "currency"}}
	for _, line := range pl.Income {
		rows = append(rows, []string{"income", line.Label, decimalString(line.Amount), line.Amount.Currency})
	}
	rows = append(rows, []string{"income", pl.RealisedFXGains.Label, decimalString(pl.RealisedFXGains.Amount), pl.RealisedFXGains.Amount.Currency})
	rows = append(rows, []string{"total", "Turnover", decimalString(pl.IncomeTotal), pl.IncomeTotal.Currency})
	for _, line := range pl.Expenses {
		rows = append(rows, []string{"expense", line.AccountName, decimalString(line.Amount), line.Amount.Currency})
	}
	rows = append(rows, []string{"total", "Expenses", decimalString(pl.ExpenseTotal), pl.ExpenseTotal.Currency})
	rows = append(rows, []string{"total", "Profit before tax", decimalString(pl.ProfitBeforeTax), pl.ProfitBeforeTax.Currency})
	rows = append(rows, []string{"tax", pl.CorporateTax.Label, decimalString(pl.CorporateTax.Amount), pl.CorporateTax.Amount.Currency})
	rows = append(rows, []string{"total", "Net profit", decimalString(pl.NetProfit), pl.NetProfit.Currency})
	return rows
}

func vatCSVRows(vat VATFigures) [][]string {
	return [][]string{
		{"box", "label", "amount", "currency"},
		{"1", "VAT due on sales", decimalString(vat.Box1), vat.Box1.Currency},
		{"4", "VAT reclaimed", decimalString(vat.Box4), vat.Box4.Currency},
		{"6", "Total sales ex-VAT", decimalString(vat.Box6), vat.Box6.Currency},
		{"net", "Net position", decimalString(vat.NetPosition), vat.NetPosition.Currency},
	}
}

func journalCSVRows(entries []ledger.JournalEntry) [][]string {
	rows := [][]string{{
		"entry_id",
		"date",
		"description",
		"source_module",
		"source_ref",
		"account_code",
		"native_amount",
		"native_currency",
		"debit",
		"credit",
		"currency",
	}}
	for _, entry := range entries {
		for _, posting := range entry.Postings {
			debit := "0.00"
			credit := "0.00"
			if posting.AmountGBP.Amount >= 0 {
				debit = decimalMinorString(posting.AmountGBP.Amount)
			} else {
				credit = decimalMinorString(-posting.AmountGBP.Amount)
			}
			rows = append(rows, []string{
				fmt.Sprintf("%d", entry.ID),
				entry.Date.UTC().Format(time.DateOnly),
				entry.Description,
				entry.SourceModule,
				entry.SourceRef,
				string(posting.AccountCode),
				decimalString(posting.Amount),
				posting.Amount.Currency,
				debit,
				credit,
				posting.AmountGBP.Currency,
			})
		}
	}
	return rows
}

func dlaCSVRows(entries []dla.Entry) [][]string {
	rows := [][]string{{
		"id",
		"date",
		"kind",
		"description",
		"source_ref",
		"amount",
		"currency",
		"owed_to_you",
		"drawn",
		"running_balance",
		"balance_side",
	}}
	for _, entry := range entries {
		rows = append(rows, []string{
			fmt.Sprintf("%d", entry.ID),
			entry.Date.UTC().Format(time.DateOnly),
			string(entry.Kind),
			entry.Description,
			entry.Source,
			decimalString(entry.Amount),
			entry.Amount.Currency,
			decimalString(entry.OwedToYou),
			decimalString(entry.Drawn),
			decimalString(entry.RunningBalance),
			string(entry.BalanceSide),
		})
	}
	return rows
}

func exportManifestJSON(data exportPackData, files []archiveFile) ([]byte, error) {
	fileRecords := make([]manifestFile, 0, len(files)+3)
	fileRecords = append(fileRecords, manifestFile{Name: "invoices/", Size: 0}, manifestFile{Name: "dividends/", Size: 0}, manifestFile{Name: "receipts/", Size: 0})
	for _, file := range files {
		fileRecords = append(fileRecords, manifestFile{
			Name:   file.Name,
			Size:   int64(len(file.Bytes)),
			SHA256: sha256Hex(file.Bytes),
		})
	}
	sort.SliceStable(fileRecords, func(i, j int) bool { return fileRecords[i].Name < fileRecords[j].Name })
	manifest := exportManifest{
		Period: periodResponse{
			From: data.period.From.UTC().Format(time.DateOnly),
			To:   data.period.To.UTC().Format(time.DateOnly),
		},
		GeneratedAt: data.generatedAt.Format(time.RFC3339),
		DataVersion: data.dataVersion,
		AppVersion:  data.appVersion,
		Company: manifestCompany{
			TradingName:       data.profile.TradingName,
			LegalName:         data.profile.LegalName,
			CompanyNumber:     data.profile.CompanyNumber,
			IncorporationDate: data.facts.IncorporationDate.UTC().Format(time.DateOnly),
			YearEnd: manifestYearEnd{
				Month: int(data.facts.YearEnd.Month),
				Day:   data.facts.YearEnd.Day,
			},
		},
		Files: fileRecords,
	}
	out, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("reports: marshal export manifest: %w", err)
	}
	out = append(out, '\n')
	return out, nil
}

type exportManifest struct {
	Period      periodResponse  `json:"period"`
	GeneratedAt string          `json:"generated_at"`
	DataVersion string          `json:"data_version"`
	AppVersion  string          `json:"app_version"`
	Company     manifestCompany `json:"company"`
	Files       []manifestFile  `json:"files"`
}

type manifestCompany struct {
	TradingName       string          `json:"trading_name"`
	LegalName         string          `json:"legal_name"`
	CompanyNumber     string          `json:"company_number"`
	IncorporationDate string          `json:"incorporation_date"`
	YearEnd           manifestYearEnd `json:"year_end"`
}

type manifestYearEnd struct {
	Month int `json:"month"`
	Day   int `json:"day"`
}

type manifestFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

func exportPackDataVersion(data exportPackData, appVersion string) (string, error) {
	var vat *vatResponse
	if data.vat != nil {
		response := vatToResponse(*data.vat)
		vat = &response
	}
	payload := struct {
		Period    periodResponse        `json:"period"`
		App       string                `json:"app_version"`
		PL        plResponse            `json:"pl"`
		VAT       *vatResponse          `json:"vat,omitempty"`
		Journal   []ledger.JournalEntry `json:"journal"`
		DLA       []dla.Entry           `json:"dla"`
		Invoices  []documentDigest      `json:"invoices"`
		Dividends []documentDigest      `json:"dividends"`
		Receipts  []documentDigest      `json:"receipts"`
		Company   manifestCompany       `json:"company"`
	}{
		Period: periodResponse{
			From: data.period.From.UTC().Format(time.DateOnly),
			To:   data.period.To.UTC().Format(time.DateOnly),
		},
		App:     appVersion,
		PL:      plToResponse(data.pl),
		VAT:     vat,
		Journal: data.journal,
		DLA:     data.dlaRows,
		Company: manifestCompany{
			TradingName:       data.profile.TradingName,
			LegalName:         data.profile.LegalName,
			CompanyNumber:     data.profile.CompanyNumber,
			IncorporationDate: data.facts.IncorporationDate.UTC().Format(time.DateOnly),
			YearEnd: manifestYearEnd{
				Month: int(data.facts.YearEnd.Month),
				Day:   data.facts.YearEnd.Day,
			},
		},
	}
	payload.Invoices = documentDigests(data.invoices)
	payload.Dividends = documentDigests(data.dividends)
	payload.Receipts = documentDigests(data.receipts)
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("reports: marshal export data version: %w", err)
	}
	return sha256Hex(raw), nil
}

type documentDigest struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func documentDigests(documents []StoredDocument) []documentDigest {
	digests := make([]documentDigest, 0, len(documents))
	for _, document := range documents {
		digests = append(digests, documentDigest{
			Path:   document.Path,
			SHA256: sha256Hex(document.Bytes),
		})
	}
	sort.SliceStable(digests, func(i, j int) bool { return digests[i].Path < digests[j].Path })
	return digests
}

func exportArchiveKey(period Period, dataVersion string) string {
	return "reports-export:" + period.From.UTC().Format(time.DateOnly) + ":" + period.To.UTC().Format(time.DateOnly) + ":" + dataVersion
}

func exportArchiveFilename(period Period) string {
	return "ledgerly-export-" + periodFilenamePart(period) + ".zip"
}

func periodFilenamePart(period Period) string {
	return period.From.UTC().Format(time.DateOnly) + "_" + period.To.UTC().Format(time.DateOnly)
}

func shareEmailBody(period Period) string {
	return fmt.Sprintf(
		"Attached is the Ledgerly export pack for %s to %s.\n\nGenerated by Ledgerly.\n",
		period.From.UTC().Format(time.DateOnly),
		period.To.UTC().Format(time.DateOnly),
	)
}

func (d exportPackData) companyName() string {
	if strings.TrimSpace(d.profile.TradingName) != "" {
		return strings.TrimSpace(d.profile.TradingName)
	}
	if strings.TrimSpace(d.profile.LegalName) != "" {
		return strings.TrimSpace(d.profile.LegalName)
	}
	return "Ledgerly"
}

func invoiceDocumentName(invoice invoicing.Invoice) string {
	if invoice.Number != nil && strings.TrimSpace(*invoice.Number) != "" {
		return safeName(*invoice.Number)
	}
	return safeName(invoice.ID)
}

func uniqueArchivePath(name string, used map[string]int) string {
	name = safeArchivePath(name)
	count := used[name]
	used[name] = count + 1
	if count == 0 {
		return name
	}
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	return fmt.Sprintf("%s-%d%s", base, count+1, ext)
}

func safeArchivePath(name string) string {
	cleaned := path.Clean(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	cleaned = strings.TrimPrefix(cleaned, "/")
	for strings.HasPrefix(cleaned, "../") {
		cleaned = strings.TrimPrefix(cleaned, "../")
	}
	if cleaned == "." || cleaned == "" {
		return "document"
	}
	return cleaned
}

func safeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var out strings.Builder
	lastDash := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			out.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(out.String(), "-")
	if result == "" {
		return "document"
	}
	return result
}

func sortStoredDocuments(documents []StoredDocument) {
	sort.SliceStable(documents, func(i, j int) bool {
		return documents[i].Path < documents[j].Path
	})
}

func decimalString(amount money.Money) string {
	return decimalMinorString(amount.Amount)
}

func decimalMinorString(amount int64) string {
	sign := ""
	if amount < 0 {
		sign = "-"
		amount = -amount
	}
	return fmt.Sprintf("%s%d.%02d", sign, amount/100, amount%100)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
