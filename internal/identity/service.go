package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

const dateLayout = "2006-01-02"

// ErrProfileNotFound is returned when the single profile row has not been
// initialised yet.
var ErrProfileNotFound = errors.New("identity: company profile not found")

type storedUser struct {
	User
	PasswordHash string
}

type storedSession struct {
	User
	ExpiresAt time.Time
	CreatedAt time.Time
}

type Store interface {
	UsersExist(ctx context.Context) (bool, error)
	CreateFirstUser(ctx context.Context, email, passwordHash, name string) (User, error)
	FindUserByEmail(ctx context.Context, email string) (storedUser, error)
	CreateSession(ctx context.Context, userID int64, tokenHash []byte, expiresAt time.Time) error
	FindSessionByTokenHash(ctx context.Context, tokenHash []byte) (storedSession, error)
	RefreshSession(ctx context.Context, tokenHash []byte, expiresAt time.Time) error
	DeleteSession(ctx context.Context, tokenHash []byte) error
	DeleteExpiredSessions(ctx context.Context, now time.Time) error
}

// Service owns identity auth use cases.
type Service struct {
	store          Store
	clock          clock.Clock
	passwordParams PasswordParams
	tokenReader    io.Reader
}

type ServiceOption func(*Service)

func WithPasswordParams(params PasswordParams) ServiceOption {
	return func(s *Service) {
		s.passwordParams = normalizePasswordParams(params)
	}
}

func WithTokenReader(reader io.Reader) ServiceOption {
	return func(s *Service) {
		if reader != nil {
			s.tokenReader = reader
		}
	}
}

