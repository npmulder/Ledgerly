package identity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

func TestUpdateProfilePatchValidation(t *testing.T) {
	profile := npmProfile()

	blankTradingName := "  "
	if _, err := (UpdateProfilePatch{TradingName: &blankTradingName}).apply(profile); err == nil {
		t.Fatal("UpdateProfilePatch.apply() trading name error = nil, want error")
	}

	blankLegalName := "  "
	if _, err := (UpdateProfilePatch{LegalName: &blankLegalName}).apply(profile); err == nil {
		t.Fatal("UpdateProfilePatch.apply() legal name error = nil, want error")
	}

	blankCompanyNumber := "  "
	if _, err := (UpdateProfilePatch{CompanyNumber: &blankCompanyNumber}).apply(profile); err == nil {
		t.Fatal("UpdateProfilePatch.apply() company number error = nil, want error")
	}

	badDate := "not-a-date"
	if _, err := (UpdateProfilePatch{IncorporationDate: &badDate}).apply(profile); err == nil {
		t.Fatal("UpdateProfilePatch.apply() date error = nil, want error")
	}

	badYearEnd := YearEnd{Month: time.February, Day: 30}
	if _, err := (UpdateProfilePatch{YearEnd: &badYearEnd}).apply(profile); err == nil {
		t.Fatal("UpdateProfilePatch.apply() year end error = nil, want error")
	}

	var nilShareholders []Shareholder
	updated, err := (UpdateProfilePatch{Shareholders: &nilShareholders}).apply(profile)
	if err != nil {
		t.Fatalf("UpdateProfilePatch.apply() nil shareholders error = %v", err)
	}
	if updated.Shareholders == nil || len(updated.Shareholders) != 0 {
		t.Fatalf("Shareholders = %#v, want non-nil empty slice", updated.Shareholders)
	}
}

func TestSeedMigrationCreatesNPMFixture(t *testing.T) {
	ctx, pool := temporaryMigratedDatabase(t)

	profile, err := New(pool, discardBus()).Profile(ctx)
	if err != nil {
		t.Fatalf("Profile() error = %v", err)
	}
	assertNPMProfile(t, profile)
	if profile.LogoAssetID == nil || *profile.LogoAssetID != DevSeedLogoAssetID {
		t.Fatalf("LogoAssetID = %v, want dev seed asset %s", profile.LogoAssetID, DevSeedLogoAssetID)
	}

	dataDir := t.TempDir()
	seedPath := filepath.Join(findRepoRoot(t), "docs", "design_handoff_keel", "uploads", "invoice_brand-1783009881094.png")
	seedID, err := SeedDevLogoAsset(ctx, pool, dataDir, seedPath)
	if err != nil {
		t.Fatalf("SeedDevLogoAsset() error = %v", err)
	}
	if seedID != DevSeedLogoAssetID {
		t.Fatalf("SeedDevLogoAsset() id = %s, want %s", seedID, DevSeedLogoAssetID)
	}
	asset, err := New(pool, discardBus(), WithDataDir(dataDir)).Asset(ctx, seedID)
	if err != nil {
		t.Fatalf("Asset(seed) error = %v", err)
	}
	if asset.SHA256 != devSeedLogoSHA256 || asset.MIME != devSeedLogoMIME || asset.Size != devSeedLogoSize {
		t.Fatalf("seed asset = %#v, want handoff logo metadata", asset)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "assets", devSeedLogoSHA256)); err != nil {
		t.Fatalf("seed asset file stat error = %v", err)
	}
}

func TestSingleRowCompanyProfileEnforced(t *testing.T) {
	ctx, tx := migratedIdentityTx(t)

	_, err := tx.Exec(ctx, `
INSERT INTO identity.company_profile (
	id,
	trading_name,
	legal_name,
	company_number,
	registered_office,
	incorporation_date,
	year_end_month,
	year_end_day
) VALUES (
	2,
	'Second',
	'Second Limited',
	'2',
	'{}'::jsonb,
	DATE '2020-01-01',
	3,
	31
)`)
	if err == nil {
		t.Fatal("second company_profile insert with id=2 succeeded, want single-row check failure")
	}

	_, err = tx.Exec(ctx, `
INSERT INTO identity.company_profile (
	id,
	trading_name,
	legal_name,
	company_number,
	registered_office,
	incorporation_date,
	year_end_month,
	year_end_day
) VALUES (
	1,
	'Duplicate',
	'Duplicate Limited',
	'DUP',
	'{}'::jsonb,
	DATE '2020-01-01',
	3,
	31
)`)
	if err == nil {
		t.Fatal("second company_profile insert with id=1 succeeded, want primary-key failure")
	}
}

