ALTER TABLE services ADD COLUMN current_release_id TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN previous_release_id TEXT;

CREATE TABLE rollbacks (
	id TEXT NOT NULL PRIMARY KEY,
	org_id TEXT NOT NULL REFERENCES orgs (id),
	service_id TEXT NOT NULL REFERENCES services (id),
	request_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'rejected')),
	detail TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	finished_at TEXT NOT NULL DEFAULT ''
);

ALTER TABLE rollbacks ENABLE ROW LEVEL SECURITY;
CREATE POLICY org_isolation ON rollbacks
	USING (org_id = nullif(current_setting('app.org_id', true), ''))
	WITH CHECK (org_id = nullif(current_setting('app.org_id', true), ''));
