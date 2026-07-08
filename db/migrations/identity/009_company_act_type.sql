ALTER TABLE identity.company_profile
	ADD COLUMN IF NOT EXISTS act_type text;

DO $ledgerly_company_act_type_constraint$
BEGIN
	IF NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'company_profile_act_type_nonempty'
			AND conrelid = 'identity.company_profile'::regclass
	) THEN
		ALTER TABLE identity.company_profile
			ADD CONSTRAINT company_profile_act_type_nonempty CHECK (
				act_type IS NULL OR btrim(act_type) <> ''
			);
	END IF;
END
$ledgerly_company_act_type_constraint$;
