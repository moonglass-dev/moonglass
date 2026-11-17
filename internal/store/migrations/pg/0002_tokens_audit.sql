CREATE TABLE tokens (
	id TEXT NOT NULL PRIMARY KEY,
	org_id TEXT NOT NULL REFERENCES orgs (id),
	user_id TEXT NOT NULL,
	name TEXT NOT NULL,
	last4 TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE token_scopes (
	token_id TEXT NOT NULL REFERENCES tokens (id),
	org_id TEXT NOT NULL REFERENCES orgs (id),
	project TEXT NOT NULL,
	environment TEXT NOT NULL,
	operation TEXT NOT NULL,
	PRIMARY KEY (token_id, project, environment, operation)
);

CREATE TABLE token_hashes (
	hash TEXT NOT NULL PRIMARY KEY,
	org_id TEXT NOT NULL REFERENCES orgs (id),
	token_id TEXT NOT NULL REFERENCES tokens (id),
	revoked_at TEXT
);

CREATE TABLE audit_events (
	org_id TEXT NOT NULL REFERENCES orgs (id),
	seq BIGINT NOT NULL,
	id TEXT NOT NULL,
	at TEXT NOT NULL,
	principal_kind TEXT NOT NULL,
	user_id TEXT NOT NULL,
	token_id TEXT NOT NULL,
	operation TEXT NOT NULL,
	outcome TEXT NOT NULL,
	reason TEXT NOT NULL,
	prev_hash TEXT NOT NULL,
	hash TEXT NOT NULL,
	PRIMARY KEY (org_id, seq)
);

CREATE INDEX tokens_org_id ON tokens (org_id);
CREATE INDEX token_scopes_org_id ON token_scopes (org_id);
CREATE INDEX token_hashes_org_id ON token_hashes (org_id);

ALTER TABLE tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE token_scopes ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_events ENABLE ROW LEVEL SECURITY;

CREATE POLICY org_isolation ON tokens
	USING (org_id = nullif(current_setting('app.org_id', true), ''))
	WITH CHECK (org_id = nullif(current_setting('app.org_id', true), ''));
CREATE POLICY org_isolation ON token_scopes
	USING (org_id = nullif(current_setting('app.org_id', true), ''))
	WITH CHECK (org_id = nullif(current_setting('app.org_id', true), ''));
CREATE POLICY org_isolation ON audit_events
	USING (org_id = nullif(current_setting('app.org_id', true), ''))
	WITH CHECK (org_id = nullif(current_setting('app.org_id', true), ''));
