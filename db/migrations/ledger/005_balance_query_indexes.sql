ALTER TABLE ledger.postings
	ADD COLUMN IF NOT EXISTS entry_date date;

UPDATE ledger.postings AS p
SET entry_date = je.date
FROM ledger.journal_entries AS je
WHERE p.entry_id = je.id
	AND p.entry_date IS NULL;

CREATE OR REPLACE FUNCTION ledger.set_posting_entry_date()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ledger, pg_temp
AS $ledgerly$
DECLARE
	parent_entry_date date;
BEGIN
	SELECT je.date
	INTO parent_entry_date
	FROM ledger.journal_entries AS je
	WHERE je.id = NEW.entry_id;

	IF parent_entry_date IS NULL THEN
		RAISE EXCEPTION 'ledger posting references missing journal entry %', NEW.entry_id
			USING ERRCODE = '23503';
	END IF;

	IF NEW.entry_date IS NULL THEN
		NEW.entry_date := parent_entry_date;
	ELSIF NEW.entry_date <> parent_entry_date THEN
		RAISE EXCEPTION 'ledger posting entry_date % does not match journal entry % date %',
			NEW.entry_date,
			NEW.entry_id,
			parent_entry_date
			USING ERRCODE = '23514';
	END IF;

	RETURN NEW;
END;
$ledgerly$;

DROP TRIGGER IF EXISTS postings_entry_date_tg ON ledger.postings;
CREATE TRIGGER postings_entry_date_tg
	BEFORE INSERT ON ledger.postings
	FOR EACH ROW
	EXECUTE FUNCTION ledger.set_posting_entry_date();

ALTER TABLE ledger.postings
	ALTER COLUMN entry_date SET NOT NULL;

CREATE INDEX IF NOT EXISTS postings_account_entry_date_covering_idx
	ON ledger.postings (account_code, entry_date, currency, entry_id, id)
	INCLUDE (amount, amount_gbp);

CREATE INDEX IF NOT EXISTS postings_entry_date_account_covering_idx
	ON ledger.postings (entry_date, account_code, currency, entry_id, id)
	INCLUDE (amount, amount_gbp);
