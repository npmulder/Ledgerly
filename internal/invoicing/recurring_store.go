package invoicing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

func (s Store) ListRecurringTemplates(ctx context.Context, tx db.Tx) ([]RecurringTemplate, error) {
	rows, err := tx.Query(ctx, selectRecurringTemplateSQL()+`
ORDER BY rt.status ASC, rt.next_run_date ASC, lower(c.name), rt.created_at DESC, rt.id`)
	if err != nil {
		return nil, fmt.Errorf("invoicing: list recurring templates: %w", err)
	}
	defer rows.Close()

	templates, err := pgx.CollectRows(rows, scanRecurringTemplate)
	if err != nil {
		return nil, fmt.Errorf("invoicing: collect recurring templates: %w", err)
	}
	if err := s.attachRecurringTemplateLines(ctx, tx, templates); err != nil {
		return nil, err
	}
	return templates, nil
}

func (s Store) RecurringTemplate(ctx context.Context, tx db.Tx, id string) (RecurringTemplate, error) {
	template, err := scanRecurringTemplateRow(tx.QueryRow(ctx, selectRecurringTemplateSQL()+`
WHERE rt.id = $1`, id))
	if err != nil {
		return RecurringTemplate{}, err
	}
	lines, err := s.RecurringTemplateLines(ctx, tx, id)
	if err != nil {
		return RecurringTemplate{}, err
	}
	template.Lines = lines
	return template, nil
}

func (s Store) InsertRecurringTemplate(ctx context.Context, tx db.Tx, template RecurringTemplate) (RecurringTemplate, error) {
	inserted, err := scanRecurringTemplateRow(tx.QueryRow(ctx, `
WITH inserted AS (
	INSERT INTO recurring_templates (
		id,
		client_id,
		status,
		cadence,
		day_of_month,
		next_run_date,
		currency,
		vat_treatment,
		auto_send,
		max_occurrences,
		occurrences_created,
		created_from_invoice_id,
		canceled_at
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
		$13
	)
	RETURNING *
)
`+recurringTemplateSelectSQL("inserted", "rt", "c"),
		template.ID,
		template.ClientID,
		string(template.Status),
		string(template.Cadence),
		template.DayOfMonth,
		template.NextRunDate,
		string(template.Currency),
		string(template.VATTreatment),
		template.AutoSend,
		nullableInt(template.MaxOccurrences),
		template.OccurrencesCreated,
		nullableString(template.CreatedFromInvoiceID),
		nullableTimestamp(template.CanceledAt),
	))
	if err != nil {
		return RecurringTemplate{}, err
	}
	if err := s.ReplaceRecurringTemplateLines(ctx, tx, inserted.ID, template.Lines); err != nil {
		return RecurringTemplate{}, err
	}
	inserted.Lines = template.Lines
	return inserted, nil
}

func (s Store) CancelRecurringTemplate(ctx context.Context, tx db.Tx, id string, canceledAt time.Time) (RecurringTemplate, error) {
	template, err := scanRecurringTemplateRow(tx.QueryRow(ctx, `
WITH updated AS (
	UPDATE recurring_templates
	SET status = 'canceled',
		canceled_at = $2,
		updated_at = now()
	WHERE id = $1
		AND status = 'active'
	RETURNING *
)
`+recurringTemplateSelectSQL("updated", "rt", "c"),
		id,
		canceledAt.UTC(),
	))
	if errors.Is(err, ErrRecurringTemplateNotFound) {
		exists, existsErr := s.recurringTemplateExists(ctx, tx, id)
		if existsErr != nil {
			return RecurringTemplate{}, existsErr
		}
		if exists {
			return RecurringTemplate{}, ErrRecurringTemplateImmutable
		}
	}
	if err != nil {
		return RecurringTemplate{}, err
	}
	lines, err := s.RecurringTemplateLines(ctx, tx, template.ID)
	if err != nil {
		return RecurringTemplate{}, err
	}
	template.Lines = lines
	return template, nil
}

