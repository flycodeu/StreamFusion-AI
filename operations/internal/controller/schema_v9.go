package controller

// Observation records are additive. They never own mutation jobs or locks.
const schemaV9 = `
ALTER TABLE software_instances ADD COLUMN attempt_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE software_instances ADD COLUMN observation_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE software_instances ADD COLUMN observation_identity TEXT NOT NULL DEFAULT '';
CREATE TABLE host_observation_snapshots(host_id TEXT PRIMARY KEY REFERENCES targets(id) ON DELETE CASCADE,identity TEXT NOT NULL,confirmed_json TEXT NOT NULL DEFAULT '{}',attempt_json TEXT NOT NULL DEFAULT '{}',revision INTEGER NOT NULL DEFAULT 0);
CREATE TABLE observation_plans(host_id TEXT PRIMARY KEY REFERENCES targets(id) ON DELETE CASCADE,enabled INTEGER NOT NULL DEFAULT 1,identity TEXT NOT NULL DEFAULT '',resource_due INTEGER NOT NULL DEFAULT 0,software_due INTEGER NOT NULL DEFAULT 0,software_cursor TEXT NOT NULL DEFAULT '',failures INTEGER NOT NULL DEFAULT 0,auth_paused INTEGER NOT NULL DEFAULT 0);
CREATE TABLE observation_batches(id TEXT PRIMARY KEY,actor_id TEXT NOT NULL REFERENCES admins(id),actor_generation INTEGER NOT NULL,idempotency_key TEXT NOT NULL,request_hash TEXT NOT NULL,scope_hash TEXT NOT NULL,trace_id TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'QUEUED',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(actor_id,idempotency_key));
CREATE TABLE observation_batch_items(batch_id TEXT NOT NULL REFERENCES observation_batches(id) ON DELETE CASCADE,object_id TEXT NOT NULL,host_id TEXT NOT NULL,kind TEXT NOT NULL,config_hash TEXT NOT NULL,name TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'QUEUED',error_code TEXT NOT NULL DEFAULT '',observed_at TEXT NOT NULL DEFAULT '',PRIMARY KEY(batch_id,object_id));
CREATE INDEX observation_items_pending ON observation_batch_items(host_id,status);
CREATE TABLE observation_batch_requests(actor_id TEXT NOT NULL REFERENCES admins(id),idempotency_key TEXT NOT NULL,request_hash TEXT NOT NULL,batch_id TEXT NOT NULL REFERENCES observation_batches(id) ON DELETE CASCADE,PRIMARY KEY(actor_id,idempotency_key));
PRAGMA user_version=9;
`
