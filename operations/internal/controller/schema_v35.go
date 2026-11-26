package controller

import (
	"errors"
	"fmt"
)

// The SQLite action check requires rebuilding the job table. Keep the plans
// and running pointers, and validate their references in the same transaction.
func (s *Store) migrateProjectDeployments() (err error) {
	if _, err = s.DB.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	defer func() {
		_, enableErr := s.DB.Exec("PRAGMA foreign_keys=ON")
		err = errors.Join(err, enableErr)
	}()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(schemaV35); err != nil {
		return err
	}
	var invalid int
	if err = tx.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("SCHEMA_MISMATCH: 迁移35发现 %d 条无效外键", invalid)
	}
	if _, err = tx.Exec("INSERT INTO schema_migrations VALUES(35,?,?,?)", hashToken(schemaV35), Version, Now()); err != nil {
		return err
	}
	return tx.Commit()
}

const schemaV35 = `
DROP TRIGGER project_deployment_guard_delete;
DROP TRIGGER project_deployment_guard_binding;
DROP TRIGGER project_deployment_guard_target;
DROP TRIGGER project_deployment_legacy_lock;
CREATE TABLE project_deployment_jobs_v35 (
 id TEXT PRIMARY KEY,
 deployment_id TEXT REFERENCES application_deployments(id) ON DELETE SET NULL,
 target_id TEXT REFERENCES targets(id) ON DELETE SET NULL,
 actor_id TEXT NOT NULL REFERENCES admins(id),
 action TEXT NOT NULL CHECK(action IN ('DEPLOY','ROLLBACK','VERIFY','DECOMMISSION')),
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
INSERT INTO project_deployment_jobs_v35 SELECT * FROM project_deployment_jobs;
UPDATE project_deployment_jobs_v35
 SET status='FAILED', error_code='PROJECT_CHECK_INTERRUPTED', health=''
 WHERE action='VERIFY' AND status='UNKNOWN';
UPDATE project_deployment_jobs_v35
 SET status='UNKNOWN', error_code='PROJECT_RECOVERY_REQUIRED', health=''
 WHERE action<>'VERIFY' AND status='QUEUED' AND phase='RECONCILING';
DROP TABLE project_deployment_jobs;
ALTER TABLE project_deployment_jobs_v35 RENAME TO project_deployment_jobs;
CREATE INDEX project_deployment_jobs_latest ON project_deployment_jobs(deployment_id,created_at DESC,id DESC);
CREATE INDEX project_deployment_jobs_recent ON project_deployment_jobs(created_at DESC,id DESC);
CREATE UNIQUE INDEX project_deployment_job_lock ON project_deployment_jobs(deployment_id)
 WHERE status IN ('QUEUED','RUNNING','UNKNOWN') AND action<>'VERIFY';
CREATE UNIQUE INDEX project_deployment_host_lock ON project_deployment_jobs(target_id)
 WHERE status IN ('QUEUED','RUNNING','UNKNOWN') AND action<>'VERIFY';
CREATE UNIQUE INDEX project_deployment_verify_lock ON project_deployment_jobs(deployment_id)
 WHERE status IN ('QUEUED','RUNNING') AND action='VERIFY';
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
PRAGMA user_version=35;`
