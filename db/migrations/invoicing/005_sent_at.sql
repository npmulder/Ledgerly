ALTER TABLE invoicing.invoices
	ADD COLUMN IF NOT EXISTS sent_at timestamptz;

UPDATE invoicing.invoices
SET sent_at = updated_at
WHERE sent_at IS NULL
	AND status IN ('sent', 'paid');

COMMENT ON COLUMN invoicing.invoices.sent_at IS
	'Timestamp when the invoice was sent; same-day unsend checks this rather than editable issue_date.';
