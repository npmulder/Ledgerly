package invoicing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

const (
	// OverdueSweepJobName is the deterministic app job and cron registration
	// name for publishing newly overdue invoice facts.
	OverdueSweepJobName = "invoicing.overdue-sweep"

	// OverdueSweepSchedule runs once per day after other overnight consistency
	// jobs have had a chance to complete.
	OverdueSweepSchedule = "20 2 * * *"
)

type normalizedInvoiceListFilter struct {
	statuses []InvoiceStatus
	search   string
	limit    int
	offset   int
}

type invoiceListRecord struct {
	invoice     Invoice
	clientName  string
	status      InvoiceStatus
	daysOverdue int
}

type overdueSweepClaim struct {
	invoiceID   string
	daysOverdue int
}

// List returns a paginated invoice list with virtual overdue status and status
// counts for the same search filter.
func (s *Service) List(ctx context.Context, filter InvoiceListFilter) (InvoiceListResult, error) {
	normalized, err := normalizeInvoiceListFilter(filter, true)
	if err != nil {
		return InvoiceListResult{}, err
	}
	today := dateOnly(s.now())

	records, err := s.store.ListInvoiceRows(ctx, s.pool, normalized, today, true)
	if err != nil {
		return InvoiceListResult{}, err
	}
	items, err := s.invoiceListItems(ctx, records)
	if err != nil {
		return InvoiceListResult{}, err
	}
	counts, err := s.store.InvoiceStatusCounts(ctx, s.pool, normalized, today)
	if err != nil {
		return InvoiceListResult{}, err
	}
	totalCount, err := s.store.InvoiceListCount(ctx, s.pool, normalized, today)
	if err != nil {
		return InvoiceListResult{}, err
	}
	return InvoiceListResult{
		Invoices:   items,
		Counts:     invoiceStatusCountList(counts),
		TotalCount: totalCount,
	}, nil
}

// Totals returns footer totals for the filtered invoice set. Pagination fields
// are deliberately ignored because the footer describes the whole filter.
func (s *Service) Totals(ctx context.Context, filter InvoiceListFilter) (InvoiceTotalsSummary, error) {
	normalized, err := normalizeInvoiceListFilter(filter, false)
	if err != nil {
		return InvoiceTotalsSummary{}, err
	}
	records, err := s.store.ListInvoiceRows(ctx, s.pool, normalized, dateOnly(s.now()), false)
	if err != nil {
		return InvoiceTotalsSummary{}, err
	}
	items, err := s.invoiceListItems(ctx, records)
	if err != nil {
		return InvoiceTotalsSummary{}, err
	}

	lockIDs := make(map[string]*string, len(records))
	for _, record := range records {
		lockIDs[record.invoice.ID] = record.invoice.LockID
	}
	native := make(map[string]Money)
	totalGBP := Money{Currency: string(CurrencyGBP)}
	for _, item := range items {
		total := item.Totals.Total
		var err error
		native[total.Currency], err = addMoney(native[total.Currency], total)
		if err != nil {
			return InvoiceTotalsSummary{}, err
		}

		invoice := Invoice{
			ID:       item.ID,
			Number:   item.Number,
			Status:   item.Status,
			Currency: item.Currency,
			Totals:   item.Totals,
		}
		invoice.LockID = lockIDs[item.ID]
		amountGBP, ok, err := s.invoiceTotalGBP(ctx, invoice)
		if err != nil {
			return InvoiceTotalsSummary{}, err
		}
		if ok {
			totalGBP, err = totalGBP.Add(amountGBP)
			if err != nil {
				return InvoiceTotalsSummary{}, err
			}
		}
	}

	return InvoiceTotalsSummary{
		Subtotals: sortedMoneyTotals(native),
		TotalGBP:  totalGBP,
	}, nil
}

