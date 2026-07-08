ALTER TABLE dla.dla_entries
	ADD COLUMN IF NOT EXISTS director text NOT NULL DEFAULT 'director-1';

DO $ledgerly_dla_director_constraint$
BEGIN
	IF NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'dla_entries_director_identifier'
			AND conrelid = 'dla.dla_entries'::regclass
	) THEN
		ALTER TABLE dla.dla_entries
			ADD CONSTRAINT dla_entries_director_identifier
			CHECK (director ~ '^director-[1-9][0-9]*$');
	END IF;
END
$ledgerly_dla_director_constraint$;

COMMENT ON COLUMN dla.dla_entries.director IS
	'Stable director identifier from identity.company_profile.directors[].id. Existing shared-ledger rows migrate to director-1.';

CREATE INDEX IF NOT EXISTS dla_entries_director_date_id_idx
	ON dla.dla_entries (director, date, id);
