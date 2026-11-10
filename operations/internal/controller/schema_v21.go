package controller

const schemaV21 = `
CREATE TABLE command_templates (
 id TEXT PRIMARY KEY,
 recipe_key TEXT NOT NULL UNIQUE,
 draft_json TEXT NOT NULL,
 published_json TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 0,
 version INTEGER NOT NULL DEFAULT 1,
 updated_at TEXT NOT NULL
);
PRAGMA user_version=21;
`
