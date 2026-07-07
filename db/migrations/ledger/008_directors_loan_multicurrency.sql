UPDATE ledger.accounts
SET currency = NULL
WHERE code = '2300-directors-loan'
	AND currency IS NOT NULL;

COMMENT ON COLUMN ledger.accounts.currency IS
	'Optional account currency guard; NULL permits module-controlled multi-currency postings such as VAT control and DLA entries with locked GBP presentation.';
