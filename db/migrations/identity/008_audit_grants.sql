GRANT USAGE ON SCHEMA audit TO ledgerly_identity;
GRANT INSERT, SELECT ON audit.entries TO ledgerly_identity;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA audit TO ledgerly_identity;
