CREATE TABLE IF NOT EXISTS identity.pats (
	id bigserial PRIMARY KEY,
	token_sha256 bytea NOT NULL UNIQUE,
	user_id bigint NOT NULL REFERENCES identity.users(id) ON DELETE CASCADE,
	name text NOT NULL,
	scope text NOT NULL CHECK (scope IN ('read-only', 'full')),
	created_at timestamptz NOT NULL DEFAULT now(),
	last_used_at timestamptz,
	expires_at timestamptz
);

CREATE INDEX IF NOT EXISTS pats_user_id_idx ON identity.pats (user_id);
CREATE INDEX IF NOT EXISTS pats_expires_at_idx ON identity.pats (expires_at);
