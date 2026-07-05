package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) UsersExist(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM identity.users)").Scan(&exists); err != nil {
		return false, fmt.Errorf("check users exist: %w", err)
	}
	return exists, nil
}

func (s *PostgresStore) CreateFirstUser(ctx context.Context, email, passwordHash, name string) (user User, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("begin first user registration: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, "LOCK TABLE identity.users IN EXCLUSIVE MODE"); err != nil {
		return User{}, fmt.Errorf("lock users for first registration: %w", err)
	}

	var userCount int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM identity.users").Scan(&userCount); err != nil {
		return User{}, fmt.Errorf("count users: %w", err)
	}
	if userCount > 0 {
		return User{}, ErrRegistrationClosed
	}

	if err := tx.QueryRow(
		ctx,
		`INSERT INTO identity.users (email, password_hash, name)
		 VALUES ($1, $2, $3)
		 RETURNING id, email, name, created_at`,
		email,
		passwordHash,
		name,
	).Scan(&user.ID, &user.Email, &user.Name, &user.CreatedAt); err != nil {
		return User{}, fmt.Errorf("insert first user: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit first user registration: %w", err)
	}
	return user, nil
}

func (s *PostgresStore) FindUserByEmail(ctx context.Context, email string) (storedUser, error) {
	var user storedUser
	err := s.pool.QueryRow(
		ctx,
		`SELECT id, email, password_hash, name, created_at
		 FROM identity.users
		 WHERE email = $1`,
		email,
	).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.CreatedAt)
	if err == nil {
		return user, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return storedUser{}, ErrUserNotFound
	}
	return storedUser{}, fmt.Errorf("find user by email: %w", err)
}

func (s *PostgresStore) CreateSession(ctx context.Context, userID int64, tokenHash []byte, expiresAt time.Time) error {
	_, err := s.pool.Exec(
		ctx,
		`INSERT INTO identity.sessions (token_sha256, user_id, expires_at)
		 VALUES ($1, $2, $3)`,
		tokenHash,
		userID,
		expiresAt,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *PostgresStore) FindSessionByTokenHash(ctx context.Context, tokenHash []byte) (storedSession, error) {
	var session storedSession
	err := s.pool.QueryRow(
		ctx,
		`SELECT u.id, u.email, u.name, u.created_at, s.expires_at, s.created_at
		 FROM identity.sessions s
		 JOIN identity.users u ON u.id = s.user_id
		 WHERE s.token_sha256 = $1`,
		tokenHash,
	).Scan(
		&session.ID,
		&session.Email,
		&session.Name,
		&session.User.CreatedAt,
		&session.ExpiresAt,
		&session.CreatedAt,
	)
	if err == nil {
		return session, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return storedSession{}, ErrUnauthenticated
	}
	return storedSession{}, fmt.Errorf("find session: %w", err)
}

func (s *PostgresStore) RefreshSession(ctx context.Context, tokenHash []byte, expiresAt time.Time) error {
	tag, err := s.pool.Exec(
		ctx,
		`UPDATE identity.sessions
		 SET expires_at = $2
		 WHERE token_sha256 = $1`,
		tokenHash,
		expiresAt,
	)
	if err != nil {
		return fmt.Errorf("refresh session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUnauthenticated
	}
	return nil
}

func (s *PostgresStore) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := s.pool.Exec(ctx, "DELETE FROM identity.sessions WHERE token_sha256 = $1", tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *PostgresStore) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	if _, err := s.pool.Exec(ctx, "DELETE FROM identity.sessions WHERE expires_at <= $1", now); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}

type profileStore struct{}

type assetRecord struct {
	ID        AssetID
	SHA256    string
	MIME      string
	Size      int64
	CreatedAt time.Time
}

func (profileStore) profile(ctx context.Context, tx db.Tx) (CompanyProfile, error) {
	return scanProfile(ctx, tx, "")
}

func (profileStore) profileForUpdate(ctx context.Context, tx db.Tx) (CompanyProfile, error) {
	return scanProfile(ctx, tx, " FOR UPDATE")
}

func (profileStore) createProfile(ctx context.Context, tx db.Tx, profile CompanyProfile) error {
	values, err := profileStorageValuesFrom(profile)
	if err != nil {
		return err
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
	year_end_day,
	vat_number,
	bank_details,
	shareholders,
	logo_asset_id
) VALUES (
	1,
	$1,
	$2,
	$3,
	$4::jsonb,
	$5,
	$6,
	$7,
	$8,
	$9::jsonb,
	$10::jsonb,
	$11
)`,
		profile.TradingName,
		profile.LegalName,
		profile.CompanyNumber,
		string(values.office),
		profile.IncorporationDate,
		int(profile.YearEnd.Month),
		profile.YearEnd.Day,
		values.vatNumber,
		string(values.bankDetails),
		string(values.shareholders),
		values.logoAssetID,
	)
	if err != nil {
		return fmt.Errorf("create company profile: %w", err)
	}
	return nil
}

func (profileStore) updateProfile(ctx context.Context, tx db.Tx, profile CompanyProfile) error {
	values, err := profileStorageValuesFrom(profile)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
UPDATE identity.company_profile
SET trading_name = $1,
	legal_name = $2,
	company_number = $3,
	registered_office = $4::jsonb,
	incorporation_date = $5,
	year_end_month = $6,
	year_end_day = $7,
	vat_number = $8,
	bank_details = $9::jsonb,
	shareholders = $10::jsonb,
	logo_asset_id = $11,
	updated_at = now()
WHERE id = 1`,
		profile.TradingName,
		profile.LegalName,
		profile.CompanyNumber,
		string(values.office),
		profile.IncorporationDate,
		int(profile.YearEnd.Month),
		profile.YearEnd.Day,
		values.vatNumber,
		string(values.bankDetails),
		string(values.shareholders),
		values.logoAssetID,
	)
	if err != nil {
		return fmt.Errorf("update company profile: %w", err)
	}
	return nil
}

