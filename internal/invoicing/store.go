package invoicing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

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

func (s Store) ClientHasInvoices(ctx context.Context, tx db.Tx, clientID string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
	SELECT 1
	FROM invoices
	WHERE client_id = $1
)`, clientID).Scan(&exists); err != nil {
		return false, fmt.Errorf("invoicing: check client invoices: %w", err)
	}
	return exists, nil
}

func (s Store) Invoice(ctx context.Context, tx db.Tx, id string) (Invoice, error) {
	invoice, err := scanInvoiceRow(tx.QueryRow(ctx, selectInvoiceSQL()+"\nWHERE id = $1", id))
	if err != nil {
		return Invoice{}, err
	}
	lines, err := s.InvoiceLines(ctx, tx, id)
	if err != nil {
		return Invoice{}, err
	}
	invoice.Lines = lines
	return invoice, nil
}

func (Store) InvoiceVATContextBySendEntryID(ctx context.Context, tx db.Tx, entryID int64) (InvoiceVATContext, error) {
	var context InvoiceVATContext
	err := tx.QueryRow(ctx, `
SELECT invoice_id, vat_treatment
FROM invoicing.invoice_send_vat_context
WHERE send_ledger_entry_id = $1`, entryID).Scan(
		&context.InvoiceID,
		&context.VATTreatment,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return InvoiceVATContext{}, ErrInvoiceNotFound
	}
	if err != nil {
		return InvoiceVATContext{}, fmt.Errorf("invoicing: invoice VAT context for send entry %d: %w", entryID, err)
	}
	return context, nil
}

func (s Store) InvoiceByNumber(ctx context.Context, tx db.Tx, number string) (Invoice, error) {
	invoice, err := scanInvoiceRow(tx.QueryRow(ctx, selectInvoiceSQL()+"\nWHERE number = $1", number))
	if err != nil {
		return Invoice{}, err
	}
	lines, err := s.InvoiceLines(ctx, tx, invoice.ID)
	if err != nil {
		return Invoice{}, err
	}
	invoice.Lines = lines
	return invoice, nil
}

func (s Store) InvoiceForUpdate(ctx context.Context, tx db.Tx, id string) (Invoice, error) {
	invoice, err := scanInvoiceRow(tx.QueryRow(ctx, selectInvoiceSQL()+"\nWHERE id = $1\nFOR UPDATE", id))
	if err != nil {
		return Invoice{}, err
	}
	lines, err := s.InvoiceLines(ctx, tx, id)
	if err != nil {
		return Invoice{}, err
	}
	invoice.Lines = lines
	return invoice, nil
}

func (Store) InvoiceLines(ctx context.Context, tx db.Tx, invoiceID string) ([]InvoiceLine, error) {
	rows, err := tx.Query(ctx, `
SELECT id,
	invoice_id,
	position,
	description,
	qty::text,
	unit_price_amount_minor,
	unit_price_currency
FROM invoicing.invoice_lines
WHERE invoice_id = $1
ORDER BY position, id`, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("invoicing: list invoice lines: %w", err)
	}
	defer rows.Close()

	lines, err := pgx.CollectRows(rows, scanInvoiceLine)
	if err != nil {
		return nil, fmt.Errorf("invoicing: collect invoice lines: %w", err)
	}
	return lines, nil
}

func (Store) InsertDraftInvoice(ctx context.Context, tx db.Tx, invoice Invoice) (Invoice, error) {
	return scanInvoiceRow(tx.QueryRow(ctx, `
INSERT INTO invoices (
	id,
	number,
	client_id,
	status,
	issue_date,
	due_date,
	currency,
	lock_id,
	vat_treatment,
	settlement_txn_ref,
	settled_date,
	settled_amount_minor,
	settled_amount_currency,
	pdf_asset
) VALUES (
	$1,
	$2,
	$3,
	$4,
	$5,
	$6,
	$7,
	$8,
	$9,
	$10,
	$11,
	$12,
	$13,
	$14
)
RETURNING `+invoiceColumnsSQL(),
		invoice.ID,
		nullableString(invoice.Number),
		invoice.ClientID,
		string(invoice.Status),
		invoice.IssueDate,
		invoice.DueDate,
		string(invoice.Currency),
		nullableString(invoice.LockID),
		string(invoice.VATTreatment),
		nullableString(invoice.SettlementTxnRef),
		nullableTime(invoice.SettledDate),
		nullableInvoiceMoneyAmount(invoice.SettledAmount),
		nullableInvoiceMoneyCurrency(invoice.SettledAmount),
		nullableString(invoice.PDFAsset),
	))
}

func (s Store) UpdateDraftInvoice(ctx context.Context, tx db.Tx, invoice Invoice) (Invoice, error) {
	updated, err := scanInvoiceRow(tx.QueryRow(ctx, `
