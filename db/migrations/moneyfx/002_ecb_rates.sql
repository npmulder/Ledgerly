CREATE TABLE IF NOT EXISTS moneyfx.ecb_rates (
	date date NOT NULL,
	currency text NOT NULL,
	rate numeric(18,8) NOT NULL,
	CONSTRAINT ecb_rates_currency_not_blank CHECK (btrim(currency) <> ''),
	CONSTRAINT ecb_rates_currency_upper CHECK (currency = upper(currency)),
	CONSTRAINT ecb_rates_rate_positive CHECK (rate > 0),
	CONSTRAINT ecb_rates_date_currency_key UNIQUE (date, currency)
);

GRANT ALL PRIVILEGES ON TABLE moneyfx.ecb_rates TO ledgerly_moneyfx;
