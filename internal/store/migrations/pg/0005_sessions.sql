CREATE TABLE session_hashes (
	hash TEXT NOT NULL PRIMARY KEY,
	id TEXT NOT NULL UNIQUE,
	user_id TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	revoked_at TEXT
);

ALTER TABLE audit_events ADD COLUMN session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_events ADD COLUMN step_up_id TEXT NOT NULL DEFAULT '';
