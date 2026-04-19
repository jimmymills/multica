ALTER TABLE agent ADD COLUMN runtime_id UUID REFERENCES agent_runtime(id) ON DELETE RESTRICT;

-- Restore from the earliest-created assignment row per agent.
UPDATE agent a
SET runtime_id = sub.runtime_id
FROM (
    SELECT DISTINCT ON (agent_id) agent_id, runtime_id
    FROM agent_runtime_assignment
    ORDER BY agent_id, created_at ASC
) sub
WHERE a.id = sub.agent_id;

DROP INDEX IF EXISTS idx_agent_runtime_assignment_runtime;
DROP TABLE agent_runtime_assignment;
