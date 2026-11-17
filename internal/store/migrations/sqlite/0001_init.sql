CREATE TABLE orgs (
	id TEXT NOT NULL PRIMARY KEY,
	org_id TEXT NOT NULL,
	created_at TEXT NOT NULL,
	CHECK (org_id = id)
);

CREATE TABLE memberships (
	org_id TEXT NOT NULL REFERENCES orgs (id),
	user_id TEXT NOT NULL,
	role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
	created_at TEXT NOT NULL,
	PRIMARY KEY (org_id, user_id)
);

CREATE TABLE servers (
	id TEXT NOT NULL PRIMARY KEY,
	org_id TEXT NOT NULL REFERENCES orgs (id),
	created_at TEXT NOT NULL
);

CREATE TABLE user_identities (
	identity TEXT NOT NULL PRIMARY KEY,
	user_id TEXT NOT NULL
);

CREATE TABLE user_orgs (
	user_id TEXT NOT NULL,
	org_id TEXT NOT NULL REFERENCES orgs (id),
	PRIMARY KEY (user_id, org_id)
);

CREATE TABLE agent_keys (
	public_key TEXT NOT NULL PRIMARY KEY,
	server_id TEXT NOT NULL REFERENCES servers (id),
	org_id TEXT NOT NULL REFERENCES orgs (id),
	revoked_at TEXT
);

CREATE TABLE enrolment_token_hashes (
	token_hash TEXT NOT NULL PRIMARY KEY,
	org_id TEXT NOT NULL REFERENCES orgs (id),
	expires_at TEXT NOT NULL,
	used_at TEXT
);

CREATE INDEX memberships_user_id ON memberships (user_id);
CREATE INDEX servers_org_id ON servers (org_id);
CREATE INDEX enrolment_token_hashes_org_id ON enrolment_token_hashes (org_id);
