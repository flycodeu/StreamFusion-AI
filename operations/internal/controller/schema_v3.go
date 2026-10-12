package controller

const schemaV3 = `
CREATE TABLE plugin_installations(id TEXT PRIMARY KEY,target_id TEXT NOT NULL REFERENCES targets(id),plugin_key TEXT NOT NULL,plugin_version TEXT NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('JAVA_RUNTIME','MAVEN_JDK_BUILD')),install_mode TEXT NOT NULL CHECK(install_mode='DOCKER_IMAGE'),image_ref TEXT NOT NULL,image_digest TEXT NOT NULL,image_id TEXT NOT NULL,recipe_revision TEXT NOT NULL,platform TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('INSTALLING','READY','FAILED','UNKNOWN','REMOVING')),observed_version TEXT NOT NULL DEFAULT '',last_job_id TEXT NOT NULL,reference_count INTEGER NOT NULL DEFAULT 0,error_code TEXT NOT NULL DEFAULT '',observed_at TEXT NOT NULL DEFAULT '',version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(target_id,plugin_key,plugin_version,image_digest));
CREATE TABLE plugin_install_plans(id TEXT PRIMARY KEY,target_id TEXT NOT NULL REFERENCES targets(id),plugin_key TEXT NOT NULL,plugin_version TEXT NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('JAVA_RUNTIME','MAVEN_JDK_BUILD')),image_digest TEXT NOT NULL,recipe_revision TEXT NOT NULL,snapshot_json TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('DRAFT','SUBMITTED','EXPIRED')),expires_at TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE INDEX plugin_installations_target ON plugin_installations(target_id,created_at DESC);
CREATE INDEX plugin_installations_status ON plugin_installations(status,updated_at DESC);
CREATE INDEX plugin_plans_target ON plugin_install_plans(target_id,created_at DESC);
CREATE TABLE jobs_v3(id TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES admins(id),kind TEXT NOT NULL CHECK(kind IN ('IMPORT','PUBLISH','ROLLBACK','PLUGIN_INSTALL','PLUGIN_REMOVE')),target_id TEXT REFERENCES targets(id),instance_id TEXT REFERENCES instances(id),plan_id TEXT UNIQUE REFERENCES operation_plans(id),plugin_plan_id TEXT UNIQUE REFERENCES plugin_install_plans(id),release_id TEXT REFERENCES releases(id),idempotency_key TEXT NOT NULL,request_hash TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('QUEUED','RUNNING','SUCCEEDED','FAILED','UNKNOWN')),phase TEXT NOT NULL,input_json TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '{}',error_code TEXT NOT NULL DEFAULT '',target_ack_pending INTEGER NOT NULL DEFAULT 0,trace_id TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(actor_id,kind,idempotency_key));
INSERT INTO jobs_v3(id,actor_id,kind,target_id,instance_id,plan_id,plugin_plan_id,release_id,idempotency_key,request_hash,status,phase,input_json,result_json,error_code,target_ack_pending,trace_id,created_at,updated_at) SELECT id,actor_id,kind,target_id,instance_id,plan_id,NULL,release_id,idempotency_key,request_hash,status,phase,input_json,result_json,error_code,target_ack_pending,trace_id,created_at,updated_at FROM jobs;
CREATE TABLE operation_locks_v3(resource_key TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES jobs_v3(id));
INSERT INTO operation_locks_v3(resource_key,job_id) SELECT resource_key,job_id FROM operation_locks;
DROP TABLE operation_locks;
DROP TABLE jobs;
ALTER TABLE jobs_v3 RENAME TO jobs;
ALTER TABLE operation_locks_v3 RENAME TO operation_locks;
CREATE INDEX jobs_recent ON jobs(created_at DESC,id DESC);
CREATE INDEX jobs_target ON jobs(target_id);
CREATE INDEX jobs_plugin_plan ON jobs(plugin_plan_id);
PRAGMA user_version=3;
`
