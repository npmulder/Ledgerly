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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/clock"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

const dateLayout = "2006-01-02"

const patTokenPrefix = "lgy_"

type storedUser struct {
	User
	PasswordHash string
}

type storedSession struct {
	User
	ExpiresAt time.Time
	CreatedAt time.Time
}

type storedPAT struct {
	PersonalAccessToken
	User User
}

type Store interface {
	UsersExist(ctx context.Context) (bool, error)
	ProfileExists(ctx context.Context) (bool, error)
	CreateFirstUser(ctx context.Context, email, passwordHash, name string) (User, error)
	CreateFirstUserWithProfile(ctx context.Context, email, passwordHash, name string, profile CompanyProfile, tokenHash []byte, expiresAt time.Time, publish profileUpdatedPublisher) (User, error)
	FindUserByEmail(ctx context.Context, email string) (storedUser, error)
	CreateSession(ctx context.Context, userID int64, tokenHash []byte, expiresAt time.Time) error
	FindSessionByTokenHash(ctx context.Context, tokenHash []byte) (storedSession, error)
	RefreshSession(ctx context.Context, tokenHash []byte, expiresAt time.Time) error
	DeleteSession(ctx context.Context, tokenHash []byte) error
	DeleteExpiredSessions(ctx context.Context, now time.Time) error
	CreatePAT(ctx context.Context, userID int64, tokenHash []byte, name string, scope PATScope, expiresAt *time.Time) (PersonalAccessToken, error)
	ListPATs(ctx context.Context, userID int64) ([]PersonalAccessToken, error)
	DeletePAT(ctx context.Context, userID int64, id int64) error
	DeletePATByTokenHash(ctx context.Context, tokenHash []byte) error
	FindPATByTokenHash(ctx context.Context, tokenHash []byte) (storedPAT, error)
	MarkPATUsed(ctx context.Context, tokenHash []byte, usedAt time.Time) error
}

type profileUpdatedPublisher func(context.Context, db.Tx) error