UPDATE invoices
SET client_id = $2,
	issue_date = $3,
	due_date = $4,
	currency = $5,
	vat_treatment = $6,
	pdf_asset = $7,
	updated_at = now()
WHERE id = $1
	AND status = 'draft'
RETURNING `+invoiceColumnsSQL(),
		invoice.ID,
		invoice.ClientID,
		invoice.IssueDate,
		invoice.DueDate,
		string(invoice.Currency),
		string(invoice.VATTreatment),
		nullableString(invoice.PDFAsset),
	))
	if errors.Is(err, ErrInvoiceNotFound) {
		exists, existsErr := s.invoiceExists(ctx, tx, invoice.ID)
		if existsErr != nil {
			return Invoice{}, existsErr
		}
		if exists {
			return Invoice{}, ErrInvoiceImmutable
		}
	}
	return updated, err
}

func (Store) ReplaceInvoiceLines(ctx context.Context, tx db.Tx, invoiceID string, lines []InvoiceLine) error {
	if _, err := tx.Exec(ctx, `DELETE FROM invoice_lines WHERE invoice_id = $1`, invoiceID); err != nil {
		return fmt.Errorf("invoicing: delete invoice lines: %w", err)
	}
	for _, line := range lines {
		storageLineID := invoiceLineStorageID(invoiceID, line.ID)
		if _, err := tx.Exec(ctx, `
INSERT INTO invoice_lines (
	id,
	invoice_id,
	position,
	description,
	qty,
	unit_price_amount_minor,
	unit_price_currency
) VALUES (
	$1,
	$2,
	$3,
	$4,
	$5::numeric,
	$6,
	$7
)`,
			storageLineID,
			invoiceID,
			line.Position,
			line.Description,
			string(line.Qty),
			line.UnitPrice.Amount,
			line.UnitPrice.Currency,
		); err != nil {
			return fmt.Errorf("invoicing: insert invoice line: %w", err)
		}
	}
	return nil
}

func (s Store) DeleteDraft(ctx context.Context, tx db.Tx, id string) error {
	tag, err := tx.Exec(ctx, `
DELETE FROM invoices
WHERE id = $1
	AND status = 'draft'`, id)
	if err != nil {
		return fmt.Errorf("invoicing: delete draft invoice: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	exists, err := s.invoiceExists(ctx, tx, id)
	if err != nil {
		return err
	}
	if exists {
		return ErrInvoiceImmutable
	}
	return ErrInvoiceNotFound
}

func (s Store) SetInvoiceSent(ctx context.Context, tx db.Tx, id string, number string, lockID int64, sendLedgerEntryID int64, sentAt time.Time) (Invoice, error) {
	updated, err := scanInvoiceRow(tx.QueryRow(ctx, `
UPDATE invoicing.invoices
SET number = $2,
	lock_id = $3,
	send_ledger_entry_id = $4,
	sent_at = $5,
	status = 'sent',
	updated_at = now()
WHERE id = $1
	AND status = 'draft'
RETURNING `+invoiceColumnsSQL(),
		id,
		number,
		strconv.FormatInt(lockID, 10),
		sendLedgerEntryID,
		sentAt.UTC(),
	))
	if errors.Is(err, ErrInvoiceNotFound) {
		exists, existsErr := s.invoiceExists(ctx, tx, id)
		if existsErr != nil {
			return Invoice{}, existsErr
		}
		if exists {
			return Invoice{}, ErrInvoiceImmutable
		}
	}
	if err != nil {
		return updated, err
	}
	if err := s.InsertInvoiceSendVATContext(ctx, tx, updated.ID, sendLedgerEntryID, updated.VATTreatment); err != nil {
		return Invoice{}, err
	}
	return updated, nil
}

func (Store) InsertInvoiceSendVATContext(ctx context.Context, tx db.Tx, invoiceID string, sendLedgerEntryID int64, treatment VATTreatment) error {
	_, err := tx.Exec(ctx, `