func (s Store) DueRecurringTemplatesForUpdate(ctx context.Context, tx db.Tx, today time.Time, limit int) ([]RecurringTemplate, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := tx.Query(ctx, selectRecurringTemplateSQL()+`
WHERE rt.status = 'active'
	AND rt.next_run_date <= $1
	AND (rt.max_occurrences IS NULL OR rt.occurrences_created < rt.max_occurrences)
ORDER BY rt.next_run_date ASC, rt.id ASC
LIMIT $2
FOR UPDATE OF rt SKIP LOCKED`, dateOnly(today), limit)
	if err != nil {
		return nil, fmt.Errorf("invoicing: list due recurring templates: %w", err)
	}
	defer rows.Close()

	templates, err := pgx.CollectRows(rows, scanRecurringTemplate)
	if err != nil {
		return nil, fmt.Errorf("invoicing: collect due recurring templates: %w", err)
	}
	if err := s.attachRecurringTemplateLines(ctx, tx, templates); err != nil {
		return nil, err
	}
	return templates, nil
}

func (s Store) AdvanceRecurringTemplate(ctx context.Context, tx db.Tx, id string, nextRunDate time.Time, created int, advancedAt time.Time) (RecurringTemplate, error) {
	if created <= 0 {
		return s.RecurringTemplate(ctx, tx, id)
	}
	template, err := scanRecurringTemplateRow(tx.QueryRow(ctx, `
WITH updated AS (
	UPDATE recurring_templates
	SET occurrences_created = occurrences_created + $3,
		next_run_date = $2,
		status = CASE
			WHEN max_occurrences IS NOT NULL
				AND occurrences_created + $3 >= max_occurrences
				THEN 'canceled'
			ELSE status
		END,
		canceled_at = CASE
			WHEN max_occurrences IS NOT NULL
				AND occurrences_created + $3 >= max_occurrences
				THEN COALESCE(canceled_at, $4)
			ELSE canceled_at
		END,
		updated_at = now()
	WHERE id = $1
	RETURNING *
)
`+recurringTemplateSelectSQL("updated", "rt", "c"),
		id,
		dateOnly(nextRunDate),
		created,
		advancedAt.UTC(),
	))
	if err != nil {
		return RecurringTemplate{}, err
	}
	lines, err := s.RecurringTemplateLines(ctx, tx, template.ID)
	if err != nil {
		return RecurringTemplate{}, err
	}
	template.Lines = lines
	return template, nil
}

