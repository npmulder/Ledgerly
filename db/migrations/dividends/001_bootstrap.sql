DO $ledgerly$
BEGIN
	EXECUTE 'CREATE SCHEMA IF NOT EXISTS dividends';
	EXECUTE 'REVOKE ALL ON SCHEMA dividends FROM PUBLIC';

	IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ledgerly_dividends') THEN
		EXECUTE 'CREATE ROLE ledgerly_dividends LOGIN PASSWORD ''ledgerly_dividends''';
	END IF;

	EXECUTE 'ALTER ROLE ledgerly_dividends WITH LOGIN PASSWORD ''ledgerly_dividends'' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION';
	EXECUTE 'GRANT USAGE, CREATE ON SCHEMA dividends TO ledgerly_dividends';
	EXECUTE 'GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA dividends TO ledgerly_dividends';
	EXECUTE 'GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA dividends TO ledgerly_dividends';
	EXECUTE 'ALTER DEFAULT PRIVILEGES IN SCHEMA dividends GRANT ALL PRIVILEGES ON TABLES TO ledgerly_dividends';
	EXECUTE 'ALTER DEFAULT PRIVILEGES IN SCHEMA dividends GRANT ALL PRIVILEGES ON SEQUENCES TO ledgerly_dividends';
	EXECUTE 'ALTER ROLE ledgerly_dividends SET search_path = dividends';
END
$ledgerly$;
