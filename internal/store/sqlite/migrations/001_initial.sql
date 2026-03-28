CREATE TABLE projects (
    project_id   TEXT PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    metadata     TEXT
);

CREATE TABLE sandboxes (
    sandbox_id         TEXT PRIMARY KEY,
    project_id         TEXT NOT NULL REFERENCES projects(project_id),
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

CREATE TABLE snapshots (
    snapshot_id      TEXT PRIMARY KEY,
    sandbox_id       TEXT NOT NULL REFERENCES sandboxes(sandbox_id),
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

CREATE TABLE sessions (
    session_id       TEXT PRIMARY KEY,
    sandbox_id       TEXT NOT NULL REFERENCES sandboxes(sandbox_id),
    backing          TEXT NOT NULL,
    shell            TEXT NOT NULL DEFAULT '/bin/bash',
    state            TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    last_attached_at TEXT,
    idle_timeout_sec INTEGER,
    metadata         TEXT
);

CREATE TABLE base_images (
    image_id           TEXT PRIMARY KEY,
    project_scope      TEXT,
    name               TEXT NOT NULL,
    version            TEXT NOT NULL,
    source_type        TEXT NOT NULL,
    source_snapshot_id TEXT,
    backend            TEXT NOT NULL,
    backend_ref        TEXT,
    architecture       TEXT NOT NULL,
    state              TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    metadata           TEXT,
    UNIQUE(name, version)
);

CREATE TABLE operations (
    operation_id   TEXT PRIMARY KEY,
    resource_type  TEXT NOT NULL,
    resource_id    TEXT NOT NULL,
    sandbox_id     TEXT,
    snapshot_id    TEXT,
    type           TEXT NOT NULL,
    state          TEXT NOT NULL,
    started_at     TEXT NOT NULL,
    finished_at    TEXT,
    error_text     TEXT,
    metadata       TEXT
);

CREATE TABLE port_bindings (
    sandbox_id     TEXT NOT NULL REFERENCES sandboxes(sandbox_id),
    target_port    INTEGER NOT NULL,
    published_port INTEGER NOT NULL UNIQUE,
    host_address   TEXT NOT NULL,
    created_at     TEXT NOT NULL,
    PRIMARY KEY (sandbox_id, target_port)
);

CREATE INDEX idx_sandboxes_project ON sandboxes(project_id);
CREATE INDEX idx_sandboxes_state ON sandboxes(state);
CREATE INDEX idx_sandboxes_expires ON sandboxes(expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX idx_snapshots_sandbox ON snapshots(sandbox_id);
CREATE INDEX idx_sessions_sandbox ON sessions(sandbox_id);
CREATE INDEX idx_sessions_state ON sessions(state);
CREATE INDEX idx_operations_resource ON operations(resource_type, resource_id);
CREATE INDEX idx_operations_sandbox ON operations(sandbox_id) WHERE sandbox_id IS NOT NULL;
CREATE INDEX idx_operations_state ON operations(state);
