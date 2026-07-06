CREATE TABLE IF NOT EXISTS advisor.insights (
	key text PRIMARY KEY,
	rule_id text NOT NULL,
	fact_hash text NOT NULL,
	severity text NOT NULL,
	surfaces text[] NOT NULL,
	rendered_text text NOT NULL,
	bindings jsonb NOT NULL,
	cta jsonb NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	resolved_at timestamptz,
	resolution text,
	superseded_by text REFERENCES advisor.insights (key),
	CONSTRAINT insights_key_not_blank CHECK (key = btrim(key) AND key <> ''),
	CONSTRAINT insights_rule_id_not_blank CHECK (rule_id = btrim(rule_id) AND rule_id <> ''),
	CONSTRAINT insights_fact_hash_not_blank CHECK (fact_hash = btrim(fact_hash) AND fact_hash <> ''),
	CONSTRAINT insights_severity_check CHECK (severity IN ('teal', 'amber')),
	CONSTRAINT insights_surfaces_not_empty CHECK (cardinality(surfaces) > 0),
	CONSTRAINT insights_rendered_text_not_blank CHECK (rendered_text = btrim(rendered_text) AND rendered_text <> ''),
	CONSTRAINT insights_resolution_check CHECK (resolution IS NULL OR resolution IN ('superseded', 'no_longer_firing')),
	CONSTRAINT insights_resolved_resolution_pair CHECK ((resolved_at IS NULL) = (resolution IS NULL)),
	CONSTRAINT insights_superseded_by_resolution_check CHECK (superseded_by IS NULL OR resolution = 'superseded')
);

CREATE INDEX IF NOT EXISTS insights_active_rule_idx
	ON advisor.insights (rule_id)
	WHERE resolved_at IS NULL;

CREATE INDEX IF NOT EXISTS insights_active_surfaces_idx
	ON advisor.insights USING gin (surfaces)
	WHERE resolved_at IS NULL;

CREATE TABLE IF NOT EXISTS advisor.dismissals (
	insight_key text PRIMARY KEY REFERENCES advisor.insights (key) ON DELETE CASCADE,
	dismissed_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE advisor.insights IS
	'Deterministic advisor rule firings keyed by rule id plus canonical fact binding hash. Resolved rows are retained for audit.';

COMMENT ON TABLE advisor.dismissals IS
	'Dismissed insight keys stay suppressed until a fact change creates a different insight key.';

GRANT USAGE ON SCHEMA advisor TO PUBLIC;

REVOKE ALL PRIVILEGES ON advisor.insights FROM PUBLIC;
REVOKE ALL PRIVILEGES ON advisor.insights FROM ledgerly_advisor;
GRANT SELECT, INSERT, UPDATE ON advisor.insights TO PUBLIC;
GRANT SELECT, INSERT, UPDATE ON advisor.insights TO ledgerly_advisor;

REVOKE ALL PRIVILEGES ON advisor.dismissals FROM PUBLIC;
REVOKE ALL PRIVILEGES ON advisor.dismissals FROM ledgerly_advisor;
GRANT SELECT, INSERT, UPDATE ON advisor.dismissals TO PUBLIC;
GRANT SELECT, INSERT, UPDATE ON advisor.dismissals TO ledgerly_advisor;
