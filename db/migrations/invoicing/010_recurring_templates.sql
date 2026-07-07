CREATE TABLE IF NOT EXISTS invoicing.recurring_templates (
	id text PRIMARY KEY,
	client_id text NOT NULL REFERENCES invoicing.clients(id),
	status text NOT NULL DEFAULT 'active',
	cadence text NOT NULL,
	day_of_month integer NOT NULL,
	next_run_date date NOT NULL,
	currency text NOT NULL,
	vat_treatment text NOT NULL,
	auto_send boolean NOT NULL DEFAULT false,
	max_occurrences integer,
	occurrences_created integer NOT NULL DEFAULT 0,
	created_from_invoice_id text REFERENCES invoicing.invoices(id) ON DELETE SET NULL,
	canceled_at timestamptz,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	CONSTRAINT recurring_templates_id_nonempty CHECK (btrim(id) <> ''),
	CONSTRAINT recurring_templates_status_check CHECK (status IN ('active', 'canceled')),
	CONSTRAINT recurring_templates_cadence_check CHECK (cadence IN ('monthly', 'quarterly')),
	CONSTRAINT recurring_templates_day_of_month_check CHECK (day_of_month BETWEEN 1 AND 31),
	CONSTRAINT recurring_templates_currency_check CHECK (currency IN ('EUR', 'GBP')),
	CONSTRAINT recurring_templates_vat_treatment_check CHECK (vat_treatment IN ('domestic', 'reverse-charge-eu-b2b')),
	CONSTRAINT recurring_templates_max_occurrences_positive CHECK (max_occurrences IS NULL OR max_occurrences > 0),
	CONSTRAINT recurring_templates_occurrences_created_nonnegative CHECK (occurrences_created >= 0),
	CONSTRAINT recurring_templates_occurrences_within_limit CHECK (max_occurrences IS NULL OR occurrences_created <= max_occurrences),
	CONSTRAINT recurring_templates_canceled_at_status_check CHECK ((status = 'canceled') = (canceled_at IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS recurring_templates_due_idx
	ON invoicing.recurring_templates (next_run_date, id)
	WHERE status = 'active';

CREATE INDEX IF NOT EXISTS recurring_templates_client_idx
	ON invoicing.recurring_templates (client_id, created_at, id);

CREATE TABLE IF NOT EXISTS invoicing.recurring_template_lines (
	id text PRIMARY KEY,
	template_id text NOT NULL REFERENCES invoicing.recurring_templates(id) ON DELETE CASCADE,
	position integer NOT NULL,
	description text NOT NULL,
	qty numeric NOT NULL,
	unit_price_amount_minor bigint NOT NULL,
	unit_price_currency text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	CONSTRAINT recurring_template_lines_id_nonempty CHECK (btrim(id) <> ''),
	CONSTRAINT recurring_template_lines_position_positive CHECK (position > 0),
	CONSTRAINT recurring_template_lines_description_nonempty CHECK (btrim(description) <> ''),
	CONSTRAINT recurring_template_lines_qty_positive CHECK (qty > 0),
	CONSTRAINT recurring_template_lines_unit_price_positive CHECK (unit_price_amount_minor > 0),
	CONSTRAINT recurring_template_lines_unit_price_currency_check CHECK (unit_price_currency IN ('EUR', 'GBP')),
	CONSTRAINT recurring_template_lines_template_position_unique UNIQUE (template_id, position)
);

CREATE INDEX IF NOT EXISTS recurring_template_lines_template_id_idx
	ON invoicing.recurring_template_lines (template_id, position, id);

ALTER TABLE invoicing.invoices
	ADD COLUMN IF NOT EXISTS recurring_template_id text REFERENCES invoicing.recurring_templates(id) ON DELETE SET NULL,
	ADD COLUMN IF NOT EXISTS recurring_run_date date;

CREATE UNIQUE INDEX IF NOT EXISTS invoices_recurring_run_unique_idx
	ON invoicing.invoices (recurring_template_id, recurring_run_date)
	WHERE recurring_template_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS invoices_recurring_template_idx
	ON invoicing.invoices (recurring_template_id, recurring_run_date, id)
	WHERE recurring_template_id IS NOT NULL;

GRANT ALL PRIVILEGES ON TABLE invoicing.recurring_templates TO ledgerly_invoicing;
GRANT ALL PRIVILEGES ON TABLE invoicing.recurring_template_lines TO ledgerly_invoicing;
