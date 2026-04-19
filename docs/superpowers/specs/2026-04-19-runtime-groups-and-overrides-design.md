# Runtime Groups + Temporary Priority Overrides — Design

**Status:** Draft
**Date:** 2026-04-19
**Follows:** `2026-04-19-agent-multi-runtime-design.md`

## Problem

After the multi-runtime landed, users still have to pick the same long list of individual runtimes for every agent they create. And when one team member goes away (vacation, leave), their mostly-idle runtime is a great candidate to bear more of the team's load — but the current selection algorithm has no way to prefer it specifically.

## Goal

Two linked features:

1. **Runtime groups** — named, reusable collections of runtimes. Agents can be linked to one or more groups; the group membership contributes to the agent's effective runtime set at enqueue time. Editing a group propagates to every agent using it.
2. **Temporary priority override** — on any group, pin one member runtime to "use this first while it's online" for a bounded time window. Useful when a person is offline and their runtime should carry more team work until they return.

The two features share the group concept, so they ship in one spec/plan.

## Non-goals

- Pre-scheduled overrides (start time in the future). Schema has `starts_at`, adding a scheduler is an additive follow-up.
- Workspace-level quotas / max members per group.
- Cross-workspace group sharing.
- Override history / audit view (data is there; UI is a follow-up).
- Nested groups (a group containing another group). Flat only.
- Changing the underlying least-loaded selection algorithm when no override is active.

## Architecture

### Effective runtime set per agent

```
candidates = agent_runtime_assignment ∪ runtime_group_member (via agent_runtime_group)
```

UNION dedupes naturally. An agent can be linked to individual runtimes, groups, or both (see "hybrid" decision below). Editing a group propagates live — the next `SelectRuntimeForAgent` sees the updated member set.

### Selection order (extended)

Four tiers, sorted in order:

1. Online **and** currently in the active-override set for any of this agent's groups.
2. Online, not overriding.
3. Offline, overriding. (Grouped with tier 4 in practice — if the override runtime is offline, nothing is won by preferring it over other offline runtimes.)
4. Offline.

Within each tier: existing `tokens_7d ASC, last_used_at ASC NULLS FIRST`.

### Override semantics

- Absolute preference: if the override's runtime is online, it wins regardless of 7-day tokens or LRU.
- One active override per group at a time. Creating a new override on a group retires any existing one (sets `ends_at = now()`).
- Override's `runtime_id` must be a current member of the group. Enforced by composite FK `(group_id, runtime_id) → runtime_group_member(group_id, runtime_id) ON DELETE CASCADE`, so removing a runtime from a group auto-deletes its override.
- Expiration is soft: the selection query filters `starts_at <= now() < ends_at`. No cron/worker.
- Manual cancellation: `UPDATE runtime_group_override SET ends_at = now() WHERE group_id = $1 AND ends_at > now()`.

### Hybrid assignment (decision)

Agents can combine groups and individual runtimes. The effective set is the union. This preserves the current multi-runtime UX for one-off cases while letting teams lean on shared groups.

### Ownership / permissions

Groups are workspace-shared. Any workspace member can create, edit, or delete a group. Same policy for overrides.

### Invariant

Every agent must have at least one source of runtimes: `len(runtime_ids) + len(group_ids) >= 1`. An agent assigned only to an empty group (no members) is technically allowed by the schema, but the UI warns, and enqueue returns the existing "agent has no runtimes" error.

## Schema

New migration (next number after 060):

