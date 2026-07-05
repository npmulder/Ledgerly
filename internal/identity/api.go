package identity

import (
	"context"
	"errors"
	"time"
)

const (
	// SessionCookieName uses the __Host- prefix so browsers require Secure,
	// Path=/, and no Domain attribute.
	SessionCookieName = "__Host-ledgerly_session"

	sessionDuration = 30 * 24 * time.Hour
)

var (
	ErrRegistrationClosed = errors.New("registration is closed")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthenticated    = errors.New("unauthenticated")
	ErrUserNotFound       = errors.New("user not found")
	ErrAssetNotFound      = errors.New("identity: asset not found")
	ErrAssetTooLarge      = errors.New("identity: asset exceeds maximum size")
	ErrUnsupportedAsset   = errors.New("identity: unsupported asset MIME type")
)

// Identity is the company-profile API exposed to other modules.
//
// No copies rule: consumers must call Profile at render time and never store
// company name or logo copies in their own module state.
type Identity interface {
	Profile(context.Context) (CompanyProfile, error)
	UpdateProfile(context.Context, UpdateProfilePatch) error
	ReplaceLogo(context.Context, LogoUpload) (AssetID, error)
	Asset(context.Context, AssetID) (Asset, error)
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

// AssetID is the opaque identifier for a logo asset.
type AssetID string

// LogoUpload is a validated image upload candidate for the company logo.
type LogoUpload struct {
	MIME  string
	Bytes []byte
}

// Asset is the stored logo asset read model.
type Asset struct {
	ID        AssetID
	SHA256    string
	MIME      string
	Size      int64
	CreatedAt time.Time
	Bytes     []byte
}

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

// User is the authenticated account shape shared across identity HTTP and
// module boundaries.
type User struct {
	ID        int64
	Email     string
	Name      string
	CreatedAt time.Time
}

// RegisterInput creates the first local owner account.
type RegisterInput struct {
	Email    string
	Password string
	Name     string
}

// LoginInput verifies a password and opens a session.
type LoginInput struct {
	Email    string
	Password string
}

// LoginResult is returned after a successful password login.
type LoginResult struct {
	User      User
	Token     string
	ExpiresAt time.Time
}

type CredentialKind string

const CredentialKindSessionCookie CredentialKind = "session_cookie"

// Credential is the parsed authentication material supplied by middleware.
type Credential struct {
	Kind  CredentialKind
	Token string
}

// Principal is the authenticated caller attached to protected request contexts.
type Principal struct {
	User      User
	ExpiresAt time.Time
}

// CredentialCheckResult lets middleware refresh browser credentials after a
// successful check.
type CredentialCheckResult struct {
	Principal Principal
	Token     string
	ExpiresAt time.Time
}

// CredentialChecker is intentionally the only auth dependency used by the
// middleware. CV-242 PAT support should add another credential parser/checker
// behind this interface and reuse the same protected-route middleware.
type CredentialChecker interface {
	CheckCredential(ctx context.Context, credential Credential) (CredentialCheckResult, error)
}

// NewYearEnd validates and returns a year-end value.
func NewYearEnd(month int, day int) (YearEnd, error) {
	yearEnd := YearEnd{Month: time.Month(month), Day: day}
	if err := yearEnd.validate(); err != nil {
		return YearEnd{}, err
	}
	return yearEnd, nil
}

type principalContextKey struct{}

func contextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// PrincipalFromContext returns the authenticated caller attached by middleware.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}
