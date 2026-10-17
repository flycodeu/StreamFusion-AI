package controller

const schemaV5 = `
CREATE TABLE artifacts_v5(id TEXT PRIMARY KEY,name TEXT NOT NULL,archive_sha256 TEXT NOT NULL,byte_size INTEGER NOT NULL,image_id TEXT NOT NULL,created_at TEXT NOT NULL,recipe TEXT NOT NULL DEFAULT 'java-compose-v1' CHECK(recipe IN ('java-compose-v1','preview-node-v1','zlm-direct-v1')));
INSERT INTO artifacts_v5(id,name,archive_sha256,byte_size,image_id,created_at,recipe) SELECT id,name,archive_sha256,byte_size,image_id,created_at,recipe FROM artifacts;
DROP TABLE artifacts;
ALTER TABLE artifacts_v5 RENAME TO artifacts;
PRAGMA user_version=5;
`
