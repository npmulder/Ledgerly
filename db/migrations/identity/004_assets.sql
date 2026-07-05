CREATE TABLE IF NOT EXISTS identity.assets (
	id uuid PRIMARY KEY,
	sha256 text NOT NULL,
	mime text NOT NULL,
	size bigint NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	CONSTRAINT assets_sha256_hex CHECK (sha256 ~ '^[0-9a-f]{64}$'),
	CONSTRAINT assets_mime_nonempty CHECK (btrim(mime) <> ''),
	CONSTRAINT assets_size_nonnegative CHECK (size >= 0)
);

CREATE INDEX IF NOT EXISTS assets_sha256_idx ON identity.assets (sha256);

GRANT ALL PRIVILEGES ON TABLE identity.assets TO ledgerly_identity;

DO $ledgerly_seed$
BEGIN
	IF current_database() = 'ledgerly_dev'
		OR current_database() = 'ledgerly_test'
		OR current_database() LIKE 'ledgerly\_test\_%' ESCAPE '\'
		OR current_database() LIKE '%\_test' ESCAPE '\'
	THEN
		INSERT INTO identity.assets (
			id,
			sha256,
			mime,
			size
		)
		VALUES (
			'17830098-8109-4a00-8b00-000000000001',
			'8d2bd59537987e78dd8259ad3b12b3a897e0eabb1306b05c2aa6a93cb51b1948',
			'image/png',
			508905
		)
		ON CONFLICT (id) DO NOTHING;

		UPDATE identity.company_profile
		SET logo_asset_id = '17830098-8109-4a00-8b00-000000000001',
			updated_at = now()
		WHERE id = 1
			AND logo_asset_id IS NULL;
	END IF;
END
$ledgerly_seed$;

ALTER TABLE identity.company_profile
	ADD CONSTRAINT company_profile_logo_asset_id_fk
	FOREIGN KEY (logo_asset_id)
	REFERENCES identity.assets (id);
