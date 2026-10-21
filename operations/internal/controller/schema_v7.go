package controller

// Host identity and history remain in targets so instance/job foreign keys are
// preserved. Fingerprint confirmations are short-lived, single-use challenges.
const schemaV7 = `
ALTER TABLE targets ADD COLUMN maintenance INTEGER NOT NULL DEFAULT 0 CHECK(maintenance IN (0,1));
ALTER TABLE targets ADD COLUMN capabilities_json TEXT NOT NULL DEFAULT '{}';
CREATE TABLE host_fingerprint_challenges(token_hash TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES admins(id),address TEXT NOT NULL,ssh_port INTEGER NOT NULL,ssh_user TEXT NOT NULL,credential_id TEXT NOT NULL,credential_revision INTEGER NOT NULL,host_fingerprint TEXT NOT NULL,expires_at TEXT NOT NULL,FOREIGN KEY(credential_id,credential_revision) REFERENCES credential_refs(id,revision));
CREATE INDEX host_fingerprint_expiry ON host_fingerprint_challenges(expires_at);
PRAGMA user_version=7;
`
