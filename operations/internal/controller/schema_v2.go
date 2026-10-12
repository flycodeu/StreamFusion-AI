package controller

const schemaV2 = `
CREATE TABLE targets(id TEXT PRIMARY KEY,name TEXT NOT NULL UNIQUE,install_root TEXT NOT NULL,kind TEXT NOT NULL DEFAULT 'LOCAL' CHECK(kind IN ('LOCAL','SSH')),address TEXT NOT NULL DEFAULT '127.0.0.1',ssh_port INTEGER NOT NULL DEFAULT 22,ssh_user TEXT NOT NULL DEFAULT '',host_fingerprint TEXT NOT NULL DEFAULT '',credential_id TEXT,credential_revision INTEGER,version INTEGER NOT NULL DEFAULT 1,observation_json TEXT NOT NULL DEFAULT '{}',observed_at TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,FOREIGN KEY(credential_id,credential_revision) REFERENCES credential_refs(id,revision),UNIQUE(address,ssh_port,install_root));
CREATE TABLE component_bindings(id TEXT PRIMARY KEY,config_json TEXT NOT NULL,credential_id TEXT,credential_revision INTEGER,created_at TEXT NOT NULL,FOREIGN KEY(credential_id,credential_revision) REFERENCES credential_refs(id,revision));
CREATE TABLE artifacts(id TEXT PRIMARY KEY,name TEXT NOT NULL,archive_sha256 TEXT NOT NULL,byte_size INTEGER NOT NULL,image_id TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE instances(id TEXT PRIMARY KEY,name TEXT NOT NULL,target_id TEXT NOT NULL REFERENCES targets(id),version INTEGER NOT NULL DEFAULT 1,draft_revision INTEGER NOT NULL DEFAULT 1,applied_generation INTEGER NOT NULL DEFAULT 0,draft_json TEXT NOT NULL,current_release_id TEXT,previous_release_id TEXT,created_at TEXT NOT NULL,
 FOREIGN KEY(id,current_release_id) REFERENCES releases(instance_id,id), FOREIGN KEY(id,previous_release_id) REFERENCES releases(instance_id,id), UNIQUE(target_id,name));
CREATE TABLE releases(id TEXT PRIMARY KEY,instance_id TEXT NOT NULL REFERENCES instances(id),artifact_id TEXT NOT NULL REFERENCES artifacts(id),snapshot_json TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(instance_id,id));
CREATE TABLE release_bindings(release_id TEXT NOT NULL REFERENCES releases(id),binding_id TEXT NOT NULL REFERENCES component_bindings(id),PRIMARY KEY(release_id,binding_id));
CREATE TABLE release_credentials(release_id TEXT NOT NULL REFERENCES releases(id),credential_id TEXT NOT NULL,credential_revision INTEGER NOT NULL,PRIMARY KEY(release_id,credential_id,credential_revision),FOREIGN KEY(credential_id,credential_revision) REFERENCES credential_refs(id,revision));
CREATE TABLE operation_plans(id TEXT PRIMARY KEY,instance_id TEXT NOT NULL REFERENCES instances(id),snapshot_json TEXT NOT NULL,expires_at TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE jobs(id TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES admins(id),kind TEXT NOT NULL CHECK(kind IN ('IMPORT','PUBLISH','ROLLBACK')),target_id TEXT REFERENCES targets(id),instance_id TEXT REFERENCES instances(id),plan_id TEXT UNIQUE REFERENCES operation_plans(id),release_id TEXT REFERENCES releases(id),idempotency_key TEXT NOT NULL,request_hash TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('QUEUED','RUNNING','SUCCEEDED','FAILED','UNKNOWN')),phase TEXT NOT NULL,input_json TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '{}',error_code TEXT NOT NULL DEFAULT '',target_ack_pending INTEGER NOT NULL DEFAULT 0,trace_id TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(actor_id,kind,idempotency_key));
CREATE TABLE operation_locks(resource_key TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES jobs(id));
CREATE TABLE observations(instance_id TEXT PRIMARY KEY REFERENCES instances(id),payload_json TEXT NOT NULL,observed_at TEXT NOT NULL);
CREATE INDEX jobs_recent ON jobs(created_at DESC,id DESC);
CREATE INDEX releases_instance ON releases(instance_id,created_at DESC);
CREATE INDEX releases_artifact ON releases(artifact_id);
CREATE INDEX jobs_target ON jobs(target_id);
`
