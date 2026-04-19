CREATE TABLE agent_runtime_assignment (
    agent_id   UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    runtime_id UUID NOT NULL REFERENCES agent_runtime(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, runtime_id)
);

CREATE INDEX idx_agent_runtime_assignment_runtime ON agent_runtime_assignment(runtime_id);

-- Backfill from the single-runtime column.
INSERT INTO agent_runtime_assignment (agent_id, runtime_id)
SELECT id, runtime_id FROM agent WHERE runtime_id IS NOT NULL;

-- Drop the legacy FK and column.
ALTER TABLE agent DROP COLUMN runtime_id;
