package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/platform/config"
)

func TestBuildLoadsConfiguredJurisdictionBeforeOpeningDatabases(t *testing.T) {
	loadErr := errors.New("bad jurisdiction pack")
	var gotSelector string

	_, err := Build(context.Background(), Config{
		Runtime: config.Config{
			DatabaseURL:  "postgres://ledgerly@example.test/ledgerly",
			Jurisdiction: "testland@0.1",
		},
		Version: "test",
	}, Dependencies{
		JurisdictionLoader: func(selector string) error {
			gotSelector = selector
			return loadErr
		},
		OpenSQL: func(driverName, dataSourceName string) (*sql.DB, error) {
			t.Fatalf("OpenSQL called before jurisdiction load failed")
			return nil, nil
		},
	})
	if !errors.Is(err, loadErr) {
		t.Fatalf("Build() error = %v, want wrapped jurisdiction loader error", err)
	}
	if gotSelector != "testland@0.1" {
		t.Fatalf("jurisdiction selector = %q, want testland@0.1", gotSelector)
	}
}

func TestIdentityAssetIDFromURLAcceptsStoredInvoiceAssetURLForms(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want identity.AssetID
	}{
		{
			name: "relative identity asset URL",
			raw:  "/api/identity/assets/17830098-8109-4a00-8b00-000000000001",
			want: "17830098-8109-4a00-8b00-000000000001",
		},
		{
			name: "absolute identity asset URL with query string",
			raw:  "https://ledgerly.example.test/api/identity/assets/17830098-8109-4a00-8b00-000000000001?download=1",
			want: "17830098-8109-4a00-8b00-000000000001",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := identityAssetIDFromURL(test.raw)
			if err != nil {
				t.Fatalf("identityAssetIDFromURL() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("identityAssetIDFromURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestIdentityInvoicePDFAssetStoreLoadInvoicePDF(t *testing.T) {
	pdf := []byte("%PDF-1.4\nstored invoice\n")
	profile := &fakeInvoicePDFProfile{
		asset: identity.Asset{
			MIME:  "application/pdf",
			Bytes: pdf,
		},
	}
	store := identityInvoicePDFAssetStore{profile: profile}

	got, err := store.LoadInvoicePDF(context.Background(), "https://ledgerly.example.test/api/identity/assets/pdf-asset-1?download=1")
	if err != nil {
		t.Fatalf("LoadInvoicePDF() error = %v", err)
	}
	if profile.gotID != "pdf-asset-1" {
		t.Fatalf("Asset() id = %q, want pdf-asset-1", profile.gotID)
	}
	if !bytes.Equal(got, pdf) {
		t.Fatalf("LoadInvoicePDF() bytes = %q, want %q", got, pdf)
	}
	got[0] = 'X'
	if profile.asset.Bytes[0] != '%' {
		t.Fatal("LoadInvoicePDF() returned aliased asset bytes")
	}
}

func TestIdentityDirectorNamesTreatsMissingProfileAsNoNames(t *testing.T) {
	names, err := (identityDirectorNames{
		profile: fakeDirectorNameProfile{err: identity.ErrProfileNotFound},
	}).DirectorNames(context.Background())
	if err != nil {
		t.Fatalf("DirectorNames() error = %v, want nil", err)
	}
	if len(names) != 0 {
		t.Fatalf("DirectorNames() = %v, want no names", names)
	}
}

func TestIdentityDirectorNamesPropagatesUnexpectedProfileError(t *testing.T) {
	wantErr := errors.New("profile unavailable")
	_, err := (identityDirectorNames{
		profile: fakeDirectorNameProfile{err: wantErr},
	}).DirectorNames(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("DirectorNames() error = %v, want %v", err, wantErr)
	}
}

func TestIdentityDirectorNamesReturnsTrimmedDirectorNames(t *testing.T) {
	names, err := (identityDirectorNames{
		profile: fakeDirectorNameProfile{profile: identity.CompanyProfile{
			Shareholders: []identity.Shareholder{
				{Name: "Shareholder Only"},
			},
			Directors: []identity.Director{
				{Name: "  Neil Mulder  "},
				{Name: " "},
				{Name: "Jane Director"},
			},
		}},
	}).DirectorNames(context.Background())
	if err != nil {
		t.Fatalf("DirectorNames() error = %v", err)
	}
	want := []string{"Neil Mulder", "Jane Director"}
	if len(names) != len(want) {
		t.Fatalf("DirectorNames() = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("DirectorNames()[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

func TestIdentityDirectorNamesIgnoresShareholders(t *testing.T) {
	names, err := (identityDirectorNames{
		profile: fakeDirectorNameProfile{profile: identity.CompanyProfile{
			Shareholders: []identity.Shareholder{
				{Name: "Shareholder Only"},
			},
		}},
	}).DirectorNames(context.Background())
	if err != nil {
		t.Fatalf("DirectorNames() error = %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("DirectorNames() = %v, want no names without directors", names)
	}
}

type fakeInvoicePDFProfile struct {
	gotID identity.AssetID
	asset identity.Asset
}

func (p *fakeInvoicePDFProfile) Asset(_ context.Context, id identity.AssetID) (identity.Asset, error) {
	p.gotID = id
	return p.asset, nil
}

type fakeDirectorNameProfile struct {
	profile identity.CompanyProfile
	err     error
}

func (p fakeDirectorNameProfile) Profile(context.Context) (identity.CompanyProfile, error) {
	if p.err != nil {
		return identity.CompanyProfile{}, p.err
	}
	return p.profile, nil
}
