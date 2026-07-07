ALTER TABLE identity.company_profile
	ADD COLUMN IF NOT EXISTS directors jsonb NOT NULL DEFAULT '[]'::jsonb;

UPDATE identity.company_profile
SET directors = jsonb_build_array(
	jsonb_build_object(
		'name', btrim(shareholders->0->>'name'),
		'is_chair', true
	)
)
WHERE jsonb_typeof(directors) = 'array'
	AND jsonb_array_length(directors) = 0
	AND jsonb_typeof(shareholders) = 'array'
	AND jsonb_array_length(shareholders) > 0
	AND NULLIF(btrim(shareholders->0->>'name'), '') IS NOT NULL;

DO $ledgerly_directors_constraint$
BEGIN
	IF NOT EXISTS (
		SELECT 1
		FROM pg_constraint
		WHERE conname = 'company_profile_directors_array'
			AND conrelid = 'identity.company_profile'::regclass
	) THEN
		ALTER TABLE identity.company_profile
			ADD CONSTRAINT company_profile_directors_array CHECK (jsonb_typeof(directors) = 'array');
	END IF;
END
$ledgerly_directors_constraint$;
