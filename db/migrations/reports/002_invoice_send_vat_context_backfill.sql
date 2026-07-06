INSERT INTO invoicing.invoice_send_vat_context (
	send_ledger_entry_id,
	invoice_id,
	vat_treatment
)
SELECT send_entries.id,
	COALESCE(invoices.id, send_entries.source_ref),
	CASE
		WHEN EXISTS (
			SELECT 1
			FROM ledger.postings vat_postings
			WHERE vat_postings.entry_id = send_entries.id
				AND vat_postings.account_code = '2200-vat-control'
				AND vat_postings.amount_gbp <> 0
		) THEN 'domestic'
		ELSE 'reverse-charge-eu-b2b'
	END
FROM ledger.journal_entries send_entries
LEFT JOIN invoicing.invoices invoices
	ON invoices.send_ledger_entry_id = send_entries.id
WHERE send_entries.source_module = 'invoicing'
	AND send_entries.source_ref LIKE 'invoice:%:send'
	AND send_entries.reversal_of IS NULL
	AND EXISTS (
		SELECT 1
		FROM ledger.postings sales_postings
		WHERE sales_postings.entry_id = send_entries.id
			AND sales_postings.account_code = '4000-sales'
	)
ON CONFLICT (send_ledger_entry_id) DO NOTHING;