// OverdueInvoices returns advisor facts for currently overdue invoices.
func (s *Service) OverdueInvoices(ctx context.Context) ([]OverdueInvoiceFact, error) {
	normalized, err := normalizeInvoiceListFilter(InvoiceListFilter{
		Statuses: []InvoiceStatus{InvoiceStatusOverdue},
	}, false)
	if err != nil {
		return nil, err
	}
	records, err := s.store.ListInvoiceRows(ctx, s.pool, normalized, dateOnly(s.now()), false)
	if err != nil {
		return nil, err
	}
	items, err := s.invoiceListItems(ctx, records)
	if err != nil {
		return nil, err
	}

	facts := make([]OverdueInvoiceFact, 0, len(items))
	for _, item := range items {
		number := ""
		if item.Number != nil {
			number = *item.Number
		}
		facts = append(facts, OverdueInvoiceFact{
			InvoiceID:     item.ID,
			InvoiceNumber: number,
			ClientID:      item.ClientID,
			ClientName:    item.ClientName,
			DueDate:       item.DueDate,
			DaysOverdue:   item.DaysOverdue,
			Amount:        item.Totals.Total,
		})
	}
	return facts, nil
}

// RunOverdueSweep publishes InvoiceOverdue once per invoice per due-date
// crossing. The sweep state insert and event publication share one transaction.
func (s *Service) RunOverdueSweep(ctx context.Context) (err error) {
	if s.pool == nil {
		return fmt.Errorf("invoicing: overdue sweep requires pool")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("invoicing: begin overdue sweep transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	claims, err := s.store.ClaimOverdueInvoices(ctx, tx, dateOnly(s.now()))
	if err != nil {
		return err
	}
	for _, claim := range claims {
		if err := s.publish(ctx, tx, InvoiceOverdue{
			InvoiceID:   claim.invoiceID,
			DaysOverdue: claim.daysOverdue,
		}); err != nil {
			return fmt.Errorf("invoicing: publish invoice overdue: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("invoicing: commit overdue sweep transaction: %w", err)
	}
	return nil
}

func (m *Module) RunOverdueSweep(ctx context.Context) error {
	if m == nil || m.service == nil {
		return fmt.Errorf("invoicing: overdue sweep requires module service")
	}
	return m.service.RunOverdueSweep(ctx)
}

func (s *Service) invoiceListItems(ctx context.Context, records []invoiceListRecord) ([]InvoiceListItem, error) {
	if len(records) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.invoice.ID)
	}
	linesByInvoice, err := s.store.InvoiceLinesForInvoices(ctx, s.pool, ids)
	if err != nil {
		return nil, err
	}

	items := make([]InvoiceListItem, 0, len(records))
	for _, record := range records {
		invoice := record.invoice
		invoice.Status = record.status
		invoice.Lines = linesByInvoice[invoice.ID]
		computed, err := s.computeTotals(ctx, invoice, false)
		if err != nil {
			return nil, err
		}
		approxGBP, err := s.invoiceListApproxGBP(ctx, computed)
		if err != nil {
			return nil, err
		}
		computed.Totals.ApproxGBP = approxGBP
		items = append(items, InvoiceListItem{
			ID:          computed.ID,
			Number:      computed.Number,
			ClientID:    computed.ClientID,
			ClientName:  record.clientName,
			Status:      record.status,
			IssueDate:   computed.IssueDate,
			DueDate:     computed.DueDate,
			DaysOverdue: record.daysOverdue,
			Currency:    computed.Currency,
			Totals:      computed.Totals,
			CreatedAt:   computed.CreatedAt,
			UpdatedAt:   computed.UpdatedAt,
		})
	}
	return items, nil
}

func (s *Service) invoiceListApproxGBP(ctx context.Context, invoice Invoice) (*InvoiceGBPApprox, error) {
	total := invoice.Totals.Total
	if total.Currency == string(CurrencyGBP) {
		return nil, nil
	}
	if invoice.LockID != nil {
		lockID, err := strconv.ParseInt(strings.TrimSpace(*invoice.LockID), 10, 64)
		if err != nil || lockID <= 0 {
			return nil, fmt.Errorf("invoicing: invalid invoice lock id %q", *invoice.LockID)
		}
		if s.rateLocks == nil {
			return nil, fmt.Errorf("invoicing: rate lock reader is required for locked invoice list totals")
		}
		lock, err := s.rateLocks.RateLock(ctx, lockID)
		if err != nil {
			return nil, err
		}
		amount, err := convertInvoiceTotalGBP(total, lock.Rate)
		if err != nil {
			return nil, err
		}
		rateDate := dateOnly(invoice.IssueDate).UTC()
		return &InvoiceGBPApprox{
			Amount: amount,
			Rate: FXRate{
				From:     total.Currency,
				To:       string(CurrencyGBP),
				Value:    lock.Rate,
				RateDate: rateDate,
				Source:   "locked",
			},
			AsOf:   rateDate,
			Locked: true,
		}, nil
	}
	return s.approxGBP(ctx, total)
}

func (s *Service) invoiceTotalGBP(ctx context.Context, invoice Invoice) (Money, bool, error) {
	total := invoice.Totals.Total
	if total.Currency == string(CurrencyGBP) {
		return total, true, nil
	}
	if invoice.LockID != nil {
		lockID, err := strconv.ParseInt(strings.TrimSpace(*invoice.LockID), 10, 64)
		if err != nil || lockID <= 0 {
			return Money{}, false, fmt.Errorf("invoicing: invalid invoice lock id %q", *invoice.LockID)
		}
		if s.rateLocks == nil {
			return Money{}, false, fmt.Errorf("invoicing: rate lock reader is required for locked totals")
		}
		lock, err := s.rateLocks.RateLock(ctx, lockID)
		if err != nil {
			return Money{}, false, err
		}
		amount, err := convertInvoiceTotalGBP(total, lock.Rate)
		if err != nil {
			return Money{}, false, err
		}
		return amount, true, nil
	}
	if s.todayRate == nil {
		return Money{}, false, nil
	}
	rate, _, err := s.todayRate(ctx, total.Currency, string(CurrencyGBP))
	if err != nil {
		if errors.Is(err, ErrRateUnavailable) {
			return Money{}, false, nil
		}
		return Money{}, false, fmt.Errorf("invoicing: today GBP rate for totals: %w", err)
	}
	amount, err := convertInvoiceTotalGBP(total, rate.Value)
	if err != nil {
		return Money{}, false, err
	}
	return amount, true, nil
}

func convertInvoiceTotalGBP(total Money, rate string) (Money, error) {
	rat, err := rateRat(rate)
	if err != nil {
		return Money{}, err
	}
	amount := total.MulRat(rat)
	amount.Currency = string(CurrencyGBP)
	return amount, nil
}

func normalizeInvoiceListFilter(filter InvoiceListFilter, paginate bool) (normalizedInvoiceListFilter, error) {
	statuses, err := normalizeInvoiceStatusFilters(filter.Statuses)
	if err != nil {
		return normalizedInvoiceListFilter{}, err
	}
	limit := filter.Limit
	offset := filter.Offset
	if paginate {
		if limit == 0 {
			limit = DefaultInvoiceListLimit
		}
		if limit < 0 || limit > MaxInvoiceListLimit {
			return normalizedInvoiceListFilter{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInvoiceListFilter, MaxInvoiceListLimit)
		}
		if offset < 0 {
			return normalizedInvoiceListFilter{}, fmt.Errorf("%w: offset must be non-negative", ErrInvalidInvoiceListFilter)
		}
	} else {
		limit = 0
		offset = 0
	}
	return normalizedInvoiceListFilter{
		statuses: statuses,
		search:   strings.TrimSpace(filter.Search),
		limit:    limit,
		offset:   offset,
	}, nil
}

func normalizeInvoiceStatusFilters(statuses []InvoiceStatus) ([]InvoiceStatus, error) {
	seen := make(map[InvoiceStatus]bool, len(statuses))
	normalized := make([]InvoiceStatus, 0, len(statuses))
	for _, status := range statuses {
		value := InvoiceStatus(strings.ToLower(strings.TrimSpace(string(status))))
		switch value {
		case InvoiceStatusDraft, InvoiceStatusSent, InvoiceStatusPaid, InvoiceStatusOverdue:
		default:
			return nil, fmt.Errorf("%w: unknown status %q", ErrInvalidInvoiceListFilter, status)
		}
		if !seen[value] {
			seen[value] = true
			normalized = append(normalized, value)
		}
	}
	return normalized, nil
}

func invoiceStatusCountList(counts map[InvoiceStatus]int) []InvoiceStatusCount {
	statuses := []InvoiceStatus{
		InvoiceStatusDraft,
		InvoiceStatusSent,
		InvoiceStatusPaid,
		InvoiceStatusOverdue,
	}
	result := make([]InvoiceStatusCount, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, InvoiceStatusCount{
			Status: status,
			Count:  counts[status],
		})
	}
	return result
}

func sortedMoneyTotals(totals map[string]Money) []Money {
	currencies := make([]string, 0, len(totals))
	for currency := range totals {
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)

	result := make([]Money, 0, len(currencies))
	for _, currency := range currencies {
		result = append(result, totals[currency])
	}
	return result
}

func addMoney(existing Money, next Money) (Money, error) {
	if existing.Currency == "" {
		existing.Currency = next.Currency
	}
	return existing.Add(next)
}

func (s Store) ListInvoiceRows(ctx context.Context, tx db.Tx, filter normalizedInvoiceListFilter, today time.Time, paginate bool) ([]invoiceListRecord, error) {
	query, args := buildInvoiceListRowsQuery(filter, today, paginate)
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("invoicing: list invoice rows: %w", err)
	}
	defer rows.Close()

	records := []invoiceListRecord{}
	for rows.Next() {
		record, err := scanInvoiceListRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("invoicing: collect invoice rows: %w", err)
	}
	return records, nil
}