func NewService(store Store, clk clock.Clock, opts ...ServiceOption) *Service {
	if clk == nil {
		clk = clock.New()
	}

	service := &Service{
		store:          store,
		clock:          clk,
		passwordParams: DefaultPasswordParams(),
		tokenReader:    rand.Reader,
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

// ProfileService implements the company-profile API.
type ProfileService struct {
	tx     db.Tx
	bus    *bus.Bus
	store  profileStore
	assets fileAssetStore
}

var _ Identity = (*ProfileService)(nil)

type ProfileOption func(*ProfileService)

// WithDataDir configures disk-backed asset storage for profile APIs.
func WithDataDir(dataDir string) ProfileOption {
	return func(s *ProfileService) {
		s.assets = fileAssetStore{dataDir: dataDir}
	}
}

// New returns a profile API bound to tx. The caller owns the transaction
// lifetime; UpdateProfile publishes its event on the supplied transaction.
func New(tx db.Tx, eventBus *bus.Bus, opts ...ProfileOption) *ProfileService {
	return NewProfileService(tx, eventBus, opts...)
}

// NewProfileService returns a profile API bound to tx. The caller owns the
// transaction lifetime; UpdateProfile publishes its event on tx.
func NewProfileService(tx db.Tx, eventBus *bus.Bus, opts ...ProfileOption) *ProfileService {
	service := &ProfileService{
		tx:     tx,
		bus:    eventBus,
		store:  profileStore{},
		assets: fileAssetStoreFromEnv(),
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

// Profile returns the current company profile.
func (s *ProfileService) Profile(ctx context.Context) (CompanyProfile, error) {
	return s.store.profile(ctx, s.tx)
}

// UpdateProfile applies a partial profile update and publishes ProfileUpdated
// inside the same caller-owned transaction.
func (s *ProfileService) UpdateProfile(ctx context.Context, patch UpdateProfilePatch) error {
	profile, err := s.store.profileForUpdate(ctx, s.tx)
	create := false
	if errors.Is(err, ErrProfileNotFound) {
		create = true
		profile = CompanyProfile{}
	} else if err != nil {
		return err
	}

	updated, err := patch.apply(profile)
	if err != nil {
		return err
	}
	if create {
		if err := s.store.createProfile(ctx, s.tx, updated); err != nil {
			return err
		}
	} else {
		if err := s.store.updateProfile(ctx, s.tx, updated); err != nil {
			return err
		}
	}
	return s.publishProfileUpdated(ctx)
}

// ReplaceLogo stores upload as a content-addressed logo asset, points the
// company profile at the new asset row, and publishes ProfileUpdated.
func (s *ProfileService) ReplaceLogo(ctx context.Context, upload LogoUpload) (AssetID, error) {
	validated, err := validateLogoUpload(upload)
	if err != nil {
		return "", err
	}
	if err := s.assets.write(validated.sha256, validated.bytes); err != nil {
		return "", err
	}

	id, err := newAssetID()
	if err != nil {
		return "", err
	}
	if err := s.store.createAsset(ctx, s.tx, assetRecord{
		ID:     id,
		SHA256: validated.sha256,
		MIME:   validated.mime,
		Size:   validated.size,
	}); err != nil {
		return "", err
	}

	profile, err := s.store.profileForUpdate(ctx, s.tx)
	if err != nil {
		return "", err
	}
	profile.LogoAssetID = &id
	if err := s.store.updateProfile(ctx, s.tx, profile); err != nil {
		return "", err
	}
	if err := s.publishProfileUpdated(ctx); err != nil {
		return "", err
	}
	return id, nil
}

// Asset returns a stored asset's metadata and bytes.
func (s *ProfileService) Asset(ctx context.Context, id AssetID) (Asset, error) {
	record, err := s.store.asset(ctx, s.tx, id)
	if err != nil {
		return Asset{}, err
	}
	data, err := s.assets.read(record.SHA256)
	if err != nil {
		return Asset{}, err
	}
	if int64(len(data)) != record.Size {
		return Asset{}, fmt.Errorf("identity: asset %s size = %d, want %d", record.ID, len(data), record.Size)
	}
	return Asset{
		ID:        record.ID,
		SHA256:    record.SHA256,
		MIME:      record.MIME,
		Size:      record.Size,
		CreatedAt: record.CreatedAt,
		Bytes:     data,
	}, nil
}

// CompanyFacts returns identity facts consumed by jurisdiction and reports.
func (s *ProfileService) CompanyFacts(ctx context.Context) (CompanyFacts, error) {
	profile, err := s.Profile(ctx)
	if err != nil {
		return CompanyFacts{}, err
	}
	return CompanyFacts{
		IncorporationDate: profile.IncorporationDate,
		YearEnd:           profile.YearEnd,
	}, nil
}

func (s *ProfileService) publishProfileUpdated(ctx context.Context) error {
	if s.bus == nil {
		return nil
	}
	if err := s.bus.Publish(ctx, s.tx, ProfileUpdated{}); err != nil {
		return fmt.Errorf("publish profile updated: %w", err)
	}
	return nil
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (User, error) {
	email, err := normalizeEmail(input.Email)
	if err != nil {
		return User{}, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return User{}, fmt.Errorf("name is required")
	}
	if strings.TrimSpace(input.Password) == "" {
		return User{}, fmt.Errorf("password is required")
	}

	closed, err := s.store.UsersExist(ctx)
	if err != nil {
		return User{}, err
	}
	if closed {
		return User{}, ErrRegistrationClosed
	}

	hash, err := HashPassword(input.Password, s.passwordParams, s.tokenReader)
	if err != nil {
		return User{}, err
	}

	return s.store.CreateFirstUser(ctx, email, hash, name)
}

func (s *Service) Login(ctx context.Context, input LoginInput) (LoginResult, error) {
	email, err := normalizeEmail(input.Email)
	if err != nil {
		return LoginResult{}, ErrInvalidCredentials
	}

	user, err := s.store.FindUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, err
	}

	ok, err := VerifyPassword(input.Password, user.PasswordHash)
	if err != nil {
		return LoginResult{}, err
	}
	if !ok {
		return LoginResult{}, ErrInvalidCredentials
	}

	token, err := newSessionToken(s.tokenReader)
	if err != nil {
		return LoginResult{}, fmt.Errorf("create session token: %w", err)
	}
	now := s.clock.Now().UTC()
	if err := s.store.DeleteExpiredSessions(ctx, now); err != nil {
		return LoginResult{}, err
	}

	expiresAt := now.Add(sessionDuration)
	tokenHash := hashSessionToken(token)
	if err := s.store.CreateSession(ctx, user.ID, tokenHash[:], expiresAt); err != nil {
		return LoginResult{}, err
	}

	return LoginResult{
		User:      user.User,
		Token:     token,
		ExpiresAt: expiresAt,
	}, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}

	tokenHash := hashSessionToken(token)
	return s.store.DeleteSession(ctx, tokenHash[:])
}

func (s *Service) CheckCredential(ctx context.Context, credential Credential) (CredentialCheckResult, error) {
	if credential.Kind != CredentialKindSessionCookie || strings.TrimSpace(credential.Token) == "" {
		return CredentialCheckResult{}, ErrUnauthenticated
	}

	tokenHash := hashSessionToken(credential.Token)
	session, err := s.store.FindSessionByTokenHash(ctx, tokenHash[:])
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			return CredentialCheckResult{}, ErrUnauthenticated
		}
		return CredentialCheckResult{}, err
	}

	now := s.clock.Now().UTC()
	if !session.ExpiresAt.After(now) {
		_ = s.store.DeleteSession(ctx, tokenHash[:])
		return CredentialCheckResult{}, ErrUnauthenticated
	}

	expiresAt := now.Add(sessionDuration)
	if err := s.store.RefreshSession(ctx, tokenHash[:], expiresAt); err != nil {
		return CredentialCheckResult{}, err
	}

	principal := Principal{
		User:      session.User,
		ExpiresAt: expiresAt,
	}
	return CredentialCheckResult{
		Principal: principal,
		Token:     credential.Token,
		ExpiresAt: expiresAt,
	}, nil
}

func (patch UpdateProfilePatch) apply(profile CompanyProfile) (CompanyProfile, error) {
	if patch.TradingName != nil {
		profile.TradingName = strings.TrimSpace(*patch.TradingName)
	}
	if patch.LegalName != nil {
		profile.LegalName = strings.TrimSpace(*patch.LegalName)
	}
	if patch.CompanyNumber != nil {
		profile.CompanyNumber = strings.TrimSpace(*patch.CompanyNumber)
	}
	if patch.RegisteredOffice != nil {
		profile.RegisteredOffice = *patch.RegisteredOffice
	}
	if patch.IncorporationDate != nil {
		incorporationDate, err := parseDate(*patch.IncorporationDate)
		if err != nil {
			return CompanyProfile{}, err
		}
		profile.IncorporationDate = incorporationDate
	}
	if patch.YearEnd != nil {
		if err := patch.YearEnd.validate(); err != nil {
			return CompanyProfile{}, err
		}
		profile.YearEnd = *patch.YearEnd
	}
	if patch.VATNumber != nil {
		vatNumber := strings.TrimSpace(*patch.VATNumber)
		if vatNumber == "" {
			profile.VATNumber = nil
		} else {
			profile.VATNumber = &vatNumber
		}
	}
	if patch.BankDetails != nil {
		profile.BankDetails = *patch.BankDetails
	}
	if patch.Shareholders != nil {
		profile.Shareholders = append([]Shareholder{}, (*patch.Shareholders)...)
	}
	if patch.LogoAssetID != nil {
		logoAssetID := AssetID(strings.TrimSpace(string(*patch.LogoAssetID)))
		if logoAssetID == "" {
			profile.LogoAssetID = nil
		} else {
			profile.LogoAssetID = &logoAssetID
		}
	}

	var err error
	if profile.TradingName, err = requiredProfileText("trading name", profile.TradingName); err != nil {
		return CompanyProfile{}, err
	}
	if profile.LegalName, err = requiredProfileText("legal name", profile.LegalName); err != nil {
		return CompanyProfile{}, err
	}
	if profile.CompanyNumber, err = requiredProfileText("company number", profile.CompanyNumber); err != nil {
		return CompanyProfile{}, err
	}
	if profile.IncorporationDate.IsZero() {
		return CompanyProfile{}, fmt.Errorf("identity: incorporation date is required")
	}
	if err := profile.YearEnd.validate(); err != nil {
		return CompanyProfile{}, err
	}
	return profile, nil
}

func requiredProfileText(field string, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("identity: %s is required", field)
	}
	return trimmed, nil
}

func normalizeEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	if email == "" {
		return "", fmt.Errorf("email is required")
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return "", fmt.Errorf("email is invalid")
	}
	return email, nil
}

func parseDate(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("identity: date is required")
	}
	parsed, err := time.Parse(dateLayout, trimmed)
	if err != nil {
		return time.Time{}, fmt.Errorf("identity: parse date %q as YYYY-MM-DD: %w", trimmed, err)
	}
	return parsed, nil
}

func (yearEnd YearEnd) validate() error {
	month := int(yearEnd.Month)
	if month < 1 || month > 12 {
		return fmt.Errorf("identity: year-end month %d out of range", month)
	}
	if yearEnd.Day < 1 || yearEnd.Day > daysInMonth(yearEnd.Month) {
		return fmt.Errorf("identity: year-end day %d out of range for month %d", yearEnd.Day, month)
	}
	return nil
}

func daysInMonth(month time.Month) int {
	switch month {
	case time.April, time.June, time.September, time.November:
		return 30
	case time.February:
		return 29
	default:
		return 31
	}
}

func newSessionToken(reader io.Reader) (string, error) {
	var raw [32]byte
	if _, err := io.ReadFull(reader, raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func hashSessionToken(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(token))
}
