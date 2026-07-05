CREATE TABLE IF NOT EXISTS identity.users (
	id bigserial PRIMARY KEY,
	email text NOT NULL UNIQUE,
	password_hash text NOT NULL,
	name text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS identity.sessions (
	token_sha256 bytea PRIMARY KEY,
	user_id bigint NOT NULL REFERENCES identity.users(id) ON DELETE CASCADE,
	expires_at timestamptz NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS sessions_user_id_idx ON identity.sessions (user_id);
CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON identity.sessions (expires_at);
