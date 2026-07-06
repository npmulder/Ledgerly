GRANT USAGE ON SCHEMA ledger TO ledgerly_banking;
GRANT SELECT ON ledger.accounts TO ledgerly_banking;
GRANT SELECT, INSERT ON ledger.journal_entries TO ledgerly_banking;
GRANT SELECT, INSERT ON ledger.postings TO ledgerly_banking;
GRANT USAGE, SELECT ON SEQUENCE ledger.journal_entries_id_seq TO ledgerly_banking;
GRANT USAGE, SELECT ON SEQUENCE ledger.postings_id_seq TO ledgerly_banking;