func (s Store) InvoiceListCount(ctx context.Context, tx db.Tx, filter normalizedInvoiceListFilter, today time.Time) (int, error) {
	query, args := buildInvoiceListCountQuery(filter, today)
	var count int
	if err := tx.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("invoicing: count invoice rows: %w", err)
	}
	return count, nil
}

func (s Store) InvoiceStatusCounts(ctx context.Context, tx db.Tx, filter normalizedInvoiceListFilter, today time.Time) (map[InvoiceStatus]int, error) {
	query, args := buildInvoiceStatusCountsQuery(filter, today)
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("invoicing: invoice status counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[InvoiceStatus]int)
	for rows.Next() {
		var (
			status string
			count  int
		)
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("invoicing: scan invoice status count: %w", err)
		}
		counts[InvoiceStatus(status)] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("invoicing: collect invoice status counts: %w", err)
	}
	return counts, nil
}

func (s Store) InvoiceLinesForInvoices(ctx context.Context, tx db.Tx, invoiceIDs []string) (map[string][]InvoiceLine, error) {
	result := make(map[string][]InvoiceLine, len(invoiceIDs))
	if len(invoiceIDs) == 0 {
		return result, nil
	}

	rows, err := tx.Query(ctx, `
SELECT id,
	invoice_id,
	position,
	description,
	qty::text,
	unit_price_amount_minor,
	unit_price_currency
FROM invoicing.invoice_lines
WHERE invoice_id = ANY($1::text[])
ORDER BY invoice_id, position, id`, invoiceIDs)
	if err != nil {
		return nil, fmt.Errorf("invoicing: list invoice lines for invoices: %w", err)
	}
	defer rows.Close()

	lines, err := pgx.CollectRows(rows, scanInvoiceLine)
	if err != nil {
		return nil, fmt.Errorf("invoicing: collect invoice lines for invoices: %w", err)
	}
	for _, line := range lines {
		result[line.InvoiceID] = append(result[line.InvoiceID], line)
	}
	return result, nil
}

