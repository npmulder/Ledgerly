package invoicing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

// Store owns invoicing persistence. All SQL relies on the module search_path.
type Store struct{}

func (Store) ListClients(ctx context.Context, tx db.Tx, includeArchived bool) ([]Client, error) {
	query := `
SELECT id,
	name,
	address,
	vat_number,
	default_currency,
	terms_days,
	vat_treatment,
	retainer_amount_minor,
	retainer_currency,
	day_rate_amount_minor,
	day_rate_currency,
	created_at,
	archived_at
FROM clients`
	if !includeArchived {
		query += "\nWHERE archived_at IS NULL"
	}
	query += "\nORDER BY lower(name), created_at, id"

	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("invoicing: list clients: %w", err)
	}
	defer rows.Close()

	clients, err := pgx.CollectRows(rows, scanClient)
	if err != nil {
		return nil, fmt.Errorf("invoicing: collect clients: %w", err)
	}
	return clients, nil
}

func (Store) Client(ctx context.Context, tx db.Tx, id string) (Client, error) {
	return scanClientRow(tx.QueryRow(ctx, selectClientSQL()+"\nWHERE id = $1", id))
}

func (Store) ClientForUpdate(ctx context.Context, tx db.Tx, id string) (Client, error) {
	return scanClientRow(tx.QueryRow(ctx, selectClientSQL()+"\nWHERE id = $1\nFOR UPDATE", id))
}

func (Store) InsertClient(ctx context.Context, tx db.Tx, client Client) (Client, error) {
	values, err := storageValuesFromClient(client)
	if err != nil {
		return Client{}, err
	}

	return scanClientRow(tx.QueryRow(ctx, `
INSERT INTO clients (
	id,
	name,
	address,
	vat_number,
	default_currency,
	terms_days,
	vat_treatment,
	retainer_amount_minor,
	retainer_currency,
	day_rate_amount_minor,
	day_rate_currency
) VALUES (
	$1,
	$2,
	$3::jsonb,
	$4,
	$5,
	$6,
	$7,
	$8,
	$9,
	$10,
	$11
)
RETURNING id,
	name,
	address,
	vat_number,
	default_currency,
	terms_days,
	vat_treatment,
	retainer_amount_minor,
	retainer_currency,
	day_rate_amount_minor,
	day_rate_currency,
	created_at,
	archived_at`,
		client.ID,
		client.Name,
		string(values.address),
		nullableString(client.VATNumber),
		string(client.DefaultCurrency),
		client.TermsDays,
		string(client.VATTreatment),
		values.retainerAmount,
		values.retainerCurrency,
		values.dayRateAmount,
		values.dayRateCurrency,
	))
}

func (Store) UpdateClient(ctx context.Context, tx db.Tx, client Client) (Client, error) {
	values, err := storageValuesFromClient(client)
	if err != nil {
		return Client{}, err
	}

	return scanClientRow(tx.QueryRow(ctx, `
UPDATE clients
SET name = $2,
	address = $3::jsonb,
	vat_number = $4,
	default_currency = $5,
	terms_days = $6,
	vat_treatment = $7,
	retainer_amount_minor = $8,
	retainer_currency = $9,
	day_rate_amount_minor = $10,
	day_rate_currency = $11
WHERE id = $1
RETURNING id,
	name,
	address,
	vat_number,
	default_currency,
	terms_days,
	vat_treatment,
	retainer_amount_minor,
	retainer_currency,
	day_rate_amount_minor,
	day_rate_currency,
	created_at,
	archived_at`,
		client.ID,
		client.Name,
		string(values.address),
		nullableString(client.VATNumber),
		string(client.DefaultCurrency),
		client.TermsDays,
		string(client.VATTreatment),
		values.retainerAmount,
		values.retainerCurrency,
		values.dayRateAmount,
		values.dayRateCurrency,
	))
}

