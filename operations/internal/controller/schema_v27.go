package controller

// Preserve durable requests, locks and references while extending lifecycle jobs.
const schemaV27 = `
CREATE TABLE jobs_v27(id TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES admins(id),kind TEXT NOT NULL CHECK(kind IN ('IMPORT','PUBLISH','ROLLBACK','PLUGIN_INSTALL','PLUGIN_REMOVE','SOFTWARE_ADOPT','SOFTWARE_START','SOFTWARE_STOP','SOFTWARE_UNMANAGE','SOFTWARE_INSTALL','SOFTWARE_CONFIGURE','FIREWALL_CHANGE','HOST_PREPARE','SOFTWARE_UNINSTALL','NODE_ENVIRONMENT_CHANGE','SOFTWARE_DOCKER_ACCESS','GIT_REPOSITORY_CHANGE','SOFTWARE_RESTART','SOFTWARE_RELOAD','SOFTWARE_PAUSE','SOFTWARE_UNPAUSE','SOFTWARE_OPERATION','SOFTWARE_RUNTIME_POLICY','APP_RUNTIME_POLICY','APP_START','APP_STOP','APP_RESTART')),target_id TEXT REFERENCES targets(id),instance_id TEXT REFERENCES instances(id),plan_id TEXT UNIQUE REFERENCES operation_plans(id),plugin_plan_id TEXT UNIQUE REFERENCES plugin_install_plans(id),release_id TEXT REFERENCES releases(id),idempotency_key TEXT NOT NULL,request_hash TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('QUEUED','RUNNING','SUCCEEDED','FAILED','UNKNOWN')),phase TEXT NOT NULL,input_json TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '{}',error_code TEXT NOT NULL DEFAULT '',target_ack_pending INTEGER NOT NULL DEFAULT 0,trace_id TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,software_plan_id TEXT UNIQUE REFERENCES software_install_plans(id),configuration_plan_id TEXT UNIQUE REFERENCES software_configuration_plans(id),firewall_plan_id TEXT UNIQUE REFERENCES firewall_plans(id),node_environment_plan_id TEXT UNIQUE REFERENCES node_environment_plans(id));
INSERT INTO jobs_v27 SELECT * FROM jobs;
CREATE TABLE operation_locks_v27(resource_key TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES jobs_v27(id));
INSERT INTO operation_locks_v27 SELECT * FROM operation_locks;
CREATE TABLE git_repository_plans_v27(id TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES admins(id),host_id TEXT NOT NULL REFERENCES targets(id),binding_id TEXT NOT NULL,snapshot_json TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'DRAFT',expires_at TEXT NOT NULL,created_at TEXT NOT NULL,job_id TEXT UNIQUE REFERENCES jobs_v27(id));
INSERT INTO git_repository_plans_v27 SELECT * FROM git_repository_plans;
CREATE TABLE software_management_state_v27(software_id TEXT PRIMARY KEY REFERENCES software_instances(id),template_id TEXT NOT NULL REFERENCES command_templates(id),template_revision INTEGER NOT NULL,definition_json TEXT NOT NULL,parameters_json TEXT NOT NULL DEFAULT '{}',credential_refs_json TEXT NOT NULL DEFAULT '{}',resources_json TEXT NOT NULL DEFAULT '[]',version INTEGER NOT NULL DEFAULT 1,last_job_id TEXT REFERENCES jobs_v27(id),updated_at TEXT NOT NULL);
INSERT INTO software_management_state_v27 SELECT * FROM software_management_state;
CREATE TABLE software_management_credentials_v27(software_id TEXT NOT NULL REFERENCES software_management_state_v27(software_id),credential_id TEXT NOT NULL,credential_revision INTEGER NOT NULL,PRIMARY KEY(software_id,credential_id,credential_revision),FOREIGN KEY(credential_id,credential_revision) REFERENCES credential_refs(id,revision));
INSERT INTO software_management_credentials_v27 SELECT * FROM software_management_credentials;
DROP TABLE software_management_credentials;
DROP TABLE software_management_state;
DROP TABLE operation_locks;
DROP TABLE git_repository_plans;
DROP TABLE jobs;
ALTER TABLE jobs_v27 RENAME TO jobs;
ALTER TABLE operation_locks_v27 RENAME TO operation_locks;
ALTER TABLE git_repository_plans_v27 RENAME TO git_repository_plans;
ALTER TABLE software_management_state_v27 RENAME TO software_management_state;
ALTER TABLE software_management_credentials_v27 RENAME TO software_management_credentials;
CREATE INDEX jobs_recent ON jobs(created_at DESC,id DESC);
CREATE INDEX jobs_target ON jobs(target_id);
CREATE INDEX jobs_plugin_plan ON jobs(plugin_plan_id);
CREATE INDEX jobs_software_plan ON jobs(software_plan_id);
CREATE INDEX jobs_configuration_plan ON jobs(configuration_plan_id);
CREATE INDEX jobs_firewall_plan ON jobs(firewall_plan_id);
CREATE INDEX jobs_node_environment_plan ON jobs(node_environment_plan_id);
CREATE INDEX git_repository_plan_binding ON git_repository_plans(binding_id,created_at);
PRAGMA user_version=27;
`
