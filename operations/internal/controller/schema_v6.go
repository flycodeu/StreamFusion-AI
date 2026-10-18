package controller

const schemaV6 = `
ALTER TABLE artifacts ADD COLUMN runtime_environment_json TEXT;
CREATE INDEX jobs_environment_reference ON jobs(json_extract(CASE WHEN json_valid(input_json) THEN input_json ELSE '{}' END,'$.pluginInstallationId'));
PRAGMA user_version=6;
`
