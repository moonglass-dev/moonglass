CREATE TABLE releases (
	app_id TEXT NOT NULL,
	release_id TEXT NOT NULL,
	image_ref TEXT NOT NULL,
	image_id TEXT NOT NULL,
	repo_digest TEXT NOT NULL,
	port INTEGER NOT NULL,
	health_path TEXT NOT NULL,
	domains TEXT NOT NULL,
	has_pre_deploy INTEGER NOT NULL CHECK (has_pre_deploy IN (0, 1)),
	pre_deploy_reversible INTEGER CHECK (pre_deploy_reversible IN (0, 1)),
	status TEXT NOT NULL CHECK (status IN ('candidate', 'current', 'previous', 'retired', 'failed', 'aborted')),
	container_id TEXT NOT NULL,
	error TEXT NOT NULL,
	created_at TEXT NOT NULL,
	switched_at TEXT,
	stop_after TEXT,
	PRIMARY KEY (app_id, release_id)
);

CREATE TABLE apps (
	app_id TEXT NOT NULL PRIMARY KEY,
	project TEXT NOT NULL,
	environment TEXT NOT NULL,
	service TEXT NOT NULL,
	current_release_id TEXT,
	previous_release_id TEXT,
	candidate_release_id TEXT,
	phase TEXT NOT NULL CHECK (phase IN ('none', 'pending', 'ready')),
	created_at TEXT NOT NULL,
	FOREIGN KEY (app_id, current_release_id) REFERENCES releases (app_id, release_id),
	FOREIGN KEY (app_id, previous_release_id) REFERENCES releases (app_id, release_id),
	FOREIGN KEY (app_id, candidate_release_id) REFERENCES releases (app_id, release_id)
);
