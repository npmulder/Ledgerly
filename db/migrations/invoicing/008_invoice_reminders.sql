ALTER TABLE invoicing.clients
	ADD COLUMN IF NOT EXISTS email text;

DO $$
BEGIN
	IF NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'clients_email_nonempty'
			AND conrelid = 'invoicing.clients'::regclass
	) THEN
		ALTER TABLE invoicing.clients
			ADD CONSTRAINT clients_email_nonempty CHECK (email IS NULL OR btrim(email) <> '');
	END IF;
END $$;

CREATE TABLE IF NOT EXISTS invoicing.reminders (
	invoice_id text NOT NULL REFERENCES invoicing.invoices(id) ON DELETE CASCADE,
	sent_at timestamptz NOT NULL,
	PRIMARY KEY (invoice_id, sent_at)
);

CREATE INDEX IF NOT EXISTS reminders_invoice_sent_at_idx
	ON invoicing.reminders (invoice_id, sent_at DESC);

GRANT ALL PRIVILEGES ON invoicing.reminders TO ledgerly_invoicing;