func (Store) ArchiveClient(ctx context.Context, tx db.Tx, id string) error {
	tag, err := tx.Exec(ctx, `
UPDATE clients
SET archived_at = COALESCE(archived_at, now())
WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("invoicing: archive client: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrClientNotFound
	}
	return nil
}

func selectClientSQL() string {
	return `SELECT id,
	name,
	address,
	vat_number,
	default_currency,
	terms_days,
	vat_treatment,
	retainer_amount_minor,
	retainer_currency,
	day_rate_amount_minor,
	day_rate_currency,
	created_at,
	archived_at
FROM clients`
}

type clientStorageValues struct {
	address          []byte
	retainerAmount   sql.NullInt64
	retainerCurrency sql.NullString
	dayRateAmount    sql.NullInt64
	dayRateCurrency  sql.NullString
}

func storageValuesFromClient(client Client) (clientStorageValues, error) {
	address, err := json.Marshal(client.Address)
	if err != nil {
		return clientStorageValues{}, fmt.Errorf("invoicing: marshal client address: %w", err)
	}
	retainerAmount, retainerCurrency := nullableMoney(client.RetainerAmount)
	dayRateAmount, dayRateCurrency := nullableMoney(client.DayRate)
	return clientStorageValues{
		address:          address,
		retainerAmount:   retainerAmount,
		retainerCurrency: retainerCurrency,
		dayRateAmount:    dayRateAmount,
		dayRateCurrency:  dayRateCurrency,
	}, nil
}

func nullableMoney(amount *MoneyAmount) (sql.NullInt64, sql.NullString) {
	if amount == nil {
		return sql.NullInt64{}, sql.NullString{}
	}
	return sql.NullInt64{Int64: amount.AmountMinor, Valid: true},
		sql.NullString{String: string(amount.Currency), Valid: true}
}

func nullableString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func scanClient(row pgx.CollectableRow) (Client, error) {
	return scanClientRow(row)
}

type clientRow interface {
	Scan(dest ...any) error
}

func scanClientRow(row clientRow) (Client, error) {
	var (
		client           Client
		address          []byte
		vatNumber        sql.NullString
		defaultCurrency  string
		vatTreatment     string
		retainerAmount   sql.NullInt64
		retainerCurrency sql.NullString
		dayRateAmount    sql.NullInt64
		dayRateCurrency  sql.NullString
		archivedAt       sql.NullTime
	)
	err := row.Scan(
		&client.ID,
		&client.Name,
		&address,
		&vatNumber,
		&defaultCurrency,
		&client.TermsDays,
		&vatTreatment,
		&retainerAmount,
		&retainerCurrency,
		&dayRateAmount,
		&dayRateCurrency,
		&client.CreatedAt,
		&archivedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Client{}, ErrClientNotFound
	}
	if err != nil {
		return Client{}, fmt.Errorf("invoicing: scan client: %w", err)
	}
	if err := json.Unmarshal(address, &client.Address); err != nil {
		return Client{}, fmt.Errorf("invoicing: unmarshal client address: %w", err)
	}
	if vatNumber.Valid {
		client.VATNumber = &vatNumber.String
	}
	client.DefaultCurrency = Currency(defaultCurrency)
	client.VATTreatment = VATTreatment(vatTreatment)
	client.RetainerAmount = moneyFromNullable(retainerAmount, retainerCurrency)
	client.DayRate = moneyFromNullable(dayRateAmount, dayRateCurrency)
	client.CreatedAt = client.CreatedAt.UTC()
	if archivedAt.Valid {
		archived := archivedAt.Time.UTC()
		client.ArchivedAt = &archived
	}
	return client, nil
}

func moneyFromNullable(amount sql.NullInt64, currency sql.NullString) *MoneyAmount {
	if !amount.Valid || !currency.Valid {
		return nil
	}
	return &MoneyAmount{
		AmountMinor: amount.Int64,
		Currency:    Currency(currency.String),
	}
}