func (Store) ReplaceRecurringTemplateLines(ctx context.Context, tx db.Tx, templateID string, lines []RecurringTemplateLine) error {
	if _, err := tx.Exec(ctx, `DELETE FROM recurring_template_lines WHERE template_id = $1`, templateID); err != nil {
		return fmt.Errorf("invoicing: delete recurring template lines: %w", err)
	}
	for _, line := range lines {
		storageLineID := recurringTemplateLineStorageID(templateID, line.ID)
		if _, err := tx.Exec(ctx, `
INSERT INTO recurring_template_lines (
	id,
	template_id,
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
			templateID,
			line.Position,
			line.Description,
			string(line.Qty),
			line.UnitPrice.Amount,
			line.UnitPrice.Currency,
		); err != nil {
			return fmt.Errorf("invoicing: insert recurring template line: %w", err)
		}
	}
	return nil
}

func (Store) RecurringTemplateLines(ctx context.Context, tx db.Tx, templateID string) ([]RecurringTemplateLine, error) {
	rows, err := tx.Query(ctx, `
SELECT id,
	template_id,
	position,
	description,
	qty::text,
	unit_price_amount_minor,
	unit_price_currency
FROM recurring_template_lines
WHERE template_id = $1
ORDER BY position, id`, templateID)
	if err != nil {
		return nil, fmt.Errorf("invoicing: list recurring template lines: %w", err)
	}
	defer rows.Close()

	lines, err := pgx.CollectRows(rows, scanRecurringTemplateLine)
	if err != nil {
		return nil, fmt.Errorf("invoicing: collect recurring template lines: %w", err)
	}
	return lines, nil
}

func (s Store) PendingRecurringAutoSendDrafts(ctx context.Context, tx db.Tx) ([]string, error) {
	rows, err := tx.Query(ctx, `
SELECT i.id
FROM invoices i
JOIN recurring_templates rt ON rt.id = i.recurring_template_id
WHERE i.status = 'draft'
	AND rt.auto_send = true
ORDER BY i.issue_date ASC, i.created_at ASC, i.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("invoicing: list recurring auto-send drafts: %w", err)
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("invoicing: scan recurring auto-send draft: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("invoicing: collect recurring auto-send drafts: %w", err)
	}
	return ids, nil
}

func (s Store) RecurringDraftInvoiceFacts(ctx context.Context, tx db.Tx) ([]RecurringDraftInvoiceFact, error) {
	rows, err := tx.Query(ctx, `
SELECT i.id,
	i.client_id,
	c.name,
	i.recurring_run_date,
	COALESCE(SUM(ROUND(il.qty * il.unit_price_amount_minor)), 0)::bigint AS total_amount_minor,
	i.currency
FROM invoices i
JOIN clients c ON c.id = i.client_id
JOIN recurring_templates rt ON rt.id = i.recurring_template_id
LEFT JOIN invoice_lines il ON il.invoice_id = i.id
WHERE i.status = 'draft'
	AND i.recurring_template_id IS NOT NULL
	AND i.recurring_run_date IS NOT NULL
GROUP BY i.id, i.client_id, c.name, i.recurring_run_date, i.currency
ORDER BY i.recurring_run_date ASC, i.created_at ASC, i.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("invoicing: list recurring draft invoice facts: %w", err)
	}
	defer rows.Close()

	facts := []RecurringDraftInvoiceFact{}
	for rows.Next() {
		var (
			fact     RecurringDraftInvoiceFact
			runDate  time.Time
			currency string
		)
		if err := rows.Scan(&fact.InvoiceID, &fact.ClientID, &fact.ClientName, &runDate, &fact.Amount.Amount, &currency); err != nil {
			return nil, fmt.Errorf("invoicing: scan recurring draft invoice fact: %w", err)
		}
		fact.RunDate = dateOnly(runDate)
		fact.Amount.Currency = currency
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("invoicing: collect recurring draft invoice facts: %w", err)
	}
	return facts, nil
}

func (s Store) attachRecurringTemplateLines(ctx context.Context, tx db.Tx, templates []RecurringTemplate) error {
	if len(templates) == 0 {
		return nil
	}
	ids := make([]string, 0, len(templates))
	for _, template := range templates {
		ids = append(ids, template.ID)
	}
	linesByTemplate, err := s.RecurringTemplateLinesForTemplates(ctx, tx, ids)
	if err != nil {
		return err
	}
	for i := range templates {
		templates[i].Lines = linesByTemplate[templates[i].ID]
	}
	return nil
}

func (Store) RecurringTemplateLinesForTemplates(ctx context.Context, tx db.Tx, templateIDs []string) (map[string][]RecurringTemplateLine, error) {
	out := make(map[string][]RecurringTemplateLine, len(templateIDs))
	if len(templateIDs) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `
SELECT id,
	template_id,
	position,
	description,
	qty::text,
	unit_price_amount_minor,
	unit_price_currency
FROM recurring_template_lines
WHERE template_id = ANY($1)
ORDER BY template_id, position, id`, templateIDs)
	if err != nil {
		return nil, fmt.Errorf("invoicing: list recurring template lines for templates: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		line, err := scanRecurringTemplateLine(rows)
		if err != nil {
			return nil, err
		}
		out[line.TemplateID] = append(out[line.TemplateID], line)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("invoicing: collect recurring template lines for templates: %w", err)
	}
	return out, nil
}

func (s Store) recurringTemplateExists(ctx context.Context, tx db.Tx, id string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM recurring_templates WHERE id = $1)`, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("invoicing: check recurring template exists: %w", err)
	}
	return exists, nil
}

func selectRecurringTemplateSQL() string {
	return recurringTemplateSelectSQL("recurring_templates", "rt", "c")
}

func recurringTemplateSelectSQL(source string, templateAlias string, clientAlias string) string {
	return `SELECT ` + templateAlias + `.id,
	` + templateAlias + `.client_id,
	` + clientAlias + `.name,
	` + templateAlias + `.status,
	` + templateAlias + `.cadence,
	` + templateAlias + `.day_of_month,
	` + templateAlias + `.next_run_date,
	` + templateAlias + `.currency,
	` + templateAlias + `.vat_treatment,
	` + templateAlias + `.auto_send,
	` + templateAlias + `.max_occurrences,
	` + templateAlias + `.occurrences_created,
	` + templateAlias + `.created_from_invoice_id,
	` + templateAlias + `.canceled_at,
	` + templateAlias + `.created_at,
	` + templateAlias + `.updated_at
FROM ` + source + ` ` + templateAlias + `
JOIN clients ` + clientAlias + ` ON ` + clientAlias + `.id = ` + templateAlias + `.client_id`
}

func scanRecurringTemplate(row pgx.CollectableRow) (RecurringTemplate, error) {
	return scanRecurringTemplateRow(row)
}

func scanRecurringTemplateRow(row clientRow) (RecurringTemplate, error) {
	var (
		template             RecurringTemplate
		status               string
		cadence              string
		currency             string
		vatTreatment         string
		maxOccurrences       sql.NullInt64
		createdFromInvoiceID sql.NullString
		canceledAt           sql.NullTime
	)
	err := row.Scan(
		&template.ID,
		&template.ClientID,
		&template.ClientName,
		&status,
		&cadence,
		&template.DayOfMonth,
		&template.NextRunDate,
		&currency,
		&vatTreatment,
		&template.AutoSend,
		&maxOccurrences,
		&template.OccurrencesCreated,
		&createdFromInvoiceID,
		&canceledAt,
		&template.CreatedAt,
		&template.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return RecurringTemplate{}, ErrRecurringTemplateNotFound
	}
	if err != nil {
		return RecurringTemplate{}, fmt.Errorf("invoicing: scan recurring template: %w", err)
	}
	template.Status = RecurringTemplateStatus(status)
	template.Cadence = RecurringCadence(cadence)
	template.NextRunDate = dateOnly(template.NextRunDate)
	template.Currency = Currency(currency)
	template.VATTreatment = VATTreatment(vatTreatment)
	if maxOccurrences.Valid {
		value := int(maxOccurrences.Int64)
		template.MaxOccurrences = &value
	}
	if createdFromInvoiceID.Valid {
		template.CreatedFromInvoiceID = &createdFromInvoiceID.String
	}
	if canceledAt.Valid {
		value := canceledAt.Time.UTC()
		template.CanceledAt = &value
	}
	template.CreatedAt = template.CreatedAt.UTC()
	template.UpdatedAt = template.UpdatedAt.UTC()
	return template, nil
}

func scanRecurringTemplateLine(row pgx.CollectableRow) (RecurringTemplateLine, error) {
	var (
		line     RecurringTemplateLine
		qty      string
		currency string
	)
	if err := row.Scan(
		&line.ID,
		&line.TemplateID,
		&line.Position,
		&line.Description,
		&qty,
		&line.UnitPrice.Amount,
		&currency,
	); err != nil {
		return RecurringTemplateLine{}, fmt.Errorf("invoicing: scan recurring template line: %w", err)
	}
	line.Qty = Quantity(qty)
	line.UnitPrice.Currency = currency
	line.ID = recurringTemplateLineClientID(line.TemplateID, line.ID)
	return line, nil
}

func recurringTemplateLineStorageID(templateID string, clientLineID string) string {
	prefix := strings.TrimSpace(templateID)
	lineID := strings.TrimSpace(clientLineID)
	if prefix == "" {
		return lineID
	}
	return prefix + ":" + lineID
}

func recurringTemplateLineClientID(templateID string, storageLineID string) string {
	prefix := strings.TrimSpace(templateID) + ":"
	return strings.TrimPrefix(storageLineID, prefix)
}

func nullableInt(value *int) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*value), Valid: true}
}

func nullableTimestamp(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: value.UTC(), Valid: true}
}