func (s Store) ClaimOverdueInvoices(ctx context.Context, tx db.Tx, today time.Time) ([]overdueSweepClaim, error) {
	rows, err := tx.Query(ctx, `
WITH inserted AS (
	INSERT INTO invoicing.overdue_sweep_state (
		invoice_id,
		due_date,
		days_overdue_at_publish
	)
	SELECT i.id,
		i.due_date,
		($1::date - i.due_date)::integer AS days_overdue
	FROM invoicing.invoices AS i
	WHERE i.status = 'sent'
		AND i.due_date < $1::date
	ON CONFLICT (invoice_id, due_date) DO NOTHING
	RETURNING invoice_id, days_overdue_at_publish
)
SELECT invoice_id, days_overdue_at_publish
FROM inserted
ORDER BY invoice_id`, today)
	if err != nil {
		return nil, fmt.Errorf("invoicing: claim overdue invoices: %w", err)
	}
	defer rows.Close()

	var claims []overdueSweepClaim
	for rows.Next() {
		var claim overdueSweepClaim
		if err := rows.Scan(&claim.invoiceID, &claim.daysOverdue); err != nil {
			return nil, fmt.Errorf("invoicing: scan overdue claim: %w", err)
		}
		claims = append(claims, claim)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("invoicing: collect overdue claims: %w", err)
	}
	return claims, nil
}

