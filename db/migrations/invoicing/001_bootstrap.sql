DO $ledgerly$
BEGIN
	EXECUTE 'CREATE SCHEMA IF NOT EXISTS invoicing';
	EXECUTE 'REVOKE ALL ON SCHEMA invoicing FROM PUBLIC';

	IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ledgerly_invoicing') THEN
		EXECUTE 'CREATE ROLE ledgerly_invoicing LOGIN PASSWORD ''ledgerly_invoicing''';
	END IF;

	EXECUTE 'ALTER ROLE ledgerly_invoicing WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION';
	EXECUTE 'GRANT USAGE, CREATE ON SCHEMA invoicing TO ledgerly_invoicing';
	EXECUTE 'GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA invoicing TO ledgerly_invoicing';
	EXECUTE 'GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA invoicing TO ledgerly_invoicing';
	EXECUTE 'ALTER DEFAULT PRIVILEGES IN SCHEMA invoicing GRANT ALL PRIVILEGES ON TABLES TO ledgerly_invoicing';
	EXECUTE 'ALTER DEFAULT PRIVILEGES IN SCHEMA invoicing GRANT ALL PRIVILEGES ON SEQUENCES TO ledgerly_invoicing';
	EXECUTE 'ALTER ROLE ledgerly_invoicing SET search_path = invoicing';
END
$ledgerly$;

CREATE TABLE IF NOT EXISTS invoicing.clients (
	id text PRIMARY KEY,
	name text NOT NULL,
	address jsonb NOT NULL,
	vat_number text,
	default_currency text NOT NULL,
	terms_days smallint NOT NULL,
	vat_treatment text NOT NULL,
	retainer_amount_minor bigint,
	retainer_currency text,
	day_rate_amount_minor bigint,
	day_rate_currency text,
	created_at timestamptz NOT NULL DEFAULT now(),
	archived_at timestamptz,
	CONSTRAINT clients_id_nonempty CHECK (btrim(id) <> ''),
	CONSTRAINT clients_name_nonempty CHECK (btrim(name) <> ''),
	CONSTRAINT clients_address_object CHECK (jsonb_typeof(address) = 'object'),
	CONSTRAINT clients_default_currency_check CHECK (default_currency IN ('EUR', 'GBP')),
	CONSTRAINT clients_terms_days_check CHECK (terms_days IN (14, 30)),
	CONSTRAINT clients_vat_treatment_check CHECK (vat_treatment IN ('domestic', 'reverse-charge-eu-b2b')),
	CONSTRAINT clients_retainer_pair_check CHECK ((retainer_amount_minor IS NULL) = (retainer_currency IS NULL)),
	CONSTRAINT clients_retainer_amount_positive CHECK (retainer_amount_minor IS NULL OR retainer_amount_minor > 0),
	CONSTRAINT clients_retainer_currency_check CHECK (retainer_currency IS NULL OR retainer_currency IN ('EUR', 'GBP')),
	CONSTRAINT clients_retainer_currency_matches_default CHECK (retainer_currency IS NULL OR retainer_currency = default_currency),
	CONSTRAINT clients_day_rate_pair_check CHECK ((day_rate_amount_minor IS NULL) = (day_rate_currency IS NULL)),
	CONSTRAINT clients_day_rate_amount_positive CHECK (day_rate_amount_minor IS NULL OR day_rate_amount_minor > 0),
	CONSTRAINT clients_day_rate_currency_check CHECK (day_rate_currency IS NULL OR day_rate_currency IN ('EUR', 'GBP')),
	CONSTRAINT clients_day_rate_currency_matches_default CHECK (day_rate_currency IS NULL OR day_rate_currency = default_currency)
);

CREATE INDEX IF NOT EXISTS clients_active_name_idx ON invoicing.clients (lower(name), created_at, id) WHERE archived_at IS NULL;
CREATE INDEX IF NOT EXISTS clients_archived_at_idx ON invoicing.clients (archived_at) WHERE archived_at IS NOT NULL;

GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA invoicing TO ledgerly_invoicing;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA invoicing TO ledgerly_invoicing;
