# GitLab Issues Integration — Phase 4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the loop between GitLab and Multica for member-side data. Deliver four subsystems: webhook author reverse-resolution, member-assignee write-through via GitLab native `assignee_ids`, webhook → agent-task dispatch, and autopilot re-pointing via new `autopilot_issue` mapping table.

**Architecture:** Webhook/read-path gets a reverse-resolver (`gitlab_user_id` → Multica `user_type/user_id`) using `user_gitlab_connection` first, then `gitlab_project_member` cache, falling back to a new `assignee_type='gitlab_user'` reference or NULL. Write-path lifts Phase 3b's cache-only member-assignee deferral by resolving Multica member UUID → GitLab `user_id` via the same tables. Autopilot refactors to call the existing write-through path internally and records its run → issue mapping in a new table.

**Tech Stack:** Go 1.26, Chi router, pgx/v5, sqlc. Three migrations (056 + 057 + 058).

---

## Scope

**In scope for Phase 4:**
1. Webhook + initial-sync author/actor/assignee reverse-resolution
2. Enable Note-level `award_emoji` webhook sync (Phase 2b gap; comment reactions forward path exists in 3d)
3. Member-assignee write-through via GitLab `assignee_ids` (lifts Phase 3b deferral)
4. Webhook → agent-task dispatch on issue-label changes (Phase 4 new)
5. `autopilot_issue` mapping table + autopilot re-point through write-through
6. Migrations: relax `comment_reaction.actor_type` NOT NULL; add `gitlab_user` to `issue.assignee_type` check; new `autopilot_issue` table

**Out of scope (Phase 5+):**
- `@user` mention translation — Multica has no `@user` mention primitive yet; nothing to translate in either direction. If Multica adds user mentions later, translation is a follow-up.
- Dropping `issues.origin_type` column — Phase 5 cleanup.
- GitLab project-member lifecycle webhooks (add/remove teammate) — current `initial_sync` populates on connect; periodic reconciler keeps it fresh enough for Phase 4. A webhook-driven member sync is a Phase 5 follow-up if needed.
- Fresh-install-only for autopilot: no backfill of pre-Phase-4 autopilot runs.

## File Structure

**New migrations:**
- `server/migrations/056_relax_comment_reaction_actor.up.sql` + `.down.sql`
- `server/migrations/057_issue_assignee_type_gitlab_user.up.sql` + `.down.sql`
- `server/migrations/058_autopilot_issue.up.sql` + `.down.sql`

**Files to modify:**

| File | Responsibility |
|---|---|
| `server/pkg/db/queries/gitlab_cache.sql` | Add `GetGitlabProjectMember` |
| `server/pkg/db/queries/gitlab_connection.sql` | Add `GetUserGitlabConnectionByGitlabUserID` |
| `server/pkg/db/queries/autopilot_issue.sql` (new) | Upsert/Get/Delete mapping |
| `server/internal/gitlab/resolver.go` | Add `ResolveMulticaUserFromGitlabUserID` method on existing `Resolver` (reuses its DB handle) |
| `server/internal/gitlab/translator.go` | `TranslateNote`/`TranslateIssueAward` accept resolver-resolved author/actor; `TranslateIssue` resolves assignee; `BuildCreateIssueInput`/`BuildUpdateIssueInput` resolve member → GitLab `assignee_ids` |
| `server/internal/gitlab/webhook_handlers.go` | Apply resolver in note + issue + emoji hooks; enable Note-level award hook branch; post-upsert emit event for agent-task enqueue |
| `server/internal/gitlab/initial_sync.go` | Apply resolver in notes + awards loops |
| `server/internal/handler/issue.go` | Member-assignee write-through in `CreateIssue` + `updateSingleIssueWriteThrough`; lift the cache-only deferral |
| `server/cmd/server/autopilot_listeners.go` | Look up `autopilot_issue` mapping instead of `origin_type` |
| `server/internal/service/autopilot/*` (path TBD) | Refactor issue creation to call the existing handler internal path; populate `autopilot_issue` on success |

## Hard rules

1. **Write-through authoritative** — Member-assignee PATCH/POST failures return 5xx; no silent fallthrough.
2. **Resolver fallback order** — (a) `user_gitlab_connection`, (b) `gitlab_project_member` → `assignee_type='gitlab_user'`, (c) NULL. Encoded once in `Resolver.ResolveMulticaUserFromGitlabUserID`.
3. **Agent writes unchanged** — Agents still skip GitLab (reactions + subscribe + own comments carry `**[agent:<slug>]** ` prefix). Phase 4 only extends member-side round-tripping.
4. **`user_gitlab_connection` beats `gitlab_project_member`** — A human who connected their PAT is the authoritative match. The project-member fallback only fires when no PAT is registered for that GitLab user in this workspace.
5. **Webhook upsert skipped by clobber guard** — When `UpsertIssueFromGitlab` returns `pgx.ErrNoRows`, the post-upsert agent-task enqueue still fires (we load the current cache row first) because the row state may already reflect the new agent label. Mirror the Phase 3c/3d recovery pattern.
6. **Autopilot uses write-through as a library** — Do not make autopilot HTTP-call its own server. Extract the write-through create logic into a handler method that both HTTP + autopilot call in-process.

---

## Task 0: Migrations

Three small migrations, one task, one commit.

- [ ] **Step 1: Write `056_relax_comment_reaction_actor.up.sql`**

```sql
-- Allow NULL actor_type/actor_id on comment_reaction for webhook-origin
-- reactions from unmapped GitLab users. Mirror the relaxation migration 051
-- already applied to comment.author_type and issue_reaction.actor_type.
ALTER TABLE comment_reaction ALTER COLUMN actor_type DROP NOT NULL;
ALTER TABLE comment_reaction ALTER COLUMN actor_id DROP NOT NULL;

-- Replace the NOT NULL + enumerated check with a NULL-permissive one.
ALTER TABLE comment_reaction DROP CONSTRAINT IF EXISTS comment_reaction_actor_type_check;
ALTER TABLE comment_reaction ADD CONSTRAINT comment_reaction_actor_type_check
    CHECK (actor_type IS NULL OR actor_type IN ('member', 'agent'));
```

- [ ] **Step 2: Write `056_relax_comment_reaction_actor.down.sql`**

```sql
-- Rollback: re-tighten to NOT NULL + enumerated. Note: if any rows with
-- NULL actor_type exist at rollback time, this will fail — intentional.
ALTER TABLE comment_reaction DROP CONSTRAINT IF EXISTS comment_reaction_actor_type_check;
ALTER TABLE comment_reaction ADD CONSTRAINT comment_reaction_actor_type_check
    CHECK (actor_type IN ('member', 'agent'));
ALTER TABLE comment_reaction ALTER COLUMN actor_id SET NOT NULL;
ALTER TABLE comment_reaction ALTER COLUMN actor_type SET NOT NULL;
```

- [ ] **Step 3: Write `057_issue_assignee_type_gitlab_user.up.sql`**

First inspect the current `issue.assignee_type` check constraint. Read `server/migrations/` for the original `issue` table definition + any later relaxations. The current check is likely `CHECK (assignee_type IN ('member', 'agent'))` (possibly with NULL). Phase 4 adds `'gitlab_user'`:

```sql
-- Allow GitLab-native assignees (users present on GitLab but not mapped to a
-- Multica user via user_gitlab_connection) to be cached with a reference to
-- the gitlab_project_member row. assignee_id for 'gitlab_user' type is
-- a gitlab_project_member.gitlab_user_id — NOT a Multica UUID.

ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_assignee_type_check;
ALTER TABLE issue ADD CONSTRAINT issue_assignee_type_check
    CHECK (assignee_type IS NULL OR assignee_type IN ('member', 'agent', 'gitlab_user'));
```