func (profileStore) createAsset(ctx context.Context, tx db.Tx, record assetRecord) error {
	id, err := assetUUID(record.ID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
INSERT INTO identity.assets (
	id,
	sha256,
	mime,
	size
) VALUES (
	$1,
	$2,
	$3,
	$4
)`,
		id,
		record.SHA256,
		record.MIME,
		record.Size,
	)
	if err != nil {
		return fmt.Errorf("create identity asset: %w", err)
	}
	return nil
}

func (profileStore) ensureAsset(ctx context.Context, tx db.Tx, record assetRecord) error {
	id, err := assetUUID(record.ID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
INSERT INTO identity.assets (
	id,
	sha256,
	mime,
	size
) VALUES (
	$1,
	$2,
	$3,
	$4
)
ON CONFLICT (id) DO NOTHING`,
		id,
		record.SHA256,
		record.MIME,
		record.Size,
	)
	if err != nil {
		return fmt.Errorf("ensure identity asset: %w", err)
	}
	return nil
}

func (profileStore) asset(ctx context.Context, tx db.Tx, id AssetID) (assetRecord, error) {
	assetID, err := assetUUID(id)
	if err != nil {
		return assetRecord{}, err
	}

	var (
		record assetRecord
		uuid   pgtype.UUID
	)
	err = tx.QueryRow(ctx, `
SELECT id,
	sha256,
	mime,
	size,
	created_at
FROM identity.assets
WHERE id = $1`, assetID).
		Scan(
			&uuid,
			&record.SHA256,
			&record.MIME,
			&record.Size,
			&record.CreatedAt,
		)
	if errors.Is(err, pgx.ErrNoRows) {
		return assetRecord{}, ErrAssetNotFound
	}
	if err != nil {
		return assetRecord{}, fmt.Errorf("select identity asset: %w", err)
	}
	record.ID = AssetID(uuid.String())
	return record, nil
}

