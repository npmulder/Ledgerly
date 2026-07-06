CREATE TABLE IF NOT EXISTS invoicing.invoice_send_vat_context (
	send_ledger_entry_id bigint PRIMARY KEY,
	invoice_id text NOT NULL,
	vat_treatment text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	CONSTRAINT invoice_send_vat_context_invoice_id_nonempty CHECK (btrim(invoice_id) <> ''),
	CONSTRAINT invoice_send_vat_context_vat_treatment_check CHECK (vat_treatment IN ('domestic', 'reverse-charge-eu-b2b'))
);

COMMENT ON TABLE invoicing.invoice_send_vat_context IS
	'Immutable VAT treatment context for invoice send ledger entries, retained after same-day unsends clear invoices.send_ledger_entry_id and after reverted drafts are deleted.';

CREATE INDEX IF NOT EXISTS invoice_send_vat_context_invoice_id_idx
	ON invoicing.invoice_send_vat_context (invoice_id, created_at);

INSERT INTO invoicing.invoice_send_vat_context (
	send_ledger_entry_id,
	invoice_id,
	vat_treatment
)
SELECT send_ledger_entry_id,
	id,
	vat_treatment
FROM invoicing.invoices
WHERE send_ledger_entry_id IS NOT NULL
ON CONFLICT (send_ledger_entry_id) DO NOTHING;
