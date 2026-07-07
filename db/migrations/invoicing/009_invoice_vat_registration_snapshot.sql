ALTER TABLE invoicing.invoices
	ADD COLUMN IF NOT EXISTS vat_registered_at_send boolean;

COMMENT ON COLUMN invoicing.invoices.vat_registered_at_send IS
	'Company VAT registration snapshot used for immutable sent invoice totals and print payloads.';

ALTER TABLE invoicing.invoice_send_vat_context
	ADD COLUMN IF NOT EXISTS vat_registered_at_send boolean NOT NULL DEFAULT true;

COMMENT ON COLUMN invoicing.invoice_send_vat_context.vat_registered_at_send IS
	'Company VAT registration snapshot for immutable invoice send ledger entries and VAT return reporting.';
