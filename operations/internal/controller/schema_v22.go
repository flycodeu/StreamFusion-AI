package controller

// Built-in command snapshots are persisted independently from immutable package
// identities. The marker also prevents older controllers from opening this data.
const schemaV22 = `
ALTER TABLE command_templates ADD COLUMN builtin INTEGER NOT NULL DEFAULT 0;
PRAGMA user_version=22;
`
