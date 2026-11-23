package controller

// Historical rows retain their instance ID as their account ID and keep their
// original encryption binding and version.
const schemaV31 = `
ALTER TABLE software_login_accounts RENAME TO software_login_accounts_v30;
CREATE TABLE software_login_accounts(
    software_id TEXT NOT NULL REFERENCES software_instances(id),
    account_id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK(version > 0),
    ciphertext BLOB NOT NULL,
    PRIMARY KEY(software_id,account_id)
);
INSERT INTO software_login_accounts(software_id,account_id,version,ciphertext)
SELECT software_id,software_id,version,ciphertext FROM software_login_accounts_v30;
DROP TABLE software_login_accounts_v30;
PRAGMA user_version=31;
`
