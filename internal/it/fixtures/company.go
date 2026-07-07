package fixtures

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/it/harness"
)

const companySeedName = "company:NPM Limited"

// CompanyOverride customizes the canonical NPM Limited company fixture.
type CompanyOverride func(*identity.CompanyProfile)

// CompanyFixture is the persisted company profile plus override helpers.
type CompanyFixture struct {
	identity.CompanyProfile

	t testing.TB
	h *harness.Harness
}

// Company seeds the canonical NPM Limited profile through the identity HTTP API.
func Company(t testing.TB, h *harness.Harness, overrides ...CompanyOverride) CompanyFixture {
	t.Helper()

	profile, err := TryCompany(t, h, overrides...)
	if err != nil {
		t.Fatalf("seed company fixture: %v", err)
	}
	return profile
}

// TryCompany is the error-returning form of Company for duplicate-seed tests.
func TryCompany(t testing.TB, h *harness.Harness, overrides ...CompanyOverride) (CompanyFixture, error) {
	t.Helper()

	release, err := claimSeed(t, h, companySeedName)
	if err != nil {
		return CompanyFixture{}, err
	}
	success := false
	defer func() {
		release(success)
	}()

	want := npmCompanyProfile()
	for _, override := range overrides {
		if override != nil {
			override(&want)
		}
	}
	profile, err := patchCompanyProfile(t, h, want)
	if err != nil {
		return CompanyFixture{}, err
	}
	success = true
	return CompanyFixture{CompanyProfile: profile, t: t, h: h}, nil
}

// With applies company overrides through the identity HTTP API.
func (fixture CompanyFixture) With(overrides ...CompanyOverride) CompanyFixture {
	fixture.t.Helper()

	updated, err := fixture.TryWith(overrides...)
	if err != nil {
		fixture.t.Fatalf("override company fixture: %v", err)
	}
	return updated
}

// TryWith is the error-returning form of With.
func (fixture CompanyFixture) TryWith(overrides ...CompanyOverride) (CompanyFixture, error) {
	fixture.t.Helper()

	if fixture.h == nil {
		return CompanyFixture{}, errors.New("fixtures: company fixture missing harness")
	}
	want := fixture.CompanyProfile
	for _, override := range overrides {
		if override != nil {
			override(&want)
		}
	}
	profile, err := patchCompanyProfile(fixture.t, fixture.h, want)
	if err != nil {
		return CompanyFixture{}, err
	}
	return CompanyFixture{CompanyProfile: profile, t: fixture.t, h: fixture.h}, nil
}

// CompanyYearEnd overrides the accounting year end.
func CompanyYearEnd(month time.Month, day int) CompanyOverride {
	return func(profile *identity.CompanyProfile) {
		profile.YearEnd = identity.YearEnd{Month: month, Day: day}
	}
}

// CompanyVATRegistered overrides whether the company is registered for VAT.
func CompanyVATRegistered(registered bool) CompanyOverride {
	return func(profile *identity.CompanyProfile) {
		profile.IsVATRegistered = registered
	}
}

// CompanyIncorporationDate overrides the incorporation date.
func CompanyIncorporationDate(date time.Time) CompanyOverride {
	return func(profile *identity.CompanyProfile) {
		profile.IncorporationDate = date
	}
}

func npmCompanyProfile() identity.CompanyProfile {
	return identity.CompanyProfile{
		TradingName:   "NPM Limited",
		LegalName:     "NPM Limited",
		CompanyNumber: "137792C",
		RegisteredOffice: identity.RegisteredOffice{
			Line1:      "18 Athol St",
			Line2:      "",
			Locality:   "Douglas",
			Region:     "",
			PostalCode: "",
			Country:    "IM",
		},
		IncorporationDate: fixtureDate(2020, time.July, 14),
		YearEnd:           identity.YearEnd{Month: time.March, Day: 31},
		IsVATRegistered:   true,
		BankDetails: identity.BankDetails{
			IBAN:     "GB29 REVO 0099 6912 3456 78",
			BIC:      "REVOGB21",
			BankName: "Revolut Business",
		},
		Shareholders: []identity.Shareholder{
			{Name: "N. Meyer", Shares: 100, Class: "ordinary \u00a31"},
		},
		Directors: []identity.Director{
			{Name: "N. Meyer", IsChair: true},
			{Name: "A. Patel"},
		},
	}
}

