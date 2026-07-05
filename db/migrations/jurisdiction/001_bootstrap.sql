DO $ledgerly$
BEGIN
	EXECUTE 'CREATE SCHEMA IF NOT EXISTS jurisdiction';
	EXECUTE 'REVOKE ALL ON SCHEMA jurisdiction FROM PUBLIC';

	IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ledgerly_jurisdiction') THEN
		EXECUTE 'CREATE ROLE ledgerly_jurisdiction LOGIN PASSWORD ''ledgerly_jurisdiction''';
	END IF;

	EXECUTE 'ALTER ROLE ledgerly_jurisdiction WITH LOGIN PASSWORD ''ledgerly_jurisdiction'' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION';
	EXECUTE 'GRANT USAGE, CREATE ON SCHEMA jurisdiction TO ledgerly_jurisdiction';
	EXECUTE 'GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA jurisdiction TO ledgerly_jurisdiction';
	EXECUTE 'GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA jurisdiction TO ledgerly_jurisdiction';
	EXECUTE 'ALTER DEFAULT PRIVILEGES IN SCHEMA jurisdiction GRANT ALL PRIVILEGES ON TABLES TO ledgerly_jurisdiction';
	EXECUTE 'ALTER DEFAULT PRIVILEGES IN SCHEMA jurisdiction GRANT ALL PRIVILEGES ON SEQUENCES TO ledgerly_jurisdiction';
	EXECUTE 'ALTER ROLE ledgerly_jurisdiction SET search_path = jurisdiction';
END
$ledgerly$;