Before committing, verify via psql that the constraint name matches what you see in `\d issue`. If the existing constraint has a different name (e.g. `issue_assignee_type_check1` or auto-generated), use the correct one.

**Design note on `assignee_id` for `gitlab_user`:** the column is currently `UUID`. GitLab user IDs are `BIGINT`. We have two options:
- (a) Reference the `gitlab_project_member` row's implicit id (need to check — the table keys on `(workspace_id, gitlab_user_id)` with no UUID PK, so there's no UUID to reference)
- (b) Add a UUID PK to `gitlab_project_member` and reference that

Option (b) is cleaner. Migration 057 should ALSO add a UUID PK column to `gitlab_project_member`:

```sql
-- Add UUID PK to gitlab_project_member so other tables (issue.assignee_id
-- when assignee_type='gitlab_user') can reference a stable UUID.
ALTER TABLE gitlab_project_member ADD COLUMN id UUID NOT NULL DEFAULT gen_random_uuid();
-- The existing PK on (workspace_id, gitlab_user_id) stays; we add a unique
-- index on id to make it addressable.
CREATE UNIQUE INDEX gitlab_project_member_id_unique ON gitlab_project_member (id);
```

(The existing composite PK stays; `id` is a secondary unique key. Alternatively drop the composite PK and make `id` the PK — but that's a bigger change with more impact on existing sqlc queries. Keep composite PK; add `id` as unique.)

- [ ] **Step 4: Write `057_issue_assignee_type_gitlab_user.down.sql`**

```sql
DROP INDEX IF EXISTS gitlab_project_member_id_unique;
ALTER TABLE gitlab_project_member DROP COLUMN IF EXISTS id;

ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_assignee_type_check;
ALTER TABLE issue ADD CONSTRAINT issue_assignee_type_check
    CHECK (assignee_type IS NULL OR assignee_type IN ('member', 'agent'));
```

- [ ] **Step 5: Write `058_autopilot_issue.up.sql`**

```sql
-- Phase 4: maps an autopilot run to the GitLab issue it created, replacing
-- the old issues.origin_type='autopilot' / origin_id lookup path. Fresh
-- install only — no backfill.
CREATE TABLE autopilot_issue (
    autopilot_run_id UUID NOT NULL REFERENCES autopilot_run(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    gitlab_iid INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (autopilot_run_id, workspace_id, gitlab_iid)
);

CREATE INDEX autopilot_issue_workspace_iid
    ON autopilot_issue (workspace_id, gitlab_iid);
```

- [ ] **Step 6: Write `058_autopilot_issue.down.sql`**

```sql
DROP INDEX IF EXISTS autopilot_issue_workspace_iid;
DROP TABLE IF EXISTS autopilot_issue;
```

- [ ] **Step 7: Run migrations**

```bash
cd server && DATABASE_URL="postgres://multica:multica@localhost:5432/multica_multica_gitlab_phase_4_<port>?sslmode=disable" go run ./cmd/migrate up
```

Expected: `up 056`, `up 057`, `up 058`. Before running, create the phase-4 DB via `psql -h localhost -d postgres -c "CREATE DATABASE ... OWNER multica;"` (same as Phases 3c/3d setup).

- [ ] **Step 8: Verify**

```bash
psql "postgres://multica:multica@localhost:5432/multica_multica_gitlab_phase_4_<port>?sslmode=disable" -c "\d comment_reaction" -c "\d issue" -c "\d autopilot_issue"
```

Expected: comment_reaction's actor_type/actor_id are nullable; issue check-constraint lists all three values; autopilot_issue exists.

- [ ] **Step 9: Commit**

```bash
git add server/migrations/056_*.sql server/migrations/057_*.sql server/migrations/058_*.sql
git commit -m "feat(db): phase-4 migrations — relax comment_reaction, add gitlab_user assignee type, autopilot_issue mapping"
```

---

## Task 1: sqlc queries — reverse lookups + autopilot_issue CRUD

**Files:**
- Modify: `server/pkg/db/queries/gitlab_cache.sql` — add `GetGitlabProjectMember`, `GetGitlabProjectMemberByID`
- Modify: `server/pkg/db/queries/gitlab_connection.sql` — add `GetUserGitlabConnectionByGitlabUserID`
- Create: `server/pkg/db/queries/autopilot_issue.sql` — autopilot_issue CRUD

- [ ] **Step 1: Add to `gitlab_cache.sql`**

```sql
-- name: GetGitlabProjectMember :one
-- Reverse lookup of a GitLab user in a workspace's cached project-member list.
SELECT * FROM gitlab_project_member
WHERE workspace_id = $1 AND gitlab_user_id = $2
LIMIT 1;

-- name: GetGitlabProjectMemberByID :one
-- Lookup by the UUID id (used when resolving an issue.assignee_id where
-- assignee_type='gitlab_user').
SELECT * FROM gitlab_project_member WHERE id = $1 LIMIT 1;
```

- [ ] **Step 2: Add to `gitlab_connection.sql`**

```sql
-- name: GetUserGitlabConnectionByGitlabUserID :one
-- Reverse lookup: who (Multica user) is this GitLab user in this workspace?
-- Phase 4 uses this to resolve webhook author/actor/assignee gitlab_user_id
-- back to Multica's user_id.
SELECT * FROM user_gitlab_connection
WHERE workspace_id = $1 AND gitlab_user_id = $2
LIMIT 1;
```

- [ ] **Step 3: Create `autopilot_issue.sql`**

```sql
-- name: UpsertAutopilotIssue :one
-- Record that an autopilot run created (or is tracking) a specific GitLab
-- issue. Idempotent on the composite key.
INSERT INTO autopilot_issue (autopilot_run_id, workspace_id, gitlab_iid)
VALUES ($1, $2, $3)
ON CONFLICT (autopilot_run_id, workspace_id, gitlab_iid) DO NOTHING
RETURNING *;

-- name: GetAutopilotIssueByIID :one
-- Given a workspace + gitlab_iid, return the autopilot run that owns this
-- issue, if any. Used by the autopilot listener to identify autopilot-origin
-- issues from webhook events.
SELECT * FROM autopilot_issue
WHERE workspace_id = $1 AND gitlab_iid = $2
LIMIT 1;

-- name: ListAutopilotIssuesByRun :many
SELECT * FROM autopilot_issue WHERE autopilot_run_id = $1;
```

- [ ] **Step 4: Regenerate + build**

```bash
cd server && make sqlc && go build ./...
```

Expected: generated types include `GetGitlabProjectMember`, `GetGitlabProjectMemberByID`, `GetUserGitlabConnectionByGitlabUserID`, `UpsertAutopilotIssue`, `GetAutopilotIssueByIID`, `ListAutopilotIssuesByRun`.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/db/queries/gitlab_cache.sql server/pkg/db/queries/gitlab_connection.sql server/pkg/db/queries/autopilot_issue.sql server/pkg/db/generated/
git commit -m "feat(db): phase-4 sqlc queries — reverse lookups + autopilot_issue CRUD"
```

---

## Task 2: Resolver — `ResolveMulticaUserFromGitlabUserID`

**Files:**
- Modify: `server/internal/gitlab/resolver.go`
- Test: `server/internal/gitlab/resolver_test.go`

Extend the existing `Resolver` with a second method. Same access pattern as `ResolveTokenForWrite` (uses `Queries` handle).

Semantics: `(workspaceID, gitlabUserID) → (userType, userID, gitlabProjectMemberID)`
- `user_gitlab_connection` hit → `("member", <multica_user_id>, "")`
- `gitlab_project_member` hit → `("gitlab_user", "", <project_member_uuid>)`
- Neither → `("", "", "")` — caller treats as unmapped

- [ ] **Step 1: Write the failing tests**

Add to `server/internal/gitlab/resolver_test.go`:

```go
func TestResolveMulticaUserFromGitlabUserID_UserGitlabConnectionWins(t *testing.T) {
	q := &fakeResolverQueries{
		userConnByGitlabUser: &db.UserGitlabConnection{
			UserID:       pgUUIDForTest(multicaUserID),
			WorkspaceID:  pgUUIDForTest(workspaceUUID),
			GitlabUserID: 7,
		},
		projectMember: &db.GitlabProjectMember{
			ID:           pgUUIDForTest(memberRowID),
			WorkspaceID:  pgUUIDForTest(workspaceUUID),
			GitlabUserID: 7,
		},
	}
	r := NewResolver(q, stubDecrypt("dec"))
	userType, userID, memberID, err := r.ResolveMulticaUserFromGitlabUserID(context.Background(), workspaceUUID, 7)
	if err != nil {
		t.Fatalf("ResolveMulticaUserFromGitlabUserID: %v", err)
	}
	if userType != "member" {
		t.Errorf("userType = %q, want member", userType)
	}
	if userID != multicaUserID {
		t.Errorf("userID = %q, want %q", userID, multicaUserID)
	}
	if memberID != "" {
		t.Errorf("memberID should be empty when user-connection hit, got %q", memberID)
	}
}

func TestResolveMulticaUserFromGitlabUserID_ProjectMemberFallback(t *testing.T) {
	q := &fakeResolverQueries{
		userConnByGitlabUser: nil, // no PAT connection
		projectMember: &db.GitlabProjectMember{
			ID:           pgUUIDForTest(memberRowID),
			WorkspaceID:  pgUUIDForTest(workspaceUUID),
			GitlabUserID: 7,
		},
	}
	r := NewResolver(q, stubDecrypt("dec"))
	userType, userID, memberID, err := r.ResolveMulticaUserFromGitlabUserID(context.Background(), workspaceUUID, 7)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if userType != "gitlab_user" {
		t.Errorf("userType = %q, want gitlab_user", userType)
	}
	if userID != "" {
		t.Errorf("userID should be empty for gitlab_user type, got %q", userID)
	}
	if memberID != memberRowID {
		t.Errorf("memberID = %q, want %q", memberID, memberRowID)
	}
}

func TestResolveMulticaUserFromGitlabUserID_NoMapping(t *testing.T) {
	q := &fakeResolverQueries{} // both nil
	r := NewResolver(q, stubDecrypt("dec"))
	userType, userID, memberID, err := r.ResolveMulticaUserFromGitlabUserID(context.Background(), workspaceUUID, 7)
	if err != nil {
		t.Fatalf("unmapped user must not error, got %v", err)
	}
	if userType != "" || userID != "" || memberID != "" {
		t.Errorf("expected all empty, got (%q, %q, %q)", userType, userID, memberID)
	}
}
```

Test helpers to add at top of file (after existing constants):

```go
const (
	multicaUserID = "00000000-0000-0000-0000-000000000003"
	memberRowID   = "00000000-0000-0000-0000-000000000004"
)

// pgUUIDForTest parses a UUID string into pgtype.UUID for fake rows.
func pgUUIDForTest(s string) pgtype.UUID {
	var u pgtype.UUID
	_ = u.Scan(s)
	return u
}
```

And extend `fakeResolverQueries` to cover the new query methods:

```go
// Existing fields + new ones:
type fakeResolverQueries struct {
	workspaceConn        *db.WorkspaceGitlabConnection
	userConn             *db.UserGitlabConnection
	userConnByGitlabUser *db.UserGitlabConnection
	projectMember        *db.GitlabProjectMember
}

func (f *fakeResolverQueries) GetUserGitlabConnectionByGitlabUserID(_ context.Context, _ db.GetUserGitlabConnectionByGitlabUserIDParams) (db.UserGitlabConnection, error) {
	if f.userConnByGitlabUser == nil {
		return db.UserGitlabConnection{}, pgx.ErrNoRows
	}
	return *f.userConnByGitlabUser, nil
}

func (f *fakeResolverQueries) GetGitlabProjectMember(_ context.Context, _ db.GetGitlabProjectMemberParams) (db.GitlabProjectMember, error) {
	if f.projectMember == nil {
		return db.GitlabProjectMember{}, pgx.ErrNoRows
	}
	return *f.projectMember, nil
}
```

- [ ] **Step 2: Run — FAIL** (`ResolveMulticaUserFromGitlabUserID` undefined, fakeResolverQueries doesn't implement the new interface methods)

- [ ] **Step 3: Update the `resolverQueries` interface in `resolver.go`**

Find the existing interface used by `Resolver` (grep `resolverQueries` or look at `NewResolver`'s parameter type). Add the two new methods to the interface.

- [ ] **Step 4: Add the method**

```go
// ResolveMulticaUserFromGitlabUserID reverse-resolves a GitLab user ID to a
// Multica user reference. Preference order:
//   1. user_gitlab_connection — human who connected their personal PAT
//   2. gitlab_project_member — GitLab user cached but no Multica mapping
//   3. Unmapped — returns all empty strings; caller decides how to handle
//
// Returns:
//   userType      — "member" | "gitlab_user" | ""
//   userID        — Multica user UUID when type="member", empty otherwise
//   memberRowID   — gitlab_project_member.id when type="gitlab_user", empty otherwise
func (r *Resolver) ResolveMulticaUserFromGitlabUserID(ctx context.Context, workspaceID string, gitlabUserID int64) (userType, userID, memberRowID string, err error) {
	wsUUID := pgUUID(workspaceID)

	// 1. Prefer user_gitlab_connection (human who connected their PAT).
	conn, connErr := r.queries.GetUserGitlabConnectionByGitlabUserID(ctx, db.GetUserGitlabConnectionByGitlabUserIDParams{
		WorkspaceID:  wsUUID,
		GitlabUserID: gitlabUserID,
	})
	if connErr == nil {
		return "member", uuidToString(conn.UserID), "", nil
	}
	if !errors.Is(connErr, pgx.ErrNoRows) {
		return "", "", "", fmt.Errorf("resolver: user connection lookup: %w", connErr)
	}

	// 2. Fall back to gitlab_project_member.
	member, memErr := r.queries.GetGitlabProjectMember(ctx, db.GetGitlabProjectMemberParams{
		WorkspaceID:  wsUUID,
		GitlabUserID: gitlabUserID,
	})
	if memErr == nil {
		return "gitlab_user", "", uuidToString(member.ID), nil
	}
	if !errors.Is(memErr, pgx.ErrNoRows) {
		return "", "", "", fmt.Errorf("resolver: project member lookup: %w", memErr)
	}

	// 3. Unmapped — no error, just empty.
	return "", "", "", nil
}
```

Use the package's existing `pgUUID` / `uuidToString` helpers (they live in `resolver.go` already or a nearby file — confirm and import if needed).

- [ ] **Step 5: Run tests — PASS**

- [ ] **Step 6: Commit**

```bash
git add server/internal/gitlab/resolver.go server/internal/gitlab/resolver_test.go
git commit -m "feat(gitlab): Resolver.ResolveMulticaUserFromGitlabUserID with connection+member fallback"
```

---

## Task 3: Translator — author/actor resolution in `TranslateNote` + emoji path

**Files:**
- Modify: `server/internal/gitlab/translator.go`
- Test: `server/internal/gitlab/translator_test.go`

Current `TranslateNote` returns `NoteValues{Body, Type, AuthorType, AuthorSlug, GitlabUserID, UpdatedAt}`. `AuthorSlug` is only populated for agent-prefixed notes. For non-agent notes, the webhook handler currently leaves `AuthorType`/`AuthorID` empty.

Phase 4 adds a resolver-aware translation: the caller (webhook/initial-sync) passes the resolver, and the translator returns `AuthorType`/`AuthorID` populated from reverse-lookup.

**Design choice:** keep the pure translator (no DB access) and do the resolution in the *caller*. The translator stays stateless; the caller (webhook handler) calls `TranslateNote` THEN `Resolver.ResolveMulticaUserFromGitlabUserID` on the returned `GitlabUserID`. This keeps the translator testable without a resolver stub.

So Task 3's actual work is:
1. `NoteValues` gains no new fields — the caller does the resolution after translating.
2. Verify the webhook handler's code shape when it consumes `NoteValues` — Phase 4 rewrites that in Task 5.

**This task is actually: a placeholder to confirm the design decision, plus a small refactor if `TranslateNote` currently does more than it should.**

Alternative: if splitting is awkward, make the translator accept a resolver. But pure functions are easier to test.

Stick with the pure-translator approach. **This task becomes a design-note-only task** with no code change. Skip to Task 4.

Actually no — there IS work for Task 3: the **assignee resolution for issues** belongs in the translator. `TranslateIssue` currently extracts the first GitLab assignee's `user.ID` but doesn't translate to Multica. The webhook handler hard-wires NULL.

OK scope clearer. **Task 3 handles `TranslateIssue` assignees.** (`TranslateNote`/emoji author resolution happens in the caller in Task 5.)

- [ ] **Step 1: Inspect current `TranslateIssue`**

Read `server/internal/gitlab/translator.go::TranslateIssue`. Understand:
- What fields are in `IssueValues`?
- Does it already extract assignees from `gitlabapi.Issue.Assignees`?
- Where is the first-assignee-wins logic if any?

Likely today: `TranslateIssue` picks agent via `agent::<slug>` label; ignores `issue.Assignees`. Phase 4: also extract the first human assignee's `user.ID` and return it as a new `IssueValues.GitlabAssigneeUserID int64` field.

- [ ] **Step 2: Write failing tests for issue assignee extraction**

```go
func TestTranslateIssue_ExtractsFirstAssigneeGitlabUserID(t *testing.T) {
	in := gitlabapi.Issue{
		ID: 42,
		IID: 5,
		Assignees: []gitlabapi.User{{ID: 7, Username: "alice"}, {ID: 8, Username: "bob"}},
	}
	vals := TranslateIssue(in, &TranslateContext{})
	if vals.GitlabAssigneeUserID != 7 {
		t.Errorf("GitlabAssigneeUserID = %d, want 7 (first assignee)", vals.GitlabAssigneeUserID)
	}
}

func TestTranslateIssue_NoAssigneeMeansZero(t *testing.T) {
	in := gitlabapi.Issue{ID: 42, IID: 5, Assignees: nil}
	vals := TranslateIssue(in, &TranslateContext{})
	if vals.GitlabAssigneeUserID != 0 {
		t.Errorf("GitlabAssigneeUserID = %d, want 0", vals.GitlabAssigneeUserID)
	}
}

func TestTranslateIssue_AgentLabelWinsOverNativeAssignee(t *testing.T) {
	// Existing rule per spec: agent::<slug> label always beats native assignees.
	in := gitlabapi.Issue{
		ID: 42, IID: 5,
		Labels: []string{"agent::builder"},
		Assignees: []gitlabapi.User{{ID: 7, Username: "alice"}},
	}
	vals := TranslateIssue(in, &TranslateContext{AgentBySlug: map[string]string{"builder": "agent-uuid"}})
	// Agent assignee still wins — but GitlabAssigneeUserID is still populated
	// so the caller can store it for later reverse resolution.
	if vals.AssigneeType != "agent" {
		t.Errorf("AssigneeType = %q, want agent", vals.AssigneeType)
	}
	if vals.GitlabAssigneeUserID != 7 {
		t.Errorf("GitlabAssigneeUserID = %d, want 7 (still extracted alongside agent label)", vals.GitlabAssigneeUserID)
	}
}
```

- [ ] **Step 3: Run — FAIL** (field doesn't exist)

- [ ] **Step 4: Extend `IssueValues`**

```go
type IssueValues struct {
	Title        string
	Description  string
	Status       string
	Priority     string
	AssigneeType string // "agent" | "" — agent set via label; member/gitlab_user resolution is caller's job
	AssigneeID   string // agent UUID when AssigneeType=agent; empty otherwise
	GitlabAssigneeUserID int64 // Populated from the first GitLab assignee, regardless of agent label. Caller resolves.
	CreatorGitlabUserID  int64 // Populated from GitLab issue author.id. Caller resolves.
	DueDate      string
	ExternalUpdatedAt time.Time
}
```

Update `TranslateIssue` to populate `GitlabAssigneeUserID` from `in.Assignees[0].ID` when non-empty, and `CreatorGitlabUserID` from `in.Author.ID` when non-zero.

- [ ] **Step 5: Run — PASS**

- [ ] **Step 6: Commit**

```bash
git add server/internal/gitlab/translator.go server/internal/gitlab/translator_test.go
git commit -m "feat(gitlab): TranslateIssue surfaces GitLab assignee/creator user IDs for caller reverse-resolution"
```

---

## Task 4: Webhook + initial-sync apply resolver for authors/actors/assignees

**Files:**
- Modify: `server/internal/gitlab/webhook_handlers.go` — `ApplyNoteHookEvent`, `ApplyEmojiHookEvent`, `ApplyIssueHookEvent` + enable Note-level award branch
- Modify: `server/internal/gitlab/initial_sync.go` — notes + awards loops + issue assignees
- Test: `server/internal/gitlab/webhook_handlers_test.go` + `initial_sync_test.go`

`WebhookDeps` already holds `Queries` — add `Resolver` (or reuse the existing resolver via a new dep). For the caller-side resolution in this task, we need access to the resolver. Check how `ApplyNoteHookEvent` is currently called and where a `*Resolver` can be injected.

Quick audit: `WebhookDeps` today has `Queries`, `WorkspaceID`, and a few helpers. The `Resolver` wraps `Queries` + `decrypter`. For reverse-resolution we only need `Queries`; we can add a helper function in the `gitlab` package that takes `Queries` directly (avoids threading a full resolver):

```go
// resolveMulticaUser is the webhook-side reverse resolver. Returns the
// Multica user reference, or all zero-values if unmapped. Identical logic
// to Resolver.ResolveMulticaUserFromGitlabUserID but stateless (takes the
// queries handle directly) — suitable for webhook workers that don't have
// a full Resolver constructed.
func resolveMulticaUser(ctx context.Context, q ReverseResolverQueries, workspaceID pgtype.UUID, gitlabUserID int64) (userType string, userID pgtype.UUID, memberRowID pgtype.UUID, err error) {
	// same precedence: user_gitlab_connection → gitlab_project_member → unmapped
}
```

Or simpler: add `Resolver` as a `*Resolver` field on `WebhookDeps` and use it. Pick whichever is cleaner given the existing code shape.

- [ ] **Step 1: Write failing tests for note hook author resolution**

```go
func TestApplyNoteHookEvent_ResolvesAuthorViaUserConnection(t *testing.T) {
	ctx := context.Background()
	// Seed a user_gitlab_connection so the webhook author maps to a Multica user.
	seedUserGitlabConnection(t, testUserID, testWorkspaceID, 7, "alice")
	// Seed an issue to attach the note to.
	issueID := seedGitlabConnectedIssue(t, 100, 42)

	payload := noteHookPayload{ /* ... note with user.id=7 ... */ }
	err := ApplyNoteHookEvent(ctx, webhookDepsFor(t), payload)
	if err != nil {
		t.Fatalf("ApplyNoteHookEvent: %v", err)
	}

	var authorType string
	var authorID string
	_ = testPool.QueryRow(ctx, `SELECT author_type::text, author_id::text FROM comment WHERE issue_id = $1`, issueID).Scan(&authorType, &authorID)
	if authorType != "member" {
		t.Errorf("author_type = %q, want member", authorType)
	}
	if authorID != testUserID {
		t.Errorf("author_id = %q, want testUserID", authorID)
	}
}

func TestApplyNoteHookEvent_UnmappedAuthorLeavesNullRefs(t *testing.T) {
	// No user_gitlab_connection or gitlab_project_member for user.id=999.
	// Expect author_type=NULL and author_id=NULL; gitlab_author_user_id=999.
}

// Parallel tests for ApplyEmojiHookEvent (issue-level) and the new
// Note-level emoji path.
func TestApplyEmojiHookEvent_IssueAwardResolvesActor(t *testing.T) { /* ... */ }
func TestApplyEmojiHookEvent_NoteAwardUsesUpsertCommentReactionFromGitlab(t *testing.T) { /* ... */ }
```

Add `seedUserGitlabConnection(t, userID, workspaceID, gitlabUserID, gitlabUsername)` and `seedGitlabProjectMember(t, gitlabUserID, username, name)` test helpers.

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Update `ApplyNoteHookEvent`**

After the existing `nv := TranslateNote(apiNote)` call, add reverse resolution:

```go
// If no agent prefix was detected, try to resolve the human author from
// their GitLab user ID via user_gitlab_connection (PAT-connected human)
// or gitlab_project_member (GitLab-only user).
if nv.AuthorType == "" && p.User.ID != 0 {
    userType, userID, memberRowID, rErr := resolveMulticaUser(ctx, deps.Queries, deps.WorkspaceID, p.User.ID)
    if rErr != nil {
        return fmt.Errorf("resolve note author: %w", rErr)
    }
    if userType == "member" {
        authorType = pgtype.Text{String: "member", Valid: true}
        authorID = userID
    } else if userType == "gitlab_user" {
        // comments don't support a 'gitlab_user' author type — leave NULL
        // on comment; the gitlab_author_user_id is enough to render avatar.
        _ = memberRowID
    }
    // If userType == "" (unmapped), leave authorType/authorID NULL.
} else if nv.AuthorType == "agent" {
    // existing agent-prefix branch (unchanged)
}
```

Make sure the `authorType` / `authorID` variables used in the later `UpsertCommentFromGitlab` call are the ones updated by this block.

- [ ] **Step 4: Update `ApplyEmojiHookEvent`**

The current `ApplyEmojiHookEvent` only handles `awardable_type == "Issue"` and leaves actor_type/actor_id NULL. Phase 4 change:
1. After the existing filter, when `awardable_type == "Issue"`, do the reverse-resolve + set actor_type/actor_id on the upsert params.
2. Add a new branch for `awardable_type == "Note"` that:
   - Looks up the parent comment via `GetCommentByGitlabNoteID` (exists from Phase 3c) using `awardable_id` as the note ID.
   - Resolves actor same way.
   - Calls `UpsertCommentReactionFromGitlab` (Phase 3d added this).

- [ ] **Step 5: Update `ApplyIssueHookEvent`**

After `TranslateIssue`, if `vals.AssigneeType == ""` (no agent label) AND `vals.GitlabAssigneeUserID != 0`, reverse-resolve:
- `member` → set `assignee_type='member'`, `assignee_id=<uuid>`
- `gitlab_user` → set `assignee_type='gitlab_user'`, `assignee_id=<member_row_id>`
- Unmapped → leave NULL

Also resolve the issue creator similarly from `vals.CreatorGitlabUserID` → `creator_type/creator_id`.

Pass the resolved values to `UpsertIssueFromGitlab`. Verify that sqlc query's params include these fields (Phase 2a schema) and the types match.

- [ ] **Step 6: Run tests — PASS**

- [ ] **Step 7: Apply the same pattern to `initial_sync.go`**

The notes + awards loops in initial_sync currently pass NULL actor_type/actor_id. Mirror the webhook handler pattern. Add tests to `initial_sync_test.go`.

- [ ] **Step 8: Commit**

```bash
git add server/internal/gitlab/webhook_handlers.go server/internal/gitlab/initial_sync.go server/internal/gitlab/webhook_handlers_test.go server/internal/gitlab/initial_sync_test.go
git commit -m "feat(gitlab): webhook + initial-sync resolve author/actor/assignee via user mapping"
```

---

## Task 5: Webhook → agent-task dispatch

**Files:**
- Modify: `server/internal/gitlab/webhook_worker.go` or wherever the post-upsert wiring lives
- Test: `server/internal/gitlab/webhook_handlers_test.go`

Current gap: `ApplyIssueHookEvent` upserts the cache row but doesn't trigger the agent-task enqueue that the write-through path does. When a human adds `~agent::builder` label from gitlab.com, the cache gets updated with `assignee_type='agent'` but no task lands in the queue.

Fix: after the upsert, if the resulting cache row has `assignee_type='agent'` AND the assignment is new (i.e. the prior cache state didn't have this agent assigned), call `TaskService.EnqueueTaskForIssue`.

- [ ] **Step 1: Locate the seam**

Two options:
- **(a)** Add `TaskService` to `WebhookDeps`, call `EnqueueTaskForIssue` from inside `ApplyIssueHookEvent` after successful upsert.
- **(b)** Emit `EventIssueUpdated` from the webhook worker, let the existing listener in the handler layer enqueue.

Read how the autopilot listener + write-through enqueue interact. If events already fire on cache updates via a pubsub/listener pattern, (b) is cleaner. If the write-through path calls `EnqueueTaskForIssue` directly (inline after commit), (a) is more consistent.

From the context report: write-through calls `EnqueueTaskForIssue` directly (not via event). So use (a).

- [ ] **Step 2: Write the failing test**

```go
func TestApplyIssueHookEvent_EnqueuesAgentTaskOnNewAssignment(t *testing.T) {
	ctx := context.Background()
	deps := webhookDepsFor(t) // includes TaskService
	agentID := seedAgent(t, "builder")

	// Seed a cache row initially with no assignee.
	issueID := seedGitlabConnectedIssue(t, 200, 42)

	// Webhook arrives with agent::builder label.
	payload := issueHookPayload{ /* ... labels includes agent::builder ... */ }
	if err := ApplyIssueHookEvent(ctx, deps, payload); err != nil {
		t.Fatalf("ApplyIssueHookEvent: %v", err)
	}

	// Assert cache row now has assignee_type='agent' and agent_task_queue has a row.
	var count int
	_ = testPool.QueryRow(ctx, `SELECT COUNT(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2`, issueID, agentID).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 task enqueued, got %d", count)
	}
}

