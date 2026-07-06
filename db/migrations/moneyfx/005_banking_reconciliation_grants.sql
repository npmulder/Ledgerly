GRANT USAGE ON SCHEMA moneyfx TO ledgerly_banking;
GRANT SELECT ON moneyfx.ecb_rates TO ledgerly_banking;
GRANT SELECT ON moneyfx.rate_locks TO ledgerly_banking;
GRANT SELECT, INSERT ON moneyfx.realised_fx TO ledgerly_banking;
GRANT USAGE, SELECT ON SEQUENCE moneyfx.realised_fx_id_seq TO ledgerly_banking;
