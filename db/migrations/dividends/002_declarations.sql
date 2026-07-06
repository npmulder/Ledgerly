CREATE TABLE IF NOT EXISTS dividends.declarations (
	id text PRIMARY KEY,
	declared_date date NOT NULL,
	amount bigint NOT NULL,
	amount_currency text NOT NULL DEFAULT 'GBP',
	per_share_amount bigint NOT NULL,
	per_share_currency text NOT NULL DEFAULT 'GBP',
	shares integer NOT NULL,
	shareholder_name text NOT NULL,
	voucher_asset text,
	minutes_asset text,
	created_at timestamptz NOT NULL DEFAULT now(),
	CONSTRAINT declarations_id_not_blank CHECK (id = btrim(id) AND id <> ''),
	CONSTRAINT declarations_amount_positive CHECK (amount > 0),
	CONSTRAINT declarations_amount_currency_gbp CHECK (amount_currency = 'GBP'),
	CONSTRAINT declarations_per_share_positive CHECK (per_share_amount > 0),
	CONSTRAINT declarations_per_share_currency_gbp CHECK (per_share_currency = 'GBP'),
	CONSTRAINT declarations_shares_positive CHECK (shares > 0),
	CONSTRAINT declarations_shareholder_name_not_blank CHECK (shareholder_name = btrim(shareholder_name) AND shareholder_name <> ''),
	CONSTRAINT declarations_voucher_asset_not_blank CHECK (voucher_asset IS NULL OR (voucher_asset = btrim(voucher_asset) AND voucher_asset <> '')),
	CONSTRAINT declarations_minutes_asset_not_blank CHECK (minutes_asset IS NULL OR (minutes_asset = btrim(minutes_asset) AND minutes_asset <> ''))
);

COMMENT ON TABLE dividends.declarations IS
	'Immutable dividend declarations. Headroom is derived live and never stored.';

CREATE INDEX IF NOT EXISTS declarations_declared_date_created_id_idx
	ON dividends.declarations (declared_date DESC, created_at DESC, id DESC);

REVOKE ALL PRIVILEGES ON dividends.declarations FROM PUBLIC;
REVOKE ALL PRIVILEGES ON dividends.declarations FROM ledgerly_dividends;
GRANT SELECT, INSERT, UPDATE ON dividends.declarations TO ledgerly_dividends;
