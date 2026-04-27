-- Persist the boost source ("external" vs "in_sandbox") on the boost row so
-- it can be surfaced via GET /v1/sandboxes/{id}/boost (and the UI), not only
-- on the EventBoostStarted payload. Defaults to "external" so existing rows
-- and external API callers continue to work unchanged.
ALTER TABLE boosts ADD COLUMN source TEXT NOT NULL DEFAULT 'external';
