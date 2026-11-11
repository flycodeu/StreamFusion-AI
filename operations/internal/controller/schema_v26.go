package controller

// Project codes are added without changing project IDs or their references.
const schemaV26 = `
ALTER TABLE application_projects ADD COLUMN code TEXT NOT NULL DEFAULT '' COLLATE NOCASE;
UPDATE application_projects SET code='project-'||id;
CREATE UNIQUE INDEX application_projects_code ON application_projects(code);
PRAGMA user_version=26;
`
