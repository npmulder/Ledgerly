CREATE OR REPLACE FUNCTION ledger.posting_account_currencies(p_codes text[])
RETURNS TABLE (
	account_code text,
	account_currency text
)
LANGUAGE sql
SECURITY DEFINER
SET search_path = ledger, pg_temp
AS $ledgerly$
	SELECT a.code, a.currency
	FROM ledger.accounts AS a
	WHERE a.code = ANY(p_codes);
$ledgerly$;

CREATE OR REPLACE FUNCTION ledger.account_by_code(p_code text)
RETURNS TABLE (
	id bigint,
	code text,
	name text,
	type text,
	currency text,
	created_at timestamptz
)
LANGUAGE sql
SECURITY DEFINER
SET search_path = ledger, pg_temp
AS $ledgerly$
	SELECT a.id, a.code, a.name, a.type::text, a.currency, a.created_at
	FROM ledger.accounts AS a
	WHERE a.code = p_code;
$ledgerly$;

CREATE OR REPLACE FUNCTION ledger.accounts_list()
RETURNS TABLE (
	id bigint,
	code text,
	name text,
	type text,
	currency text,
	created_at timestamptz
)
LANGUAGE sql
SECURITY DEFINER
SET search_path = ledger, pg_temp
AS $ledgerly$
	SELECT a.id, a.code, a.name, a.type::text, a.currency, a.created_at
	FROM ledger.accounts AS a
	ORDER BY a.code;
$ledgerly$;

CREATE OR REPLACE FUNCTION ledger.account_balance_rows(p_code text, p_as_of date)
RETURNS TABLE (
	currency text,
	amount bigint,
	amount_gbp bigint
)
LANGUAGE sql
SECURITY DEFINER
SET search_path = ledger, pg_temp
AS $ledgerly$
	SELECT p.currency, COALESCE(sum(p.amount), 0)::bigint, COALESCE(sum(p.amount_gbp), 0)::bigint
	FROM ledger.postings AS p
	WHERE p.account_code = p_code
		AND p.entry_date <= p_as_of
	GROUP BY p.currency
	ORDER BY p.currency;
$ledgerly$;

CREATE OR REPLACE FUNCTION ledger.balances_by_type_rows(p_from date, p_to date)
RETURNS TABLE (
	account_type text,
	currency text,
	amount bigint,
	amount_gbp bigint
)
LANGUAGE sql
SECURITY DEFINER
SET search_path = ledger, pg_temp
AS $ledgerly$
	SELECT a.type::text,
		p.currency,
		COALESCE(sum(p.amount), 0)::bigint,
		COALESCE(sum(p.amount_gbp), 0)::bigint
	FROM ledger.accounts AS a
	LEFT JOIN ledger.postings AS p
		ON p.account_code = a.code
		AND p.entry_date <= p_to
		AND (a.type NOT IN ('income', 'expense') OR p.entry_date >= p_from)
	GROUP BY a.type, p.currency
	ORDER BY CASE a.type
		WHEN 'asset' THEN 1
		WHEN 'liability' THEN 2
		WHEN 'equity' THEN 3
		WHEN 'income' THEN 4
		WHEN 'expense' THEN 5
		ELSE 6
	END, p.currency;
$ledgerly$;

CREATE OR REPLACE FUNCTION ledger.insert_journal_entry(
	p_date date,
	p_description text,
	p_source_module text,
	p_source_ref text,
	p_reversal_of bigint,
	p_account_codes text[],
	p_amounts bigint[],
	p_currencies text[],
	p_amount_gbps bigint[]
)
RETURNS bigint
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ledger, pg_temp
AS $ledgerly$
DECLARE
	new_entry_id bigint;
	posting_count integer;
	i integer;
