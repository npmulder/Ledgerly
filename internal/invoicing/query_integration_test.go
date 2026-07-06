//go:build integration

package invoicing

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/npmulder/ledgerly/internal/it/testdb"
)

func TestMain(m *testing.M) {
	os.Exit(testdb.Main(m))
}

func BenchmarkInvoiceListExplainPlan5kInvoices(b *testing.B) {
	ctx := context.Background()
	pool := testdb.AsModule(b, ModuleName)
	seedInvoiceListBenchmarkRows(b, ctx, pool)

	for _, table := range []string{"invoicing.clients", "invoicing.invoices", "invoicing.invoice_lines"} {
		if _, err := pool.Exec(ctx, "VACUUM ANALYZE "+table); err != nil {
			b.Fatalf("VACUUM ANALYZE %s: %v", table, err)
		}
	}

	filter := normalizedInvoiceListFilter{
		statuses: []InvoiceStatus{InvoiceStatusOverdue},
		limit:    50,
		offset:   0,
	}
	query, args := buildInvoiceListRowsQuery(filter, time.Date(2030, 2, 2, 0, 0, 0, 0, time.UTC), true)
	plan := explainInvoiceListPlan(b, ctx, pool, query, args)
	if !strings.Contains(plan, "invoices_sent_due_overdue_idx") {
		b.Fatalf("invoice overdue list EXPLAIN did not use invoices_sent_due_overdue_idx:\n%s", plan)
	}
	if strings.Contains(plan, "Seq Scan on invoices") || strings.Contains(plan, "Seq Scan on invoicing.invoices") {
		b.Fatalf("invoice overdue list EXPLAIN used a sequential scan on invoices:\n%s", plan)
	}
	b.Logf("Invoice overdue List 5k EXPLAIN:\n%s", plan)

	searchFilter := normalizedInvoiceListFilter{
		search: "INV-2030-4999",
		limit:  50,
		offset: 0,
	}
	searchQuery, searchArgs := buildInvoiceListRowsQuery(searchFilter, time.Date(2030, 3, 1, 0, 0, 0, 0, time.UTC), true)
	searchPlan := explainInvoiceListPlanWithSeqScanDisabled(b, ctx, pool, searchQuery, searchArgs)
	if !strings.Contains(searchPlan, "invoices_number_trgm_idx") {
		b.Fatalf("invoice list search EXPLAIN did not use invoices_number_trgm_idx:\n%s", searchPlan)
	}
	b.Logf("Invoice list search 5k EXPLAIN with seqscan disabled:\n%s", searchPlan)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := pool.Query(ctx, query, args...)
		if err != nil {
			b.Fatalf("ListInvoiceRows benchmark query: %v", err)
		}
		for rows.Next() {
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			b.Fatalf("ListInvoiceRows benchmark rows: %v", err)
		}
		rows.Close()
	}
}

func seedInvoiceListBenchmarkRows(b testing.TB, ctx context.Context, pool *pgxpool.Pool) {
	b.Helper()
	if _, err := pool.Exec(ctx, `
INSERT INTO invoicing.clients (
	id,
	name,
	address,
	default_currency,
	terms_days,
	vat_treatment
)
SELECT 'client_perf_' || g::text,
	CASE WHEN g = 4999 THEN 'Needle Industries' ELSE 'Client ' || lpad(g::text, 4, '0') END,
	'{}'::jsonb,
	'GBP',
	30,
	'domestic'
FROM generate_series(1, 5000) AS g
ON CONFLICT (id) DO NOTHING`); err != nil {
		b.Fatalf("seed benchmark clients: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO invoicing.invoices (
	id,
	number,
	client_id,
	status,
	issue_date,
	due_date,
	currency,
	vat_treatment
)
SELECT 'invoice_perf_' || g::text,
	'INV-2030-' || g::text,
	'client_perf_' || g::text,
	'sent',
	date '2030-01-01' + (g % 30)::integer,
	date '2030-02-01' + (g % 30)::integer,
	'GBP',
	'domestic'
FROM generate_series(1, 5000) AS g
ON CONFLICT (id) DO NOTHING`); err != nil {
		b.Fatalf("seed benchmark invoices: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO invoicing.invoice_lines (
	id,
	invoice_id,
	position,
	description,
	qty,
	unit_price_amount_minor,
	unit_price_currency
)
SELECT 'line_perf_' || g::text,
	'invoice_perf_' || g::text,
	1,
	'Benchmark line',
	1,
	10000,
	'GBP'
FROM generate_series(1, 5000) AS g
ON CONFLICT (id) DO NOTHING`); err != nil {
		b.Fatalf("seed benchmark invoice lines: %v", err)
	}
}

func explainInvoiceListPlan(b testing.TB, ctx context.Context, pool *pgxpool.Pool, query string, args []any) string {
	b.Helper()
	rows, err := pool.Query(ctx, "EXPLAIN (COSTS OFF) "+query, args...)
	if err != nil {
		b.Fatalf("EXPLAIN invoice list query: %v", err)
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			b.Fatalf("scan EXPLAIN line: %v", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		b.Fatalf("collect EXPLAIN plan: %v", err)
	}
	return strings.Join(lines, "\n")
}

func explainInvoiceListPlanWithSeqScanDisabled(b testing.TB, ctx context.Context, pool *pgxpool.Pool, query string, args []any) string {
	b.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		b.Fatalf("acquire EXPLAIN connection: %v", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		b.Fatalf("begin EXPLAIN transaction: %v", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		b.Fatalf("SET LOCAL enable_seqscan: %v", err)
	}

	rows, err := tx.Query(ctx, "EXPLAIN (COSTS OFF) "+query, args...)
	if err != nil {
		b.Fatalf("EXPLAIN invoice list query with seqscan disabled: %v", err)
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			b.Fatalf("scan forced EXPLAIN line: %v", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		b.Fatalf("collect forced EXPLAIN plan: %v", err)
	}
	return strings.Join(lines, "\n")
}
