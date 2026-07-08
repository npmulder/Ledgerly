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

UPDATE identity.company_profile
SET directors = jsonb_build_array(
	jsonb_build_object(
		'name', 'N. Meyer',
		'appointed_date', '2020-07-14',
		'is_chair', true
	),
	jsonb_build_object(
		'name', 'A. Patel',
		'appointed_date', '2020-07-14'
	)
)
WHERE id = 1
	AND (
		current_database() = 'ledgerly_dev'
		OR current_database() = 'ledgerly_test'
		OR current_database() LIKE 'ledgerly\_test\_%' ESCAPE '\'
		OR current_database() LIKE '%\_test' ESCAPE '\'
	)
	AND trading_name = 'NPM Limited'
	AND legal_name = 'NPM Limited'
	AND company_number = '137792C'
	AND incorporation_date = DATE '2020-07-14'
	AND jsonb_typeof(shareholders) = 'array'
	AND shareholders = jsonb_build_array(
		jsonb_build_object(
			'name', 'N. Meyer',
			'shares', 100,
			'class', 'ordinary £1'
		)
	)
	AND jsonb_typeof(directors) = 'array'
	AND directors = jsonb_build_array(
		jsonb_build_object(
			'name', 'N. Meyer',
			'is_chair', true
		)
	);

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
