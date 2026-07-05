DO $ledgerly$
BEGIN
	EXECUTE 'CREATE SCHEMA IF NOT EXISTS demo';
	EXECUTE 'REVOKE ALL ON SCHEMA demo FROM PUBLIC';

	IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ledgerly_demo') THEN
		EXECUTE 'CREATE ROLE ledgerly_demo LOGIN PASSWORD ''ledgerly_demo''';
	END IF;

	EXECUTE 'ALTER ROLE ledgerly_demo WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION';
	EXECUTE 'GRANT USAGE, CREATE ON SCHEMA demo TO ledgerly_demo';
	EXECUTE 'GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA demo TO ledgerly_demo';
	EXECUTE 'GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA demo TO ledgerly_demo';
	EXECUTE 'ALTER DEFAULT PRIVILEGES IN SCHEMA demo GRANT ALL PRIVILEGES ON TABLES TO ledgerly_demo';
	EXECUTE 'ALTER DEFAULT PRIVILEGES IN SCHEMA demo GRANT ALL PRIVILEGES ON SEQUENCES TO ledgerly_demo';
	EXECUTE 'ALTER ROLE ledgerly_demo SET search_path = demo';
END
$ledgerly$;

CREATE TABLE IF NOT EXISTS demo.notes (
	id text PRIMARY KEY,
	kind text NOT NULL CHECK (kind IN ('note', 'audit')),
	note_id text REFERENCES demo.notes(id),
	body text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	CHECK (
		(kind = 'note' AND note_id IS NULL)
		OR (kind = 'audit' AND note_id IS NOT NULL)
	)
);

CREATE INDEX IF NOT EXISTS notes_kind_created_at_idx ON demo.notes (kind, created_at, id);
CREATE INDEX IF NOT EXISTS notes_note_id_idx ON demo.notes (note_id) WHERE note_id IS NOT NULL;

GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA demo TO ledgerly_demo;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA demo TO ledgerly_demo;