BEGIN
	posting_count := COALESCE(array_length(p_account_codes, 1), 0);
	IF posting_count <> COALESCE(array_length(p_amounts, 1), 0)
		OR posting_count <> COALESCE(array_length(p_currencies, 1), 0)
		OR posting_count <> COALESCE(array_length(p_amount_gbps, 1), 0) THEN
		RAISE EXCEPTION 'ledger posting arrays have mismatched lengths'
			USING ERRCODE = '22023';
	END IF;

	INSERT INTO ledger.journal_entries (date, description, source_module, source_ref, reversal_of)
	VALUES (p_date, p_description, p_source_module, p_source_ref, p_reversal_of)
	RETURNING id INTO new_entry_id;

	FOR i IN 1..posting_count LOOP
		INSERT INTO ledger.postings (entry_id, entry_date, account_code, amount, currency, amount_gbp)
		VALUES (
			new_entry_id,
			p_date,
			p_account_codes[i],
			p_amounts[i],
			p_currencies[i],
			p_amount_gbps[i]
		);
	END LOOP;

	RETURN new_entry_id;
END;
$ledgerly$;

CREATE OR REPLACE FUNCTION ledger.journal_entry(p_id bigint)
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
	WHERE je.id = p_id;
$ledgerly$;

CREATE OR REPLACE FUNCTION ledger.entry_postings(p_entry_id bigint)
RETURNS TABLE (
	account_code text,
	amount bigint,
	currency text,
	amount_gbp bigint
)
LANGUAGE sql
SECURITY DEFINER
SET search_path = ledger, pg_temp
AS $ledgerly$
	SELECT p.account_code, p.amount, p.currency, p.amount_gbp
	FROM ledger.postings AS p
	WHERE p.entry_id = p_entry_id
	ORDER BY p.id;
$ledgerly$;

CREATE OR REPLACE FUNCTION ledger.entry_postings_for_entries(p_entry_ids bigint[])
RETURNS TABLE (
	entry_id bigint,
	account_code text,
	amount bigint,
	currency text,
	amount_gbp bigint
)
LANGUAGE sql
SECURITY DEFINER
SET search_path = ledger, pg_temp
AS $ledgerly$
	SELECT p.entry_id, p.account_code, p.amount, p.currency, p.amount_gbp
	FROM ledger.postings AS p
	WHERE p.entry_id = ANY(p_entry_ids)
	ORDER BY p.entry_id, p.id;
$ledgerly$;

