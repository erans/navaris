-- Boost rows: time-bounded resource bumps with auto-revert.
-- See docs/superpowers/specs/2026-04-26-sandbox-boost-design.md.

CREATE TABLE boosts (
    boost_id                  TEXT PRIMARY KEY,
    sandbox_id                TEXT NOT NULL UNIQUE REFERENCES sandboxes(sandbox_id) ON DELETE CASCADE,
    original_cpu_limit        INTEGER,
    original_memory_limit_mb  INTEGER,
    boosted_cpu_limit         INTEGER,
    boosted_memory_limit_mb   INTEGER,
    started_at                TEXT NOT NULL,
    expires_at                TEXT NOT NULL,
    state                     TEXT NOT NULL,
    revert_attempts           INTEGER NOT NULL DEFAULT 0,
    last_error                TEXT
);

CREATE INDEX idx_boosts_expires_at ON boosts(expires_at);
