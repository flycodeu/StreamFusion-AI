package controller

// Service installation has its own plans/materials; existing image-tool records
// and all mutation receipts remain intact. Software instances remain the sole
// service identity, including an installation which has not produced a PID yet.
const schemaV10 = `
CREATE TABLE installation_materials(id TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES admins(id),name TEXT NOT NULL,definition_json TEXT NOT NULL,sha256 TEXT NOT NULL,byte_size INTEGER NOT NULL,status TEXT NOT NULL,image_id TEXT NOT NULL DEFAULT '',platform TEXT NOT NULL DEFAULT '',error_code TEXT NOT NULL DEFAULT '',idempotency_key TEXT NOT NULL,request_hash TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(actor_id,idempotency_key));
CREATE TABLE software_install_plans(id TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES admins(id),target_id TEXT NOT NULL REFERENCES targets(id),material_id TEXT REFERENCES installation_materials(id),snapshot_json TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'DRAFT',expires_at TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE software_install_credentials(plan_id TEXT NOT NULL REFERENCES software_install_plans(id),credential_id TEXT NOT NULL,credential_revision INTEGER NOT NULL,PRIMARY KEY(plan_id,credential_id,credential_revision),FOREIGN KEY(credential_id,credential_revision) REFERENCES credential_refs(id,revision));
ALTER TABLE software_instances ADD COLUMN install_plan_id TEXT REFERENCES software_install_plans(id);
ALTER TABLE software_instances ADD COLUMN installation_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE software_instances ADD COLUMN installation_status TEXT NOT NULL DEFAULT '';
CREATE TABLE jobs_v10(id TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES admins(id),kind TEXT NOT NULL CHECK(kind IN ('IMPORT','PUBLISH','ROLLBACK','PLUGIN_INSTALL','PLUGIN_REMOVE','SOFTWARE_ADOPT','SOFTWARE_START','SOFTWARE_STOP','SOFTWARE_UNMANAGE','SOFTWARE_INSTALL')),target_id TEXT REFERENCES targets(id),instance_id TEXT REFERENCES instances(id),plan_id TEXT UNIQUE REFERENCES operation_plans(id),plugin_plan_id TEXT UNIQUE REFERENCES plugin_install_plans(id),release_id TEXT REFERENCES releases(id),idempotency_key TEXT NOT NULL,request_hash TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('QUEUED','RUNNING','SUCCEEDED','FAILED','UNKNOWN')),phase TEXT NOT NULL,input_json TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '{}',error_code TEXT NOT NULL DEFAULT '',target_ack_pending INTEGER NOT NULL DEFAULT 0,trace_id TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,software_plan_id TEXT UNIQUE REFERENCES software_install_plans(id));
INSERT INTO jobs_v10(id,actor_id,kind,target_id,instance_id,plan_id,plugin_plan_id,release_id,idempotency_key,request_hash,status,phase,input_json,result_json,error_code,target_ack_pending,trace_id,created_at,updated_at) SELECT id,actor_id,kind,target_id,instance_id,plan_id,plugin_plan_id,release_id,idempotency_key,request_hash,status,phase,input_json,result_json,error_code,target_ack_pending,trace_id,created_at,updated_at FROM jobs;
CREATE TABLE operation_locks_v10(resource_key TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES jobs_v10(id));
INSERT INTO operation_locks_v10 SELECT * FROM operation_locks;
DROP TABLE operation_locks;
DROP TABLE jobs;
ALTER TABLE jobs_v10 RENAME TO jobs;
ALTER TABLE operation_locks_v10 RENAME TO operation_locks;
CREATE INDEX jobs_recent ON jobs(created_at DESC,id DESC);
CREATE INDEX jobs_target ON jobs(target_id);
CREATE INDEX jobs_plugin_plan ON jobs(plugin_plan_id);
CREATE INDEX jobs_software_plan ON jobs(software_plan_id);
CREATE INDEX software_install_plans_target ON software_install_plans(target_id,created_at);
CREATE INDEX installation_material_status ON installation_materials(status,created_at);
PRAGMA user_version=10;
`
