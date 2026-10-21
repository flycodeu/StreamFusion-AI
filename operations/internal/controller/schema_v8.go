package controller

// Software registration is separate from image-material installations. Existing
// jobs, locks and release identities survive this additive migration.
const schemaV8 = `
CREATE TABLE software_instances(id TEXT PRIMARY KEY,name TEXT NOT NULL,host_id TEXT NOT NULL REFERENCES targets(id),software_type TEXT NOT NULL,software_version TEXT NOT NULL DEFAULT '',runtime_kind TEXT NOT NULL CHECK(runtime_kind IN ('SYSTEMD','DOCKER','TOOL')),locator TEXT NOT NULL,management_mode TEXT NOT NULL DEFAULT 'OBSERVE' CHECK(management_mode IN ('OBSERVE','MANAGED','RELEASED')),desired_state TEXT NOT NULL DEFAULT 'UNCHANGED',actual_state TEXT NOT NULL DEFAULT 'UNKNOWN',observation_json TEXT NOT NULL DEFAULT '{}',observed_at TEXT NOT NULL DEFAULT '',identity_hash TEXT NOT NULL DEFAULT '',binding_revision INTEGER NOT NULL DEFAULT 0,version INTEGER NOT NULL DEFAULT 1,last_job_id TEXT NOT NULL DEFAULT '',error_code TEXT NOT NULL DEFAULT '',data_ownership TEXT NOT NULL DEFAULT 'EXTERNAL',created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE UNIQUE INDEX software_active_identity ON software_instances(host_id,runtime_kind,locator) WHERE management_mode<>'RELEASED';
CREATE INDEX software_host ON software_instances(host_id,created_at DESC);
CREATE TABLE software_unmanage_receipts(actor_id TEXT NOT NULL REFERENCES admins(id),idempotency_key TEXT NOT NULL,software_instance_id TEXT NOT NULL REFERENCES software_instances(id),request_hash TEXT NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(actor_id,idempotency_key));
CREATE TABLE jobs_v8(id TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES admins(id),kind TEXT NOT NULL CHECK(kind IN ('IMPORT','PUBLISH','ROLLBACK','PLUGIN_INSTALL','PLUGIN_REMOVE','SOFTWARE_ADOPT','SOFTWARE_START','SOFTWARE_STOP','SOFTWARE_UNMANAGE')),target_id TEXT REFERENCES targets(id),instance_id TEXT REFERENCES instances(id),plan_id TEXT UNIQUE REFERENCES operation_plans(id),plugin_plan_id TEXT UNIQUE REFERENCES plugin_install_plans(id),release_id TEXT REFERENCES releases(id),idempotency_key TEXT NOT NULL,request_hash TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('QUEUED','RUNNING','SUCCEEDED','FAILED','UNKNOWN')),phase TEXT NOT NULL,input_json TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '{}',error_code TEXT NOT NULL DEFAULT '',target_ack_pending INTEGER NOT NULL DEFAULT 0,trace_id TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(actor_id,kind,idempotency_key));
INSERT INTO jobs_v8 SELECT * FROM jobs;
CREATE TABLE operation_locks_v8(resource_key TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES jobs_v8(id));
INSERT INTO operation_locks_v8 SELECT * FROM operation_locks;
DROP TABLE operation_locks;
DROP TABLE jobs;
ALTER TABLE jobs_v8 RENAME TO jobs;
ALTER TABLE operation_locks_v8 RENAME TO operation_locks;
CREATE INDEX jobs_recent ON jobs(created_at DESC,id DESC);
CREATE INDEX jobs_target ON jobs(target_id);
CREATE INDEX jobs_plugin_plan ON jobs(plugin_plan_id);
PRAGMA user_version=8;
`
