package controller

const schemaV32 = `
CREATE TABLE application_qa_monitors(
    deployment_id TEXT PRIMARY KEY REFERENCES application_deployments(id) ON DELETE CASCADE,
    software_id TEXT NOT NULL REFERENCES software_instances(id),
    job_url TEXT NOT NULL,
    endpoint_identity TEXT NOT NULL,
    username TEXT NOT NULL,
    version INTEGER NOT NULL CHECK(version>0),
    token_ciphertext BLOB NOT NULL,
    UNIQUE(software_id,job_url)
);
CREATE TABLE application_qa_requests(
    deployment_id TEXT NOT NULL REFERENCES application_qa_monitors(deployment_id) ON DELETE CASCADE,
    request_id TEXT NOT NULL,
    monitor_version INTEGER NOT NULL,
    queue_id INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(deployment_id,request_id)
);
CREATE TRIGGER application_qa_scope_changed AFTER UPDATE OF project_id,environment_id,module_id,host_id ON application_deployments
WHEN OLD.project_id IS NOT NEW.project_id OR OLD.environment_id IS NOT NEW.environment_id
     OR OLD.module_id IS NOT NEW.module_id OR OLD.host_id IS NOT NEW.host_id
BEGIN
    DELETE FROM application_qa_monitors WHERE deployment_id=NEW.id;
END;
PRAGMA user_version=32;
`
