GRANT USAGE ON SCHEMA audit TO ledgerly_banking;
GRANT INSERT, SELECT ON audit.entries TO ledgerly_banking;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA audit TO ledgerly_banking;
