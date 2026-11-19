CREATE TABLE link_token_hashes (
	hash TEXT NOT NULL PRIMARY KEY,
	id TEXT NOT NULL UNIQUE,
	user_id TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	used_at TEXT
);

CREATE TABLE step_ups (
	org_id TEXT NOT NULL REFERENCES orgs (id),
	id TEXT NOT NULL PRIMARY KEY,
	user_id TEXT NOT NULL,
	token_id TEXT NOT NULL DEFAULT '',
	session_id TEXT NOT NULL DEFAULT '',
	operation TEXT NOT NULL,
	project_id TEXT NOT NULL DEFAULT '',
	environment_id TEXT NOT NULL DEFAULT '',
	input_digest TEXT NOT NULL,
	confirm_hash TEXT,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	signed_in_at TEXT,
	approved_at TEXT,
	declined_at TEXT,
	used_at TEXT,
	CHECK ((token_id = '') <> (session_id = ''))
);

ALTER TABLE step_ups ENABLE ROW LEVEL SECURITY;
CREATE POLICY org_isolation ON step_ups
	USING (org_id = nullif(current_setting('app.org_id', true), ''))
	WITH CHECK (org_id = nullif(current_setting('app.org_id', true), ''));
