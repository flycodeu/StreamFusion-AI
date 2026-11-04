package controller

// Git metadata is additive; the existing job rows, receipts and locks are copied verbatim.
const schemaV18 = `
CREATE TABLE jobs_v18(id TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES admins(id),kind TEXT NOT NULL CHECK(kind IN ('IMPORT','PUBLISH','ROLLBACK','PLUGIN_INSTALL','PLUGIN_REMOVE','SOFTWARE_ADOPT','SOFTWARE_START','SOFTWARE_STOP','SOFTWARE_UNMANAGE','SOFTWARE_INSTALL','SOFTWARE_CONFIGURE','FIREWALL_CHANGE','HOST_PREPARE','SOFTWARE_UNINSTALL','NODE_ENVIRONMENT_CHANGE','SOFTWARE_DOCKER_ACCESS','GIT_REPOSITORY_CHANGE')),target_id TEXT REFERENCES targets(id),instance_id TEXT REFERENCES instances(id),plan_id TEXT UNIQUE REFERENCES operation_plans(id),plugin_plan_id TEXT UNIQUE REFERENCES plugin_install_plans(id),release_id TEXT REFERENCES releases(id),idempotency_key TEXT NOT NULL,request_hash TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('QUEUED','RUNNING','SUCCEEDED','FAILED','UNKNOWN')),phase TEXT NOT NULL,input_json TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '{}',error_code TEXT NOT NULL DEFAULT '',target_ack_pending INTEGER NOT NULL DEFAULT 0,trace_id TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,software_plan_id TEXT UNIQUE REFERENCES software_install_plans(id),configuration_plan_id TEXT UNIQUE REFERENCES software_configuration_plans(id),firewall_plan_id TEXT UNIQUE REFERENCES firewall_plans(id),node_environment_plan_id TEXT UNIQUE REFERENCES node_environment_plans(id));
INSERT INTO jobs_v18(id,actor_id,kind,target_id,instance_id,plan_id,plugin_plan_id,release_id,idempotency_key,request_hash,status,phase,input_json,result_json,error_code,target_ack_pending,trace_id,created_at,updated_at,software_plan_id,configuration_plan_id,firewall_plan_id,node_environment_plan_id) SELECT id,actor_id,kind,target_id,instance_id,plan_id,plugin_plan_id,release_id,idempotency_key,request_hash,status,phase,input_json,result_json,error_code,target_ack_pending,trace_id,created_at,updated_at,software_plan_id,configuration_plan_id,firewall_plan_id,node_environment_plan_id FROM jobs;
CREATE TABLE operation_locks_v18(resource_key TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES jobs_v18(id));
INSERT INTO operation_locks_v18(resource_key,job_id) SELECT resource_key,job_id FROM operation_locks;
DROP TABLE operation_locks;
DROP TABLE jobs;
ALTER TABLE jobs_v18 RENAME TO jobs;
ALTER TABLE operation_locks_v18 RENAME TO operation_locks;
CREATE INDEX jobs_recent ON jobs(created_at DESC,id DESC);
CREATE INDEX jobs_target ON jobs(target_id);
CREATE INDEX jobs_plugin_plan ON jobs(plugin_plan_id);
CREATE INDEX jobs_software_plan ON jobs(software_plan_id);
CREATE INDEX jobs_configuration_plan ON jobs(configuration_plan_id);
CREATE INDEX jobs_firewall_plan ON jobs(firewall_plan_id);
CREATE INDEX jobs_node_environment_plan ON jobs(node_environment_plan_id);
CREATE TABLE git_accounts(id TEXT PRIMARY KEY,name TEXT NOT NULL,ssh_host TEXT NOT NULL,ssh_port INTEGER NOT NULL,ssh_user TEXT NOT NULL,author_name TEXT NOT NULL,author_email TEXT NOT NULL,credential_id TEXT NOT NULL,credential_revision INTEGER NOT NULL,host_public_key TEXT NOT NULL,key_fingerprint TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1,deleted_at TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,FOREIGN KEY(credential_id,credential_revision) REFERENCES credential_refs(id,revision));
CREATE TABLE git_repositories(id TEXT PRIMARY KEY,host_id TEXT NOT NULL REFERENCES targets(id),account TEXT NOT NULL,account_uid TEXT NOT NULL,repo_path TEXT NOT NULL,profile_id TEXT NOT NULL REFERENCES git_accounts(id),change_json TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'PENDING',version INTEGER NOT NULL DEFAULT 1,observation_json TEXT NOT NULL DEFAULT '{}',error_code TEXT NOT NULL DEFAULT '',last_job_id TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE UNIQUE INDEX git_repository_active ON git_repositories(host_id,repo_path) WHERE status<>'RELEASED';
CREATE TABLE git_repository_plans(id TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES admins(id),host_id TEXT NOT NULL REFERENCES targets(id),binding_id TEXT NOT NULL,snapshot_json TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'DRAFT',expires_at TEXT NOT NULL,created_at TEXT NOT NULL,job_id TEXT UNIQUE REFERENCES jobs(id));
CREATE INDEX git_repository_plan_binding ON git_repository_plans(binding_id,created_at);
PRAGMA user_version=18;
`