func TestApplyIssueHookEvent_DoesNotEnqueueOnUnchangedAssignment(t *testing.T) {
	// Seed a cache row that already has agent::builder. Webhook comes in
	// with the same assignment. No new task should be enqueued (prevent
	// dupes on idle webhook replays).
}
```

- [ ] **Step 3: Run — FAIL**

- [ ] **Step 4: Implement**

In `ApplyIssueHookEvent`, after `UpsertIssueFromGitlab` returns (handling `pgx.ErrNoRows` via load-current-row fallback), check if `cacheRow.AssigneeType == "agent"` AND the prior row (if any) didn't have the same agent. If so, call `deps.TaskService.EnqueueTaskForIssue(ctx, cacheRow)`.

To detect "changed assignment," load the prior cache state BEFORE the upsert. If the pre-upsert row had no agent assignee or a different agent, consider this a new assignment.

- [ ] **Step 5: Run — PASS**

- [ ] **Step 6: Commit**

```bash
git add server/internal/gitlab/webhook_handlers.go server/internal/gitlab/webhook_handlers_test.go
git commit -m "feat(gitlab): webhook issue-hook enqueues agent task on new agent-label assignment"
```

---

## Task 6: Translator — `BuildCreateIssueInput` + `BuildUpdateIssueInput` resolve member → GitLab user IDs

**Files:**
- Modify: `server/internal/gitlab/translator.go`
- Test: `server/internal/gitlab/translator_test.go`

Phase 3b deferred member assignees to cache-only. Phase 4 resolves Multica member UUID → GitLab user ID and sets GitLab's `assignee_ids`.

Design: translator takes a new optional `memberGitlabUserIDByUUID map[string]int64` parameter. Handler pre-resolves the mapping for any assignee in the request and passes it in.

- [ ] **Step 1: Write failing tests**

```go
func TestBuildCreateIssueInput_MemberAssigneeResolvesToGitlabUserID(t *testing.T) {
	memberByUUID := map[string]int64{
		"11111111-1111-1111-1111-111111111111": 7,
	}
	req := CreateIssueRequest{
		Title:        "T",
		AssigneeType: "member",
		AssigneeID:   "11111111-1111-1111-1111-111111111111",
	}
	out := BuildCreateIssueInput(req, nil, memberByUUID)
	if len(out.AssigneeIDs) != 1 || out.AssigneeIDs[0] != 7 {
		t.Errorf("AssigneeIDs = %v, want [7]", out.AssigneeIDs)
	}
}