INSERT INTO invoicing.invoice_send_vat_context (
	send_ledger_entry_id,
	invoice_id,
	vat_treatment
) VALUES (
	$1,
	$2,
	$3
)`,
		sendLedgerEntryID,
		invoiceID,
		string(treatment),
	)
	if err != nil {
		return fmt.Errorf("invoicing: insert invoice send VAT context: %w", err)
	}
	return nil
}

func (s Store) SetInvoiceSettlement(ctx context.Context, tx db.Tx, id string, settlement InvoiceSettlement) (Invoice, error) {
	amount, currency := nullableInvoiceMoney(settlement.SettledAmount)
	updated, err := scanInvoiceRow(tx.QueryRow(ctx, `
UPDATE invoicing.invoices
SET settlement_txn_ref = $2,
	settled_date = $3,
	settled_amount_minor = $4,
	settled_amount_currency = $5,
	status = 'paid',
	updated_at = now()
WHERE id = $1
	AND status = 'sent'
RETURNING `+invoiceColumnsSQL(),
		id,
		nullableString(settlement.TxnRef),
		nullableTime(settlement.SettledDate),
		amount,
		currency,
	))
	if errors.Is(err, ErrInvoiceNotFound) {
		exists, existsErr := s.invoiceExists(ctx, tx, id)
		if existsErr != nil {
			return Invoice{}, existsErr
		}
		if exists {
			return Invoice{}, ErrInvoiceImmutable
		}
	}
	return updated, err
}

func (s Store) RevertSentToDraft(ctx context.Context, tx db.Tx, id string) (Invoice, error) {
	updated, err := scanInvoiceRow(tx.QueryRow(ctx, `
UPDATE invoicing.invoices
SET number = NULL,
	lock_id = NULL,
	send_ledger_entry_id = NULL,
	sent_at = NULL,
	status = 'draft',
	updated_at = now()
WHERE id = $1
	AND status = 'sent'
	AND settlement_txn_ref IS NULL
	AND settled_date IS NULL
	AND settled_amount_minor IS NULL
RETURNING `+invoiceColumnsSQL(), id))
	if errors.Is(err, ErrInvoiceNotFound) {
		exists, existsErr := s.invoiceExists(ctx, tx, id)
		if existsErr != nil {
			return Invoice{}, existsErr
		}
		if exists {
			return Invoice{}, ErrInvoiceImmutable
		}
	}
	return updated, err
}

func (Store) NextNumber(ctx context.Context, tx db.Tx, year int) (string, error) {
	if year < 1 || year > 9999 {
		return "", fmt.Errorf("invoicing: invoice number year %d out of range", year)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO invoice_numbering (year, last_seq)
VALUES ($1, 0)
ON CONFLICT (year) DO NOTHING`, year); err != nil {
		return "", fmt.Errorf("invoicing: initialize invoice numbering: %w", err)
	}

	var lastSeq int
	if err := tx.QueryRow(ctx, `
SELECT last_seq
FROM invoice_numbering
WHERE year = $1
FOR UPDATE`, year).Scan(&lastSeq); err != nil {
		return "", fmt.Errorf("invoicing: lock invoice numbering: %w", err)
	}
	nextSeq := lastSeq + 1
	tag, err := tx.Exec(ctx, `
UPDATE invoice_numbering
SET last_seq = $2
WHERE year = $1`, year, nextSeq)
	if err != nil {
		return "", fmt.Errorf("invoicing: advance invoice numbering: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", fmt.Errorf("invoicing: invoice numbering row disappeared for %d", year)
	}
	return fmt.Sprintf("INV-%04d-%02d", year, nextSeq), nil
}

