-- Per-sandbox toggle for the in-sandbox boost channel.
-- See docs/superpowers/specs/2026-04-26-in-sandbox-boost-channel-design.md.
ALTER TABLE sandboxes ADD COLUMN enable_boost_channel INTEGER NOT NULL DEFAULT 1;
