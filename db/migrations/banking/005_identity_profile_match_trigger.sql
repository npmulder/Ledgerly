ALTER TABLE banking.match_engine_runs
	DROP CONSTRAINT IF EXISTS match_engine_runs_trigger_check;

ALTER TABLE banking.match_engine_runs
	ADD CONSTRAINT match_engine_runs_trigger_check CHECK (
		trigger IN ('import-completion', 'invoicing.InvoiceSent', 'identity.ProfileUpdated', 'manual-refresh')
	);
