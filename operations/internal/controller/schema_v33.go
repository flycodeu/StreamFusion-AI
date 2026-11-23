package controller

const schemaV33 = `
CREATE TABLE application_jenkins_connections (
  project_id TEXT PRIMARY KEY REFERENCES application_projects(id) ON DELETE CASCADE,
  software_id TEXT NOT NULL REFERENCES software_instances(id),
  endpoint_identity TEXT NOT NULL, username TEXT NOT NULL,
  version INTEGER NOT NULL CHECK(version>0), token_ciphertext BLOB NOT NULL
);
CREATE TABLE application_qa_requests_v33 (
  deployment_id TEXT REFERENCES application_deployments(id) ON DELETE CASCADE,
  module_id TEXT REFERENCES application_modules(id) ON DELETE CASCADE,
  scope_id TEXT GENERATED ALWAYS AS (COALESCE('env/'||deployment_id,'module/'||module_id)) STORED,
  request_id TEXT NOT NULL, monitor_version INTEGER NOT NULL, queue_id INTEGER NOT NULL DEFAULT 0,
  CHECK((deployment_id IS NULL)<>(module_id IS NULL)), UNIQUE(scope_id,request_id)
);
INSERT INTO application_qa_requests_v33(deployment_id,request_id,monitor_version,queue_id)
  SELECT deployment_id,request_id,monitor_version,queue_id FROM application_qa_requests;
DROP TABLE application_qa_requests;
ALTER TABLE application_qa_requests_v33 RENAME TO application_qa_requests;
CREATE TRIGGER application_qa_clear_old_requests AFTER DELETE ON application_qa_monitors BEGIN
  DELETE FROM application_qa_requests WHERE deployment_id=OLD.deployment_id;
END;
PRAGMA user_version=33;`
