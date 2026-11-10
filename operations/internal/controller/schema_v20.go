package controller

const schemaV20 = `
ALTER TABLE git_accounts ADD COLUMN auth_method TEXT NOT NULL DEFAULT 'SSH' CHECK(auth_method IN ('SSH','HTTPS'));
ALTER TABLE git_accounts ADD COLUMN login_username TEXT NOT NULL DEFAULT '';
ALTER TABLE git_accounts ADD COLUMN configured_version INTEGER NOT NULL DEFAULT 0;
PRAGMA user_version=20;
`
