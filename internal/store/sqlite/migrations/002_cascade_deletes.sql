-- Add ON DELETE CASCADE to foreign keys so deleting a project removes its
-- sandboxes, and deleting a sandbox removes its snapshots/sessions/port bindings.
-- SQLite does not support ALTER TABLE ... ALTER CONSTRAINT, so we recreate the
-- tables preserving all data.

PRAGMA foreign_keys = OFF;

-- Rebuild sandboxes with CASCADE on project_id.
CREATE TABLE sandboxes_new (
    sandbox_id         TEXT PRIMARY KEY,
    project_id         TEXT NOT NULL REFERENCES projects(project_id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    state              TEXT NOT NULL,
    backend            TEXT NOT NULL,
    backend_ref        TEXT,
    host_id            TEXT,
    source_image_id    TEXT,
    parent_snapshot_id TEXT,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    expires_at         TEXT,
    cpu_limit          INTEGER,
    memory_limit_mb    INTEGER,
    network_mode       TEXT NOT NULL DEFAULT 'isolated',
    metadata           TEXT,
    UNIQUE(project_id, name)
);
INSERT INTO sandboxes_new SELECT * FROM sandboxes;
DROP TABLE sandboxes;
ALTER TABLE sandboxes_new RENAME TO sandboxes;

-- Rebuild snapshots with CASCADE on sandbox_id.
CREATE TABLE snapshots_new (
    snapshot_id      TEXT PRIMARY KEY,
    sandbox_id       TEXT NOT NULL REFERENCES sandboxes(sandbox_id) ON DELETE CASCADE,
    backend          TEXT NOT NULL,
    backend_ref      TEXT,
    label            TEXT NOT NULL,
    state            TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    parent_image_id  TEXT,
    publishable      INTEGER NOT NULL DEFAULT 0,
    consistency_mode TEXT NOT NULL DEFAULT 'stopped',
    metadata         TEXT
);
INSERT INTO snapshots_new SELECT * FROM snapshots;
DROP TABLE snapshots;
ALTER TABLE snapshots_new RENAME TO snapshots;

-- Rebuild sessions with CASCADE on sandbox_id.
CREATE TABLE sessions_new (
    session_id       TEXT PRIMARY KEY,
    sandbox_id       TEXT NOT NULL REFERENCES sandboxes(sandbox_id) ON DELETE CASCADE,
    backing          TEXT NOT NULL,
    shell            TEXT NOT NULL DEFAULT '/bin/bash',
    state            TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    last_attached_at TEXT,
    idle_timeout_sec INTEGER,
    metadata         TEXT
);
INSERT INTO sessions_new SELECT * FROM sessions;
DROP TABLE sessions;
ALTER TABLE sessions_new RENAME TO sessions;

-- Rebuild port_bindings with CASCADE on sandbox_id.
CREATE TABLE port_bindings_new (
    sandbox_id     TEXT NOT NULL REFERENCES sandboxes(sandbox_id) ON DELETE CASCADE,
    target_port    INTEGER NOT NULL,
    published_port INTEGER NOT NULL UNIQUE,
    host_address   TEXT NOT NULL,
    created_at     TEXT NOT NULL,
    PRIMARY KEY (sandbox_id, target_port)
);
INSERT INTO port_bindings_new SELECT * FROM port_bindings;
DROP TABLE port_bindings;
ALTER TABLE port_bindings_new RENAME TO port_bindings;

-- Recreate indexes that were dropped with the original tables.
CREATE INDEX idx_sandboxes_project ON sandboxes(project_id);
CREATE INDEX idx_sandboxes_state ON sandboxes(state);
CREATE INDEX idx_sandboxes_expires ON sandboxes(expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX idx_snapshots_sandbox ON snapshots(sandbox_id);
CREATE INDEX idx_sessions_sandbox ON sessions(sandbox_id);
CREATE INDEX idx_sessions_state ON sessions(state);

PRAGMA foreign_keys = ON;
