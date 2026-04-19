# Agent Multi-Runtime Load Balancing — Design

**Status:** Draft
**Date:** 2026-04-19

## Problem

An agent is currently pinned to exactly one runtime (`agent.runtime_id`, NOT NULL). If a user has multiple machines available — a workstation, a laptop, a cloud runtime — only one of them can actually work on that agent's tasks. Users want to assign several runtimes to a single agent so work spreads across the machines they have, weighted toward whichever one has been doing the least work recently.

## Goal

Let a user designate multiple runtimes as valid for a single agent. When a new task is enqueued, select the runtime that has consumed the fewest tokens over the last 7 days (with a least-recently-used tiebreak), preferring online runtimes.

## Non-goals

- Re-routing a task to a different runtime if the chosen one goes offline after enqueue. (Task stays pinned to its original `runtime_id`, same behavior as today's single-runtime system.)
- Weighted or priority-based runtime selection. 7-day token usage is the only signal.
- Runtimes shared across workspaces.
- Rebalancing already-queued tasks.

## Architecture

Agent ↔ runtime becomes **many-to-many**. The join table records which runtimes are valid for which agents. The `agent.runtime_id` column is dropped (product is pre-live, no compat shim per CLAUDE.md).

At task enqueue (`EnqueueTaskForIssue`), the service queries for the least-loaded valid runtime and pins that `runtime_id` to the task row. The existing claim flow (`ClaimTaskForRuntime`) is unchanged — the task is already tagged, the runtime pulls it on its next poll.

"Load" is defined as the sum of tokens (input + output + cache read + cache write) attributed to tasks on that runtime over the last 7 days, derived from `task_usage` joined through `agent_task_queue.runtime_id`. Online runtimes are preferred; offline runtimes only participate if every assigned runtime is offline. Tiebreak is the most recent `agent_task_queue.created_at` on the runtime (NULL first, so never-used runtimes are picked before any ever-used runtime).

The tiebreak uses the task-queue enqueue timestamp (not the `task_usage.created_at` completion timestamp) so that burst enqueues distribute correctly: when ten tasks are enqueued in a few seconds, the second enqueue must see the first as "recent" even though no token usage has been reported yet.

Both the load sum and the LRU signal count tasks across **all** agents that have used the runtime, not just this agent's tasks. Load is a property of the machine; a runtime that another agent is hammering should look busy to this agent too.

Invariant: every agent has at least one valid runtime. Create/update endpoints reject an empty `runtime_ids` array.

## Schema

New migration:

```sql
CREATE TABLE agent_runtime_assignment (
  agent_id    UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  runtime_id  UUID NOT NULL REFERENCES agent_runtime(id) ON DELETE RESTRICT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (agent_id, runtime_id)
);

CREATE INDEX idx_agent_runtime_assignment_runtime ON agent_runtime_assignment(runtime_id);

INSERT INTO agent_runtime_assignment (agent_id, runtime_id)
SELECT id, runtime_id FROM agent WHERE runtime_id IS NOT NULL;

ALTER TABLE agent DROP COLUMN runtime_id;
```

Down migration re-adds `agent.runtime_id UUID`, backfills from the earliest-created assignment row per agent, then drops `agent_runtime_assignment`.

The table deliberately has no `last_assigned_at` column. LRU is derived from `task_usage.created_at` so there is a single source of truth for "when was this runtime last used."

## SQL queries

New queries in `server/pkg/db/queries/agent.sql`:

### `SelectRuntimeForAgent(agent_id)`

```sql
WITH window_since AS (
  SELECT DATE_TRUNC('day', now() - INTERVAL '7 days', 'UTC') AS ts
),
runtime_load AS (
  SELECT
    atq.runtime_id,
    COALESCE(SUM(tu.input_tokens + tu.output_tokens
               + tu.cache_read_tokens + tu.cache_write_tokens), 0) AS tokens_7d,
    MAX(atq.created_at) AS last_used_at
  FROM agent_task_queue atq
  LEFT JOIN task_usage tu
    ON tu.task_id = atq.id
   AND tu.created_at >= (SELECT ts FROM window_since)
  WHERE atq.runtime_id IN (
    SELECT runtime_id FROM agent_runtime_assignment WHERE agent_id = @agent_id
  )
  GROUP BY atq.runtime_id
)
SELECT ara.runtime_id
FROM agent_runtime_assignment ara
JOIN agent_runtime r ON r.id = ara.runtime_id
LEFT JOIN runtime_load rl ON rl.runtime_id = ara.runtime_id
WHERE ara.agent_id = @agent_id
ORDER BY
  (r.status = 'online') DESC,
  COALESCE(rl.tokens_7d, 0) ASC,
  rl.last_used_at ASC NULLS FIRST
LIMIT 1;
```

Returns a single `runtime_id` UUID. Caller is expected to have already verified the agent has ≥1 assignment (enforced by the create/update invariant), so the query is not expected to return zero rows. If it does, `EnqueueTaskForIssue` returns an error.

### `ListAgentRuntimeAssignments(agent_id)`

Returns `(runtime_id, runtime_name, runtime_status, runtime_mode, runtime_provider, last_used_at)` for the settings tab. Joins `agent_runtime_assignment` to `agent_runtime` and to a lateral `MAX(agent_task_queue.created_at)` subquery (matching the tiebreak signal used by selection) so the UI can render "last used Xd ago" per chip.

### `ReplaceAgentRuntimeAssignments(agent_id, runtime_ids[])`

Atomic replacement. In one transaction:
1. `DELETE FROM agent_runtime_assignment WHERE agent_id = $1 AND runtime_id <> ALL($2)`
2. `INSERT INTO agent_runtime_assignment (agent_id, runtime_id) SELECT $1, unnest($2) ON CONFLICT DO NOTHING`

Rows for runtimes that survive the update keep their original `created_at`.

### Removed queries

- `CreateAgent` and `UpdateAgent` no longer reference `runtime_id`.
- `CountActiveAgentsByRuntime` is rewritten against `agent_runtime_assignment`.
- Any other caller of `agent.runtime_id` (grep audit in the plan).

## Service layer

`server/internal/service/task.go`:

- `EnqueueTaskForIssue` replaces the `agent.runtime_id` read with `db.SelectRuntimeForAgent(ctx, agentID)`. Selection and task insert run in a single transaction. No separate "mark assigned" step — the token signal updates itself when the task completes and reports usage.

`server/internal/service/agent.go`:

- New `AgentService.SetAgentRuntimes(ctx, agentID, runtimeIDs []string)`:
  - Validates `len(runtimeIDs) >= 1`.
  - Validates every `runtimeID` resolves to a runtime in the agent's workspace.
  - Calls `ReplaceAgentRuntimeAssignments` in a transaction.
- `AgentService.CreateAgent` takes `runtimeIDs []string` in the input DTO with the same validation. First insert of assignments happens in the same tx as the agent insert.

## HTTP API

`server/internal/handler/agent.go`:

- `POST /agents` request body: `runtime_id` string → `runtime_ids` `[]string` (min length 1). Returns 400 on empty or unknown runtime.
- `PATCH /agents/{id}` request body: same swap. Full replacement semantics (not incremental add/remove).
- `GET /agents/{id}` response: new field `runtimes` — array of `{id, name, status, mode, provider, last_used_at}`. The old single `runtime` object is removed.

No new endpoints. Add/remove is expressed as "PATCH with a new full array."

## Frontend types

`packages/core/types/agent.ts`:

- `Agent.runtime_id: string` → `Agent.runtime_ids: string[]`
- `Agent.runtimes: AgentRuntimeAssignment[]` — new field populated from the list response
- `AgentRuntimeAssignment { runtime_id, name, status, mode, provider, last_used_at }`
- `UpdateAgentRequest.runtime_id` → `runtime_ids`
- `CreateAgentRequest.runtime_id` → `runtime_ids`

## UI

`packages/views/agents/components/tabs/settings-tab.tsx` replaces the single-select Popover picker with a multi-select chip list:

- A "Runtimes" row renders one chip per assigned runtime, showing name, cloud/local badge, online/offline dot, and a muted "last used 3d ago" line (or "never used" if `last_used_at` is null).
- Each chip has an X button to remove it. Clicking Remove updates local draft state; a Save action commits via `PATCH /agents/{id}`.
- A `+ Add runtime` button opens the existing Mine/All popover, filtered to exclude already-assigned runtimes.
- Save is disabled and a helper error shows if the draft list is empty.
- Dirty detection compares array membership, not reference equality.

No other UI surface changes — agent detail page, issue assignment, inbox consume `Agent` from the API and don't care about runtime internals.

## Testing

### Go unit — `server/internal/service/task_test.go`

- `SelectRuntimeForAgent` picks the runtime with the lowest 7-day token sum among online assignments.
- Never-used runtime (NULL `MAX(agent_task_queue.created_at)`) wins the tiebreak over ever-used.
- Ever-used runtimes tiebreak by older `MAX(agent_task_queue.created_at)` first.
- Burst enqueue: 3 tasks back-to-back to an agent with 3 zero-usage runtimes land on 3 distinct runtimes (LRU advances per enqueue even though no token usage has been reported yet).
- `task_usage` rows outside the 7-day window do not count.
- When every assigned runtime is offline, selection still returns a runtime (lowest 7-day + LRU wins).
- An online runtime with heavy usage still beats an offline runtime with zero usage.
- `EnqueueTaskForIssue` pins the selected `runtime_id` to the created task row.

### Go integration — `server/internal/handler/agent_test.go`

- `POST /agents` with empty `runtime_ids` → 400.
- `POST /agents` with an unknown runtime ID → 400.
- `POST /agents` with a runtime belonging to a different workspace → 400.
- `PATCH /agents/{id}` replaces the assignment set; surviving rows keep their `created_at`.
- Enqueuing 10 tasks to an agent with 3 online runtimes of equal (zero) initial usage distributes them roughly evenly (within ±1). Each enqueue bumps that runtime's recent usage, which is what drives the spread.

### Views — `packages/views/agents/components/tabs/settings-tab.test.tsx`

- Chips render for each assigned runtime with correct status dot and `last_used_at` copy.
- Removing the last chip disables Save and shows the helper error.
- The Add Runtime popover excludes already-assigned runtimes.

### E2E — `e2e/tests/agent-multi-runtime.spec.ts`

- Create agent with two online runtimes; create two issues and assign each to the agent; verify the `runtime_id` on the resulting tasks differs.

## Deferred

- Re-routing stale tasks when the selected runtime goes offline before claiming.
- Weighted assignment (e.g., "workstation gets 2x the work of the laptop").
- Materialized view for 7-day per-runtime token totals (the direct query is fine while assigned-runtime counts per agent stay small; swap to a materialized view if enqueue latency ever becomes an issue).
- UI surface for cross-agent visibility ("which agents use this runtime?") — not needed for the load-balancing feature itself.