func TestBuildCreateIssueInput_UnmappedMemberDoesNotSendAssignee(t *testing.T) {
	// Member UUID not in map (user hasn't connected GitLab) — don't send
	// AssigneeIDs; fall back to cache-only behavior.
	req := CreateIssueRequest{AssigneeType: "member", AssigneeID: "99999999-9999-9999-9999-999999999999"}
	out := BuildCreateIssueInput(req, nil, map[string]int64{})
	if len(out.AssigneeIDs) != 0 {
		t.Errorf("AssigneeIDs should be empty for unmapped member, got %v", out.AssigneeIDs)
	}
}

func TestBuildUpdateIssueInput_MemberAssigneeResolvesToGitlabUserID(t *testing.T) { /* similar */ }

func TestBuildUpdateIssueInput_ClearMemberAssigneeSendsEmptyArray(t *testing.T) {
	// PATCH {"assignee_type": null, "assignee_id": null} on a member-assigned
	// issue. Send AssigneeIDs: &[] (pointer to empty) so GitLab clears.
	req := UpdateIssueRequest{AssigneeType: strPtr(""), AssigneeID: strPtr("")}
	old := OldIssueSnapshot{AssigneeType: "member", AssigneeUUID: "11111111-1111-1111-1111-111111111111"}
	out := BuildUpdateIssueInput(old, req, nil, map[string]int64{"11111111-1111-1111-1111-111111111111": 7})
	if out.AssigneeIDs == nil {
		t.Fatal("AssigneeIDs should be &[] (not nil) for explicit clear")
	}
	if len(*out.AssigneeIDs) != 0 {
		t.Errorf("want empty slice, got %v", *out.AssigneeIDs)
	}
}
```

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Extend function signatures**

```go
func BuildCreateIssueInput(req CreateIssueRequest, agentSlugByUUID map[string]string, memberGitlabUserIDByUUID map[string]int64) gitlabapi.CreateIssueInput {
	// ... existing agent label logic ...
	
	if req.AssigneeType == "member" && req.AssigneeID != "" {
		if glID, ok := memberGitlabUserIDByUUID[req.AssigneeID]; ok {
			out.AssigneeIDs = []int64{glID}
		}
	}
	return out
}