func TestUpdateProfilePartialRoundTrip(t *testing.T) {
	ctx, tx := migratedIdentityTx(t)
	service := New(tx, discardBus())

	tradingName := "NPM Trading"
	vatNumber := "IM123456"
	bankDetails := BankDetails{
		IBAN:     "GB82WEST12345698765432",
		BIC:      "REVOGB21",
		BankName: "Revolut Business",
	}
	shareholders := []Shareholder{
		{Name: "N. Meyer", Shares: 100, Class: "ordinary £1"},
		{Name: "Employee Trust", Shares: 10, Class: "growth"},
	}

	if err := service.UpdateProfile(ctx, UpdateProfilePatch{
		TradingName:  &tradingName,
		VATNumber:    &vatNumber,
		BankDetails:  &bankDetails,
		Shareholders: &shareholders,
	}); err != nil {
		t.Fatalf("UpdateProfile() error = %v", err)
	}

	got, err := service.Profile(ctx)
	if err != nil {
		t.Fatalf("Profile() error = %v", err)
	}
	if got.TradingName != tradingName {
		t.Fatalf("TradingName = %q, want %q", got.TradingName, tradingName)
	}
	if got.LegalName != "NPM Limited" {
		t.Fatalf("LegalName = %q, want existing legal name", got.LegalName)
	}
	if got.CompanyNumber != "137792C" {
		t.Fatalf("CompanyNumber = %q, want existing company number", got.CompanyNumber)
	}
	if got.VATNumber == nil || *got.VATNumber != vatNumber {
		t.Fatalf("VATNumber = %v, want %q", got.VATNumber, vatNumber)
	}
	if got.BankDetails != bankDetails {
		t.Fatalf("BankDetails = %#v, want %#v", got.BankDetails, bankDetails)
	}
	if len(got.Shareholders) != 2 || got.Shareholders[1].Name != "Employee Trust" {
		t.Fatalf("Shareholders = %#v, want partial update value", got.Shareholders)
	}
	assertDate(t, got.IncorporationDate, 2020, time.July, 14)
	if got.YearEnd.Month != time.March || got.YearEnd.Day != 31 {
		t.Fatalf("YearEnd = %#v, want 31 March", got.YearEnd)
	}
}

func TestUpdateProfileClearsShareholders(t *testing.T) {
	ctx, tx := migratedIdentityTx(t)
	service := New(tx, discardBus())

	shareholders := []Shareholder{}
	if err := service.UpdateProfile(ctx, UpdateProfilePatch{Shareholders: &shareholders}); err != nil {
		t.Fatalf("UpdateProfile() clear shareholders error = %v", err)
	}

	got, err := service.Profile(ctx)
	if err != nil {
		t.Fatalf("Profile() error = %v", err)
	}
	if got.Shareholders == nil || len(got.Shareholders) != 0 {
		t.Fatalf("Shareholders = %#v, want non-nil empty slice", got.Shareholders)
	}
}

func TestUpdateProfilePublishesEventInSameTransaction(t *testing.T) {
	ctx, tx := migratedIdentityTx(t)
	eventBus := discardBus()
	service := New(tx, eventBus)
	tradingName := "NPM Evented"
	handlerRan := false

	eventBus.Subscribe(ProfileUpdatedEventName, func(ctx context.Context, gotTx db.Tx, evt bus.Event) error {
		handlerRan = true
		if gotTx != tx {
			t.Fatalf("handler tx = %p, want update tx %p", gotTx, tx)
		}
		if _, ok := evt.(ProfileUpdated); !ok {
			t.Fatalf("event type = %T, want identity.ProfileUpdated", evt)
		}

		got, err := New(gotTx, nil).Profile(ctx)
		if err != nil {
			return err
		}
		if got.TradingName != tradingName {
			return fmt.Errorf("handler saw TradingName %q, want %q", got.TradingName, tradingName)
		}
		return nil
	})

	if err := service.UpdateProfile(ctx, UpdateProfilePatch{TradingName: &tradingName}); err != nil {
		t.Fatalf("UpdateProfile() error = %v", err)
	}
	if !handlerRan {
		t.Fatal("ProfileUpdated handler did not run")
	}
}

