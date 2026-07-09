CREATE OR REPLACE FUNCTION ledger.journal_entry_by_source(
	p_source_module text,
	p_source_ref text
)
RETURNS TABLE (
	id bigint,
	entry_date date,
	description text,
	source_module text,
	source_ref text,
	reversal_of bigint,
	created_at timestamptz
)
LANGUAGE sql
SECURITY DEFINER
SET search_path = ledger, pg_temp
AS $ledgerly$
	SELECT je.id, je.date, je.description, je.source_module, je.source_ref, je.reversal_of, je.created_at
	FROM ledger.journal_entries AS je
	WHERE je.source_module = p_source_module
		AND je.source_ref = p_source_ref
		AND je.reversal_of IS NULL
	ORDER BY je.id DESC
	LIMIT 1;
$ledgerly$;

REVOKE ALL ON FUNCTION ledger.journal_entry_by_source(text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION ledger.journal_entry_by_source(text, text) TO PUBLIC;