func BuildUpdateIssueInput(old OldIssueSnapshot, req UpdateIssueRequest, agentSlugByUUID map[string]string, memberGitlabUserIDByUUID map[string]int64) gitlabapi.UpdateIssueInput {
	// ... existing status/priority/agent label logic ...
	
	// Member assignee transitions.
	if req.AssigneeType != nil {
		if *req.AssigneeType == "" {
			// Explicit clear: send AssigneeIDs: &[] so GitLab drops assignees.
			empty := []int64{}
			out.AssigneeIDs = &empty
		} else if *req.AssigneeType == "member" && req.AssigneeID != nil && *req.AssigneeID != "" {
			if glID, ok := memberGitlabUserIDByUUID[*req.AssigneeID]; ok {
				ids := []int64{glID}
				out.AssigneeIDs = &ids
			}
			// Unmapped: leave nil (don't touch GitLab assignees; cache-only
			// behavior preserved via the handler's fallback UpdateIssue patch).
		}
		// "agent" handled via label path above.
	}
	return out
}
```

- [ ] **Step 4: Run — PASS**. Update all existing callers to pass `nil` for the new map param (they'll still compile).

- [ ] **Step 5: Commit**

```bash
git add server/internal/gitlab/translator.go server/internal/gitlab/translator_test.go
git commit -m "feat(gitlab): translator resolves member assignees to GitLab user IDs via caller-provided map"
```

---

## Task 7: Handler — `CreateIssue` member assignee write-through

**Files:**
- Modify: `server/internal/handler/issue.go` — `CreateIssue` write-through branch
- Test: `server/internal/handler/issue_test.go`

Phase 3a excluded member assignees when constructing the GitLab payload. Phase 4 adds the member-UUID → GitLab-user-ID resolution before calling `BuildCreateIssueInput`.

- [ ] **Step 1: Add a handler helper**

```go
// buildMemberGitlabUserMap resolves the Multica member UUIDs referenced in
// a request to their GitLab user IDs. Used by write-through handlers that
// need to set GitLab's native assignee_ids. Unmapped members are simply
// absent from the returned map — the translator then falls back to
// cache-only behavior for them.
func (h *Handler) buildMemberGitlabUserMap(ctx context.Context, workspaceID pgtype.UUID, memberUUIDs []string) (map[string]int64, error) {
	out := make(map[string]int64, len(memberUUIDs))
	for _, memberUUID := range memberUUIDs {
		if memberUUID == "" {
			continue
		}
		conn, err := h.Queries.GetUserGitlabConnection(ctx, db.GetUserGitlabConnectionParams{
			UserID:      parseUUID(memberUUID),
			WorkspaceID: workspaceID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, err
		}
		if conn.GitlabUserID != 0 {
			out[memberUUID] = conn.GitlabUserID
		}
	}
	return out, nil
}
```

- [ ] **Step 2: Write the failing test**

```go
func TestCreateIssue_WriteThroughMemberAssigneeSetsGitlabAssignees(t *testing.T) {
	var capturedBody map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9001,"iid":500,"title":"T","state":"opened","labels":[],"assignees":[{"id":7,"username":"alice"}]}`))
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	seedGitlabWriteThroughFixture(t, h)
	seedUserGitlabConnection(t, testUserID, testWorkspaceID, 7, "alice")

	body := fmt.Sprintf(`{"title":"T","assignee_type":"member","assignee_id":"%s"}`, testUserID)
	req := httptest.NewRequest(http.MethodPost, "/api/issues", strings.NewReader(body))
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rec := httptest.NewRecorder()

	h.CreateIssue(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	assignees, _ := capturedBody["assignee_ids"].([]any)
	if len(assignees) != 1 || int(assignees[0].(float64)) != 7 {
		t.Errorf("GitLab assignee_ids = %v, want [7]", assignees)
	}
}
```

- [ ] **Step 3: Run — FAIL**

- [ ] **Step 4: Update `CreateIssue` write-through**

In the write-through branch, before calling `BuildCreateIssueInput`, resolve the request's member assignee:

```go
memberUUIDs := []string{}
if req.AssigneeType != nil && *req.AssigneeType == "member" && req.AssigneeID != nil {
    memberUUIDs = append(memberUUIDs, *req.AssigneeID)
}
memberGitlabMap, mapErr := h.buildMemberGitlabUserMap(r.Context(), parseUUID(workspaceID), memberUUIDs)
if mapErr != nil {
    writeError(w, http.StatusInternalServerError, mapErr.Error())
    return
}
glInput := gitlabsync.BuildCreateIssueInput(translatorReq, agentSlugByUUID, memberGitlabMap)
```

- [ ] **Step 5: Run — PASS**

- [ ] **Step 6: Commit**

```bash
git add server/internal/handler/issue.go server/internal/handler/issue_test.go
git commit -m "feat(handler): CreateIssue write-through resolves member assignees to GitLab user IDs"
```

---

## Task 8: Handler — `updateSingleIssueWriteThrough` lifts Phase 3b member deferral

**Files:**
- Modify: `server/internal/handler/issue.go` — `updateSingleIssueWriteThrough` helper
- Test: `server/internal/handler/issue_test.go`

Phase 3b wrote member assignees to cache only (see line ~1551–1562 comment "Member assignees are cache-only in Phase 3b"). Phase 4 removes the comment and sends `AssigneeIDs` through `BuildUpdateIssueInput`.

- [ ] **Step 1: Write failing tests**

```go
func TestUpdateIssue_WriteThroughMemberAssigneeHitsGitLab(t *testing.T) {
	var capturedBody map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9002,"iid":501,"state":"opened","assignees":[{"id":7}],"labels":[],"updated_at":"2026-04-17T13:00:00Z"}`))
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	seedGitlabWriteThroughFixture(t, h)
	seedUserGitlabConnection(t, testUserID, testWorkspaceID, 7, "alice")
	issueID := seedGitlabConnectedIssue(t, 501, 42)

	body := fmt.Sprintf(`{"assignee_type":"member","assignee_id":"%s"}`, testUserID)
	req := httptest.NewRequest(http.MethodPut, "/api/issues/"+issueID, strings.NewReader(body))
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.UpdateIssue(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	ids, _ := capturedBody["assignee_ids"].([]any)
	if len(ids) != 1 || int(ids[0].(float64)) != 7 {
		t.Errorf("assignee_ids = %v, want [7]", ids)
	}
}

