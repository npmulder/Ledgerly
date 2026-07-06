CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

CREATE TABLE IF NOT EXISTS invoicing.overdue_sweep_state (
	invoice_id text NOT NULL REFERENCES invoicing.invoices(id) ON DELETE CASCADE,
	due_date date NOT NULL,
	days_overdue_at_publish integer NOT NULL,
	published_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (invoice_id, due_date),
	CONSTRAINT overdue_sweep_days_positive CHECK (days_overdue_at_publish > 0)
);

CREATE INDEX IF NOT EXISTS invoices_sent_due_overdue_idx
	ON invoicing.invoices (due_date, id)
	WHERE status = 'sent';

CREATE INDEX IF NOT EXISTS invoices_number_trgm_idx
	ON invoicing.invoices
	USING gin (number public.gin_trgm_ops)
	WHERE number IS NOT NULL;

CREATE INDEX IF NOT EXISTS clients_name_trgm_idx
	ON invoicing.clients
	USING gin (name public.gin_trgm_ops);

GRANT ALL PRIVILEGES ON invoicing.overdue_sweep_state TO ledgerly_invoicing;
