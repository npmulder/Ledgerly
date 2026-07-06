ALTER TABLE invoicing.invoices
	ADD COLUMN IF NOT EXISTS send_ledger_entry_id bigint;

COMMENT ON COLUMN invoicing.invoices.send_ledger_entry_id IS
	'Opaque ledger journal entry id for the invoice send posting; used only to reverse same-day unsends.';

GRANT USAGE ON SCHEMA invoicing TO ledgerly_banking;
GRANT SELECT, UPDATE ON invoicing.invoices TO ledgerly_banking;
GRANT SELECT ON invoicing.invoice_lines TO ledgerly_banking;
