-- name: CreateRuntimeGroup :one
INSERT INTO runtime_group (workspace_id, name, description, created_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateRuntimeGroup :one
UPDATE runtime_group SET
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteRuntimeGroup :exec
DELETE FROM runtime_group WHERE id = $1;

-- name: GetRuntimeGroup :one
SELECT * FROM runtime_group WHERE id = $1;

-- name: GetRuntimeGroupInWorkspace :one
SELECT * FROM runtime_group WHERE id = $1 AND workspace_id = $2;

-- name: ListRuntimeGroupsByWorkspace :many
SELECT * FROM runtime_group WHERE workspace_id = $1 ORDER BY name ASC;

-- name: AddRuntimeGroupMember :exec
INSERT INTO runtime_group_member (group_id, runtime_id)
VALUES ($1, $2)
ON CONFLICT (group_id, runtime_id) DO NOTHING;

-- name: RemoveRuntimeGroupMembersNotIn :exec
DELETE FROM runtime_group_member
WHERE group_id = $1
  AND runtime_id <> ALL(@runtime_ids::uuid[]);

-- name: ListRuntimeGroupMembers :many
SELECT
    ar.id            AS runtime_id,
    ar.name          AS runtime_name,
    ar.status        AS runtime_status,
    ar.runtime_mode  AS runtime_mode,
    ar.provider      AS runtime_provider,
    ar.owner_id      AS runtime_owner_id,
    ar.device_info   AS runtime_device_info,
    (SELECT MAX(atq.created_at) FROM agent_task_queue atq WHERE atq.runtime_id = rgm.runtime_id)::timestamptz AS last_used_at
FROM runtime_group_member rgm
JOIN agent_runtime ar ON ar.id = rgm.runtime_id
WHERE rgm.group_id = $1
ORDER BY rgm.created_at ASC;

-- name: ListRuntimeGroupMembersByWorkspace :many
-- Batched version of ListRuntimeGroupMembers for the groups list endpoint.
SELECT
    rgm.group_id     AS group_id,
    ar.id            AS runtime_id,
    ar.name          AS runtime_name,
    ar.status        AS runtime_status,
    ar.runtime_mode  AS runtime_mode,
    ar.provider      AS runtime_provider,
    ar.owner_id      AS runtime_owner_id,
    ar.device_info   AS runtime_device_info,
    (SELECT MAX(atq.created_at) FROM agent_task_queue atq WHERE atq.runtime_id = rgm.runtime_id)::timestamptz AS last_used_at
FROM runtime_group_member rgm
JOIN runtime_group rg ON rg.id = rgm.group_id
JOIN agent_runtime ar ON ar.id = rgm.runtime_id
WHERE rg.workspace_id = $1
ORDER BY rgm.group_id, rgm.created_at ASC;

-- name: CountAgentsUsingRuntimeGroup :one
SELECT count(*) FROM agent_runtime_group WHERE group_id = $1;

-- name: AddAgentRuntimeGroup :exec
INSERT INTO agent_runtime_group (agent_id, group_id)
VALUES ($1, $2)
ON CONFLICT (agent_id, group_id) DO NOTHING;

-- name: RemoveAgentRuntimeGroupsNotIn :exec
DELETE FROM agent_runtime_group
WHERE agent_id = $1
  AND group_id <> ALL(@group_ids::uuid[]);

-- name: ListAgentRuntimeGroupsByAgent :many
SELECT
    rg.id            AS group_id,
    rg.name          AS group_name,
    rg.description   AS group_description
FROM agent_runtime_group arg
JOIN runtime_group rg ON rg.id = arg.group_id
WHERE arg.agent_id = $1
ORDER BY rg.name ASC;

-- name: ListAgentRuntimeGroupsByWorkspace :many
-- Batched version for ListAgents.
SELECT
    arg.agent_id     AS agent_id,
    rg.id            AS group_id,
    rg.name          AS group_name,
    rg.description   AS group_description
FROM agent_runtime_group arg
JOIN runtime_group rg ON rg.id = arg.group_id
JOIN agent a ON a.id = arg.agent_id
WHERE a.workspace_id = $1
ORDER BY arg.agent_id, rg.name ASC;

-- name: GetActiveRuntimeGroupOverride :one
-- Returns the currently-active override on a group, if any.
SELECT
    rgo.id,
    rgo.group_id,
    rgo.runtime_id,
    rgo.starts_at,
    rgo.ends_at,
    rgo.created_by,
    rgo.created_at,
    rgo.updated_at,
    ar.name AS runtime_name
FROM runtime_group_override rgo
JOIN agent_runtime ar ON ar.id = rgo.runtime_id
WHERE rgo.group_id = $1
  AND rgo.starts_at <= now()
  AND now() < rgo.ends_at
ORDER BY rgo.starts_at DESC
LIMIT 1;

-- name: ListActiveRuntimeGroupOverridesByWorkspace :many
-- Batched active-overrides fetch for the groups list + ListAgents.
SELECT
    rgo.id,
    rgo.group_id,
    rgo.runtime_id,
    rgo.starts_at,
    rgo.ends_at,
    ar.name AS runtime_name
FROM runtime_group_override rgo
JOIN runtime_group rg ON rg.id = rgo.group_id
JOIN agent_runtime ar ON ar.id = rgo.runtime_id
WHERE rg.workspace_id = $1
  AND rgo.starts_at <= now()
  AND now() < rgo.ends_at
ORDER BY rgo.group_id, rgo.starts_at DESC;

-- name: ClipActiveRuntimeGroupOverride :exec
-- Clips any currently-active override on the group to end now. Called before
-- inserting a replacement so there is at most one active row per group.
UPDATE runtime_group_override
SET ends_at = now(), updated_at = now()
WHERE group_id = $1
  AND starts_at <= now()
  AND now() < ends_at;

-- name: InsertRuntimeGroupOverride :one
INSERT INTO runtime_group_override (group_id, runtime_id, starts_at, ends_at, created_by)
VALUES ($1, $2, now(), $3, $4)
RETURNING *;

-- name: ClearRuntimeGroupOverride :exec
-- Manually clears the active override (same action as ClipActiveRuntimeGroupOverride
-- but named for intent at call sites that are "user cancelled").
UPDATE runtime_group_override
SET ends_at = now(), updated_at = now()
WHERE group_id = $1
  AND starts_at <= now()
  AND now() < ends_at;