func TestValidateLogoUploadAcceptsSupportedImageTypes(t *testing.T) {
	tests := []struct {
		name  string
		mime  string
		bytes []byte
	}{
		{name: "png", mime: "image/png", bytes: testPNG(t)},
		{name: "jpeg", mime: "image/jpeg", bytes: testJPEG(t)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			asset, err := validateLogoUpload(LogoUpload{MIME: test.mime, Bytes: test.bytes})
			if err != nil {
				t.Fatalf("validateLogoUpload() error = %v", err)
			}
			if asset.mime != test.mime {
				t.Fatalf("mime = %q, want %q", asset.mime, test.mime)
			}
			if asset.size != int64(len(test.bytes)) {
				t.Fatalf("size = %d, want %d", asset.size, len(test.bytes))
			}
		})
	}
}

func TestReplaceLogoStoresContentAddressedAssetAndPublishesEvent(t *testing.T) {
	ctx, tx := migratedIdentityTx(t)
	dataDir := t.TempDir()
	eventBus := discardBus()
	service := New(tx, eventBus, WithDataDir(dataDir))
	logo := testPNG(t)
	logoSHA := sha256Hex(logo)
	published := 0

	eventBus.Subscribe(ProfileUpdatedEventName, func(ctx context.Context, gotTx db.Tx, evt bus.Event) error {
		published++
		if gotTx != tx {
			t.Fatalf("handler tx = %p, want replace tx %p", gotTx, tx)
		}
		if _, ok := evt.(ProfileUpdated); !ok {
			t.Fatalf("event type = %T, want identity.ProfileUpdated", evt)
		}
		return nil
	})

	firstID, err := service.ReplaceLogo(ctx, LogoUpload{MIME: "image/png", Bytes: logo})
	if err != nil {
		t.Fatalf("ReplaceLogo() first error = %v", err)
	}
	if published != 1 {
		t.Fatalf("published events after first replace = %d, want 1", published)
	}
	assertAssetFile(t, dataDir, logoSHA, logo)
	assertAssetFileCount(t, dataDir, 1)
	firstAsset, err := service.Asset(ctx, firstID)
	if err != nil {
		t.Fatalf("Asset(first) error = %v", err)
	}
	if firstAsset.MIME != "image/png" || firstAsset.SHA256 != logoSHA || !bytes.Equal(firstAsset.Bytes, logo) {
		t.Fatalf("first asset = %#v, want PNG bytes with sha %s", firstAsset, logoSHA)
	}

	secondID, err := service.ReplaceLogo(ctx, LogoUpload{MIME: "image/png", Bytes: logo})
	if err != nil {
		t.Fatalf("ReplaceLogo() second error = %v", err)
	}
	if firstID == secondID {
		t.Fatalf("second ReplaceLogo() reused asset id %s; want a new reference to the same sha file", secondID)
	}
	if published != 2 {
		t.Fatalf("published events after second replace = %d, want 2", published)
	}
	assertAssetFile(t, dataDir, logoSHA, logo)
	assertAssetFileCount(t, dataDir, 1)

	var referenceCount int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM identity.assets WHERE sha256 = $1", logoSHA).Scan(&referenceCount); err != nil {
		t.Fatalf("count assets by sha: %v", err)
	}
	if referenceCount != 2 {
		t.Fatalf("asset rows for sha = %d, want two references", referenceCount)
	}

	profile, err := service.Profile(ctx)
	if err != nil {
		t.Fatalf("Profile() error = %v", err)
	}
	if profile.LogoAssetID == nil || *profile.LogoAssetID != secondID {
		t.Fatalf("LogoAssetID = %v, want second asset %s", profile.LogoAssetID, secondID)
	}

	oldAsset, err := service.Asset(ctx, firstID)
	if err != nil {
		t.Fatalf("Asset(first) after replacement error = %v", err)
	}
	if !bytes.Equal(oldAsset.Bytes, logo) {
		t.Fatal("old asset bytes changed after replacement")
	}
}

