package controller

// Node environments retain the existing software and job identities. No
// historical request, receipt, credential or operation lock is normalized.
const schemaV15 = `
CREATE TABLE node_environments(id TEXT PRIMARY KEY,host_id TEXT NOT NULL REFERENCES targets(id),name TEXT NOT NULL,account TEXT NOT NULL,account_uid INTEGER NOT NULL,root_path TEXT NOT NULL,manager_type TEXT NOT NULL CHECK(manager_type IN ('NVM','SYSTEM')),management_mode TEXT NOT NULL CHECK(management_mode IN ('MANAGED','OBSERVE','RELEASED')),nvm_version TEXT NOT NULL DEFAULT '',nvm_hash TEXT NOT NULL DEFAULT '',desired_default_software_id TEXT REFERENCES software_instances(id),observed_default_version TEXT NOT NULL DEFAULT '',observation_json TEXT NOT NULL DEFAULT '{}',observed_at TEXT NOT NULL DEFAULT '',error_code TEXT NOT NULL DEFAULT '',version INTEGER NOT NULL DEFAULT 1,last_job_id TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE UNIQUE INDEX node_environment_active_identity ON node_environments(host_id,account_uid,root_path) WHERE management_mode<>'RELEASED';
ALTER TABLE software_instances ADD COLUMN node_environment_id TEXT REFERENCES node_environments(id);
CREATE UNIQUE INDEX node_version_active ON software_instances(node_environment_id,software_version) WHERE node_environment_id IS NOT NULL AND management_mode<>'RELEASED' AND installation_status NOT IN ('FAILED','CANCELLED','UNINSTALLED');
CREATE TABLE node_environment_plans(id TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES admins(id),host_id TEXT NOT NULL REFERENCES targets(id),environment_id TEXT REFERENCES node_environments(id),action TEXT NOT NULL,snapshot_json TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'DRAFT',expires_at TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE INDEX node_environment_plan_recent ON node_environment_plans(host_id,created_at);
CREATE TABLE jobs_v15(id TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES admins(id),kind TEXT NOT NULL CHECK(kind IN ('IMPORT','PUBLISH','ROLLBACK','PLUGIN_INSTALL','PLUGIN_REMOVE','SOFTWARE_ADOPT','SOFTWARE_START','SOFTWARE_STOP','SOFTWARE_UNMANAGE','SOFTWARE_INSTALL','SOFTWARE_CONFIGURE','FIREWALL_CHANGE','HOST_PREPARE','SOFTWARE_UNINSTALL','NODE_ENVIRONMENT_CHANGE')),target_id TEXT REFERENCES targets(id),instance_id TEXT REFERENCES instances(id),plan_id TEXT UNIQUE REFERENCES operation_plans(id),plugin_plan_id TEXT UNIQUE REFERENCES plugin_install_plans(id),release_id TEXT REFERENCES releases(id),idempotency_key TEXT NOT NULL,request_hash TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('QUEUED','RUNNING','SUCCEEDED','FAILED','UNKNOWN')),phase TEXT NOT NULL,input_json TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '{}',error_code TEXT NOT NULL DEFAULT '',target_ack_pending INTEGER NOT NULL DEFAULT 0,trace_id TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,software_plan_id TEXT UNIQUE REFERENCES software_install_plans(id),configuration_plan_id TEXT UNIQUE REFERENCES software_configuration_plans(id),firewall_plan_id TEXT UNIQUE REFERENCES firewall_plans(id),node_environment_plan_id TEXT UNIQUE REFERENCES node_environment_plans(id));
INSERT INTO jobs_v15(id,actor_id,kind,target_id,instance_id,plan_id,plugin_plan_id,release_id,idempotency_key,request_hash,status,phase,input_json,result_json,error_code,target_ack_pending,trace_id,created_at,updated_at,software_plan_id,configuration_plan_id,firewall_plan_id) SELECT id,actor_id,kind,target_id,instance_id,plan_id,plugin_plan_id,release_id,idempotency_key,request_hash,status,phase,input_json,result_json,error_code,target_ack_pending,trace_id,created_at,updated_at,software_plan_id,configuration_plan_id,firewall_plan_id FROM jobs;
CREATE TABLE operation_locks_v15(resource_key TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES jobs_v15(id));
INSERT INTO operation_locks_v15(resource_key,job_id) SELECT resource_key,job_id FROM operation_locks;
DROP TABLE operation_locks;
DROP TABLE jobs;
ALTER TABLE jobs_v15 RENAME TO jobs;
ALTER TABLE operation_locks_v15 RENAME TO operation_locks;
CREATE INDEX jobs_recent ON jobs(created_at DESC,id DESC);
CREATE INDEX jobs_target ON jobs(target_id);
CREATE INDEX jobs_plugin_plan ON jobs(plugin_plan_id);
CREATE INDEX jobs_software_plan ON jobs(software_plan_id);
CREATE INDEX jobs_configuration_plan ON jobs(configuration_plan_id);
CREATE INDEX jobs_firewall_plan ON jobs(firewall_plan_id);
CREATE INDEX jobs_node_environment_plan ON jobs(node_environment_plan_id);
PRAGMA user_version=15;
`
