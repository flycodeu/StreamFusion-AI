package controller

const schemaV29 = `
CREATE TABLE software_login_accounts(
    software_id TEXT PRIMARY KEY REFERENCES software_instances(id),
    version INTEGER NOT NULL CHECK(version > 0),
    ciphertext BLOB NOT NULL
);
PRAGMA user_version=29;
`