func TestAssetWriterStoresPDFContentAddressedAsset(t *testing.T) {
	ctx, pool := temporaryMigratedDatabase(t)
	dataDir := t.TempDir()
	writer := NewAssetWriter(pool, dataDir)
	pdfBytes := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n%%EOF\n")
	pdfSHA := sha256Hex(pdfBytes)

	firstID, err := writer.StoreAsset(ctx, AssetUpload{MIME: "application/pdf", Bytes: pdfBytes})
	if err != nil {
		t.Fatalf("StoreAsset() first error = %v", err)
	}
	assertAssetFile(t, dataDir, pdfSHA, pdfBytes)
	assertAssetFileCount(t, dataDir, 1)

	firstAsset, err := New(pool, discardBus(), WithDataDir(dataDir)).Asset(ctx, firstID)
	if err != nil {
		t.Fatalf("Asset(first PDF) error = %v", err)
	}
	if firstAsset.MIME != "application/pdf" || firstAsset.SHA256 != pdfSHA || !bytes.Equal(firstAsset.Bytes, pdfBytes) {
		t.Fatalf("first PDF asset = %#v, want application/pdf bytes with sha %s", firstAsset, pdfSHA)
	}

	secondID, err := writer.StoreAsset(ctx, AssetUpload{MIME: "application/pdf; charset=binary", Bytes: pdfBytes})
	if err != nil {
		t.Fatalf("StoreAsset() second error = %v", err)
	}
	if secondID == firstID {
		t.Fatalf("second StoreAsset() reused asset id %s; want a new reference to the same sha file", secondID)
	}
	assertAssetFile(t, dataDir, pdfSHA, pdfBytes)
	assertAssetFileCount(t, dataDir, 1)
}

func TestReplaceLogoRejectsOversizedAndWrongMIME(t *testing.T) {
	ctx, tx := migratedIdentityTx(t)
	service := New(tx, discardBus(), WithDataDir(t.TempDir()))

	oversized := bytes.Repeat([]byte("x"), MaxLogoAssetBytes+1)
	if _, err := service.ReplaceLogo(ctx, LogoUpload{MIME: "image/png", Bytes: oversized}); !errors.Is(err, ErrAssetTooLarge) {
		t.Fatalf("ReplaceLogo() oversized error = %v, want ErrAssetTooLarge", err)
	}

	if _, err := service.ReplaceLogo(ctx, LogoUpload{MIME: "text/plain", Bytes: testPNG(t)}); !errors.Is(err, ErrUnsupportedAsset) {
		t.Fatalf("ReplaceLogo() wrong MIME error = %v, want ErrUnsupportedAsset", err)
	}

	if _, err := service.ReplaceLogo(ctx, LogoUpload{
		MIME:  "image/svg+xml",
		Bytes: []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"></svg>`),
	}); !errors.Is(err, ErrUnsupportedAsset) {
		t.Fatalf("ReplaceLogo() svg error = %v, want ErrUnsupportedAsset", err)
	}
}

func TestUpdateProfileRejectsUnknownLogoAssetID(t *testing.T) {
	ctx, tx := migratedIdentityTx(t)
	service := New(tx, discardBus(), WithDataDir(t.TempDir()))

	id := AssetID("17830098-8109-4a00-8b00-000000009999")
	if err := service.UpdateProfile(ctx, UpdateProfilePatch{LogoAssetID: &id}); !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("UpdateProfile() unknown logo asset id error = %v, want ErrAssetNotFound", err)
	}
}

