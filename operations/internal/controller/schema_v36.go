package controller

import (
	"errors"
	"fmt"
)

func (s *Store) migrateAlgorithmHostBindings() (err error) {
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
	if _, err = tx.Exec(schemaV36); err != nil {
		return err
	}
	var invalid int
	if err = tx.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("SCHEMA_MISMATCH: 迁移36发现 %d 条无效外键", invalid)
	}
	if _, err = tx.Exec("INSERT INTO schema_migrations VALUES(36,?,?,?)", hashToken(schemaV36), Version, Now()); err != nil {
		return err
	}
	return tx.Commit()
}

// Rebuild the binding table without changing any deployment ID or rewriting
// the JSON, historical jobs, QA associations, plans or release pointers.
const schemaV36 = `
DROP TRIGGER application_qa_scope_changed;
DROP TRIGGER project_deployment_guard_delete;
DROP TRIGGER project_deployment_guard_binding;
DROP TRIGGER project_deployment_guard_target;
CREATE TABLE application_deployments_v36(
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES application_projects(id),
 environment_id TEXT NOT NULL,
 module_id TEXT NOT NULL,
 host_id TEXT REFERENCES targets(id),
 runtime_software_id TEXT REFERENCES software_instances(id),
 config_json TEXT NOT NULL,
 position INTEGER NOT NULL,
 FOREIGN KEY(project_id,environment_id) REFERENCES application_environments(project_id,id),
 FOREIGN KEY(project_id,module_id) REFERENCES application_modules(project_id,id)
);
INSERT INTO application_deployments_v36 SELECT id,project_id,environment_id,module_id,host_id,runtime_software_id,config_json,position FROM application_deployments;
DROP TABLE application_deployments;
ALTER TABLE application_deployments_v36 RENAME TO application_deployments;
CREATE UNIQUE INDEX application_deployments_module_host ON application_deployments(environment_id,module_id,coalesce(host_id,''));
CREATE INDEX application_deployments_runtime_software ON application_deployments(runtime_software_id);
CREATE INDEX application_deployments_host ON application_deployments(host_id);
CREATE TRIGGER application_qa_scope_changed AFTER UPDATE OF project_id,environment_id,module_id,host_id ON application_deployments
 WHEN OLD.project_id IS NOT NEW.project_id OR OLD.environment_id IS NOT NEW.environment_id
 OR OLD.module_id IS NOT NEW.module_id OR OLD.host_id IS NOT NEW.host_id
 BEGIN DELETE FROM application_qa_monitors WHERE deployment_id=NEW.id; END;
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
PRAGMA user_version=36;`
