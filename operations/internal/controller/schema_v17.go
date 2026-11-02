package controller

// Registry presets are controller choices, never an instruction to alter a host.
// Existing tables and historical operation snapshots are not rewritten.
const schemaV17 = `
CREATE TABLE node_registries(id TEXT PRIMARY KEY,name TEXT NOT NULL,url TEXT NOT NULL UNIQUE,version INTEGER NOT NULL DEFAULT 1 CHECK(version>0),built_in INTEGER NOT NULL DEFAULT 0 CHECK(built_in IN (0,1)),created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
INSERT INTO node_registries(id,name,url,built_in,created_at,updated_at) VALUES
('63ae6644-727c-4c96-9ff5-f207cc79ee15','npm 官方','https://registry.npmjs.org/',1,strftime('%Y-%m-%dT%H:%M:%SZ','now'),strftime('%Y-%m-%dT%H:%M:%SZ','now')),
('5bdc52a7-1ed0-4fa0-a64f-a997dfdf8ff6','npmmirror','https://registry.npmmirror.com/',0,strftime('%Y-%m-%dT%H:%M:%SZ','now'),strftime('%Y-%m-%dT%H:%M:%SZ','now'));
PRAGMA user_version=17;
`
