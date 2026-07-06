CREATE TABLE IF NOT EXISTS advisor.evaluation_runs (
	id bigserial PRIMARY KEY,
	trigger text NOT NULL,
	started_at timestamptz NOT NULL,
	finished_at timestamptz NOT NULL,
	duration_ms bigint NOT NULL,
	insights_created integer NOT NULL DEFAULT 0,
	insights_superseded integer NOT NULL DEFAULT 0,
	insights_resolved integer NOT NULL DEFAULT 0,
	error text,
	warnings jsonb NOT NULL DEFAULT '[]'::jsonb,
	CONSTRAINT evaluation_runs_trigger_not_blank CHECK (trigger = btrim(trigger) AND trigger <> ''),
	CONSTRAINT evaluation_runs_duration_nonnegative CHECK (duration_ms >= 0),
	CONSTRAINT evaluation_runs_counts_nonnegative CHECK (
		insights_created >= 0
		AND insights_superseded >= 0
		AND insights_resolved >= 0
	),
	CONSTRAINT evaluation_runs_time_order CHECK (finished_at >= started_at),
	CONSTRAINT evaluation_runs_warnings_array CHECK (jsonb_typeof(warnings) = 'array')
);

CREATE INDEX IF NOT EXISTS evaluation_runs_started_at_idx
	ON advisor.evaluation_runs (started_at DESC, id DESC);

COMMENT ON TABLE advisor.evaluation_runs IS
	'Whole-set advisor evaluation audit log with trigger, duration, insight delta counts, warnings, and optional failure text.';

REVOKE ALL PRIVILEGES ON advisor.evaluation_runs FROM PUBLIC;
REVOKE ALL PRIVILEGES ON advisor.evaluation_runs FROM ledgerly_advisor;
GRANT SELECT, INSERT ON advisor.evaluation_runs TO PUBLIC;
GRANT SELECT, INSERT ON advisor.evaluation_runs TO ledgerly_advisor;

GRANT USAGE, SELECT ON SEQUENCE advisor.evaluation_runs_id_seq TO PUBLIC;
GRANT USAGE, SELECT ON SEQUENCE advisor.evaluation_runs_id_seq TO ledgerly_advisor;