type invoiceQueryArgs struct {
	args []any
}

func (a *invoiceQueryArgs) add(value any) string {
	a.args = append(a.args, value)
	return fmt.Sprintf("$%d", len(a.args))
}

func buildInvoiceListRowsQuery(filter normalizedInvoiceListFilter, today time.Time, paginate bool) (string, []any) {
	args := &invoiceQueryArgs{}
	todayRef := args.add(dateOnly(today))
	where := buildInvoiceListWhere(filter, todayRef, true, args)

	query := `
SELECT ` + invoiceListSelectSQL(todayRef) + `
FROM invoicing.invoices AS i
JOIN invoicing.clients AS c ON c.id = i.client_id
WHERE ` + where + `
ORDER BY i.issue_date DESC, i.created_at DESC, i.id DESC`
	if paginate {
		query += `
LIMIT ` + args.add(filter.limit) + `
OFFSET ` + args.add(filter.offset)
	}
	return query, args.args
}

func buildInvoiceListCountQuery(filter normalizedInvoiceListFilter, today time.Time) (string, []any) {
	args := &invoiceQueryArgs{}
	todayRef := "NULL"
	if invoiceStatusFilterNeedsDate(filter.statuses) {
		todayRef = args.add(dateOnly(today))
	}
	where := buildInvoiceListWhere(filter, todayRef, true, args)
	return `
SELECT count(*)::integer
FROM invoicing.invoices AS i
WHERE ` + where, args.args
}

func buildInvoiceStatusCountsQuery(filter normalizedInvoiceListFilter, today time.Time) (string, []any) {
	args := &invoiceQueryArgs{}
	todayRef := args.add(dateOnly(today))
	where := buildInvoiceListWhere(filter, todayRef, false, args)
	statusSQL := invoiceVirtualStatusSQL(todayRef)
	return `
SELECT ` + statusSQL + ` AS status,
	count(*)::integer AS count
FROM invoicing.invoices AS i
WHERE ` + where + `
GROUP BY ` + statusSQL + `
ORDER BY status`, args.args
}

func buildInvoiceListWhere(filter normalizedInvoiceListFilter, todayRef string, includeStatus bool, args *invoiceQueryArgs) string {
	conditions := []string{"true"}
	if includeStatus && len(filter.statuses) > 0 {
		conditions = append(conditions, invoiceStatusFilterSQL(filter.statuses, todayRef))
	}
	if filter.search != "" {
		pattern := args.add("%" + escapeILikePattern(filter.search) + "%")
		conditions = append(conditions, `i.id IN (
			SELECT search_number.id
			FROM invoicing.invoices AS search_number
			WHERE search_number.number ILIKE `+pattern+` ESCAPE '\'
			UNION
			SELECT search_client_invoice.id
			FROM invoicing.invoices AS search_client_invoice
			WHERE search_client_invoice.client_id IN (
				SELECT search_clients.id
				FROM invoicing.clients AS search_clients
				WHERE search_clients.name ILIKE `+pattern+` ESCAPE '\'
			)
		)`)
	}
	return strings.Join(conditions, "\n\tAND ")
}

func invoiceStatusFilterSQL(statuses []InvoiceStatus, todayRef string) string {
	conditions := make([]string, 0, len(statuses))
	for _, status := range statuses {
		switch status {
		case InvoiceStatusDraft, InvoiceStatusPaid:
			conditions = append(conditions, "i.status = '"+string(status)+"'")
		case InvoiceStatusSent:
			conditions = append(conditions, "(i.status = 'sent' AND i.due_date >= "+todayRef+"::date)")
		case InvoiceStatusOverdue:
			conditions = append(conditions, "(i.status = 'sent' AND i.due_date < "+todayRef+"::date)")
		}
	}
	if len(conditions) == 0 {
		return "true"
	}
	return "(" + strings.Join(conditions, " OR ") + ")"
}

