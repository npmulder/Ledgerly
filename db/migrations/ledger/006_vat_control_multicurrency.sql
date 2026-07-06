UPDATE ledger.accounts
SET currency = NULL
WHERE code = '2200-vat-control'
	AND currency IS NOT NULL;

COMMENT ON COLUMN ledger.accounts.currency IS
	'Optional account currency guard; NULL permits module-controlled multi-currency postings such as VAT control with locked GBP presentation.';
