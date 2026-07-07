GRANT USAGE ON SCHEMA audit TO ledgerly_invoicing;
GRANT INSERT, SELECT ON audit.entries TO ledgerly_invoicing;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA audit TO ledgerly_invoicing;
