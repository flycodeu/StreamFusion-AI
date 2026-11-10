package controller

// Only Git account metadata changes. Existing accounts, bindings and operation
// snapshots are copied unchanged; target-generated keys have no credential ref.
const schemaV19 = `
CREATE TABLE git_accounts_v19(id TEXT PRIMARY KEY,name TEXT NOT NULL,ssh_host TEXT NOT NULL,ssh_port INTEGER NOT NULL,ssh_user TEXT NOT NULL,author_name TEXT NOT NULL,author_email TEXT NOT NULL,credential_id TEXT,credential_revision INTEGER,host_public_key TEXT NOT NULL,key_fingerprint TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1,deleted_at TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,key_source TEXT NOT NULL DEFAULT 'OPS' CHECK(key_source IN ('OPS','TARGET')),host_id TEXT REFERENCES targets(id),system_account TEXT NOT NULL DEFAULT '',account_uid TEXT NOT NULL DEFAULT '',key_id TEXT NOT NULL DEFAULT '',key_path TEXT NOT NULL DEFAULT '',public_key TEXT NOT NULL DEFAULT '',FOREIGN KEY(credential_id,credential_revision) REFERENCES credential_refs(id,revision),CHECK((key_source='OPS' AND credential_id IS NOT NULL AND credential_revision IS NOT NULL) OR (key_source='TARGET' AND credential_id IS NULL AND credential_revision IS NULL AND host_id IS NOT NULL AND system_account<>'' AND key_id<>'' AND key_path<>'')));
INSERT INTO git_accounts_v19(id,name,ssh_host,ssh_port,ssh_user,author_name,author_email,credential_id,credential_revision,host_public_key,key_fingerprint,version,deleted_at,created_at,updated_at) SELECT id,name,ssh_host,ssh_port,ssh_user,author_name,author_email,credential_id,credential_revision,host_public_key,key_fingerprint,version,deleted_at,created_at,updated_at FROM git_accounts;
CREATE TABLE git_repositories_v19(id TEXT PRIMARY KEY,host_id TEXT NOT NULL REFERENCES targets(id),account TEXT NOT NULL,account_uid TEXT NOT NULL,repo_path TEXT NOT NULL,profile_id TEXT NOT NULL REFERENCES git_accounts_v19(id),change_json TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'PENDING',version INTEGER NOT NULL DEFAULT 1,observation_json TEXT NOT NULL DEFAULT '{}',error_code TEXT NOT NULL DEFAULT '',last_job_id TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
INSERT INTO git_repositories_v19 SELECT * FROM git_repositories;
DROP TABLE git_repositories;
DROP TABLE git_accounts;
ALTER TABLE git_accounts_v19 RENAME TO git_accounts;
ALTER TABLE git_repositories_v19 RENAME TO git_repositories;
CREATE UNIQUE INDEX git_repository_active ON git_repositories(host_id,repo_path) WHERE status<>'RELEASED';
PRAGMA user_version=19;
`