```sql
CREATE TABLE runtime_group (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

CREATE TABLE runtime_group_member (
    group_id UUID NOT NULL REFERENCES runtime_group(id) ON DELETE CASCADE,
    runtime_id UUID NOT NULL REFERENCES agent_runtime(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, runtime_id)
);

CREATE INDEX idx_runtime_group_member_runtime ON runtime_group_member(runtime_id);

CREATE TABLE agent_runtime_group (
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    group_id UUID NOT NULL REFERENCES runtime_group(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, group_id)
);

CREATE INDEX idx_agent_runtime_group_group ON agent_runtime_group(group_id);

CREATE TABLE runtime_group_override (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL,
    runtime_id UUID NOT NULL,
    starts_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ends_at TIMESTAMPTZ NOT NULL,
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (group_id, runtime_id)
        REFERENCES runtime_group_member(group_id, runtime_id)
        ON DELETE CASCADE,
    CHECK (ends_at > starts_at)
);

CREATE INDEX idx_runtime_group_override_group_ends
    ON runtime_group_override(group_id, ends_at DESC);
```

Down migration drops the four new tables (cascade from the leaf tables) and the indexes. No data backfill needed on rollback — the agent's individual `runtime_id` assignments are untouched.

## SQL — core selection query

Replace the current `SelectRuntimeForAgent` body:

```sql
-- name: SelectRuntimeForAgent :one
WITH window_since AS (
    SELECT DATE_TRUNC('day', now() - INTERVAL '7 days', 'UTC') AS ts
),
candidates AS (
    SELECT runtime_id FROM agent_runtime_assignment WHERE agent_id = $1
    UNION
    SELECT rgm.runtime_id
    FROM agent_runtime_group arg
    JOIN runtime_group_member rgm ON rgm.group_id = arg.group_id
    WHERE arg.agent_id = $1
),
active_overrides AS (
    SELECT rgo.runtime_id
    FROM agent_runtime_group arg
    JOIN runtime_group_override rgo ON rgo.group_id = arg.group_id
    WHERE arg.agent_id = $1
      AND rgo.starts_at <= now()
      AND now() < rgo.ends_at
),
runtime_load AS (
    SELECT atq.runtime_id,
           COALESCE(SUM(tu.input_tokens + tu.output_tokens
                      + tu.cache_read_tokens + tu.cache_write_tokens), 0) AS tokens_7d,
           MAX(atq.created_at) AS last_used_at
    FROM agent_task_queue atq
    LEFT JOIN task_usage tu ON tu.task_id = atq.id
                           AND tu.created_at >= (SELECT ts FROM window_since)
    WHERE atq.runtime_id IN (SELECT runtime_id FROM candidates)
      AND atq.created_at >= (SELECT ts FROM window_since)
    GROUP BY atq.runtime_id
)
SELECT c.runtime_id
FROM candidates c
JOIN agent_runtime r ON r.id = c.runtime_id
LEFT JOIN runtime_load rl ON rl.runtime_id = c.runtime_id
ORDER BY
    (r.status = 'online' AND c.runtime_id IN (SELECT runtime_id FROM active_overrides)) DESC,
    (r.status = 'online') DESC,
    COALESCE(rl.tokens_7d, 0) ASC,
    rl.last_used_at ASC NULLS FIRST
LIMIT 1;
```

## SQL — new CRUD queries

**Groups:**
- `CreateRuntimeGroup`, `UpdateRuntimeGroup`, `DeleteRuntimeGroup`, `GetRuntimeGroup`, `ListRuntimeGroupsByWorkspace`.
- `AddRuntimeGroupMember`, `RemoveRuntimeGroupMembersNotIn`, `ListRuntimeGroupMembers`, `ListRuntimeGroupMembersByWorkspace` (batch for list endpoints).

**Agent ↔ group links:**
- `AddAgentRuntimeGroup`, `RemoveAgentRuntimeGroupsNotIn`.
- `ListAgentRuntimeGroupsByAgent`, `ListAgentRuntimeGroupsByWorkspace` (batch).

**Overrides:**
- `GetActiveRuntimeGroupOverride(group_id)` — returns the single active row if any.
- `UpsertRuntimeGroupOverride` — two-statement helper: clip existing active row's `ends_at` to `now()`, then insert the new row. Called in a transaction.
- `ClearRuntimeGroupOverride(group_id)` — `UPDATE ... SET ends_at = now() WHERE group_id = $1 AND ends_at > now()`.
- `ListActiveOverridesByWorkspace` — batch fetch for list endpoints.

