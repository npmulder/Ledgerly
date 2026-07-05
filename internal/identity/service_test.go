package identity

import (
	"context"
	"fmt"
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
}

func TestSeedMigrationCreatesNPMFixture(t *testing.T) {
	ctx, pool := temporaryMigratedDatabase(t)

	profile, err := New(pool, discardBus()).Profile(ctx)
	if err != nil {
		t.Fatalf("Profile() error = %v", err)
	}
	assertNPMProfile(t, profile)
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

	databaseURL := testDatabaseURL(t)
	adminPool, err := db.OpenURL(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("OpenURL() admin error = %v", err)
	}
	t.Cleanup(adminPool.Close)

	dbName := fmt.Sprintf("ledgerly_test_identity_%d", time.Now().UnixNano())
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