func TestUpdateIssue_WriteThroughExplicitNullMemberAssigneeClearsGitLab(t *testing.T) {
	// PATCH {"assignee_type":null,"assignee_id":null} on a member-assigned
	// issue should send AssigneeIDs: [] to GitLab.
}

func TestUpdateIssue_WriteThroughUnmappedMemberStaysCacheOnly(t *testing.T) {
	// Member without user_gitlab_connection → no assignee_ids on GitLab;
	// cache still records the member assignment.
}
```

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Update the handler**

Remove the Phase 3b member-assignee-cache-only special case. Pre-resolve the map:

```go
memberUUIDs := []string{}
if req.AssigneeType != nil && *req.AssigneeType == "member" && req.AssigneeID != nil && *req.AssigneeID != "" {
    memberUUIDs = append(memberUUIDs, *req.AssigneeID)
}
// Also include the OLD member assignee if we're transitioning away
// from it — needed for explicit-clear detection (but translator only
// needs the NEW assignee map, so old isn't strictly required here).

memberGitlabMap, err := h.buildMemberGitlabUserMap(r.Context(), existing.WorkspaceID, memberUUIDs)
if err != nil { /* 500 */ }

glInput := gitlabsync.BuildUpdateIssueInput(oldSnap, translatorReq, agentSlugByUUID, memberGitlabMap)
```

Remove the Phase 3b `touched = true` branch for `*req.AssigneeType == "member"` — the translator now handles it. Native `UpdateIssue` patch for `parent_issue_id`/`project_id` stays.

- [ ] **Step 4: Run — PASS**

- [ ] **Step 5: Verify Phase 3b regression tests still pass** (`TestUpdateIssue_WriteThroughPreservesAssigneeAndDueDateOnTitleOnlyPatch`, `TestUpdateIssue_WriteThroughExplicitNullMemberAssigneeClears` — the latter may need its expectation updated now that GitLab IS called).

- [ ] **Step 6: Commit**

```bash
git add server/internal/handler/issue.go server/internal/handler/issue_test.go
git commit -m "feat(handler): PATCH member assignees flow through GitLab assignee_ids"
```

---

## Task 9: Autopilot — refactor to call write-through + populate `autopilot_issue`

**Files:**
- Modify: `server/internal/handler/issue.go` — extract a `createIssueInternal(...)` method callable from non-HTTP callers
- Modify: autopilot service (path TBD from Task 0 investigation) — call `createIssueInternal`, insert `autopilot_issue` row on success
- Test: autopilot service test + handler test

**Read first:** find where autopilot creates issues today. Grep `CreateIssue` callers + `origin_type = "autopilot"` + `autopilot_run` usages. Most likely the autopilot runner calls `h.Queries.CreateIssue(...)` directly, bypassing write-through entirely.

- [ ] **Step 1: Write the failing test**

```go
func TestAutopilot_CreateIssueGoesThroughGitlab(t *testing.T) {
	var gitlabHit bool
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/projects/42/issues") && r.Method == http.MethodPost {
			gitlabHit = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9003,"iid":600,"title":"Autopilot","state":"opened","labels":[]}`))
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	seedGitlabWriteThroughFixture(t, h)
	runID := seedAutopilotRun(t) // new test helper

	ctx := context.Background()
	issue, err := autopilotSvc.CreateIssue(ctx, runID, "Autopilot", "body", nil)
	if err != nil {
		t.Fatalf("autopilotSvc.CreateIssue: %v", err)
	}

	if !gitlabHit {
		t.Error("GitLab was not called — autopilot must go through write-through")
	}

	// Verify autopilot_issue mapping row exists.
	var count int
	_ = testPool.QueryRow(ctx, `SELECT COUNT(*) FROM autopilot_issue WHERE autopilot_run_id = $1 AND gitlab_iid = $2`, runID, 600).Scan(&count)
	if count != 1 {
		t.Errorf("autopilot_issue mapping missing, count=%d", count)
	}
}
```

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Extract `createIssueInternal`**

