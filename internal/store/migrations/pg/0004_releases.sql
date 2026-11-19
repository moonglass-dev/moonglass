CREATE TABLE releases (
	id TEXT NOT NULL PRIMARY KEY,
	org_id TEXT NOT NULL REFERENCES orgs (id),
	service_id TEXT NOT NULL REFERENCES services (id),
	image_ref TEXT NOT NULL DEFAULT '',
	git_repository TEXT NOT NULL DEFAULT '',
	git_commit TEXT NOT NULL DEFAULT '',
	port INTEGER NOT NULL,
	health_path TEXT NOT NULL DEFAULT '',
	domains TEXT NOT NULL,
	pre_deploy TEXT NOT NULL DEFAULT '',
	pre_deploy_reversible BOOLEAN NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'rejected', 'superseded')),
	step TEXT NOT NULL DEFAULT '' CHECK (step IN ('', 'build', 'deploy', 'pre_deploy', 'switch')),
	request_id TEXT NOT NULL DEFAULT '',
	image_id TEXT NOT NULL DEFAULT '',
	repo_digest TEXT NOT NULL DEFAULT '',
	detail TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	finished_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX releases_one_queued ON releases (org_id, service_id) WHERE status = 'queued';
CREATE UNIQUE INDEX releases_one_running ON releases (org_id, service_id) WHERE status = 'running';
ALTER TABLE releases ENABLE ROW LEVEL SECURITY;
CREATE POLICY org_isolation ON releases
	USING (org_id = nullif(current_setting('app.org_id', true), ''))
	WITH CHECK (org_id = nullif(current_setting('app.org_id', true), ''));
