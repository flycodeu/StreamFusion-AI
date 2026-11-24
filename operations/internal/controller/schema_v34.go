package controller

const schemaV34 = `
ALTER TABLE application_repositories ADD COLUMN jenkins_credential_id TEXT NOT NULL DEFAULT '';
ALTER TABLE application_jenkins_connections ADD COLUMN build_agent_label TEXT NOT NULL DEFAULT '` + defaultJenkinsBuildAgentLabel + `'
 CHECK(length(build_agent_label) BETWEEN 1 AND 64 AND substr(build_agent_label,1,1) GLOB '[A-Za-z0-9]'
 AND build_agent_label NOT GLOB '*[^A-Za-z0-9_.-]*');
CREATE TABLE project_deployment_jobs (
 id TEXT PRIMARY KEY,
 deployment_id TEXT REFERENCES application_deployments(id) ON DELETE SET NULL,
 target_id TEXT REFERENCES targets(id) ON DELETE SET NULL,
 actor_id TEXT NOT NULL REFERENCES admins(id),
 action TEXT NOT NULL CHECK(action IN ('DEPLOY','ROLLBACK','VERIFY')),
 status TEXT NOT NULL CHECK(status IN ('QUEUED','RUNNING','UNKNOWN','SUCCEEDED','FAILED')),
 phase TEXT NOT NULL, error_code TEXT NOT NULL DEFAULT '',
 health TEXT NOT NULL DEFAULT '',
 host_name TEXT NOT NULL, subject_name TEXT NOT NULL,
 build_number INTEGER NOT NULL, commit_id TEXT NOT NULL,
 release_id TEXT NOT NULL, baseline_id TEXT NOT NULL,
 snapshot_json TEXT NOT NULL, trace_id TEXT NOT NULL,
 execution_log TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX project_deployment_jobs_latest ON project_deployment_jobs(deployment_id,created_at DESC,id DESC);
CREATE INDEX project_deployment_jobs_recent ON project_deployment_jobs(created_at DESC,id DESC);
CREATE UNIQUE INDEX project_deployment_job_lock ON project_deployment_jobs(deployment_id)
 WHERE status IN ('QUEUED','RUNNING','UNKNOWN');
CREATE UNIQUE INDEX project_deployment_host_lock ON project_deployment_jobs(target_id)
 WHERE status IN ('QUEUED','RUNNING','UNKNOWN') AND action<>'VERIFY';
CREATE TABLE project_deployment_plans (
 token_hash TEXT PRIMARY KEY, actor_id TEXT NOT NULL REFERENCES admins(id),
 deployment_id TEXT NOT NULL REFERENCES application_deployments(id) ON DELETE CASCADE,
 snapshot_json TEXT NOT NULL, expires_at TEXT NOT NULL,
 job_id TEXT REFERENCES project_deployment_jobs(id)
);
CREATE INDEX project_deployment_plan_expiry ON project_deployment_plans(expires_at);
CREATE TABLE project_deployment_running (
 deployment_id TEXT PRIMARY KEY REFERENCES application_deployments(id),
 current_id TEXT NOT NULL REFERENCES project_deployment_jobs(id),
 previous_id TEXT REFERENCES project_deployment_jobs(id)
);
CREATE TRIGGER project_deployment_guard_delete BEFORE DELETE ON application_deployments
 WHEN EXISTS(SELECT 1 FROM project_deployment_running WHERE deployment_id=OLD.id)
 OR EXISTS(SELECT 1 FROM project_deployment_jobs WHERE deployment_id=OLD.id AND status IN ('QUEUED','RUNNING','UNKNOWN'))
 BEGIN SELECT RAISE(ABORT,'PROJECT_DEPLOYMENT_IN_USE'); END;
CREATE TRIGGER project_deployment_guard_binding BEFORE UPDATE ON application_deployments
 WHEN (NEW.host_id IS NOT OLD.host_id OR NEW.module_id IS NOT OLD.module_id OR NEW.environment_id IS NOT OLD.environment_id
 OR coalesce(json_extract(NEW.config_json,'$.deployMode'),'')<>coalesce(json_extract(OLD.config_json,'$.deployMode'),'')
 OR coalesce(json_extract(NEW.config_json,'$.managedInstanceId'),'')<>coalesce(json_extract(OLD.config_json,'$.managedInstanceId'),''))
 AND (EXISTS(SELECT 1 FROM project_deployment_running WHERE deployment_id=OLD.id)
 OR EXISTS(SELECT 1 FROM project_deployment_jobs WHERE deployment_id=OLD.id AND status IN ('QUEUED','RUNNING','UNKNOWN')))
 BEGIN SELECT RAISE(ABORT,'PROJECT_DEPLOYMENT_IN_USE'); END;
CREATE TRIGGER project_deployment_guard_target BEFORE UPDATE OF address,kind,install_root ON targets
 WHEN (NEW.address IS NOT OLD.address OR NEW.kind IS NOT OLD.kind OR NEW.install_root IS NOT OLD.install_root)
 AND (EXISTS(SELECT 1 FROM project_deployment_running p JOIN application_deployments d ON d.id=p.deployment_id WHERE d.host_id=OLD.id)
 OR EXISTS(SELECT 1 FROM project_deployment_jobs WHERE target_id=OLD.id AND status IN ('QUEUED','RUNNING','UNKNOWN')))
 BEGIN SELECT RAISE(ABORT,'PROJECT_DEPLOYMENT_IN_USE'); END;
CREATE TRIGGER project_deployment_legacy_lock BEFORE INSERT ON operation_locks
 WHEN EXISTS(SELECT 1 FROM project_deployment_jobs WHERE 'target:'||target_id=NEW.resource_key
 AND status IN ('QUEUED','RUNNING','UNKNOWN') AND action<>'VERIFY')
 BEGIN SELECT RAISE(ABORT,'PROJECT_DEPLOYMENT_HOST_BUSY'); END;
PRAGMA user_version=34;`