func (profileStore) setProfileLogoAssetIDIfEmpty(ctx context.Context, tx db.Tx, id AssetID) error {
	assetID, err := assetUUID(id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
UPDATE identity.company_profile
SET logo_asset_id = $1,
	updated_at = now()
WHERE id = 1
	AND logo_asset_id IS NULL`, assetID)
	if err != nil {
		return fmt.Errorf("seed company profile logo: %w", err)
	}
	return nil
}

type profileStorageValues struct {
	office       []byte
	bankDetails  []byte
	shareholders []byte
	vatNumber    sql.NullString
	logoAssetID  pgtype.UUID
}

func profileStorageValuesFrom(profile CompanyProfile) (profileStorageValues, error) {
	office, err := json.Marshal(profile.RegisteredOffice)
	if err != nil {
		return profileStorageValues{}, fmt.Errorf("marshal registered office: %w", err)
	}
	bankDetails, err := json.Marshal(profile.BankDetails)
	if err != nil {
		return profileStorageValues{}, fmt.Errorf("marshal bank details: %w", err)
	}
	shareholdersValue := profile.Shareholders
	if shareholdersValue == nil {
		shareholdersValue = []Shareholder{}
	}
	shareholders, err := json.Marshal(shareholdersValue)
	if err != nil {
		return profileStorageValues{}, fmt.Errorf("marshal shareholders: %w", err)
	}
	logoAssetID, err := nullableUUID(profile.LogoAssetID)
	if err != nil {
		return profileStorageValues{}, err
	}
	return profileStorageValues{
		office:       office,
		bankDetails:  bankDetails,
		shareholders: shareholders,
		vatNumber:    nullableText(profile.VATNumber),
		logoAssetID:  logoAssetID,
	}, nil
}

func scanProfile(ctx context.Context, tx db.Tx, lockClause string) (CompanyProfile, error) {
	var (
		profile          CompanyProfile
		office           []byte
		bankDetails      []byte
		shareholders     []byte
		yearEndMonth     int
		vatNumber        sql.NullString
		logoAssetID      pgtype.UUID
		incorporationDay pgtype.Date
	)

	err := tx.QueryRow(ctx, `
SELECT trading_name,
	legal_name,
	company_number,
	registered_office,
	incorporation_date,
	year_end_month,
	year_end_day,
	vat_number,
	bank_details,
	shareholders,
	logo_asset_id
FROM identity.company_profile
WHERE id = 1`+lockClause).
		Scan(
			&profile.TradingName,
			&profile.LegalName,
			&profile.CompanyNumber,
			&office,
			&incorporationDay,
			&yearEndMonth,
			&profile.YearEnd.Day,
			&vatNumber,
			&bankDetails,
			&shareholders,
			&logoAssetID,
		)
	if errors.Is(err, pgx.ErrNoRows) {
		return CompanyProfile{}, ErrProfileNotFound
	}
	if err != nil {
		return CompanyProfile{}, fmt.Errorf("select company profile: %w", err)
	}

	if !incorporationDay.Valid {
		return CompanyProfile{}, fmt.Errorf("identity: company profile incorporation date is null")
	}
	profile.IncorporationDate = incorporationDay.Time
	profile.YearEnd.Month = time.Month(yearEndMonth)
	if vatNumber.Valid {
		profile.VATNumber = &vatNumber.String
	}
	if logoAssetID.Valid {
		logo := AssetID(logoAssetID.String())
		profile.LogoAssetID = &logo
	}

	if err := json.Unmarshal(office, &profile.RegisteredOffice); err != nil {
		return CompanyProfile{}, fmt.Errorf("unmarshal registered office: %w", err)
	}
	if err := json.Unmarshal(bankDetails, &profile.BankDetails); err != nil {
		return CompanyProfile{}, fmt.Errorf("unmarshal bank details: %w", err)
	}
	if err := json.Unmarshal(shareholders, &profile.Shareholders); err != nil {
		return CompanyProfile{}, fmt.Errorf("unmarshal shareholders: %w", err)
	}
	if err := profile.YearEnd.validate(); err != nil {
		return CompanyProfile{}, err
	}

	return profile, nil
}

func nullableUUID(id *AssetID) (pgtype.UUID, error) {
	if id == nil || *id == "" {
		return pgtype.UUID{}, nil
	}
	return assetUUID(*id)
}

func assetUUID(id AssetID) (pgtype.UUID, error) {
	trimmed := strings.TrimSpace(string(id))
	if trimmed == "" {
		return pgtype.UUID{}, fmt.Errorf("identity: asset id is required")
	}
	var uuid pgtype.UUID
	if err := uuid.Scan(trimmed); err != nil {
		return pgtype.UUID{}, fmt.Errorf("parse logo asset id: %w", err)
	}
	return uuid, nil
}

func nullableText(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}