func invoiceStatusFilterNeedsDate(statuses []InvoiceStatus) bool {
	for _, status := range statuses {
		if status == InvoiceStatusSent || status == InvoiceStatusOverdue {
			return true
		}
	}
	return false
}

func invoiceVirtualStatusSQL(todayRef string) string {
	return "CASE WHEN i.status = 'sent' AND i.due_date < " + todayRef + "::date THEN 'overdue' ELSE i.status END"
}

func invoiceDaysOverdueSQL(todayRef string) string {
	return "CASE WHEN i.status = 'sent' AND i.due_date < " + todayRef + "::date THEN (" + todayRef + "::date - i.due_date)::integer ELSE 0 END"
}

func invoiceListSelectSQL(todayRef string) string {
	return `i.id,
	i.number,
	i.client_id,
	i.status,
	i.issue_date,
	i.due_date,
	i.currency,
	i.lock_id,
	i.send_ledger_entry_id,
	i.sent_at,
	i.vat_treatment,
	i.settlement_txn_ref,
	i.settled_date,
	i.settled_amount_minor,
	i.settled_amount_currency,
	i.pdf_asset,
	i.created_at,
	i.updated_at,
	c.name AS client_name,
	` + invoiceVirtualStatusSQL(todayRef) + ` AS virtual_status,
	` + invoiceDaysOverdueSQL(todayRef) + ` AS days_overdue`
}

func escapeILikePattern(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(strings.TrimSpace(value))
}

func scanInvoiceListRecord(row pgx.Row) (invoiceListRecord, error) {
	var (
		record          invoiceListRecord
		number          sql.NullString
		status          string
		currency        string
		lockID          sql.NullString
		sendEntryID     sql.NullInt64
		sentAt          sql.NullTime
		vatTreatment    string
		settlementRef   sql.NullString
		settledDate     sql.NullTime
		settledAmount   sql.NullInt64
		settledCurrency sql.NullString
		pdfAsset        sql.NullString
		virtualStatus   string
	)
	err := row.Scan(
		&record.invoice.ID,
		&number,
		&record.invoice.ClientID,
		&status,
		&record.invoice.IssueDate,
		&record.invoice.DueDate,
		&currency,
		&lockID,
		&sendEntryID,
		&sentAt,
		&vatTreatment,
		&settlementRef,
		&settledDate,
		&settledAmount,
		&settledCurrency,
		&pdfAsset,
		&record.invoice.CreatedAt,
		&record.invoice.UpdatedAt,
		&record.clientName,
		&virtualStatus,
		&record.daysOverdue,
	)
	if err != nil {
		return invoiceListRecord{}, fmt.Errorf("invoicing: scan invoice list row: %w", err)
	}
	if number.Valid {
		record.invoice.Number = &number.String
	}
	record.invoice.Status = InvoiceStatus(status)
	record.status = InvoiceStatus(virtualStatus)
	record.invoice.Currency = Currency(currency)
	if lockID.Valid {
		record.invoice.LockID = &lockID.String
	}
	if sendEntryID.Valid {
		record.invoice.SendLedgerEntryID = &sendEntryID.Int64
	}
	if sentAt.Valid {
		value := sentAt.Time.UTC()
		record.invoice.SentAt = &value
	}
	record.invoice.VATTreatment = VATTreatment(vatTreatment)
	if settlementRef.Valid {
		record.invoice.SettlementTxnRef = &settlementRef.String
	}
	if settledDate.Valid {
		settled := dateOnly(settledDate.Time)
		record.invoice.SettledDate = &settled
	}
	record.invoice.SettledAmount = invoiceMoneyFromNullable(settledAmount, settledCurrency)
	if pdfAsset.Valid {
		record.invoice.PDFAsset = &pdfAsset.String
	}
	record.invoice.IssueDate = dateOnly(record.invoice.IssueDate)
	record.invoice.DueDate = dateOnly(record.invoice.DueDate)
	record.invoice.CreatedAt = record.invoice.CreatedAt.UTC()
	record.invoice.UpdatedAt = record.invoice.UpdatedAt.UTC()
	return record, nil
}
