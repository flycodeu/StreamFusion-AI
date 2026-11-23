package controller

const schemaV30 = `
CREATE TABLE application_jenkins_monitors(
    project_id TEXT PRIMARY KEY REFERENCES application_projects(id) ON DELETE CASCADE,
    software_id TEXT NOT NULL REFERENCES software_instances(id),
    job_url TEXT NOT NULL,
    endpoint_identity TEXT NOT NULL,
    username TEXT NOT NULL,
    version INTEGER NOT NULL CHECK(version > 0),
    token_ciphertext BLOB NOT NULL
);
PRAGMA user_version=30;
`