func TestUpdateProfileInitializesMissingProductionProfile(t *testing.T) {
	ctx, pool := temporaryMigratedDatabaseNamed(t, fmt.Sprintf("ledgerly_prod_identity_%d", time.Now().UnixNano()))
	service := New(pool, discardBus())

	if _, err := service.Profile(ctx); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("Profile() error = %v, want ErrProfileNotFound before initialization", err)
	}

	tradingName := "Acme Trading"
	legalName := "Acme Limited"
	companyNumber := "ACME123"
	incorporationDate := "2024-01-15"
	yearEnd := YearEnd{Month: time.December, Day: 31}
	registeredOffice := RegisteredOffice{
		Line1:    "1 Athol Street",
		Locality: "Douglas",
		Country:  "IM",
	}
	shareholders := []Shareholder{}

	if err := service.UpdateProfile(ctx, UpdateProfilePatch{
		TradingName:       &tradingName,
		LegalName:         &legalName,
		CompanyNumber:     &companyNumber,
		RegisteredOffice:  &registeredOffice,
		IncorporationDate: &incorporationDate,
		YearEnd:           &yearEnd,
		Shareholders:      &shareholders,
	}); err != nil {
		t.Fatalf("UpdateProfile() initialize missing production profile error = %v", err)
	}

	got, err := service.Profile(ctx)
	if err != nil {
		t.Fatalf("Profile() after initialization error = %v", err)
	}
	if got.TradingName != tradingName || got.LegalName != legalName || got.CompanyNumber != companyNumber {
		t.Fatalf("Profile() = %#v, want initialized names and company number", got)
	}
	assertDate(t, got.IncorporationDate, 2024, time.January, 15)
	if got.YearEnd != yearEnd {
		t.Fatalf("YearEnd = %#v, want %#v", got.YearEnd, yearEnd)
	}
	if got.Shareholders == nil || len(got.Shareholders) != 0 {
		t.Fatalf("Shareholders = %#v, want non-nil empty slice", got.Shareholders)
	}
}

func TestCompanyFactsReturnsIncorporationDateAndYearEnd(t *testing.T) {
	ctx, tx := migratedIdentityTx(t)
	facts, err := New(tx, discardBus()).CompanyFacts(ctx)
	if err != nil {
		t.Fatalf("CompanyFacts() error = %v", err)
	}

	assertDate(t, facts.IncorporationDate, 2020, time.July, 14)
	if facts.YearEnd.Month != time.March || facts.YearEnd.Day != 31 {
		t.Fatalf("YearEnd = %#v, want 31 March", facts.YearEnd)
	}
}

func migratedIdentityTx(t *testing.T) (context.Context, pgx.Tx) {
	t.Helper()

	ctx, pool := migratedPool(t, testDatabaseURL(t))
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback(context.Background())
	})

	resetCompanyProfile(t, ctx, tx)
	return ctx, tx
}

func temporaryMigratedDatabase(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()

	return temporaryMigratedDatabaseNamed(t, fmt.Sprintf("ledgerly_test_identity_%d", time.Now().UnixNano()))
}

func temporaryMigratedDatabaseNamed(t *testing.T, dbName string) (context.Context, *pgxpool.Pool) {
	t.Helper()

	databaseURL := testDatabaseURL(t)
	adminPool, err := db.OpenURL(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("OpenURL() admin error = %v", err)
	}
	t.Cleanup(adminPool.Close)

	if _, err := adminPool.Exec(context.Background(), "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()); err != nil {
		t.Skipf("CREATE DATABASE unavailable for seed migration test: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{dbName}.Sanitize()+" WITH (FORCE)")
	})

	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	cfg.ConnConfig.Database = dbName

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("NewWithConfig() temp DB error = %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping() temp DB error = %v", err)
	}

	migratePool(t, ctx, pool)
	return ctx, pool
}

func migratedPool(t *testing.T, databaseURL string) (context.Context, *pgxpool.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := db.OpenURL(ctx, databaseURL)
	if err != nil {
		t.Fatalf("OpenURL() error = %v", err)
	}
	t.Cleanup(pool.Close)

	migratePool(t, ctx, pool)
	return ctx, pool
}

func migratePool(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	if _, err := db.MigrateDir(ctx, pool, filepath.Join(findRepoRoot(t), "db", "migrations")); err != nil {
		t.Fatalf("MigrateDir() error = %v", err)
	}
}

func resetCompanyProfile(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()

	if _, err := tx.Exec(ctx, "DELETE FROM identity.company_profile"); err != nil {
		t.Fatalf("delete company profile fixture: %v", err)
	}
	if _, err := tx.Exec(ctx, insertNPMProfileSQL); err != nil {
		t.Fatalf("insert company profile fixture: %v", err)
	}
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()

	databaseURL := strings.TrimSpace(os.Getenv("LEDGERLY_TEST_DB"))
	if databaseURL == "" {
		t.Skip("set LEDGERLY_TEST_DB to run identity Postgres tests")
	}
	return databaseURL
}

func findRepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repository root containing go.mod")
		}
		dir = parent
	}
}

func assertNPMProfile(t *testing.T, profile CompanyProfile) {
	t.Helper()

	if profile.TradingName != "NPM Limited" {
		t.Fatalf("TradingName = %q, want NPM Limited", profile.TradingName)
	}
	if profile.LegalName != "NPM Limited" {
		t.Fatalf("LegalName = %q, want NPM Limited", profile.LegalName)
	}
	if profile.CompanyNumber != "137792C" {
		t.Fatalf("CompanyNumber = %q, want 137792C", profile.CompanyNumber)
	}
	if profile.RegisteredOffice.Line1 != "18 Athol St" || profile.RegisteredOffice.Locality != "Douglas" {
		t.Fatalf("RegisteredOffice = %#v, want 18 Athol St, Douglas", profile.RegisteredOffice)
	}
	assertDate(t, profile.IncorporationDate, 2020, time.July, 14)
	if profile.YearEnd.Month != time.March || profile.YearEnd.Day != 31 {
		t.Fatalf("YearEnd = %#v, want 31 March", profile.YearEnd)
	}
	if len(profile.Shareholders) != 1 {
		t.Fatalf("Shareholders = %#v, want one shareholder", profile.Shareholders)
	}
	shareholder := profile.Shareholders[0]
	if shareholder.Name != "N. Meyer" || shareholder.Shares != 100 || shareholder.Class != "ordinary £1" {
		t.Fatalf("Shareholder = %#v, want N. Meyer 100 ordinary £1", shareholder)
	}
}

func assertDate(t *testing.T, got time.Time, year int, month time.Month, day int) {
	t.Helper()

	if got.Year() != year || got.Month() != month || got.Day() != day {
		t.Fatalf("date = %s, want %04d-%02d-%02d", got.Format(time.DateOnly), year, month, day)
	}
}

func assertAssetFile(t *testing.T, dataDir string, sha string, want []byte) {
	t.Helper()

	got, err := os.ReadFile(filepath.Join(dataDir, "assets", sha))
	if err != nil {
		t.Fatalf("read asset file: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("asset file bytes do not match upload")
	}
}

func assertAssetFileCount(t *testing.T, dataDir string, want int) {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(dataDir, "assets"))
	if err != nil {
		t.Fatalf("read asset directory: %v", err)
	}
	var got int
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		got++
	}
	if got != want {
		t.Fatalf("asset file count = %d, want %d", got, want)
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 0x11, G: 0x66, B: 0xaa, A: 0xff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return buf.Bytes()
}

func testJPEG(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 0xaa, G: 0x66, B: 0x11, A: 0xff})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode test JPEG: %v", err)
	}
	return buf.Bytes()
}

func npmProfile() CompanyProfile {
	incorporationDate, err := parseDate("2020-07-14")
	if err != nil {
		panic(err)
	}
	return CompanyProfile{
		TradingName:   "NPM Limited",
		LegalName:     "NPM Limited",
		CompanyNumber: "137792C",
		RegisteredOffice: RegisteredOffice{
			Line1:    "18 Athol St",
			Locality: "Douglas",
			Country:  "IM",
		},
		IncorporationDate: incorporationDate,
		YearEnd:           YearEnd{Month: time.March, Day: 31},
		BankDetails:       BankDetails{},
		Shareholders: []Shareholder{
			{Name: "N. Meyer", Shares: 100, Class: "ordinary £1"},
		},
	}
}

func discardBus() *bus.Bus {
	return bus.New(bus.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
}

const insertNPMProfileSQL = `
INSERT INTO identity.company_profile (
	id,
	trading_name,
	legal_name,
	company_number,
	registered_office,
	incorporation_date,
	year_end_month,
	year_end_day,
	vat_number,
	bank_details,
	shareholders
) VALUES (
	1,
	'NPM Limited',
	'NPM Limited',
	'137792C',
	'{"line1":"18 Athol St","line2":"","locality":"Douglas","region":"","postal_code":"","country":"IM"}'::jsonb,
	DATE '2020-07-14',
	3,
	31,
	NULL,
	'{"iban":"","bic":"","bank_name":""}'::jsonb,
	'[{"name":"N. Meyer","shares":100,"class":"ordinary £1"}]'::jsonb
)`
