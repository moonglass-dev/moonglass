CREATE TABLE projects (
	id TEXT NOT NULL PRIMARY KEY,
	org_id TEXT NOT NULL REFERENCES orgs (id),
	name TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE (org_id, name)
);

CREATE TABLE environments (
	id TEXT NOT NULL PRIMARY KEY,
	org_id TEXT NOT NULL REFERENCES orgs (id),
	project_id TEXT NOT NULL REFERENCES projects (id),
	name TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE (org_id, project_id, name)
);

CREATE TABLE services (
	id TEXT NOT NULL PRIMARY KEY,
	org_id TEXT NOT NULL REFERENCES orgs (id),
	project_id TEXT NOT NULL REFERENCES projects (id),
	environment_id TEXT NOT NULL REFERENCES environments (id),
	name TEXT NOT NULL,
	server_id TEXT NOT NULL REFERENCES servers (id),
	created_at TEXT NOT NULL,
	UNIQUE (org_id, environment_id, name)
);

ALTER TABLE projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE environments ENABLE ROW LEVEL SECURITY;
ALTER TABLE services ENABLE ROW LEVEL SECURITY;

CREATE POLICY org_isolation ON projects
	USING (org_id = nullif(current_setting('app.org_id', true), ''))
	WITH CHECK (org_id = nullif(current_setting('app.org_id', true), ''));
CREATE POLICY org_isolation ON environments
	USING (org_id = nullif(current_setting('app.org_id', true), ''))
	WITH CHECK (org_id = nullif(current_setting('app.org_id', true), ''));
CREATE POLICY org_isolation ON services
	USING (org_id = nullif(current_setting('app.org_id', true), ''))
	WITH CHECK (org_id = nullif(current_setting('app.org_id', true), ''));
