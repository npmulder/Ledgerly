CREATE TABLE IF NOT EXISTS identity.company_profile (
	id integer PRIMARY KEY DEFAULT 1,
	trading_name text NOT NULL,
	legal_name text NOT NULL,
	company_number text NOT NULL,
	registered_office jsonb NOT NULL,
	incorporation_date date NOT NULL,
	year_end_month smallint NOT NULL,
	year_end_day smallint NOT NULL,
	vat_number text,
	bank_details jsonb NOT NULL DEFAULT '{}'::jsonb,
	shareholders jsonb NOT NULL DEFAULT '[]'::jsonb,
	logo_asset_id uuid,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	CONSTRAINT company_profile_singleton CHECK (id = 1),
	CONSTRAINT company_profile_company_number_nonempty CHECK (btrim(company_number) <> ''),
	CONSTRAINT company_profile_registered_office_object CHECK (jsonb_typeof(registered_office) = 'object'),
	CONSTRAINT company_profile_bank_details_object CHECK (jsonb_typeof(bank_details) = 'object'),
	CONSTRAINT company_profile_shareholders_array CHECK (jsonb_typeof(shareholders) = 'array'),
	CONSTRAINT company_profile_year_end_month_check CHECK (year_end_month BETWEEN 1 AND 12),
	CONSTRAINT company_profile_year_end_day_check CHECK (
		(year_end_month IN (1, 3, 5, 7, 8, 10, 12) AND year_end_day BETWEEN 1 AND 31)
		OR (year_end_month IN (4, 6, 9, 11) AND year_end_day BETWEEN 1 AND 30)
		OR (year_end_month = 2 AND year_end_day BETWEEN 1 AND 29)
	)
);

GRANT ALL PRIVILEGES ON TABLE identity.company_profile TO ledgerly_identity;

DO $ledgerly_seed$
BEGIN
	IF current_database() = 'ledgerly_dev'
		OR current_database() = 'ledgerly_test'
		OR current_database() LIKE 'ledgerly\_test\_%' ESCAPE '\'
		OR current_database() LIKE '%\_test' ESCAPE '\'
	THEN
		INSERT INTO identity.company_profile (
			id,
			trading_name,
			legal_name,
			company_number,
			registered_office,
			incorporation_date,
			year_end_month,
			year_end_day,
			vat_number,
			bank_details,
			shareholders
		)
		VALUES (
			1,
			'NPM Limited',
			'NPM Limited',
			'137792C',
			jsonb_build_object(
				'line1', '18 Athol St',
				'line2', '',
				'locality', 'Douglas',
				'region', '',
				'postal_code', '',
				'country', 'IM'
			),
			DATE '2020-07-14',
			3,
			31,
			NULL,
			jsonb_build_object(
				'iban', '',
				'bic', '',
				'bank_name', ''
			),
			jsonb_build_array(
				jsonb_build_object(
					'name', 'N. Meyer',
					'shares', 100,
					'class', 'ordinary £1'
				)
			)
		)
		ON CONFLICT (id) DO NOTHING;
	END IF;
END
$ledgerly_seed$;