// Service owns identity auth use cases.
type Service struct {
	store          Store
	bus            *bus.Bus
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

func WithEventBus(eventBus *bus.Bus) ServiceOption {
	return func(s *Service) {
		s.bus = eventBus
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
	audit  AuditRecorder
}

var _ Identity = (*ProfileService)(nil)

type ProfileOption func(*ProfileService)

// AuditRecorder records service-layer profile mutations in the caller's transaction.
type AuditRecorder interface {
	Record(ctx context.Context, tx db.Tx, module, entity, entityID string, before, after any) error
}

// WithAuditRecorder installs non-ledger mutation audit logging for profile commands.
func WithAuditRecorder(recorder AuditRecorder) ProfileOption {
	return func(s *ProfileService) {
		s.audit = recorder
	}
}

// WithDataDir configures disk-backed asset storage for profile APIs.
func WithDataDir(dataDir string) ProfileOption {
	return func(s *ProfileService) {
		s.assets = fileAssetStore{dataDir: dataDir}
	}
}

// AssetWriter stores immutable non-logo assets using the same
// content-addressed file store and identity.assets metadata table as logo
// uploads.
type AssetWriter struct {
	pool   *pgxpool.Pool
	assets fileAssetStore
	store  profileStore
}

// NewAssetWriter returns a process-level immutable asset writer.
func NewAssetWriter(pool *pgxpool.Pool, dataDir string) *AssetWriter {
	return &AssetWriter{
		pool:   pool,
		assets: fileAssetStore{dataDir: dataDir},
	}
}

// StoreAsset stores an immutable document asset and returns a fresh asset id.
func (w *AssetWriter) StoreAsset(ctx context.Context, upload AssetUpload) (_ AssetID, err error) {
	if w == nil || w.pool == nil {
		return "", fmt.Errorf("identity: asset writer requires pool")
	}
	validated, err := validateAssetUpload(upload)
	if err != nil {
		return "", err
	}
	if err := w.assets.write(validated.sha256, validated.bytes); err != nil {
		return "", err
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("identity: begin asset transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	id, err := newAssetID()
	if err != nil {
		return "", err
	}
	if err := w.store.createAsset(ctx, tx, assetRecord{
		ID:     id,
		SHA256: validated.sha256,
		MIME:   validated.mime,
		Size:   validated.size,
	}); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("identity: commit asset transaction: %w", err)
	}
	return id, nil
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

// TransactionalProfileService owns database transactions for profile commands
// while exposing the same identity API to HTTP wiring.
type TransactionalProfileService struct {
	pool *pgxpool.Pool
	bus  *bus.Bus
	opts []ProfileOption
}

var _ Identity = (*TransactionalProfileService)(nil)

// NewTransactionalProfileService returns a profile API that opens a transaction
// for mutating operations and binds event publication to that transaction.
func NewTransactionalProfileService(pool *pgxpool.Pool, eventBus *bus.Bus, opts ...ProfileOption) *TransactionalProfileService {
	copiedOpts := append([]ProfileOption{}, opts...)
	return &TransactionalProfileService{
		pool: pool,
		bus:  eventBus,
		opts: copiedOpts,
	}
}

func (s *TransactionalProfileService) Profile(ctx context.Context) (CompanyProfile, error) {
	return NewProfileService(s.pool, s.bus, s.opts...).Profile(ctx)
}

func (s *TransactionalProfileService) UpdateProfile(ctx context.Context, patch UpdateProfilePatch) error {
	return s.withTransaction(ctx, func(api *ProfileService) error {
		return api.UpdateProfile(ctx, patch)
	})
}

func (s *TransactionalProfileService) ReplaceLogo(ctx context.Context, upload LogoUpload) (id AssetID, err error) {
	err = s.withTransaction(ctx, func(api *ProfileService) error {
		var replaceErr error
		id, replaceErr = api.ReplaceLogo(ctx, upload)
		return replaceErr
	})
	return id, err
}

func (s *TransactionalProfileService) Asset(ctx context.Context, id AssetID) (Asset, error) {
	return NewProfileService(s.pool, s.bus, s.opts...).Asset(ctx, id)
}

func (s *TransactionalProfileService) CompanyFacts(ctx context.Context) (CompanyFacts, error) {
	return NewProfileService(s.pool, s.bus, s.opts...).CompanyFacts(ctx)
}

func (s *TransactionalProfileService) IsVATRegistered(ctx context.Context) (bool, error) {
	return NewProfileService(s.pool, s.bus, s.opts...).IsVATRegistered(ctx)
}

func (s *TransactionalProfileService) withTransaction(ctx context.Context, fn func(*ProfileService) error) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("identity: begin profile transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	if err = fn(NewProfileService(tx, s.bus, s.opts...)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("identity: commit profile transaction: %w", err)
	}
	return nil
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

	if patch.LogoAssetID != nil {
		logoAssetID := AssetID(strings.TrimSpace(string(*patch.LogoAssetID)))
		if logoAssetID != "" {
			if _, err := s.store.asset(ctx, s.tx, logoAssetID); err != nil {
				if errors.Is(err, ErrAssetNotFound) {
					return fmt.Errorf("identity: logo asset id %s was not found: %w", logoAssetID, err)
				}
				return err
			}
		}
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
	if err := s.recordAudit(ctx, profileAuditValue(profile, !create), profileAuditValue(updated, true)); err != nil {
		return err
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
	before := profile
	profile.LogoAssetID = &id
	if err := s.store.updateProfile(ctx, s.tx, profile); err != nil {
		return "", err
	}
	if err := s.recordAudit(ctx, profileAuditValue(before, true), profileAuditValue(profile, true)); err != nil {
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
		IsVATRegistered:   profile.IsVATRegistered,
		Directors:         append([]Director{}, profile.Directors...),
	}, nil
}

// IsVATRegistered returns whether the company is registered for VAT.
func (s *ProfileService) IsVATRegistered(ctx context.Context) (bool, error) {
	facts, err := s.CompanyFacts(ctx)
	if err != nil {
		return false, err
	}
	return facts.IsVATRegistered, nil
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

func (s *ProfileService) recordAudit(ctx context.Context, before, after any) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, s.tx, "identity", "profile", "1", before, after)
}

type profileAuditRecord struct {
	TradingName       string           `json:"trading_name"`
	LegalName         string           `json:"legal_name"`
	CompanyNumber     string           `json:"company_number"`
	RegisteredOffice  RegisteredOffice `json:"registered_office"`
	IncorporationDate string           `json:"incorporation_date"`
	YearEnd           yearEndAudit     `json:"year_end"`
	IsVATRegistered   bool             `json:"is_vat_registered"`
	VATNumber         *string          `json:"vat_number"`
	BankDetails       BankDetails      `json:"bank_details"`
	Shareholders      []Shareholder    `json:"shareholders"`
	LogoAssetID       *AssetID         `json:"logo_asset_id"`
}

type yearEndAudit struct {
	Month int `json:"month"`
	Day   int `json:"day"`
}

func profileAuditValue(profile CompanyProfile, exists bool) any {
	if !exists {
		return nil
	}
	return profileAuditRecord{
		TradingName:       profile.TradingName,
		LegalName:         profile.LegalName,
		CompanyNumber:     profile.CompanyNumber,
		RegisteredOffice:  profile.RegisteredOffice,
		IncorporationDate: profile.IncorporationDate.UTC().Format(dateLayout),
		YearEnd: yearEndAudit{
			Month: int(profile.YearEnd.Month),
			Day:   profile.YearEnd.Day,
		},
		IsVATRegistered: profile.IsVATRegistered,
		VATNumber:       cloneStringPointer(profile.VATNumber),
		BankDetails:     profile.BankDetails,
		Shareholders:    append([]Shareholder{}, profile.Shareholders...),
		LogoAssetID:     cloneAssetIDPointer(profile.LogoAssetID),
	}
}

func cloneAssetIDPointer(value *AssetID) *AssetID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
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

func (s *Service) RegisterWithProfile(ctx context.Context, email, password, name string, profile CompanyProfile) (RegisterWithProfileResult, error) {
	normalizedEmail, err := normalizeEmail(email)
	if err != nil {
		return RegisterWithProfileResult{}, err
	}
	normalizedName := strings.TrimSpace(name)
	if normalizedName == "" {
		return RegisterWithProfileResult{}, fmt.Errorf("name is required")
	}
	if strings.TrimSpace(password) == "" {
		return RegisterWithProfileResult{}, fmt.Errorf("password is required")
	}
	normalizedProfile, err := normalizeInitialCompanyProfile(profile)
	if err != nil {
		return RegisterWithProfileResult{}, err
	}

	usersExist, err := s.store.UsersExist(ctx)
	if err != nil {
		return RegisterWithProfileResult{}, err
	}
	profileExists, err := s.store.ProfileExists(ctx)
	if err != nil {
		return RegisterWithProfileResult{}, err
	}
	if usersExist || profileExists {
		return RegisterWithProfileResult{}, ErrRegistrationClosed
	}

	hash, err := HashPassword(password, s.passwordParams, s.tokenReader)
	if err != nil {
		return RegisterWithProfileResult{}, err
	}
	token, err := newSessionToken(s.tokenReader)
	if err != nil {
		return RegisterWithProfileResult{}, fmt.Errorf("create session token: %w", err)
	}
	expiresAt := s.clock.Now().UTC().Add(sessionDuration)
	tokenHash := hashSessionToken(token)

	user, err := s.store.CreateFirstUserWithProfile(
		ctx,
		normalizedEmail,
		hash,
		normalizedName,
		normalizedProfile,
		tokenHash[:],
		expiresAt,
		s.publishProfileUpdatedInTx,
	)
	if err != nil {
		return RegisterWithProfileResult{}, err
	}
	return RegisterWithProfileResult{
		User:      user,
		Profile:   normalizedProfile,
		Token:     token,
		ExpiresAt: expiresAt,
	}, nil
}

func (s *Service) publishProfileUpdatedInTx(ctx context.Context, tx db.Tx) error {
	if s.bus == nil {
		return nil
	}
	if err := s.bus.Publish(ctx, tx, ProfileUpdated{}); err != nil {
		return fmt.Errorf("publish profile updated: %w", err)
	}
	return nil
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

func (s *Service) CreatePAT(ctx context.Context, principal Principal, input CreatePATInput) (CreatePATResult, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return CreatePATResult{}, fmt.Errorf("PAT name is required")
	}
	scope, err := normalizePATScope(input.Scope)
	if err != nil {
		return CreatePATResult{}, err
	}
	var expiresAt *time.Time
	if input.ExpiresAt != nil {
		normalized := input.ExpiresAt.UTC()
		if !normalized.After(s.clock.Now().UTC()) {
			return CreatePATResult{}, fmt.Errorf("PAT expiry must be in the future")
		}
		expiresAt = &normalized
	}

	token, err := newPATToken(s.tokenReader)
	if err != nil {
		return CreatePATResult{}, fmt.Errorf("create PAT token: %w", err)
	}
	tokenHash := hashPATToken(token)
	record, err := s.store.CreatePAT(ctx, principal.User.ID, tokenHash[:], name, scope, expiresAt)
	if err != nil {
		return CreatePATResult{}, err
	}
	return CreatePATResult{
		PersonalAccessToken: record,
		Token:               token,
	}, nil
}

func (s *Service) ListPATs(ctx context.Context, principal Principal) ([]PersonalAccessToken, error) {
	return s.store.ListPATs(ctx, principal.User.ID)
}

func (s *Service) RevokePAT(ctx context.Context, principal Principal, id int64) error {
	if id <= 0 {
		return fmt.Errorf("PAT id is required")
	}
	return s.store.DeletePAT(ctx, principal.User.ID, id)
}

func (s *Service) CheckCredential(ctx context.Context, credential Credential) (CredentialCheckResult, error) {
	token := strings.TrimSpace(credential.Token)
	if token == "" {
		return CredentialCheckResult{}, ErrUnauthenticated
	}

	switch credential.Kind {
	case CredentialKindSessionCookie:
		return s.checkSessionCredential(ctx, token)
	case CredentialKindPAT:
		return s.checkPATCredential(ctx, token)
	default:
		return CredentialCheckResult{}, ErrUnauthenticated
	}
}

func (s *Service) checkSessionCredential(ctx context.Context, token string) (CredentialCheckResult, error) {
	tokenHash := hashSessionToken(token)
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
		Token:     token,
		ExpiresAt: expiresAt,
		SetCookie: true,
	}, nil
}

func (s *Service) checkPATCredential(ctx context.Context, token string) (CredentialCheckResult, error) {
	if !strings.HasPrefix(token, patTokenPrefix) {
		return CredentialCheckResult{}, ErrUnauthenticated
	}
	tokenHash := hashPATToken(token)
	record, err := s.store.FindPATByTokenHash(ctx, tokenHash[:])
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			return CredentialCheckResult{}, ErrUnauthenticated
		}
		return CredentialCheckResult{}, err
	}

	now := s.clock.Now().UTC()
	if record.ExpiresAt != nil && !record.ExpiresAt.After(now) {
		return CredentialCheckResult{}, ErrUnauthenticated
	}
	if err := s.store.MarkPATUsed(ctx, tokenHash[:], now); err != nil {
		return CredentialCheckResult{}, err
	}

	return CredentialCheckResult{
		Principal: Principal{
			User: record.User,
			PAT: &PrincipalPAT{
				ID:    record.ID,
				Name:  record.Name,
				Scope: record.Scope,
			},
		},
	}, nil
}

func normalizePATScope(scope PATScope) (PATScope, error) {
	switch scope {
	case "", PATScopeReadOnly:
		return PATScopeReadOnly, nil
	case PATScopeFull:
		return PATScopeFull, nil
	default:
		return "", fmt.Errorf("PAT scope must be read-only or full")
	}
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
	if patch.IsVATRegistered != nil {
		profile.IsVATRegistered = *patch.IsVATRegistered
	}
	if patch.VATNumber != nil {
		vatNumber := strings.TrimSpace(*patch.VATNumber)
		if vatNumber == "" {
			profile.VATNumber = nil
		} else {
			profile.VATNumber = &vatNumber
		}
		if patch.IsVATRegistered == nil {
			profile.IsVATRegistered = profile.VATNumber != nil
		}
	}
	if patch.BankDetails != nil {
		profile.BankDetails = *patch.BankDetails
	}
	if patch.Shareholders != nil {
		profile.Shareholders = append([]Shareholder{}, (*patch.Shareholders)...)
	}
	if patch.Directors != nil {
		directors, err := normalizeDirectorsWithExisting(*patch.Directors, profile.Directors)
		if err != nil {
			return CompanyProfile{}, err
		}
		profile.Directors = directors
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
	if profile.Directors == nil {
		profile.Directors = []Director{}
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

func normalizeInitialCompanyProfile(profile CompanyProfile) (CompanyProfile, error) {
	normalized := profile
	var err error
	if normalized.TradingName, err = requiredProfileText("trading name", normalized.TradingName); err != nil {
		return CompanyProfile{}, err
	}
	if normalized.LegalName, err = requiredProfileText("legal name", normalized.LegalName); err != nil {
		return CompanyProfile{}, err
	}
	if normalized.CompanyNumber, err = requiredProfileText("company number", normalized.CompanyNumber); err != nil {
		return CompanyProfile{}, err
	}
	if normalized.IncorporationDate.IsZero() {
		return CompanyProfile{}, fmt.Errorf("identity: incorporation date is required")
	}
	if err := normalized.YearEnd.validate(); err != nil {
		return CompanyProfile{}, err
	}
	if normalized.VATNumber != nil {
		vatNumber := strings.TrimSpace(*normalized.VATNumber)
		if vatNumber == "" {
			normalized.VATNumber = nil
		} else {
			normalized.VATNumber = &vatNumber
		}
	}
	normalized.Shareholders = append([]Shareholder{}, normalized.Shareholders...)
	if normalized.Shareholders == nil {
		normalized.Shareholders = []Shareholder{}
	}
	normalized.Directors, err = normalizeDirectors(normalized.Directors)
	if err != nil {
		return CompanyProfile{}, err
	}
	if normalized.LogoAssetID != nil {
		logoAssetID := AssetID(strings.TrimSpace(string(*normalized.LogoAssetID)))
		if logoAssetID == "" {
			normalized.LogoAssetID = nil
		} else {
			normalized.LogoAssetID = &logoAssetID
		}
	}
	return normalized, nil
}

func normalizeDirectors(directors []Director) ([]Director, error) {
	return normalizeDirectorsWithExisting(directors, nil)
}

func normalizeDirectorsWithExisting(directors []Director, existing []Director) ([]Director, error) {
	if directors == nil {
		return []Director{}, nil
	}
	normalized := make([]Director, 0, len(directors))
	usedIDs := make(map[string]bool, len(directors))
	reservedIDs := make(map[string]bool, len(existing))
	existingByName := make(map[string][]Director, len(existing))
	for _, director := range existing {
		id := strings.TrimSpace(director.ID)
		if id == "" || validateDirectorID(id) != nil {
			continue
		}
		reservedIDs[id] = true
		key := directorIdentityKey(director.Name)
		if key != "" {
			existingByName[key] = append(existingByName[key], Director{ID: id, Name: strings.TrimSpace(director.Name)})
		}
	}
	nextID := 1
	for _, director := range directors {
		name := strings.TrimSpace(director.Name)
		if name == "" {
			return nil, fmt.Errorf("identity: director name is required")
		}
		id := strings.TrimSpace(director.ID)
		if id == "" {
			for _, candidate := range existingByName[directorIdentityKey(name)] {
				if !usedIDs[candidate.ID] {
					id = candidate.ID
					break
				}
			}
		}
		if id == "" {
			for {
				candidate := fmt.Sprintf("director-%d", nextID)
				nextID++
				if !usedIDs[candidate] && !reservedIDs[candidate] {
					id = candidate
					break
				}
			}
		}
		if err := validateDirectorID(id); err != nil {
			return nil, err
		}
		if usedIDs[id] {
			return nil, fmt.Errorf("identity: director id %q is duplicated", id)
		}
		usedIDs[id] = true

		director.ID = id
		director.Name = name
		if director.AppointedDate != nil {
			appointedDate := strings.TrimSpace(*director.AppointedDate)
			if appointedDate == "" {
				director.AppointedDate = nil
			} else {
				if _, err := parseDate(appointedDate); err != nil {
					return nil, fmt.Errorf("identity: director appointed_date must be YYYY-MM-DD: %w", err)
				}
				director.AppointedDate = &appointedDate
			}
		}
		normalized = append(normalized, director)
	}
	return normalized, nil
}

func directorIdentityKey(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

func validateDirectorID(id string) error {
	if id == "" {
		return fmt.Errorf("identity: director id is required")
	}
	if !strings.HasPrefix(id, "director-") {
		return fmt.Errorf("identity: director id %q is invalid", id)
	}
	suffix := strings.TrimPrefix(id, "director-")
	if suffix == "" || suffix[0] == '0' {
		return fmt.Errorf("identity: director id %q is invalid", id)
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return fmt.Errorf("identity: director id %q is invalid", id)
		}
	}
	return nil
}

// DirectorNames returns non-empty director names in profile order.
func (profile CompanyProfile) DirectorNames() []string {
	names := make([]string, 0, len(profile.Directors))
	for _, director := range profile.Directors {
		if name := strings.TrimSpace(director.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
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

func newPATToken(reader io.Reader) (string, error) {
	token, err := newSessionToken(reader)
	if err != nil {
		return "", err
	}
	return patTokenPrefix + token, nil
}

func hashPATToken(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(token))
}
