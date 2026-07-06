GRANT SELECT ON ledger.accounts TO PUBLIC;
GRANT SELECT, INSERT ON ledger.journal_entries TO PUBLIC;
GRANT SELECT, INSERT ON ledger.postings TO PUBLIC;
GRANT USAGE, SELECT ON SEQUENCE ledger.journal_entries_id_seq TO PUBLIC;
GRANT USAGE, SELECT ON SEQUENCE ledger.postings_id_seq TO PUBLIC;
