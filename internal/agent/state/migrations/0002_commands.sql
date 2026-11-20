CREATE TABLE commands (
	request_id TEXT NOT NULL PRIMARY KEY,
	type TEXT NOT NULL,
	app_id TEXT NOT NULL,
	result BLOB NOT NULL,
	received_at TEXT NOT NULL,
	finished_at TEXT NOT NULL
);