func (Store) invoiceExists(ctx context.Context, tx db.Tx, id string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM invoicing.invoices WHERE id = $1)`, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("invoicing: check invoice exists: %w", err)
	}
	return exists, nil
}

func selectInvoiceSQL() string {
	return "SELECT " + invoiceColumnsSQL() + "\nFROM invoicing.invoices"
}

func invoiceColumnsSQL() string {
	return `id,
	number,
	client_id,
	status,
	issue_date,
	due_date,
	currency,
	lock_id,
	send_ledger_entry_id,
	sent_at,
	vat_treatment,
	settlement_txn_ref,
	settled_date,
	settled_amount_minor,
	settled_amount_currency,
	pdf_asset,
	created_at,
	updated_at`
}

func scanInvoiceRow(row clientRow) (Invoice, error) {
	var (
		invoice          Invoice
		number           sql.NullString
		status           string
		currency         string
		lockID           sql.NullString
		sendEntryID      sql.NullInt64
		sentAt           sql.NullTime
		vatTreatment     string
		settlementTxnRef sql.NullString
		settledDate      sql.NullTime
		settledAmount    sql.NullInt64
		settledCurrency  sql.NullString
		pdfAsset         sql.NullString
	)
	err := row.Scan(
		&invoice.ID,
		&number,
		&invoice.ClientID,
		&status,
		&invoice.IssueDate,
		&invoice.DueDate,
		&currency,
		&lockID,
		&sendEntryID,
		&sentAt,
		&vatTreatment,
		&settlementTxnRef,
		&settledDate,
		&settledAmount,
		&settledCurrency,
		&pdfAsset,
		&invoice.CreatedAt,
		&invoice.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invoice{}, ErrInvoiceNotFound
	}
	if err != nil {
		return Invoice{}, fmt.Errorf("invoicing: scan invoice: %w", err)
	}
	if number.Valid {
		invoice.Number = &number.String
	}
	invoice.Status = InvoiceStatus(status)
	invoice.Currency = Currency(currency)
	if lockID.Valid {
		invoice.LockID = &lockID.String
	}
	if sendEntryID.Valid {
		invoice.SendLedgerEntryID = &sendEntryID.Int64
	}
	if sentAt.Valid {
		value := sentAt.Time.UTC()
		invoice.SentAt = &value
	}
	invoice.VATTreatment = VATTreatment(vatTreatment)
	if settlementTxnRef.Valid {
		invoice.SettlementTxnRef = &settlementTxnRef.String
	}
	if settledDate.Valid {
		settled := dateOnly(settledDate.Time)
		invoice.SettledDate = &settled
	}
	invoice.SettledAmount = invoiceMoneyFromNullable(settledAmount, settledCurrency)
	if pdfAsset.Valid {
		invoice.PDFAsset = &pdfAsset.String
	}
	invoice.IssueDate = dateOnly(invoice.IssueDate)
	invoice.DueDate = dateOnly(invoice.DueDate)
	invoice.CreatedAt = invoice.CreatedAt.UTC()
	invoice.UpdatedAt = invoice.UpdatedAt.UTC()
	return invoice, nil
}

func scanInvoiceLine(row pgx.CollectableRow) (InvoiceLine, error) {
	var (
		line     InvoiceLine
		qty      string
		currency string
	)
	if err := row.Scan(
		&line.ID,
		&line.InvoiceID,
		&line.Position,
		&line.Description,
		&qty,
		&line.UnitPrice.Amount,
		&currency,
	); err != nil {
		return InvoiceLine{}, fmt.Errorf("invoicing: scan invoice line: %w", err)
	}
	line.Qty = Quantity(qty)
	line.UnitPrice.Currency = currency
	line.ID = invoiceLineClientID(line.InvoiceID, line.ID)
	return line, nil
}

func invoiceLineStorageID(invoiceID string, clientLineID string) string {
	prefix := strings.TrimSpace(invoiceID)
	lineID := strings.TrimSpace(clientLineID)
	if prefix == "" {
		return lineID
	}
	return prefix + ":" + lineID
}

func invoiceLineClientID(invoiceID string, storageLineID string) string {
	prefix := strings.TrimSpace(invoiceID) + ":"
	return strings.TrimPrefix(storageLineID, prefix)
}

func nullableTime(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: dateOnly(*value), Valid: true}
}

func nullableInvoiceMoney(amount *Money) (sql.NullInt64, sql.NullString) {
	if amount == nil {
		return sql.NullInt64{}, sql.NullString{}
	}
	return sql.NullInt64{Int64: amount.Amount, Valid: true},
		sql.NullString{String: amount.Currency, Valid: true}
}

func nullableInvoiceMoneyAmount(amount *Money) sql.NullInt64 {
	value, _ := nullableInvoiceMoney(amount)
	return value
}

func nullableInvoiceMoneyCurrency(amount *Money) sql.NullString {
	_, value := nullableInvoiceMoney(amount)
	return value
}

func invoiceMoneyFromNullable(amount sql.NullInt64, currency sql.NullString) *Money {
	if !amount.Valid || !currency.Valid {
		return nil
	}
	return &Money{
		Amount:   amount.Int64,
		Currency: currency.String,
	}
}

func dateOnly(value time.Time) time.Time {
	year, month, day := value.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
