package controller

// Application drafts are additive. Existing software, jobs and release snapshots
// retain their identities and are not rewritten during this migration.
const schemaV25 = `
CREATE TABLE application_projects(id TEXT PRIMARY KEY,name TEXT NOT NULL COLLATE NOCASE UNIQUE,description TEXT NOT NULL DEFAULT '',version INTEGER NOT NULL DEFAULT 1,archived INTEGER NOT NULL DEFAULT 0 CHECK(archived IN (0,1)),created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE application_repositories(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES application_projects(id),name TEXT NOT NULL,url TEXT NOT NULL DEFAULT '',git_account_id TEXT REFERENCES git_accounts(id),branch TEXT NOT NULL DEFAULT '',position INTEGER NOT NULL,UNIQUE(project_id,id));
CREATE TABLE application_modules(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES application_projects(id),name TEXT NOT NULL,repository_id TEXT,directory TEXT NOT NULL DEFAULT '.',build_host_id TEXT REFERENCES targets(id),build_software_id TEXT REFERENCES software_instances(id),config_json TEXT NOT NULL,position INTEGER NOT NULL,UNIQUE(project_id,id),FOREIGN KEY(project_id,repository_id) REFERENCES application_repositories(project_id,id));
CREATE TABLE application_environments(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES application_projects(id),name TEXT NOT NULL,position INTEGER NOT NULL,UNIQUE(project_id,id));
CREATE TABLE application_deployments(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES application_projects(id),environment_id TEXT NOT NULL,module_id TEXT NOT NULL,host_id TEXT REFERENCES targets(id),runtime_software_id TEXT REFERENCES software_instances(id),config_json TEXT NOT NULL,position INTEGER NOT NULL,UNIQUE(environment_id,module_id),FOREIGN KEY(project_id,environment_id) REFERENCES application_environments(project_id,id),FOREIGN KEY(project_id,module_id) REFERENCES application_modules(project_id,id));
CREATE INDEX application_repositories_account ON application_repositories(git_account_id);
CREATE INDEX application_modules_build_host ON application_modules(build_host_id);
CREATE INDEX application_modules_build_software ON application_modules(build_software_id);
CREATE INDEX application_deployments_runtime_software ON application_deployments(runtime_software_id);
CREATE INDEX application_deployments_host ON application_deployments(host_id);
CREATE INDEX application_projects_recent ON application_projects(updated_at DESC,id DESC);
PRAGMA user_version=25;
`