## Service layer

`server/internal/service/runtime_group.go` — new `RuntimeGroupService`:

- `CreateGroup(ctx, wsID, name, description, userID, runtimeIDs) (Group, error)` — in a transaction: insert group, then `AddRuntimeGroupMember` per runtime. Validates each runtime is in the workspace.
- `UpdateGroup(ctx, groupID, name?, description?, runtimeIDs?)` — in a transaction: optional name/description update, optional member replacement.
- `DeleteGroup(ctx, groupID)` — single query; cascades drop `agent_runtime_group` and `runtime_group_override` rows.
- `SetOverride(ctx, groupID, runtimeID, endsAt, userID)` — validates runtime is a current member, then `UpsertRuntimeGroupOverride`.
- `ClearOverride(ctx, groupID)` — single call.

No change to `TaskService.EnqueueTaskFor*` — `SelectRuntimeForAgent` encapsulates the new logic.

## HTTP API

New routes:
- `GET /api/runtime-groups?workspace_id={id}` — list.
- `POST /api/runtime-groups` — create; body `{ name, description?, runtime_ids[] }`.
- `GET /api/runtime-groups/{id}` — detail: `{ id, name, description, runtimes[], active_override?, member_agent_count }`.
- `PATCH /api/runtime-groups/{id}` — update; body `{ name?, description?, runtime_ids? }`.
- `DELETE /api/runtime-groups/{id}`.
- `PUT /api/runtime-groups/{id}/override` — body `{ runtime_id, ends_at }`. 400 if runtime not in group.
- `DELETE /api/runtime-groups/{id}/override` — clear active.

Agent endpoints:
- `POST /api/agents` and `PATCH /api/agents/{id}` accept optional `group_ids []string` alongside `runtime_ids`.
- `GET /api/agents/{id}` response gains `groups: AgentRuntimeGroupRef[]`.
- Invariant enforced: after the update is applied, `len(runtime_ids) + len(group_ids) >= 1`. 400 otherwise.
- Partial-update rule for `PATCH`: if only one of `{runtime_ids, group_ids}` is provided, the other side's current values are used for the invariant check. So `PATCH {runtime_ids: []}` against an agent with one group link passes; `PATCH {runtime_ids: [], group_ids: []}` on the same agent is rejected.
- `PUT /api/runtime-groups/{id}/override` validates `ends_at > now()`. 400 otherwise.

## Frontend types

```ts
interface RuntimeGroup {
  id: string;
  workspace_id: string;
  name: string;
  description: string;
  runtimes: AgentRuntimeRef[];
  active_override: RuntimeGroupOverride | null;
  member_agent_count: number;
  created_at: string;
  updated_at: string;
}

interface RuntimeGroupOverride {
  id: string;
  group_id: string;
  runtime_id: string;
  runtime_name: string;
  starts_at: string;
  ends_at: string;
  created_by: string | null;
}

interface AgentRuntimeGroupRef {
  id: string;
  name: string;
  active_override: Pick<RuntimeGroupOverride, "runtime_id" | "runtime_name" | "ends_at"> | null;
}

// Agent type extension
interface Agent {
  // ...existing fields
  group_ids: string[];
  groups: AgentRuntimeGroupRef[];
}
```

`CreateAgentRequest` and `UpdateAgentRequest` gain optional `group_ids?: string[]`.

## UI