func patchCompanyProfile(t testing.TB, h *harness.Harness, profile identity.CompanyProfile) (identity.CompanyProfile, error) {
	t.Helper()

	body := map[string]any{
		"trading_name":   profile.TradingName,
		"legal_name":     profile.LegalName,
		"company_number": profile.CompanyNumber,
		"registered_office": map[string]string{
			"line1":       profile.RegisteredOffice.Line1,
			"line2":       profile.RegisteredOffice.Line2,
			"locality":    profile.RegisteredOffice.Locality,
			"region":      profile.RegisteredOffice.Region,
			"postal_code": profile.RegisteredOffice.PostalCode,
			"country":     profile.RegisteredOffice.Country,
		},
		"incorporation_date": profile.IncorporationDate.UTC().Format(time.DateOnly),
		"year_end": map[string]int{
			"month": int(profile.YearEnd.Month),
			"day":   profile.YearEnd.Day,
		},
		"is_vat_registered": profile.IsVATRegistered,
		"vat_number":        profile.VATNumber,
		"bank_details":      profile.BankDetails,
		"shareholders":      profile.Shareholders,
		"directors":         profile.Directors,
	}

	responseBody, err := doJSONResult(t, h, nethttp.MethodPatch, "/api/identity/profile", body, nethttp.StatusOK)
	if err != nil {
		return identity.CompanyProfile{}, err
	}
	return decodeCompanyProfile(responseBody)
}

func doJSONResult(t testing.TB, h *harness.Harness, method string, path string, requestBody any, wantStatus int) ([]byte, error) {
	t.Helper()

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshal fixture request body: %w", err)
	}
	req, err := nethttp.NewRequestWithContext(context.Background(), method, path, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create fixture request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read fixture response: %w", err)
	}
	if resp.StatusCode != wantStatus {
		return nil, fmt.Errorf("%s %s status = %d, want %d; body=%s", method, path, resp.StatusCode, wantStatus, string(bodyBytes))
	}
	return bodyBytes, nil
}

type companyProfileResponse struct {
	TradingName       string                    `json:"trading_name"`
	LegalName         string                    `json:"legal_name"`
	CompanyNumber     string                    `json:"company_number"`
	RegisteredOffice  identity.RegisteredOffice `json:"registered_office"`
	IncorporationDate string                    `json:"incorporation_date"`
	YearEnd           yearEndResponse           `json:"year_end"`
	IsVATRegistered   bool                      `json:"is_vat_registered"`
	VATNumber         *string                   `json:"vat_number"`
	BankDetails       identity.BankDetails      `json:"bank_details"`
	Shareholders      []identity.Shareholder    `json:"shareholders"`
	Directors         []identity.Director       `json:"directors"`
}

type yearEndResponse struct {
	Month int `json:"month"`
	Day   int `json:"day"`
}

func decodeCompanyProfile(body []byte) (identity.CompanyProfile, error) {
	var response companyProfileResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return identity.CompanyProfile{}, fmt.Errorf("decode company fixture response: %w; body=%s", err, string(body))
	}
	incorporationDate, err := time.Parse(time.DateOnly, response.IncorporationDate)
	if err != nil {
		return identity.CompanyProfile{}, fmt.Errorf("decode company fixture incorporation date %q: %w", response.IncorporationDate, err)
	}
	return identity.CompanyProfile{
		TradingName:       response.TradingName,
		LegalName:         response.LegalName,
		CompanyNumber:     response.CompanyNumber,
		RegisteredOffice:  response.RegisteredOffice,
		IncorporationDate: incorporationDate,
		YearEnd: identity.YearEnd{
			Month: time.Month(response.YearEnd.Month),
			Day:   response.YearEnd.Day,
		},
		IsVATRegistered: response.IsVATRegistered,
		VATNumber:       response.VATNumber,
		BankDetails:     response.BankDetails,
		Shareholders:    response.Shareholders,
		Directors:       response.Directors,
	}, nil
}
