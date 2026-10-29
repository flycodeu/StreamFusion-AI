package controller

// Reuse the durable job and host lock chain for first-time SSH preparation.
// No secrets are stored in job input or result JSON.
const schemaV13 = `
CREATE TABLE jobs_v13(id TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES admins(id),kind TEXT NOT NULL CHECK(kind IN ('IMPORT','PUBLISH','ROLLBACK','PLUGIN_INSTALL','PLUGIN_REMOVE','SOFTWARE_ADOPT','SOFTWARE_START','SOFTWARE_STOP','SOFTWARE_UNMANAGE','SOFTWARE_INSTALL','SOFTWARE_CONFIGURE','FIREWALL_CHANGE','HOST_PREPARE')),target_id TEXT REFERENCES targets(id),instance_id TEXT REFERENCES instances(id),plan_id TEXT UNIQUE REFERENCES operation_plans(id),plugin_plan_id TEXT UNIQUE REFERENCES plugin_install_plans(id),release_id TEXT REFERENCES releases(id),idempotency_key TEXT NOT NULL,request_hash TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('QUEUED','RUNNING','SUCCEEDED','FAILED','UNKNOWN')),phase TEXT NOT NULL,input_json TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '{}',error_code TEXT NOT NULL DEFAULT '',target_ack_pending INTEGER NOT NULL DEFAULT 0,trace_id TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,software_plan_id TEXT UNIQUE REFERENCES software_install_plans(id),configuration_plan_id TEXT UNIQUE REFERENCES software_configuration_plans(id),firewall_plan_id TEXT UNIQUE REFERENCES firewall_plans(id));
INSERT INTO jobs_v13 SELECT * FROM jobs;
CREATE TABLE operation_locks_v13(resource_key TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES jobs_v13(id));
INSERT INTO operation_locks_v13 SELECT * FROM operation_locks;
DROP TABLE operation_locks;
DROP TABLE jobs;
ALTER TABLE jobs_v13 RENAME TO jobs;
ALTER TABLE operation_locks_v13 RENAME TO operation_locks;
CREATE INDEX jobs_recent ON jobs(created_at DESC,id DESC);
CREATE INDEX jobs_target ON jobs(target_id);
CREATE INDEX jobs_plugin_plan ON jobs(plugin_plan_id);
CREATE INDEX jobs_software_plan ON jobs(software_plan_id);
CREATE INDEX jobs_configuration_plan ON jobs(configuration_plan_id);
CREATE INDEX jobs_firewall_plan ON jobs(firewall_plan_id);
PRAGMA user_version=13;
`
