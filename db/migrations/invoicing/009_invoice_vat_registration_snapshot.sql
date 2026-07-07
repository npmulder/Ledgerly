ALTER TABLE invoicing.invoices
	ADD COLUMN IF NOT EXISTS vat_registered_at_send boolean;

COMMENT ON COLUMN invoicing.invoices.vat_registered_at_send IS
	'Company VAT registration snapshot used for immutable sent invoice totals and print payloads.';
