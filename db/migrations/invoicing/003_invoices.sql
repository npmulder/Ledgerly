CREATE TABLE IF NOT EXISTS invoicing.invoices (
	id text PRIMARY KEY,
	number text UNIQUE,
	client_id text NOT NULL REFERENCES invoicing.clients(id),
	status text NOT NULL DEFAULT 'draft',
	issue_date date NOT NULL,
	due_date date NOT NULL,
	currency text NOT NULL,
	lock_id text,
	vat_treatment text NOT NULL,
	settlement_txn_ref text,
	settled_date date,
	settled_amount_minor bigint,
	settled_amount_currency text,
	pdf_asset text,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	CONSTRAINT invoices_id_nonempty CHECK (btrim(id) <> ''),
	CONSTRAINT invoices_number_format CHECK (number IS NULL OR number ~ '^INV-[0-9]{4}-[0-9]+$'),
	CONSTRAINT invoices_status_check CHECK (status IN ('draft', 'sent', 'paid')),
	CONSTRAINT invoices_due_not_before_issue CHECK (due_date >= issue_date),
	CONSTRAINT invoices_currency_check CHECK (currency IN ('EUR', 'GBP')),
	CONSTRAINT invoices_vat_treatment_check CHECK (vat_treatment IN ('domestic', 'reverse-charge-eu-b2b')),
	CONSTRAINT invoices_settled_amount_pair_check CHECK ((settled_amount_minor IS NULL) = (settled_amount_currency IS NULL)),
	CONSTRAINT invoices_settled_amount_positive CHECK (settled_amount_minor IS NULL OR settled_amount_minor > 0),
	CONSTRAINT invoices_settled_amount_currency_check CHECK (settled_amount_currency IS NULL OR settled_amount_currency IN ('EUR', 'GBP')),
	CONSTRAINT invoices_settled_amount_matches_currency CHECK (settled_amount_currency IS NULL OR settled_amount_currency = currency)
);

CREATE INDEX IF NOT EXISTS invoices_client_id_idx ON invoicing.invoices (client_id, created_at, id);
CREATE INDEX IF NOT EXISTS invoices_status_due_idx ON invoicing.invoices (status, due_date, id);

CREATE TABLE IF NOT EXISTS invoicing.invoice_lines (
	id text PRIMARY KEY,
	invoice_id text NOT NULL REFERENCES invoicing.invoices(id) ON DELETE CASCADE,
	position integer NOT NULL,
	description text NOT NULL,
	qty numeric NOT NULL,
	unit_price_amount_minor bigint NOT NULL,
	unit_price_currency text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	CONSTRAINT invoice_lines_id_nonempty CHECK (btrim(id) <> ''),
	CONSTRAINT invoice_lines_position_positive CHECK (position > 0),
	CONSTRAINT invoice_lines_description_nonempty CHECK (btrim(description) <> ''),
	CONSTRAINT invoice_lines_qty_positive CHECK (qty > 0),
	CONSTRAINT invoice_lines_unit_price_positive CHECK (unit_price_amount_minor > 0),
	CONSTRAINT invoice_lines_unit_price_currency_check CHECK (unit_price_currency IN ('EUR', 'GBP')),
	CONSTRAINT invoice_lines_invoice_position_unique UNIQUE (invoice_id, position)
);

CREATE INDEX IF NOT EXISTS invoice_lines_invoice_id_idx ON invoicing.invoice_lines (invoice_id, position, id);

CREATE TABLE IF NOT EXISTS invoicing.invoice_numbering (
	year integer PRIMARY KEY,
	last_seq integer NOT NULL DEFAULT 0,
	CONSTRAINT invoice_numbering_year_check CHECK (year BETWEEN 1 AND 9999),
	CONSTRAINT invoice_numbering_last_seq_check CHECK (last_seq >= 0)
);

GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA invoicing TO ledgerly_invoicing;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA invoicing TO ledgerly_invoicing;
