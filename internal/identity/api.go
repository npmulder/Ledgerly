package identity

import (
	"context"
	"time"
)

// Identity is the company-profile API exposed to other modules.
//
// No copies rule: consumers must call Profile at render time and never store
// company name or logo copies in their own module state.
type Identity interface {
	Profile(context.Context) (CompanyProfile, error)
	UpdateProfile(context.Context, UpdateProfilePatch) error
	CompanyFacts(context.Context) (CompanyFacts, error)
}

// CompanyProfile is the canonical single-row company identity record.
type CompanyProfile struct {
	TradingName       string
	LegalName         string
	CompanyNumber     string
	RegisteredOffice  RegisteredOffice
	IncorporationDate time.Time
	YearEnd           YearEnd
	VATNumber         *string
	BankDetails       BankDetails
	Shareholders      []Shareholder
	LogoAssetID       *AssetID
}

// RegisteredOffice is a structured postal address for the company.
type RegisteredOffice struct {
	Line1      string `json:"line1"`
	Line2      string `json:"line2"`
	Locality   string `json:"locality"`
	Region     string `json:"region"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
}

// BankDetails contains SEPA footer details for rendered documents.
type BankDetails struct {
	IBAN     string `json:"iban"`
	BIC      string `json:"bic"`
	BankName string `json:"bank_name"`
}

// Shareholder describes the shares held by one shareholder.
type Shareholder struct {
	Name   string `json:"name"`
	Shares int64  `json:"shares"`
	Class  string `json:"class"`
}

// AssetID is the opaque identifier for a logo asset owned by the future asset
// store. ID-1 only carries the nullable reference; it does not implement logo
// storage.
type AssetID string

// YearEnd stores the company's accounting year end as month and day.
type YearEnd struct {
	Month time.Month
	Day   int
}

// CompanyFacts are stable identity facts used by jurisdiction and reports.
type CompanyFacts struct {
	IncorporationDate time.Time
	YearEnd           YearEnd
}

// UpdateProfilePatch is a partial company-profile update.
type UpdateProfilePatch struct {
	TradingName       *string
	LegalName         *string
	CompanyNumber     *string
	RegisteredOffice  *RegisteredOffice
	IncorporationDate *string
	YearEnd           *YearEnd
	VATNumber         *string
	BankDetails       *BankDetails
	Shareholders      *[]Shareholder
	LogoAssetID       *AssetID
}

// NewYearEnd validates and returns a year-end value.
func NewYearEnd(month int, day int) (YearEnd, error) {
	yearEnd := YearEnd{Month: time.Month(month), Day: day}
	if err := yearEnd.validate(); err != nil {
		return YearEnd{}, err
	}
	return yearEnd, nil
}