CREATE OR REPLACE FUNCTION ledger.entries(
	p_from date,
	p_to date,
	p_source_module text,
	p_account_code text,
	p_after_date date,
	p_after_id bigint,
	p_limit integer
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
	WHERE (p_from IS NULL OR je.date >= p_from)
		AND (p_to IS NULL OR je.date <= p_to)
		AND (p_source_module IS NULL OR je.source_module = p_source_module)
		AND (
			p_account_code IS NULL
			OR EXISTS (
				SELECT 1
				FROM ledger.postings AS account_filter
				WHERE account_filter.entry_id = je.id
					AND account_filter.account_code = p_account_code
			)
		)
		AND (
			p_after_date IS NULL
			OR (je.date, je.id) > (p_after_date, p_after_id)
		)
	ORDER BY je.date, je.id
	LIMIT p_limit;
$ledgerly$;

CREATE OR REPLACE FUNCTION ledger.has_reversal(p_id bigint)
RETURNS boolean
LANGUAGE sql
SECURITY DEFINER
SET search_path = ledger, pg_temp
AS $ledgerly$
	SELECT EXISTS (
		SELECT 1
		FROM ledger.journal_entries AS je
		WHERE je.reversal_of = p_id
	);
$ledgerly$;

CREATE OR REPLACE FUNCTION ledger.check_entry_invariant(p_id bigint)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ledger, pg_temp
AS $ledgerly$
DECLARE
	posting_count integer;
	gbp_total bigint;
	native_currency text;
	native_total bigint;
BEGIN
	SELECT count(*), COALESCE(sum(p.amount_gbp), 0)::bigint
	INTO posting_count, gbp_total
	FROM ledger.postings AS p
	WHERE p.entry_id = p_id;

	IF posting_count < 2 THEN
		RAISE EXCEPTION 'ledger entry % has % postings after insert', p_id, posting_count
			USING ERRCODE = '23514';
	END IF;
	IF gbp_total <> 0 THEN
		RAISE EXCEPTION 'ledger entry % stored GBP total is %', p_id, gbp_total
			USING ERRCODE = '23514';
	END IF;

	SELECT p.currency, sum(p.amount)::bigint
	INTO native_currency, native_total
	FROM ledger.postings AS p
	WHERE p.entry_id = p_id
	GROUP BY p.currency
	HAVING sum(p.amount) <> 0
	LIMIT 1;

	IF FOUND THEN
		RAISE EXCEPTION 'ledger entry % stored % total is %', p_id, native_currency, native_total
			USING ERRCODE = '23514';
	END IF;
END;
$ledgerly$;

REVOKE ALL ON FUNCTION ledger.posting_account_currencies(text[]) FROM PUBLIC;
REVOKE ALL ON FUNCTION ledger.account_by_code(text) FROM PUBLIC;
REVOKE ALL ON FUNCTION ledger.accounts_list() FROM PUBLIC;
REVOKE ALL ON FUNCTION ledger.account_balance_rows(text, date) FROM PUBLIC;
REVOKE ALL ON FUNCTION ledger.balances_by_type_rows(date, date) FROM PUBLIC;
REVOKE ALL ON FUNCTION ledger.insert_journal_entry(date, text, text, text, bigint, text[], bigint[], text[], bigint[]) FROM PUBLIC;
REVOKE ALL ON FUNCTION ledger.journal_entry(bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION ledger.entry_postings(bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION ledger.entry_postings_for_entries(bigint[]) FROM PUBLIC;
REVOKE ALL ON FUNCTION ledger.entries(date, date, text, text, date, bigint, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION ledger.has_reversal(bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION ledger.check_entry_invariant(bigint) FROM PUBLIC;

GRANT EXECUTE ON FUNCTION ledger.posting_account_currencies(text[]) TO PUBLIC;
GRANT EXECUTE ON FUNCTION ledger.account_by_code(text) TO PUBLIC;
GRANT EXECUTE ON FUNCTION ledger.accounts_list() TO PUBLIC;
GRANT EXECUTE ON FUNCTION ledger.account_balance_rows(text, date) TO PUBLIC;
GRANT EXECUTE ON FUNCTION ledger.balances_by_type_rows(date, date) TO PUBLIC;
GRANT EXECUTE ON FUNCTION ledger.insert_journal_entry(date, text, text, text, bigint, text[], bigint[], text[], bigint[]) TO PUBLIC;
GRANT EXECUTE ON FUNCTION ledger.journal_entry(bigint) TO PUBLIC;
GRANT EXECUTE ON FUNCTION ledger.entry_postings(bigint) TO PUBLIC;
GRANT EXECUTE ON FUNCTION ledger.entry_postings_for_entries(bigint[]) TO PUBLIC;
GRANT EXECUTE ON FUNCTION ledger.entries(date, date, text, text, date, bigint, integer) TO PUBLIC;
GRANT EXECUTE ON FUNCTION ledger.has_reversal(bigint) TO PUBLIC;
GRANT EXECUTE ON FUNCTION ledger.check_entry_invariant(bigint) TO PUBLIC;

REVOKE ALL PRIVILEGES ON ledger.accounts FROM PUBLIC, ledgerly_banking;
REVOKE ALL PRIVILEGES ON ledger.journal_entries FROM PUBLIC, ledgerly_banking;
REVOKE ALL PRIVILEGES ON ledger.postings FROM PUBLIC, ledgerly_banking;
REVOKE ALL PRIVILEGES ON SEQUENCE ledger.journal_entries_id_seq FROM PUBLIC, ledgerly_banking;
REVOKE ALL PRIVILEGES ON SEQUENCE ledger.postings_id_seq FROM PUBLIC, ledgerly_banking;

GRANT SELECT, INSERT ON ledger.accounts TO ledgerly_ledger;
GRANT SELECT, INSERT ON ledger.journal_entries TO ledgerly_ledger;
GRANT SELECT, INSERT ON ledger.postings TO ledgerly_ledger;
GRANT USAGE, SELECT ON SEQUENCE ledger.journal_entries_id_seq TO ledgerly_ledger;
GRANT USAGE, SELECT ON SEQUENCE ledger.postings_id_seq TO ledgerly_ledger;
