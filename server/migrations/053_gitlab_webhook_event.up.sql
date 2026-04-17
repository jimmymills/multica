-- Phase 2b: webhook event queue + last-received timestamp.
-- The queue is the boundary between the synchronous webhook receiver
-- (must respond <10s for GitLab not to cancel) and the async worker pool
-- that applies events to the cache.

CREATE TABLE IF NOT EXISTS gitlab_webhook_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,           -- "issue", "note", "emoji", "label"
    object_id BIGINT NOT NULL,          -- gitlab issue iid / note id / award id / label id
    gitlab_updated_at TIMESTAMPTZ,      -- from the payload, used to skip stale events
    payload_hash BYTEA NOT NULL,        -- sha256 of the canonical payload — for dedupe
    payload JSONB NOT NULL,             -- full event body, consumed by the worker
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ,
    UNIQUE (workspace_id, event_type, object_id, payload_hash)
);

-- Workers claim with FOR UPDATE SKIP LOCKED ordered by received_at.
CREATE INDEX idx_gitlab_webhook_event_unprocessed
    ON gitlab_webhook_event(received_at)
    WHERE processed_at IS NULL;

-- Stale-webhook detection: when did we last successfully receive an event
-- for this workspace? NULL until the first delivery.
ALTER TABLE workspace_gitlab_connection
    ADD COLUMN last_webhook_received_at TIMESTAMPTZ;
