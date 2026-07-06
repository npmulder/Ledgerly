ALTER TABLE dividends.declarations
	ADD COLUMN IF NOT EXISTS company_snapshot jsonb,
	ADD COLUMN IF NOT EXISTS shareholder_snapshot jsonb,
	ADD COLUMN IF NOT EXISTS headroom_snapshot jsonb,
	ADD COLUMN IF NOT EXISTS withholding_snapshot jsonb;

ALTER TABLE dividends.declarations
	DROP CONSTRAINT IF EXISTS declarations_company_snapshot_object,
	ADD CONSTRAINT declarations_company_snapshot_object
		CHECK (company_snapshot IS NULL OR jsonb_typeof(company_snapshot) = 'object'),
	DROP CONSTRAINT IF EXISTS declarations_shareholder_snapshot_object,
	ADD CONSTRAINT declarations_shareholder_snapshot_object
		CHECK (shareholder_snapshot IS NULL OR jsonb_typeof(shareholder_snapshot) = 'object'),
	DROP CONSTRAINT IF EXISTS declarations_headroom_snapshot_object,
	ADD CONSTRAINT declarations_headroom_snapshot_object
		CHECK (headroom_snapshot IS NULL OR jsonb_typeof(headroom_snapshot) = 'object'),
	DROP CONSTRAINT IF EXISTS declarations_withholding_snapshot_object,
	ADD CONSTRAINT declarations_withholding_snapshot_object
		CHECK (withholding_snapshot IS NULL OR jsonb_typeof(withholding_snapshot) = 'object');

COMMENT ON COLUMN dividends.declarations.company_snapshot IS
	'Declaration-time company identity snapshot used by immutable dividend documents.';
COMMENT ON COLUMN dividends.declarations.shareholder_snapshot IS
	'Declaration-time shareholder snapshot used by immutable dividend documents.';
COMMENT ON COLUMN dividends.declarations.headroom_snapshot IS
	'Declaration-time distributable-reserves snapshot used by board minutes.';
COMMENT ON COLUMN dividends.declarations.withholding_snapshot IS
	'Declaration-time dividend withholding policy and document note.';

GRANT UPDATE (
	voucher_asset,
	minutes_asset
) ON dividends.declarations TO ledgerly_dividends;
