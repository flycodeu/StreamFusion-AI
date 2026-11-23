package controller

// Keep project, module and deployment identities while adding the Jenkins link.
const schemaV28 = `
ALTER TABLE application_projects ADD COLUMN jenkins_url TEXT NOT NULL DEFAULT '';
PRAGMA user_version=28;
`