Refactor `h.CreateIssue` (HTTP handler) so its body is:

```go
func (h *Handler) CreateIssue(w http.ResponseWriter, r *http.Request) {
    var req CreateIssueRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil { /* 400 */ }
    workspaceID := r.Context().Value(middleware.WorkspaceIDCtxKey).(string)
    actorType, actorID := h.resolveActor(r, userID, workspaceID)
    resp, cacheRow, err := h.createIssueInternal(r.Context(), workspaceID, actorType, actorID, req)
    if err != nil { /* classify + write error */ }
    _ = cacheRow // for post-commit hooks if needed outside HTTP
    writeJSON(w, http.StatusCreated, resp)
}
```

Where `createIssueInternal(ctx, workspaceID, actorType, actorID, req)` is the same write-through flow minus the HTTP body-parse and context extraction. Both the HTTP handler AND autopilot call it.

- [ ] **Step 4: Update autopilot service to call `createIssueInternal` + insert mapping**

```go
func (s *AutopilotService) CreateIssue(ctx context.Context, runID string, title, body string, opts *CreateIssueOpts) (*IssueResponse, error) {
	workspaceID, err := s.workspaceForRun(ctx, runID)
	if err != nil { return nil, err }
	// Autopilot acts with agent actor — uses service PAT + agent prefix not applicable (issues don't have prefixes; comments do).
	resp, cacheRow, err := s.handler.createIssueInternal(ctx, workspaceID, "agent", /* agent uuid for autopilot? */, CreateIssueRequest{
		Title: title, Description: body,
	})
	if err != nil {
		return nil, fmt.Errorf("autopilot create issue: %w", err)
	}

	if cacheRow.GitlabIid.Valid {
		if _, err := s.queries.UpsertAutopilotIssue(ctx, db.UpsertAutopilotIssueParams{
			AutopilotRunID: parseUUID(runID),
			WorkspaceID:    parseUUID(workspaceID),
			GitlabIid:      cacheRow.GitlabIid.Int32,
		}); err != nil {
			return nil, fmt.Errorf("autopilot_issue mapping: %w", err)
		}
	}
	return resp, nil
}
```

