CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, release TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE controller_state (id INTEGER PRIMARY KEY CHECK(id=1), controller_id TEXT NOT NULL UNIQUE, name TEXT NOT NULL, version INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL);
CREATE TABLE admins (id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash BLOB NOT NULL, session_generation INTEGER NOT NULL DEFAULT 1, failed_attempts INTEGER NOT NULL DEFAULT 0, locked_until INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL);
CREATE TABLE sessions (token_hash TEXT PRIMARY KEY, admin_id TEXT REFERENCES admins(id), csrf TEXT NOT NULL, generation INTEGER NOT NULL, created_at INTEGER NOT NULL, last_seen INTEGER NOT NULL, expires_at INTEGER NOT NULL);
CREATE INDEX sessions_admin ON sessions(admin_id);
CREATE INDEX sessions_expiry ON sessions(expires_at);
CREATE TABLE audit (id TEXT PRIMARY KEY, actor TEXT NOT NULL, action TEXT NOT NULL, object_id TEXT NOT NULL, result TEXT NOT NULL, trace_id TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE INDEX audit_created ON audit(created_at DESC, id DESC);
CREATE TABLE credential_refs (id TEXT NOT NULL, revision INTEGER NOT NULL, name TEXT NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('PASSWORD','TOKEN','SSH_KEY')), material TEXT NOT NULL, created_at TEXT NOT NULL, revoked_at TEXT, PRIMARY KEY(id, revision));
