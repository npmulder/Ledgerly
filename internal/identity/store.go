package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

type profileStore struct{}

func (profileStore) profile(ctx context.Context, tx db.Tx) (CompanyProfile, error) {
	return scanProfile(ctx, tx, "")
}

func (profileStore) profileForUpdate(ctx context.Context, tx db.Tx) (CompanyProfile, error) {
	return scanProfile(ctx, tx, " FOR UPDATE")
}

func (profileStore) updateProfile(ctx context.Context, tx db.Tx, profile CompanyProfile) error {
	office, err := json.Marshal(profile.RegisteredOffice)
	if err != nil {
		return fmt.Errorf("marshal registered office: %w", err)
	}
	bankDetails, err := json.Marshal(profile.BankDetails)
	if err != nil {
		return fmt.Errorf("marshal bank details: %w", err)
	}
	shareholders, err := json.Marshal(profile.Shareholders)
	if err != nil {
		return fmt.Errorf("marshal shareholders: %w", err)
	}
	vatNumber := nullableText(profile.VATNumber)
	logoAssetID, err := nullableUUID(profile.LogoAssetID)
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
		string(office),
		profile.IncorporationDate,
		int(profile.YearEnd.Month),
		profile.YearEnd.Day,
		vatNumber,
		string(bankDetails),
		string(shareholders),
		logoAssetID,
	)
	if err != nil {
		return fmt.Errorf("update company profile: %w", err)
	}
	return nil
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
	var uuid pgtype.UUID
	if err := uuid.Scan(string(*id)); err != nil {
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