**New page: `apps/web/app/(app)/[workspace]/settings/runtime-groups/page.tsx`** (+ equivalent desktop route).
- Table of groups: name, member count, agent-usage count, active-override badge if any, row click opens detail.
- `+ New Group` button → dialog with name, description, member chip picker.
- **Group Detail drawer/page**:
  - Name + description editor.
  - Member chip list (reuses the existing multi-select chip component).
  - **Override section**: either "No active override" with a `Set override` button, or the active override card: runtime name, live countdown to expiry, `Change` + `Cancel` actions.
  - Set-override dialog: dropdown of current members + duration picker (`1 day / 2 days / 1 week / custom…`).

**Agent settings tab**: adds a "Groups" chip row above the existing "Runtimes" row. `+ Add group` popover lists workspace groups not already assigned. Each group chip tooltip shows its current member runtimes.

**Agent detail header**: the badge count includes deduped group-sourced runtimes. If any assigned group has an active override, render an amber dot on the badge with tooltip ("Overridden to: Workstation until Mon 4 PM").

**Create-agent dialog**: new groups multi-select next to the runtimes picker. Validation message reflects the combined-sources rule.

**Component reuse**: extract the current runtime chip picker into a shared `RuntimePicker` component so the settings tab, create dialog, and group detail all render identically.

## Realtime events

- `runtime_group.created`, `runtime_group.updated`, `runtime_group.deleted` — full group payload.
- `runtime_group_override.upserted`, `runtime_group_override.cleared` — group id + override payload.
- Existing `agent.updated` event includes the new `groups` field.
- No event for override *expiration* — clients compute locally from `ends_at`.

## Testing

### Go unit (`server/internal/service/task_test.go`)

- `TestSelectRuntimeForAgent_OverrideBeatsLeastLoaded` — two runtimes in a group, one overridden with more usage; override still picked.
- `TestSelectRuntimeForAgent_OfflineOverrideFallsThrough` — overridden runtime offline → normal selection wins.
- `TestSelectRuntimeForAgent_ExpiredOverrideIgnored` — `ends_at < now()` → ordinary selection.
- `TestSelectRuntimeForAgent_UnionsAssignmentsAndGroups` — agent with 1 individual + 1 group of 2 → all 3 candidates.
- `TestSelectRuntimeForAgent_DedupesUnion` — runtime in both sources appears once.
- `TestSelectRuntimeForAgent_MultipleActiveOverrides` — agent in two groups, both overriding to different online runtimes; either picked (deterministic by sort).

### Go integration

New file `server/internal/handler/runtime_group_test.go`:
- Full CRUD roundtrip.
- `POST /api/runtime-groups` rejects cross-workspace runtime.
- `PUT /api/runtime-groups/{id}/override` with non-member runtime → 400.
- Setting a second override → first one's `ends_at` is clipped to `now()`.
- Remove runtime from group while override points at it → override row auto-deleted.

Extend `server/internal/handler/agent_test.go`:
- Create agent with `runtime_ids=[]` and `group_ids=[]` → 400.
- Create agent with only `group_ids` (non-empty) → 201.
- `PATCH` replacing `group_ids` preserves `created_at` on surviving links.

### Vitest

- New tests for the runtime-groups list page, group detail drawer, override dialog.
- Update settings-tab tests for the new groups chip row.

### E2E

`e2e/tests/agent-runtime-groups.spec.ts`:
1. Create group with 2 online runtimes.
2. Create agent linked to the group (no individual runtimes).
3. Create 4 issues; verify tasks distribute across the group's runtimes.
4. Set override on the group targeting runtime A with a short duration.
5. Create 2 more issues; both tasks land on runtime A.
6. Clear override; create 2 more issues; distribution resumes.

## Deferred

- Pre-scheduled overrides (`starts_at` in the future). Schema already supports it.
- Override history view.
- Nested groups.
- Workspace quotas.
- `CountActiveAgentsByRuntime` currently counts via `agent_runtime_assignment`; once this ships, we may want an extended "agents reaching this runtime via any path" query for runtime-deletion safety checks. Not blocking (the FK `ON DELETE RESTRICT` on `runtime_group_member.runtime_id` already blocks deletion of in-use runtimes).
