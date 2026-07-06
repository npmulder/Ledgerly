ALTER TABLE ledger.journal_entries
	ADD COLUMN IF NOT EXISTS reversal_of bigint;

DO $ledgerly$
BEGIN
	IF NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'journal_entries_reversal_of_fkey'
			AND conrelid = 'ledger.journal_entries'::regclass
	) THEN
		ALTER TABLE ledger.journal_entries
			ADD CONSTRAINT journal_entries_reversal_of_fkey
			FOREIGN KEY (reversal_of)
			REFERENCES ledger.journal_entries(id)
			ON DELETE RESTRICT;
	END IF;
END
$ledgerly$;

CREATE UNIQUE INDEX IF NOT EXISTS journal_entries_reversal_of_unique_idx
	ON ledger.journal_entries (reversal_of)
	WHERE reversal_of IS NOT NULL;