Actor attribution: autopilot is not an "agent" in the sense of having an agent_id in the workspace — it's a workspace-level autopilot run. For token resolution purposes, `actorType == "agent"` ensures the service PAT is used (per `ResolveTokenForWrite` rules). `actorID` can be the run ID or a dedicated service-actor UUID; choose whichever the `Resolver` accepts without erroring. Double-check — if `ResolveTokenForWrite` looks up the agent's row by `actorID` and it's not a valid agent UUID, it will fall back to service PAT anyway. Verify the behavior in existing resolver tests.

- [ ] **Step 5: Run — PASS**

- [ ] **Step 6: Commit**

```bash
git add server/internal/handler/issue.go server/internal/service/autopilot/* server/internal/handler/issue_test.go
git commit -m "feat(autopilot): create issues via GitLab write-through and record autopilot_issue mapping"
```

---

## Task 10: Autopilot listener — use `autopilot_issue` mapping

**Files:**
- Modify: `server/cmd/server/autopilot_listeners.go`
- Test: autopilot listener test

Current `EventIssueUpdated` listener checks `issue.OriginType.String == "autopilot"` to identify autopilot-origin issues. Phase 4 adds a lookup in `autopilot_issue` first; if found, use that run; otherwise fall back to `origin_type` (legacy path) until Phase 5 drops the column.

- [ ] **Step 1: Write failing test**

```go
func TestAutopilotListener_UsesAutopilotIssueMapping(t *testing.T) {
	runID := seedAutopilotRun(t)
	issueID := seedGitlabConnectedIssue(t, 700, 42)
	// Seed autopilot_issue mapping instead of origin_type.
	_, _ = testPool.Exec(ctx, `INSERT INTO autopilot_issue (autopilot_run_id, workspace_id, gitlab_iid) VALUES ($1, $2, $3)`, runID, parseUUID(testWorkspaceID), 700)

	// Emit EventIssueUpdated with status=done.
	// Assert the autopilot run status is synced.
}
```

- [ ] **Step 2: Implement**

In the listener:

```go
autopilotRun, err := queries.GetAutopilotIssueByIID(ctx, db.GetAutopilotIssueByIIDParams{
    WorkspaceID: issue.WorkspaceID, GitlabIid: issue.GitlabIid,
})
if err == nil {
    // Use autopilotRun.AutopilotRunID
} else if !errors.Is(err, pgx.ErrNoRows) {
    // log, return
} else {
    // Legacy fallback
    if issue.OriginType.Valid && issue.OriginType.String == "autopilot" { ... }
}
```

Keep both paths for now; Phase 5 drops the legacy path.

- [ ] **Step 3: Run — PASS**

- [ ] **Step 4: Commit**

```bash
git add server/cmd/server/autopilot_listeners.go server/cmd/server/autopilot_listeners_test.go
git commit -m "feat(autopilot): listener prefers autopilot_issue mapping; legacy origin_type fallback"
```

---

## Task 11: Final verification

- [ ] **Step 1: Full Go test suite**

```bash
cd server && DATABASE_URL=... go test ./...
```
Expected: only pre-existing date-flakes fail.

- [ ] **Step 2: Frontend checks**

```bash
pnpm typecheck && pnpm test
```
Expected: green.

- [ ] **Step 3: Router state**

```bash
grep -n "r\.With(gw)\\|GitlabWritesBlocked" server/cmd/server/router.go
```
Expected: zero matches in router.go (middleware still defined in `internal/middleware/`).

- [ ] **Step 4: Verify migrations round-trip**

```bash
cd server && DATABASE_URL=... go run ./cmd/migrate down && go run ./cmd/migrate up
```
Expected: full down → up cycle succeeds for 056, 057, 058.

- [ ] **Step 5: Smoke-test autopilot end-to-end (if time)**

Optional: spin up the test DB, simulate autopilot run completion, confirm the issue lands in GitLab + the `autopilot_issue` row appears.

---

## Self-Review Checklist

1. **Spec coverage.**
   - Member-assignee write-through → Tasks 6, 7, 8 ✓
   - Webhook reverse-resolution → Task 4 ✓
   - Webhook → agent-task dispatch → Task 5 ✓
   - `autopilot_issue` + autopilot re-point → Tasks 9, 10 ✓
   - Schema relaxation (comment_reaction, issue.assignee_type) → Task 0 ✓
   - Phase 2b Note award_emoji filter fix → Task 4 ✓
2. **Placeholder scan.** Task 9's "actor attribution for autopilot" needs verification against the resolver's behavior — flagged inline as something to confirm during implementation.
3. **Type consistency.** `ResolveMulticaUserFromGitlabUserID` returns `(string, string, string, error)` — used in Tasks 4, 5, 6, 7, 8 consistently. `IssueValues.GitlabAssigneeUserID` / `CreatorGitlabUserID` added in Task 3 — consumed in Task 4.
4. **Hard rules.**
   - Write-through authoritative — Tasks 7, 8, 9 ✓
   - Resolver fallback order encoded once (Task 2) ✓
   - Agent writes unchanged (Tasks 7, 8 don't touch agent branches) ✓
5. **TDD discipline.** Every task writes failing tests first ✓.
6. **No frontend touches.** Phase 4 is backend-only ✓.
7. **Migration safety.** 056 + 057 + 058 are additive/relaxation; down scripts present.

---

## Execution Handoff

Plan complete. Saved to `docs/superpowers/plans/2026-04-17-gitlab-issues-integration-phase-4.md`.

**Two execution options:**

1. **Subagent-Driven (recommended)** — fresh subagent per task, parallelize across different files.
2. **Inline Execution** — batch execution with checkpoints.

**Which approach?**
