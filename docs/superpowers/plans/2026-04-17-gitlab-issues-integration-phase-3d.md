# GitLab Issues Integration — Phase 3d Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable write-through for the remaining 6 write endpoints — issue subscribe/unsubscribe + issue and comment reactions — on GitLab-connected workspaces. After Phase 3d, zero routes are 501-gated.

**Architecture:** Mirror Phase 3a/3b/3c pattern — resolver picks actor-appropriate PAT → GitLab REST call → cache upsert. For reactions, use GitLab's award_emoji API (`POST /issues/:iid/award_emoji`, `DELETE /issues/:iid/award_emoji/:award_id`, and note-scoped variants). For subscribe, use `POST /issues/:iid/subscribe` + `/unsubscribe` which are per-user idempotent (304 on no-op). Agent-authored reactions and subscribes stay Multica-only (per design — agents don't have GitLab user IDs). Emoji names pass through as-is (GitLab's naming — `thumbsup`, `heart`, etc.).

**Tech Stack:** Go 1.26, Chi router, pgx/v5, sqlc, httptest for fake GitLab server. One new migration (055) to add GitLab columns to `comment_reaction`.

---

## Scope

**In scope for Phase 3d:**
- `POST /api/issues/{id}/subscribe` — write-through subscribe for members; Multica-only for agents
- `POST /api/issues/{id}/unsubscribe` — same
- `POST /api/issues/{id}/reactions` — write-through award_emoji for members; Multica-only for agents
- `DELETE /api/issues/{id}/reactions` — write-through delete award_emoji (requires `gitlab_award_id` lookup)
- `POST /api/comments/{id}/reactions` — write-through note award_emoji for members
- `DELETE /api/comments/{id}/reactions` — same (requires `gitlab_award_id` lookup)
- GitLab REST client: `CreateIssueAwardEmoji`, `DeleteIssueAwardEmoji`, `CreateNoteAwardEmoji`, `DeleteNoteAwardEmoji`, `Subscribe`, `Unsubscribe`
- Migration 055: add `gitlab_award_id`, `external_updated_at`, `gitlab_actor_user_id` to `comment_reaction` (Phase 2a oversight — `issue_reaction` already has these)
- sqlc query: `UpsertCommentReactionFromGitlab` (mirrors existing `UpsertIssueReactionFromGitlab`)

**Out of scope (flagged for future work):**
- Phase 2b webhook handler currently filters out Note award_emoji events (only Issue awards sync). Phase 3d's forward path creates note reactions, but reverse sync (GitLab UI → Multica for comment reactions) stays broken. Fix tracked as a follow-up — not a Phase 3d regression, just an unchanged pre-existing gap.
- Member-assignee → GitLab user-ID mapping (Phase 4)
- Legacy column/table cleanup (Phase 5)

## File Structure

**New files:**
- `server/migrations/055_comment_reaction_gitlab_columns.up.sql` — add 3 columns + partial unique index
- `server/migrations/055_comment_reaction_gitlab_columns.down.sql` — revert

**Files to modify:**

| File | Responsibility |
|---|---|
| `server/pkg/db/queries/gitlab_cache.sql` | Add `UpsertCommentReactionFromGitlab` (mirrors `UpsertIssueReactionFromGitlab`) |
| `server/pkg/gitlab/award_emoji.go` | Add `CreateIssueAwardEmoji`, `DeleteIssueAwardEmoji`, `CreateNoteAwardEmoji`, `DeleteNoteAwardEmoji` |
| `server/pkg/gitlab/subscribe.go` (create) | `Subscribe`, `Unsubscribe` — 304-idempotent |
| `server/pkg/gitlab/award_emoji_test.go` | Tests for 4 new methods |
| `server/pkg/gitlab/subscribe_test.go` (create) | Tests for 2 new methods |
| `server/internal/handler/issue_reaction.go` | Add write-through to `AddIssueReaction`, `RemoveIssueReaction` |
| `server/internal/handler/reaction.go` | Add write-through to `AddReaction` (comment), `RemoveReaction` |
| `server/internal/handler/subscriber.go` | Add write-through to `SubscribeToIssue`, `UnsubscribeFromIssue` |
| `server/internal/handler/*_test.go` | Write-through tests for all 6 handlers (happy + agent-only + GitLab-error where applicable) |
| `server/cmd/server/router.go` | Unmount all 6 gated routes |

## Hard rules

1. **Write-through authoritative.** GitLab API error → HTTP 502. Never fall through to legacy direct-DB path on a connected workspace.
2. **Agent actors skip the GitLab call.** For reactions and subscribe, if `resolveActor` returns `actorType == "agent"`, write to Multica cache only. No attribution mismatch on the GitLab side. Documented in the plan's design decisions.
3. **Idempotent GitLab responses treated as success:**
   - Subscribe: GitLab 304 (already subscribed) → treat as success in client method
   - Unsubscribe: GitLab 304 (not subscribed) → treat as success
   - Delete award_emoji: GitLab 404 → treat as success (already removed)
   - Create award_emoji: GitLab 409 (duplicate) → client surfaces the 409; handler should pre-check by looking up the local row first
4. **`gitlab_award_id` must be stored on cache insert.** Without it, we can't DELETE later. The POST paths capture the award ID from the GitLab response and persist it via the `UpsertXxxReactionFromGitlab` query. DELETE paths read it from the local row before calling GitLab.
5. **`ResolveTokenForWrite`** with the acting user (member case). Fails fast on unknown actor types (Phase 3a M5 guard).
6. **Emoji name pass-through.** Whatever the client sends, we pass unchanged to GitLab. No translation table.

---

## Task 0: Migration 055 + sqlc query for comment_reaction cache

**Files:**
- Create: `server/migrations/055_comment_reaction_gitlab_columns.up.sql`
- Create: `server/migrations/055_comment_reaction_gitlab_columns.down.sql`
- Modify: `server/pkg/db/queries/gitlab_cache.sql` (add `UpsertCommentReactionFromGitlab`)

Migration 050 added these columns to `issue_reaction` but not `comment_reaction` — an oversight. Phase 3d needs them to track GitLab award IDs for subsequent DELETE calls.

- [ ] **Step 1: Write the up migration**

Create `server/migrations/055_comment_reaction_gitlab_columns.up.sql`:

```sql
-- Add GitLab tracking columns to comment_reaction, mirroring what migration
-- 050 added to issue_reaction. Needed for Phase 3d write-through of
-- comment reactions (award_emoji on GitLab notes).

ALTER TABLE comment_reaction
    ADD COLUMN gitlab_award_id BIGINT,
    ADD COLUMN external_updated_at TIMESTAMPTZ,
    ADD COLUMN gitlab_actor_user_id BIGINT;

-- Unique partial index so webhook sync (or future write-through) can
-- idempotently upsert by GitLab's award_id without stepping on Multica-
-- native (pre-connection) reactions that have NULL gitlab_award_id.
CREATE UNIQUE INDEX comment_reaction_gitlab_award_id_unique
    ON comment_reaction (gitlab_award_id)
    WHERE gitlab_award_id IS NOT NULL;
```

- [ ] **Step 2: Write the down migration**

Create `server/migrations/055_comment_reaction_gitlab_columns.down.sql`:

```sql
DROP INDEX IF EXISTS comment_reaction_gitlab_award_id_unique;
ALTER TABLE comment_reaction
    DROP COLUMN IF EXISTS gitlab_actor_user_id,
    DROP COLUMN IF EXISTS external_updated_at,
    DROP COLUMN IF EXISTS gitlab_award_id;
```

- [ ] **Step 3: Run the migration**

```bash
cd server && DATABASE_URL="postgres://multica:multica@localhost:5432/multica_multica_gitlab_phase_3d_<port>?sslmode=disable" go run ./cmd/migrate up
```

Expected output: `up 055_comment_reaction_gitlab_columns`. Confirm via psql: `\d comment_reaction` should show the 3 new columns.

Before running, ensure the phase-3d DB exists: `createdb` via a superuser role if needed (the `multica` role doesn't have CREATE DB privilege). See how Phase 3c's worktree setup handled this.

- [ ] **Step 4: Add the sqlc query**

Open `server/pkg/db/queries/gitlab_cache.sql`. Locate `UpsertIssueReactionFromGitlab` (around line 153–174). Mirror it for comments — add this query:

```sql
-- name: UpsertCommentReactionFromGitlab :one
INSERT INTO comment_reaction (
    workspace_id,
    comment_id,
    actor_type,
    actor_id,
    gitlab_actor_user_id,
    emoji,
    gitlab_award_id,
    external_updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (gitlab_award_id) WHERE gitlab_award_id IS NOT NULL DO UPDATE SET
    actor_type = EXCLUDED.actor_type,
    actor_id = EXCLUDED.actor_id,
    gitlab_actor_user_id = EXCLUDED.gitlab_actor_user_id,
    emoji = EXCLUDED.emoji,
    external_updated_at = EXCLUDED.external_updated_at
WHERE comment_reaction.external_updated_at IS NULL
   OR comment_reaction.external_updated_at < EXCLUDED.external_updated_at
RETURNING *;
```

Note: `actor_type` is `TEXT NOT NULL` per the original migration (not pgtype.Text). Check the actual column nullability — if Phase 2b relaxed NOT NULL on `comment_reaction.actor_type` (to allow NULL for unmapped webhook-origin rows), use the same nullable handling. If not, ensure the caller always supplies a non-null actor_type (Phase 3d write-through always knows the actor from resolveActor).

- [ ] **Step 5: Add a Delete-by-award-id query**

Same file, same pattern the issue_reaction side presumably has — add:

```sql
-- name: DeleteCommentReactionByGitlabAwardID :exec
DELETE FROM comment_reaction WHERE gitlab_award_id = $1;
```

And verify the equivalent exists for issue reactions: `DeleteIssueReactionByGitlabAwardID`. Grep for it first; if missing, add it.

- [ ] **Step 6: Regenerate sqlc**

```bash
cd server && make sqlc
```

Expected: `server/pkg/db/generated/gitlab_cache.sql.go` updated with the new query constants + Go bindings.

- [ ] **Step 7: Confirm build and existing tests still pass**

Run:
```bash
cd server && go build ./...
cd server && DATABASE_URL=... go test ./internal/gitlab/ ./internal/handler/ -run 'Reaction|AwardEmoji|Webhook'
```

Expected: PASS. No existing test touches `comment_reaction.gitlab_award_id` yet.

- [ ] **Step 8: Commit**

```bash
git add server/migrations/055_comment_reaction_gitlab_columns.up.sql server/migrations/055_comment_reaction_gitlab_columns.down.sql server/pkg/db/queries/gitlab_cache.sql server/pkg/db/generated/gitlab_cache.sql.go
git commit -m "feat(db): add gitlab_award_id tracking to comment_reaction"
```

---

## Task 1: GitLab client — `CreateIssueAwardEmoji` + `DeleteIssueAwardEmoji`

**Files:**
- Modify: `server/pkg/gitlab/award_emoji.go` — add two methods after the existing `ListAwardEmoji`
- Test: `server/pkg/gitlab/award_emoji_test.go` — create if absent, or extend

Both methods in one task since they're short and closely related. Both modify the same file; no parallelization risk within this task.

- [ ] **Step 1: Write the failing tests**

Create or extend `server/pkg/gitlab/award_emoji_test.go`:

```go
package gitlab

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateIssueAwardEmoji_SendsPOST(t *testing.T) {
	var capturedMethod, capturedPath, capturedToken string
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		capturedToken = r.Header.Get("PRIVATE-TOKEN")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9901,"name":"thumbsup","user":{"id":7},"created_at":"2026-04-17T12:00:00Z","updated_at":"2026-04-17T12:00:00Z"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	award, err := c.CreateIssueAwardEmoji(context.Background(), "tok", 42, 7, "thumbsup")
	if err != nil {
		t.Fatalf("CreateIssueAwardEmoji: %v", err)
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", capturedMethod)
	}
	if capturedPath != "/api/v4/projects/42/issues/7/award_emoji" {
		t.Errorf("path = %s", capturedPath)
	}
	if capturedToken != "tok" {
		t.Errorf("token = %s", capturedToken)
	}
	if capturedBody["name"] != "thumbsup" {
		t.Errorf("body name = %v, want thumbsup", capturedBody["name"])
	}
	if award.ID != 9901 || award.Name != "thumbsup" {
		t.Errorf("award = %+v", award)
	}
}

func TestCreateIssueAwardEmoji_PropagatesNon2xx(t *testing.T) {
	// 409 duplicate is a real GitLab response when the user has already awarded.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"already awarded"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	_, err := c.CreateIssueAwardEmoji(context.Background(), "tok", 1, 1, "thumbsup")
	if err == nil {
		t.Fatal("expected error on 409")
	}
	if !strings.Contains(err.Error(), "409") && !strings.Contains(err.Error(), "already") {
		t.Errorf("error = %v, want 409 or 'already'", err)
	}
}

func TestDeleteIssueAwardEmoji_SendsDELETE(t *testing.T) {
	var capturedMethod, capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.DeleteIssueAwardEmoji(context.Background(), "tok", 42, 7, 9901); err != nil {
		t.Fatalf("DeleteIssueAwardEmoji: %v", err)
	}
	if capturedMethod != http.MethodDelete {
		t.Errorf("method = %s", capturedMethod)
	}
	if capturedPath != "/api/v4/projects/42/issues/7/award_emoji/9901" {
		t.Errorf("path = %s", capturedPath)
	}
}

func TestDeleteIssueAwardEmoji_404IsIdempotentSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.DeleteIssueAwardEmoji(context.Background(), "tok", 1, 1, 1); err != nil {
		t.Fatalf("expected 404 as success, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
cd server && go test ./pkg/gitlab/ -run 'TestCreateIssueAwardEmoji|TestDeleteIssueAwardEmoji' -v
```
Expected: FAIL (methods undefined).

- [ ] **Step 3: Add implementation**

Add to `server/pkg/gitlab/award_emoji.go` (after `ListAwardEmoji`):

```go
// CreateIssueAwardEmoji sends POST /api/v4/projects/:id/issues/:iid/award_emoji
// with {"name": "<emoji>"}. Returns the created AwardEmoji (including its ID,
// which the caller stores in the cache as gitlab_award_id so subsequent DELETE
// can target the specific award).
func (c *Client) CreateIssueAwardEmoji(ctx context.Context, token string, projectID int64, issueIID int, name string) (*AwardEmoji, error) {
	path := fmt.Sprintf("/api/v4/projects/%d/issues/%d/award_emoji", projectID, issueIID)
	payload := map[string]any{"name": name}
	var out AwardEmoji
	if err := c.do(ctx, http.MethodPost, token, path, payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteIssueAwardEmoji sends DELETE /api/v4/projects/:id/issues/:iid/award_emoji/:award_id.
// Treats GitLab 404 as idempotent success — if the award is already gone,
// that's the desired state.
func (c *Client) DeleteIssueAwardEmoji(ctx context.Context, token string, projectID int64, issueIID int, awardID int64) error {
	path := fmt.Sprintf("/api/v4/projects/%d/issues/%d/award_emoji/%d", projectID, issueIID, awardID)
	err := c.do(ctx, http.MethodDelete, token, path, nil, nil)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
```

Add `"errors"` and `"fmt"` to imports if not present. Match the parameter style established by Phase 3c's `CreateNote` (likely `projectID int64, issueIID int`).

- [ ] **Step 4: Run tests — should pass**

```bash
cd server && go test ./pkg/gitlab/ -run 'TestCreateIssueAwardEmoji|TestDeleteIssueAwardEmoji' -v
```
Expected: all 4 PASS.

- [ ] **Step 5: Verify full package + vet**

```bash
cd server && go test ./pkg/gitlab/ && go vet ./pkg/gitlab/
```
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add server/pkg/gitlab/award_emoji.go server/pkg/gitlab/award_emoji_test.go
git commit -m "feat(gitlab): CreateIssueAwardEmoji + DeleteIssueAwardEmoji REST methods"
```

---

## Task 2: GitLab client — `CreateNoteAwardEmoji` + `DeleteNoteAwardEmoji`

**Files:**
- Modify: `server/pkg/gitlab/award_emoji.go` — add two methods after Task 1's
- Test: `server/pkg/gitlab/award_emoji_test.go`

Note-scoped variants. The URL path differs: `/issues/:iid/notes/:note_id/award_emoji` instead of `/issues/:iid/award_emoji`.

- [ ] **Step 1: Write the failing tests**

Append to `server/pkg/gitlab/award_emoji_test.go`:

```go
func TestCreateNoteAwardEmoji_SendsPOST(t *testing.T) {
	var capturedMethod, capturedPath string
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9902,"name":"heart","user":{"id":7},"created_at":"2026-04-17T12:00:00Z","updated_at":"2026-04-17T12:00:00Z"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	award, err := c.CreateNoteAwardEmoji(context.Background(), "tok", 42, 7, 555, "heart")
	if err != nil {
		t.Fatalf("CreateNoteAwardEmoji: %v", err)
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("method = %s", capturedMethod)
	}
	if capturedPath != "/api/v4/projects/42/issues/7/notes/555/award_emoji" {
		t.Errorf("path = %s", capturedPath)
	}
	if capturedBody["name"] != "heart" {
		t.Errorf("body name = %v", capturedBody["name"])
	}
	if award.ID != 9902 || award.Name != "heart" {
		t.Errorf("award = %+v", award)
	}
}

func TestDeleteNoteAwardEmoji_SendsDELETE(t *testing.T) {
	var capturedMethod, capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.DeleteNoteAwardEmoji(context.Background(), "tok", 42, 7, 555, 9902); err != nil {
		t.Fatalf("DeleteNoteAwardEmoji: %v", err)
	}
	if capturedMethod != http.MethodDelete {
		t.Errorf("method = %s", capturedMethod)
	}
	if capturedPath != "/api/v4/projects/42/issues/7/notes/555/award_emoji/9902" {
		t.Errorf("path = %s", capturedPath)
	}
}

func TestDeleteNoteAwardEmoji_404IsIdempotentSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.DeleteNoteAwardEmoji(context.Background(), "tok", 1, 1, 1, 1); err != nil {
		t.Fatalf("expected 404 as success, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
cd server && go test ./pkg/gitlab/ -run 'TestCreateNoteAwardEmoji|TestDeleteNoteAwardEmoji' -v
```
Expected: FAIL.

- [ ] **Step 3: Add implementation**

```go
// CreateNoteAwardEmoji sends POST /api/v4/projects/:id/issues/:iid/notes/:note_id/award_emoji.
func (c *Client) CreateNoteAwardEmoji(ctx context.Context, token string, projectID int64, issueIID int, noteID int64, name string) (*AwardEmoji, error) {
	path := fmt.Sprintf("/api/v4/projects/%d/issues/%d/notes/%d/award_emoji", projectID, issueIID, noteID)
	payload := map[string]any{"name": name}
	var out AwardEmoji
	if err := c.do(ctx, http.MethodPost, token, path, payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteNoteAwardEmoji sends DELETE /api/v4/projects/:id/issues/:iid/notes/:note_id/award_emoji/:award_id.
// Treats GitLab 404 as idempotent success.
func (c *Client) DeleteNoteAwardEmoji(ctx context.Context, token string, projectID int64, issueIID int, noteID int64, awardID int64) error {
	path := fmt.Sprintf("/api/v4/projects/%d/issues/%d/notes/%d/award_emoji/%d", projectID, issueIID, noteID, awardID)
	err := c.do(ctx, http.MethodDelete, token, path, nil, nil)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
```

- [ ] **Step 4: Run tests — should pass**

Expected: all 3 PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/gitlab/award_emoji.go server/pkg/gitlab/award_emoji_test.go
git commit -m "feat(gitlab): CreateNoteAwardEmoji + DeleteNoteAwardEmoji REST methods"
```

---

## Task 3: GitLab client — `Subscribe` + `Unsubscribe`

**Files:**
- Create: `server/pkg/gitlab/subscribe.go`
- Create: `server/pkg/gitlab/subscribe_test.go`

Subscribe/unsubscribe live in their own file for clarity. They're simple POSTs with 304-idempotent semantics.

- [ ] **Step 1: Write the failing tests**

Create `server/pkg/gitlab/subscribe_test.go`:

```go
package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubscribe_SendsPOST(t *testing.T) {
	var capturedMethod, capturedPath, capturedToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		capturedToken = r.Header.Get("PRIVATE-TOKEN")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9001,"iid":7,"subscribed":true}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.Subscribe(context.Background(), "tok", 42, 7); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", capturedMethod)
	}
	if capturedPath != "/api/v4/projects/42/issues/7/subscribe" {
		t.Errorf("path = %s", capturedPath)
	}
	if capturedToken != "tok" {
		t.Errorf("token = %s", capturedToken)
	}
}

func TestSubscribe_304IsIdempotentSuccess(t *testing.T) {
	// GitLab returns 304 when the user is already subscribed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.Subscribe(context.Background(), "tok", 1, 1); err != nil {
		t.Fatalf("expected 304 as success, got %v", err)
	}
}

func TestUnsubscribe_SendsPOST(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9001,"iid":7,"subscribed":false}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.Unsubscribe(context.Background(), "tok", 42, 7); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if capturedPath != "/api/v4/projects/42/issues/7/unsubscribe" {
		t.Errorf("path = %s", capturedPath)
	}
}

func TestUnsubscribe_304IsIdempotentSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.Unsubscribe(context.Background(), "tok", 1, 1); err != nil {
		t.Fatalf("expected 304 as success, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
cd server && go test ./pkg/gitlab/ -run 'TestSubscribe|TestUnsubscribe' -v
```
Expected: FAIL (methods undefined).

- [ ] **Step 3: Add implementation**

Create `server/pkg/gitlab/subscribe.go`:

```go
package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// Subscribe sends POST /api/v4/projects/:id/issues/:iid/subscribe. GitLab
// returns 304 Not Modified when the user (identified by the PAT) is already
// subscribed — treated as idempotent success.
func (c *Client) Subscribe(ctx context.Context, token string, projectID int64, issueIID int) error {
	path := fmt.Sprintf("/api/v4/projects/%d/issues/%d/subscribe", projectID, issueIID)
	err := c.do(ctx, http.MethodPost, token, path, nil, nil)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotModified) {
		return nil
	}
	return err
}

// Unsubscribe sends POST /api/v4/projects/:id/issues/:iid/unsubscribe. GitLab
// returns 304 Not Modified when the user is not currently subscribed —
// treated as idempotent success.
func (c *Client) Unsubscribe(ctx context.Context, token string, projectID int64, issueIID int) error {
	path := fmt.Sprintf("/api/v4/projects/%d/issues/%d/unsubscribe", projectID, issueIID)
	err := c.do(ctx, http.MethodPost, token, path, nil, nil)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotModified) {
		return nil
	}
	return err
}
```

- [ ] **Step 4: Confirm `ErrNotModified` exists in the client's error taxonomy**

Read `server/pkg/gitlab/errors.go`. The existing sentinels include `ErrNotFound`, `ErrForbidden`, `ErrUnauthorized`. If `ErrNotModified` is absent, add it to `errors.go` alongside the others:

```go
var ErrNotModified = errors.New("gitlab: not modified (304)")
```

And update the `do` helper (in `client.go` or wherever the sentinel wiring lives) to map HTTP 304 to `ErrNotModified`:

```go
case http.StatusNotModified:
    return fmt.Errorf("%w", ErrNotModified)
```

Grep the existing `do` helper's status-code handling and mirror the pattern used for 403/404/401.

- [ ] **Step 5: Run tests — should pass**

```bash
cd server && go test ./pkg/gitlab/ -run 'TestSubscribe|TestUnsubscribe' -v
```
Expected: all 4 PASS.

- [ ] **Step 6: Commit**

```bash
git add server/pkg/gitlab/subscribe.go server/pkg/gitlab/subscribe_test.go server/pkg/gitlab/errors.go server/pkg/gitlab/client.go
git commit -m "feat(gitlab): Subscribe + Unsubscribe REST methods with 304-idempotent handling"
```

(Exclude `errors.go` / `client.go` from the commit if no changes were needed.)

---

## Task 4: Handler — `AddIssueReaction` write-through

**Files:**
- Modify: `server/internal/handler/issue_reaction.go`
- Test: `server/internal/handler/issue_reaction_test.go`

- [ ] **Step 1: Read prerequisites**

1. `server/internal/handler/issue_reaction.go` — legacy `AddIssueReaction` handler. Understand:
   - Request shape: probably `{emoji: string}`
   - Actor resolution via `h.resolveActor(r, userID, workspaceID)` → `(actorType, actorID)`
   - Insert via `h.Queries.AddIssueReaction(ctx, params)` (idempotent — unique on issue+actor+emoji)
   - WS event `protocol.EventIssueReactionAdded`
2. Phase 3c's `createCommentWriteThrough` for the write-through pattern to mirror.
3. `UpsertIssueReactionFromGitlab` params (in `server/pkg/db/generated/gitlab_cache.sql.go`): `workspace_id, issue_id, actor_type, actor_id, gitlab_actor_user_id, emoji, gitlab_award_id, external_updated_at`.

- [ ] **Step 2: Write the failing tests**

Add to `server/internal/handler/issue_reaction_test.go`:

```go
func TestAddIssueReaction_WriteThroughHumanCallsGitLab(t *testing.T) {
	ctx := context.Background()
	var capturedMethod, capturedPath string
	var capturedBody map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9901,"name":"thumbsup","user":{"id":7},"created_at":"2026-04-17T12:00:00Z","updated_at":"2026-04-17T12:00:00Z"}`))
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	seedGitlabWriteThroughFixture(t, h)

	issueID := seedGitlabConnectedIssue(t, 600, 42)

	// Send request as a member (no X-Agent-ID).
	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/reactions", strings.NewReader(`{"emoji":"thumbsup"}`))
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.AddIssueReaction(rec, req)

	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("method = %s", capturedMethod)
	}
	if capturedPath != "/api/v4/projects/42/issues/600/award_emoji" {
		t.Errorf("path = %s", capturedPath)
	}
	if capturedBody["name"] != "thumbsup" {
		t.Errorf("body name = %v", capturedBody["name"])
	}

	// Cache row should be present with gitlab_award_id = 9901.
	var count int
	_ = testPool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_reaction WHERE issue_id = $1 AND gitlab_award_id = 9901`, issueID).Scan(&count)
	if count != 1 {
		t.Errorf("cache row missing with gitlab_award_id=9901, got count=%d", count)
	}
}

func TestAddIssueReaction_WriteThroughAgentStaysMulticaOnly(t *testing.T) {
	ctx := context.Background()
	var gitlabCalls int
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gitlabCalls++
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	seedGitlabWriteThroughFixture(t, h)

	agentID := seedAgent(t, "reactor")
	issueID := seedGitlabConnectedIssue(t, 601, 42)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/reactions", strings.NewReader(`{"emoji":"rocket"}`))
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req.Header.Set("X-Agent-ID", agentID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.AddIssueReaction(rec, req)

	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if gitlabCalls != 0 {
		t.Errorf("GitLab got %d calls — agent reactions must stay Multica-only", gitlabCalls)
	}
	// Cache should still have the row (Multica-local).
	var count int
	_ = testPool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_reaction WHERE issue_id = $1 AND actor_type = 'agent' AND emoji = 'rocket'`, issueID).Scan(&count)
	if count != 1 {
		t.Errorf("Multica cache row missing, count=%d", count)
	}
}

func TestAddIssueReaction_WriteThroughGitLabErrorReturns502(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	seedGitlabWriteThroughFixture(t, h)

	issueID := seedGitlabConnectedIssue(t, 602, 42)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/reactions", strings.NewReader(`{"emoji":"thumbsup"}`))
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.AddIssueReaction(rec, req)

	if rec.Code < 400 {
		t.Fatalf("status = %d, want >=400", rec.Code)
	}
	// Cache must not have the row (write-through is authoritative — no fallthrough).
	var count int
	_ = testPool.QueryRow(context.Background(), `SELECT COUNT(*) FROM issue_reaction WHERE issue_id = $1`, issueID).Scan(&count)
	if count != 0 {
		t.Errorf("cache leaked on error, count = %d", count)
	}
}
```

`seedAgent` is already established in Phase 3c — grep for it. If absent in this test file, import from `comment_test.go`'s pattern (extract to a shared test helper file if needed).

- [ ] **Step 3: Run to verify failure**

Expected: all 3 FAIL.

- [ ] **Step 4: Implement the write-through branch**

In `server/internal/handler/issue_reaction.go`, insert at the top of `AddIssueReaction`, after parsing the request body, loading the issue, resolving the actor, BEFORE the legacy `h.Queries.AddIssueReaction(...)` call:

```go
if h.GitlabEnabled && h.GitlabResolver != nil {
    wsConn, wsErr := h.Queries.GetWorkspaceGitlabConnection(r.Context(), issue.WorkspaceID)
    if wsErr == nil {
        // Agent reactions stay Multica-only — don't round-trip to GitLab.
        // Fall through to the legacy insert below so Multica keeps the row.
        if actorType == "agent" {
            // fall through
        } else {
            if !issue.GitlabIid.Valid || !issue.GitlabProjectID.Valid {
                writeError(w, http.StatusBadGateway, "issue missing gitlab identifiers on connected workspace")
                return
            }

            token, _, tokErr := h.GitlabResolver.ResolveTokenForWrite(r.Context(), uuidToString(issue.WorkspaceID), actorType, actorID)
            if tokErr != nil {
                writeError(w, http.StatusInternalServerError, tokErr.Error())
                return
            }

            award, glErr := h.Gitlab.CreateIssueAwardEmoji(r.Context(), token, issue.GitlabProjectID.Int64, int(issue.GitlabIid.Int32), req.Emoji)
            if glErr != nil {
                writeError(w, http.StatusBadGateway, glErr.Error())
                return
            }

            extUpdatedAt := parseGitlabTS(award.UpdatedAt)
            row, err := h.Queries.UpsertIssueReactionFromGitlab(r.Context(), db.UpsertIssueReactionFromGitlabParams{
                WorkspaceID:        issue.WorkspaceID,
                IssueID:            issue.ID,
                ActorType:          pgtype.Text{String: actorType, Valid: true},
                ActorID:            pgUUID(actorID),
                GitlabActorUserID:  pgtype.Int8{Int64: award.User.ID, Valid: true},
                Emoji:              award.Name,
                GitlabAwardID:      pgtype.Int8{Int64: award.ID, Valid: true},
                ExternalUpdatedAt:  extUpdatedAt,
            })
            if err != nil && !errors.Is(err, pgx.ErrNoRows) {
                writeError(w, http.StatusInternalServerError, err.Error())
                return
            }
            // pgx.ErrNoRows = clobber guard rejected (webhook already wrote same award).
            // That's fine; the user-visible reaction state is correct. Load current row
            // for the WS event if needed, or just emit a simpler payload.
            if errors.Is(err, pgx.ErrNoRows) {
                row, _ = h.Queries.GetIssueReactionByGitlabAwardID(r.Context(), pgtype.Int8{Int64: award.ID, Valid: true})
            }

            resp := issueReactionToResponse(row)
            h.publish(protocol.EventIssueReactionAdded, uuidToString(issue.WorkspaceID), actorType, actorID, map[string]any{"reaction": resp})
            writeJSON(w, http.StatusCreated, resp)
            return
        }
    }
}
// Fall through to legacy h.Queries.AddIssueReaction path (agent OR non-connected workspace).
```

Helpers to verify exist (grep first):
- `parseGitlabTS` — added in Phase 3c Task 5
- `pgUUID` — established earlier
- `uuidToString`
- `writeError`
- `issueReactionToResponse` — grep; may need to be added if the legacy handler inlines the response shape
- `GetIssueReactionByGitlabAwardID` — add to `gitlab_cache.sql` if missing:
  ```sql
  -- name: GetIssueReactionByGitlabAwardID :one
  SELECT * FROM issue_reaction WHERE gitlab_award_id = $1 LIMIT 1;
  ```

- [ ] **Step 5: Run tests — should pass**

```bash
cd server && DATABASE_URL=... go test ./internal/handler/ -run TestAddIssueReaction_WriteThrough -v
```
Expected: all 3 PASS.

- [ ] **Step 6: Run full handler suite — no regressions**

- [ ] **Step 7: Commit**

```bash
git add server/internal/handler/issue_reaction.go server/internal/handler/issue_reaction_test.go server/pkg/db/queries/gitlab_cache.sql server/pkg/db/generated/gitlab_cache.sql.go
git commit -m "feat(handler): POST /api/issues/{id}/reactions writes through GitLab when connected"
```

---

## Task 5: Handler — `RemoveIssueReaction` write-through

**Files:**
- Modify: `server/internal/handler/issue_reaction.go` — `RemoveIssueReaction`
- Test: `server/internal/handler/issue_reaction_test.go`

- [ ] **Step 1: Read prerequisites**

Look at legacy `RemoveIssueReaction`. Understand:
- Request shape (probably `{emoji: string}` in body or query)
- Looks up existing row by `(issue_id, actor_type, actor_id, emoji)` to delete
- Returns 204 on success

For write-through: we need to look up the row (to get `gitlab_award_id`), call GitLab DELETE, then delete the local row.

- [ ] **Step 2: Write the failing tests**

```go
func TestRemoveIssueReaction_WriteThroughDeletesOnGitLab(t *testing.T) {
	ctx := context.Background()
	var capturedMethod, capturedPath string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	seedGitlabWriteThroughFixture(t, h)

	issueID := seedGitlabConnectedIssue(t, 610, 42)

	// Pre-seed a reaction row with gitlab_award_id=9901.
	_, err := testPool.Exec(ctx,
		`INSERT INTO issue_reaction (id, workspace_id, issue_id, actor_type, actor_id, emoji, gitlab_award_id, gitlab_actor_user_id, external_updated_at)
		 VALUES (gen_random_uuid(), $1, $2, 'member', $3, 'thumbsup', 9901, 7, '2026-04-17T12:00:00Z')`,
		parseUUID(testWorkspaceID), parseUUID(issueID), parseUUID(testUserID))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/"+issueID+"/reactions", strings.NewReader(`{"emoji":"thumbsup"}`))
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.RemoveIssueReaction(rec, req)

	if rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if capturedMethod != http.MethodDelete {
		t.Errorf("method = %s", capturedMethod)
	}
	if capturedPath != "/api/v4/projects/42/issues/610/award_emoji/9901" {
		t.Errorf("path = %s", capturedPath)
	}

	var count int
	_ = testPool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_reaction WHERE issue_id = $1 AND emoji = 'thumbsup'`, issueID).Scan(&count)
	if count != 0 {
		t.Errorf("cache row not deleted, count=%d", count)
	}
}

func TestRemoveIssueReaction_WriteThroughAgentStaysMulticaOnly(t *testing.T) {
	ctx := context.Background()
	var gitlabCalls int
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gitlabCalls++
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	seedGitlabWriteThroughFixture(t, h)

	agentID := seedAgent(t, "reactor")
	issueID := seedGitlabConnectedIssue(t, 611, 42)
	_, _ = testPool.Exec(ctx,
		`INSERT INTO issue_reaction (id, workspace_id, issue_id, actor_type, actor_id, emoji)
		 VALUES (gen_random_uuid(), $1, $2, 'agent', $3, 'rocket')`,
		parseUUID(testWorkspaceID), parseUUID(issueID), parseUUID(agentID))

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/"+issueID+"/reactions", strings.NewReader(`{"emoji":"rocket"}`))
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req.Header.Set("X-Agent-ID", agentID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.RemoveIssueReaction(rec, req)

	if rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if gitlabCalls != 0 {
		t.Errorf("GitLab got %d calls — agent removals stay Multica-only", gitlabCalls)
	}
}

func TestRemoveIssueReaction_WriteThroughGitLabErrorPreservesCache(t *testing.T) {
	ctx := context.Background()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	seedGitlabWriteThroughFixture(t, h)

	issueID := seedGitlabConnectedIssue(t, 612, 42)
	_, _ = testPool.Exec(ctx,
		`INSERT INTO issue_reaction (id, workspace_id, issue_id, actor_type, actor_id, emoji, gitlab_award_id)
		 VALUES (gen_random_uuid(), $1, $2, 'member', $3, 'thumbsup', 9902)`,
		parseUUID(testWorkspaceID), parseUUID(issueID), parseUUID(testUserID))

	req := httptest.NewRequest(http.MethodDelete, "/api/issues/"+issueID+"/reactions", strings.NewReader(`{"emoji":"thumbsup"}`))
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.RemoveIssueReaction(rec, req)

	if rec.Code < 400 {
		t.Fatalf("status = %d, want >=400", rec.Code)
	}
	var count int
	_ = testPool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_reaction WHERE issue_id = $1`, issueID).Scan(&count)
	if count != 1 {
		t.Errorf("cache mutated on error, count=%d", count)
	}
}
```

- [ ] **Step 3: Run to verify failure**

- [ ] **Step 4: Implement the write-through branch**

Pattern: load existing reaction row (to get `gitlab_award_id`), branch on agent, call GitLab DELETE if member, then delete local row.

Specifically insert at the top of `RemoveIssueReaction`, after parsing `req.Emoji` and resolving the actor:

```go
if h.GitlabEnabled && h.GitlabResolver != nil {
    wsConn, wsErr := h.Queries.GetWorkspaceGitlabConnection(r.Context(), issue.WorkspaceID)
    if wsErr == nil && actorType != "agent" {
        // Look up the existing reaction to get gitlab_award_id.
        existing, exErr := h.Queries.GetIssueReactionByKey(r.Context(), db.GetIssueReactionByKeyParams{
            IssueID:   issue.ID,
            ActorType: pgtype.Text{String: actorType, Valid: true},
            ActorID:   pgUUID(actorID),
            Emoji:     req.Emoji,
        })
        if exErr != nil {
            if errors.Is(exErr, pgx.ErrNoRows) {
                // No local row → nothing to remove; idempotent.
                w.WriteHeader(http.StatusNoContent)
                return
            }
            writeError(w, http.StatusInternalServerError, exErr.Error())
            return
        }

        if !issue.GitlabIid.Valid || !issue.GitlabProjectID.Valid {
            writeError(w, http.StatusBadGateway, "issue missing gitlab identifiers")
            return
        }

        if existing.GitlabAwardID.Valid {
            token, _, tokErr := h.GitlabResolver.ResolveTokenForWrite(r.Context(), uuidToString(issue.WorkspaceID), actorType, actorID)
            if tokErr != nil {
                writeError(w, http.StatusInternalServerError, tokErr.Error())
                return
            }

            if err := h.Gitlab.DeleteIssueAwardEmoji(r.Context(), token, issue.GitlabProjectID.Int64, int(issue.GitlabIid.Int32), existing.GitlabAwardID.Int64); err != nil {
                writeError(w, http.StatusBadGateway, err.Error())
                return
            }
        }
        // If no gitlab_award_id (pre-connection reaction), skip the GitLab
        // call — just remove locally. This is a data-integrity edge case:
        // connected workspace + Multica-native reaction. Let it clean up.

        if err := h.Queries.DeleteIssueReactionByID(r.Context(), existing.ID); err != nil {
            writeError(w, http.StatusInternalServerError, err.Error())
            return
        }

        h.publish(protocol.EventIssueReactionRemoved, uuidToString(issue.WorkspaceID), actorType, actorID, map[string]any{
            "issue_id":   uuidToString(issue.ID),
            "emoji":      req.Emoji,
            "actor_type": actorType,
            "actor_id":   actorID,
        })
        w.WriteHeader(http.StatusNoContent)
        return
    }
    // Agent actor OR non-connected workspace: fall through to legacy path.
}
```

Helpers:
- `GetIssueReactionByKey` — add to `issue_reaction.sql` or reaction.sql if missing:
  ```sql
  -- name: GetIssueReactionByKey :one
  SELECT * FROM issue_reaction
  WHERE issue_id = $1 AND actor_type = $2 AND actor_id = $3 AND emoji = $4
  LIMIT 1;
  ```
- `DeleteIssueReactionByID` — add if missing:
  ```sql
  -- name: DeleteIssueReactionByID :exec
  DELETE FROM issue_reaction WHERE id = $1;
  ```

Run `make sqlc` after adding queries.

- [ ] **Step 5: Run tests — should pass**

- [ ] **Step 6: Commit**

```bash
git add server/internal/handler/issue_reaction.go server/internal/handler/issue_reaction_test.go server/pkg/db/queries/ server/pkg/db/generated/
git commit -m "feat(handler): DELETE /api/issues/{id}/reactions writes through GitLab when connected"
```

---

## Task 6: Handler — `AddReaction` (comment) write-through

**Files:**
- Modify: `server/internal/handler/reaction.go` — `AddReaction`
- Test: `server/internal/handler/reaction_test.go`

Mirror Task 4's structure for comment reactions. Key differences:
- URL: `/api/comments/{commentId}/reactions` — URL param is `commentId`, not `id`
- Need to load the comment first (to get `gitlab_note_id`) and then its parent issue (for `gitlab_project_id` + `gitlab_iid`)
- Use `CreateNoteAwardEmoji` (not `CreateIssueAwardEmoji`)
- Use `UpsertCommentReactionFromGitlab` (Task 0 added this)

- [ ] **Step 1: Write the failing tests**

```go
func TestAddReaction_WriteThroughHumanCallsGitLab(t *testing.T) {
	ctx := context.Background()
	var capturedMethod, capturedPath string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9950,"name":"heart","user":{"id":7},"created_at":"2026-04-17T12:00:00Z","updated_at":"2026-04-17T12:00:00Z"}`))
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	seedGitlabWriteThroughFixture(t, h)

	issueID := seedGitlabConnectedIssue(t, 700, 42)
	commentID := uuid.New().String()
	_, _ = testPool.Exec(ctx,
		`INSERT INTO comment (id, workspace_id, issue_id, author_type, author_id, content, gitlab_note_id, external_updated_at)
		 VALUES ($1, $2, $3, 'member', $4, 'x', 8900, '2026-04-17T12:00:00Z')`,
		commentID, parseUUID(testWorkspaceID), parseUUID(issueID), parseUUID(testUserID))

	req := httptest.NewRequest(http.MethodPost, "/api/comments/"+commentID+"/reactions", strings.NewReader(`{"emoji":"heart"}`))
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req = withURLParam(req, "commentId", commentID)
	rec := httptest.NewRecorder()

	h.AddReaction(rec, req)

	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("method = %s", capturedMethod)
	}
	if capturedPath != "/api/v4/projects/42/issues/700/notes/8900/award_emoji" {
		t.Errorf("path = %s", capturedPath)
	}

	var count int
	_ = testPool.QueryRow(ctx, `SELECT COUNT(*) FROM comment_reaction WHERE comment_id = $1 AND gitlab_award_id = 9950`, commentID).Scan(&count)
	if count != 1 {
		t.Errorf("cache row missing, count=%d", count)
	}
}

// Agent + GitLab-error tests mirror Task 4 — copy the structure; swap paths.
func TestAddReaction_WriteThroughAgentStaysMulticaOnly(t *testing.T) { /* ... */ }
func TestAddReaction_WriteThroughGitLabErrorReturns502(t *testing.T) { /* ... */ }
```

Fill in the 2 skipped tests following Task 4's exact pattern (agent stays Multica-only; GitLab error preserves cache non-mutation).

- [ ] **Step 2: Run — all 3 FAIL**

- [ ] **Step 3: Implement the branch**

In `server/internal/handler/reaction.go` at the top of `AddReaction`:

```go
if h.GitlabEnabled && h.GitlabResolver != nil {
    comment, cErr := h.Queries.GetCommentInWorkspace(r.Context(), db.GetCommentInWorkspaceParams{
        ID: parseUUID(commentID), WorkspaceID: parseUUID(workspaceID),
    })
    if cErr != nil {
        writeError(w, http.StatusNotFound, "comment not found")
        return
    }

    wsConn, wsErr := h.Queries.GetWorkspaceGitlabConnection(r.Context(), comment.WorkspaceID)
    if wsErr == nil && actorType != "agent" {
        issue, issueErr := h.Queries.GetIssue(r.Context(), comment.IssueID)
        if issueErr != nil {
            writeError(w, http.StatusInternalServerError, issueErr.Error())
            return
        }
        if !issue.GitlabIid.Valid || !issue.GitlabProjectID.Valid || !comment.GitlabNoteID.Valid {
            writeError(w, http.StatusBadGateway, "comment or issue missing gitlab identifiers")
            return
        }

        token, _, tokErr := h.GitlabResolver.ResolveTokenForWrite(r.Context(), uuidToString(comment.WorkspaceID), actorType, actorID)
        if tokErr != nil {
            writeError(w, http.StatusInternalServerError, tokErr.Error())
            return
        }

        award, glErr := h.Gitlab.CreateNoteAwardEmoji(r.Context(), token, issue.GitlabProjectID.Int64, int(issue.GitlabIid.Int32), comment.GitlabNoteID.Int64, req.Emoji)
        if glErr != nil {
            writeError(w, http.StatusBadGateway, glErr.Error())
            return
        }

        extUpdatedAt := parseGitlabTS(award.UpdatedAt)
        row, err := h.Queries.UpsertCommentReactionFromGitlab(r.Context(), db.UpsertCommentReactionFromGitlabParams{
            WorkspaceID:        comment.WorkspaceID,
            CommentID:          comment.ID,
            ActorType:          actorType,
            ActorID:            pgUUID(actorID),
            GitlabActorUserID:  pgtype.Int8{Int64: award.User.ID, Valid: true},
            Emoji:              award.Name,
            GitlabAwardID:      pgtype.Int8{Int64: award.ID, Valid: true},
            ExternalUpdatedAt:  extUpdatedAt,
        })
        if err != nil && !errors.Is(err, pgx.ErrNoRows) {
            writeError(w, http.StatusInternalServerError, err.Error())
            return
        }
        if errors.Is(err, pgx.ErrNoRows) {
            row, _ = h.Queries.GetCommentReactionByGitlabAwardID(r.Context(), pgtype.Int8{Int64: award.ID, Valid: true})
        }

        resp := commentReactionToResponse(row)
        h.publish(protocol.EventCommentReactionAdded, uuidToString(comment.WorkspaceID), actorType, actorID, map[string]any{"reaction": resp})
        writeJSON(w, http.StatusCreated, resp)
        return
    }
}
// Fall through to legacy
```

Add sqlc query if missing:
```sql
-- name: GetCommentReactionByGitlabAwardID :one
SELECT * FROM comment_reaction WHERE gitlab_award_id = $1 LIMIT 1;
```

- [ ] **Step 4: Run tests — PASS**

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/reaction.go server/internal/handler/reaction_test.go server/pkg/db/queries/ server/pkg/db/generated/
git commit -m "feat(handler): POST /api/comments/{id}/reactions writes through GitLab when connected"
```

---

## Task 7: Handler — `RemoveReaction` (comment) write-through

**Files:** `server/internal/handler/reaction.go` (`RemoveReaction`) + `reaction_test.go`.

Mirror Task 5 for comments. Look up the comment reaction by key → get `gitlab_award_id` → call `DeleteNoteAwardEmoji` → delete local row.

- [ ] **Step 1: Write 3 failing tests** (happy path for member, agent-stays-Multica-only, GitLab-error-preserves-cache). Mirror Task 5 exactly, swapping comment_reaction + /api/comments/{commentId}/reactions + `DeleteNoteAwardEmoji` path.

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement**

Add sqlc queries:
```sql
-- name: GetCommentReactionByKey :one
SELECT * FROM comment_reaction
WHERE comment_id = $1 AND actor_type = $2 AND actor_id = $3 AND emoji = $4
LIMIT 1;

-- name: DeleteCommentReactionByID :exec
DELETE FROM comment_reaction WHERE id = $1;
```

Handler pattern:

```go
if h.GitlabEnabled && h.GitlabResolver != nil {
    comment, cErr := h.Queries.GetCommentInWorkspace(r.Context(), /* params */)
    if cErr != nil {
        writeError(w, http.StatusNotFound, "comment not found")
        return
    }
    _, wsErr := h.Queries.GetWorkspaceGitlabConnection(r.Context(), comment.WorkspaceID)
    if wsErr == nil && actorType != "agent" {
        existing, exErr := h.Queries.GetCommentReactionByKey(r.Context(), db.GetCommentReactionByKeyParams{
            CommentID: comment.ID, ActorType: actorType, ActorID: pgUUID(actorID), Emoji: req.Emoji,
        })
        if exErr != nil {
            if errors.Is(exErr, pgx.ErrNoRows) {
                w.WriteHeader(http.StatusNoContent)
                return
            }
            writeError(w, http.StatusInternalServerError, exErr.Error())
            return
        }

        issue, _ := h.Queries.GetIssue(r.Context(), comment.IssueID)
        if !issue.GitlabIid.Valid || !issue.GitlabProjectID.Valid || !comment.GitlabNoteID.Valid {
            writeError(w, http.StatusBadGateway, "identifiers missing")
            return
        }

        if existing.GitlabAwardID.Valid {
            token, _, tokErr := h.GitlabResolver.ResolveTokenForWrite(r.Context(), uuidToString(comment.WorkspaceID), actorType, actorID)
            if tokErr != nil {
                writeError(w, http.StatusInternalServerError, tokErr.Error())
                return
            }
            if err := h.Gitlab.DeleteNoteAwardEmoji(r.Context(), token, issue.GitlabProjectID.Int64, int(issue.GitlabIid.Int32), comment.GitlabNoteID.Int64, existing.GitlabAwardID.Int64); err != nil {
                writeError(w, http.StatusBadGateway, err.Error())
                return
            }
        }

        if err := h.Queries.DeleteCommentReactionByID(r.Context(), existing.ID); err != nil {
            writeError(w, http.StatusInternalServerError, err.Error())
            return
        }

        h.publish(protocol.EventCommentReactionRemoved, uuidToString(comment.WorkspaceID), actorType, actorID, map[string]any{
            "comment_id": uuidToString(comment.ID),
            "emoji":      req.Emoji,
            "actor_type": actorType,
            "actor_id":   actorID,
        })
        w.WriteHeader(http.StatusNoContent)
        return
    }
}
// Fall through to legacy.
```

- [ ] **Step 4: Tests PASS**

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/reaction.go server/internal/handler/reaction_test.go server/pkg/db/queries/ server/pkg/db/generated/
git commit -m "feat(handler): DELETE /api/comments/{id}/reactions writes through GitLab when connected"
```

---

## Task 8: Handler — `SubscribeToIssue` write-through

**Files:** `server/internal/handler/subscriber.go` + `subscriber_test.go`.

Subscribe semantics:
- Member actor on connected workspace → call GitLab `Subscribe` (304 = success), then local `AddIssueSubscriber`.
- Agent actor → local only.
- GitLab error → 502, don't add to cache.

- [ ] **Step 1: Write the failing tests**

```go
func TestSubscribeToIssue_WriteThroughHumanCallsGitLab(t *testing.T) {
	var capturedMethod, capturedPath string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	seedGitlabWriteThroughFixture(t, h)

	issueID := seedGitlabConnectedIssue(t, 800, 42)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/subscribe", nil)
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.SubscribeToIssue(rec, req)

	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("method = %s", capturedMethod)
	}
	if capturedPath != "/api/v4/projects/42/issues/800/subscribe" {
		t.Errorf("path = %s", capturedPath)
	}

	var count int
	_ = testPool.QueryRow(context.Background(), `SELECT COUNT(*) FROM issue_subscriber WHERE issue_id = $1 AND user_id = $2`, issueID, parseUUID(testUserID)).Scan(&count)
	if count != 1 {
		t.Errorf("cache subscriber missing, count=%d", count)
	}
}

func TestSubscribeToIssue_WriteThroughAgentStaysMulticaOnly(t *testing.T) {
	// Agent subscribes — no GitLab call. Cache has the subscriber row.
}

func TestSubscribeToIssue_WriteThroughGitLabErrorReturns502(t *testing.T) {
	// Fake returns 403, no cache row created.
}

func TestSubscribeToIssue_WriteThrough304TreatedAsIdempotent(t *testing.T) {
	// Fake returns 304, handler proceeds to add cache row.
}
```

Fill in the 3 skipped tests.

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement**

```go
if h.GitlabEnabled && h.GitlabResolver != nil && actorType != "agent" {
    wsConn, wsErr := h.Queries.GetWorkspaceGitlabConnection(r.Context(), issue.WorkspaceID)
    if wsErr == nil {
        if !issue.GitlabIid.Valid || !issue.GitlabProjectID.Valid {
            writeError(w, http.StatusBadGateway, "issue missing gitlab identifiers")
            return
        }

        token, _, tokErr := h.GitlabResolver.ResolveTokenForWrite(r.Context(), uuidToString(issue.WorkspaceID), actorType, actorID)
        if tokErr != nil {
            writeError(w, http.StatusInternalServerError, tokErr.Error())
            return
        }

        if err := h.Gitlab.Subscribe(r.Context(), token, issue.GitlabProjectID.Int64, int(issue.GitlabIid.Int32)); err != nil {
            writeError(w, http.StatusBadGateway, err.Error())
            return
        }

        if err := h.Queries.AddIssueSubscriber(r.Context(), db.AddIssueSubscriberParams{
            IssueID:  issue.ID,
            UserType: actorType,
            UserID:   pgUUID(actorID),
            Reason:   "manual",
        }); err != nil {
            writeError(w, http.StatusInternalServerError, err.Error())
            return
        }

        h.publish(protocol.EventIssueSubscribed, uuidToString(issue.WorkspaceID), actorType, actorID, map[string]any{
            "issue_id": uuidToString(issue.ID),
        })
        w.WriteHeader(http.StatusCreated)
        return
    }
}
// Fall through to legacy
```

- [ ] **Step 4: Run — PASS**

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/subscriber.go server/internal/handler/subscriber_test.go
git commit -m "feat(handler): POST /api/issues/{id}/subscribe writes through GitLab when connected"
```

---

## Task 9: Handler — `UnsubscribeFromIssue` write-through

**Files:** `server/internal/handler/subscriber.go` (`UnsubscribeFromIssue`) + `subscriber_test.go`.

Mirror Task 8. Call `h.Gitlab.Unsubscribe(...)` (304 idempotent), then `h.Queries.RemoveIssueSubscriber(...)`.

- [ ] **Step 1: Write 4 failing tests** (happy path, agent stays Multica-only, GitLab error preserves cache, 304 treated as idempotent).

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement** — mirror Task 8 with `Unsubscribe` + `RemoveIssueSubscriber`.

- [ ] **Step 4: Tests PASS**

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/subscriber.go server/internal/handler/subscriber_test.go
git commit -m "feat(handler): POST /api/issues/{id}/unsubscribe writes through GitLab when connected"
```

---

## Task 10: Unmount all 6 routes

**Files:** `server/cmd/server/router.go`

After all write-through work is in, unmount the 6 Phase 3d routes from the `gw` middleware.

- [ ] **Step 1: Read the current router state**

In `server/cmd/server/router.go`, find:

```go
// Lines ~276–280 (inside /api/issues/{id} sub-route):
r.With(gw).Post("/subscribe", h.SubscribeToIssue)
r.With(gw).Post("/unsubscribe", h.UnsubscribeFromIssue)
r.With(gw).Post("/reactions", h.AddIssueReaction)
r.With(gw).Delete("/reactions", h.RemoveIssueReaction)

// Lines ~334–335 (inside /api/comments/{commentId} sub-route):
r.With(gw).Post("/reactions", h.AddReaction)
r.With(gw).Delete("/reactions", h.RemoveReaction)
```

- [ ] **Step 2: Remove `r.With(gw)` from all 6 lines**

Change each to its unwrapped form:

```go
r.Post("/subscribe", h.SubscribeToIssue)
r.Post("/unsubscribe", h.UnsubscribeFromIssue)
r.Post("/reactions", h.AddIssueReaction)
r.Delete("/reactions", h.RemoveIssueReaction)

r.Post("/reactions", h.AddReaction)
r.Delete("/reactions", h.RemoveReaction)
```

- [ ] **Step 3: Verify no `gw` wraps remain for writes**

```bash
grep -n "r.With(gw)" server/cmd/server/router.go
```

Expected output: empty. All write-through work is done. The `gw := middleware.GitlabWritesBlocked(queries)` declarations can stay (they're cheap) OR be removed — prefer leaving them so a future reviewer sees the local var and asks "is this still used?" which will surface that Phase 3d completed the unmount.

Actually — since there are no remaining `r.With(gw)` callers, the local `gw` variable becomes unused. Go will flag this. Either:
- Remove the `gw := ...` declarations (cleanest).
- Convert the reactions-on-comments routes back to `r.Use(middleware.GitlabWritesBlocked(queries))`... no, don't do that — defeats the point.

Best: remove the `gw := ...` decls. Check both route blocks.

- [ ] **Step 4: Verify**

```bash
cd server && go vet ./cmd/server/
cd server && go build ./cmd/server/
cd server && DATABASE_URL=... go test ./cmd/server/ ./internal/handler/ ./internal/middleware/
```

Expected: all green. The `GitlabWritesBlocked` middleware tests should still pass (the middleware itself is unchanged — Phase 3d just stops using it).

- [ ] **Step 5: Commit**

```bash
git add server/cmd/server/router.go
git commit -m "feat(server): unmount 501 stopgap from all remaining write routes"
```

---

## Task 11: Final verification

**Files:** none (verification only)

- [ ] **Step 1: Full Go test suite**

```bash
cd server && DATABASE_URL=... go test ./...
```
Expected: only pre-existing date-bucket flakes fail.

- [ ] **Step 2: Frontend checks**

```bash
pnpm typecheck && pnpm test
```
Expected: all green.

- [ ] **Step 3: Confirm zero `gw` wraps remain**

```bash
grep -n "r.With(gw)\|GitlabWritesBlocked" server/cmd/server/router.go
```

Expected: zero matches. If any remain, verify they're intentional (e.g., if a new route was added during Phase 3d that shouldn't be write-through yet).

- [ ] **Step 4: Confirm the middleware is still wired for future use**

The `middleware.GitlabWritesBlocked` function itself stays in `server/internal/middleware/` — it's useful for future phases that might add new write endpoints. Just don't use it anywhere in `router.go` right now.

- [ ] **Step 5: Smoke build**

```bash
cd server && go build ./cmd/server/
```

- [ ] **Step 6: Manual verification checklist** (for the PR description, not CI-runnable):

Against a real GitLab instance + a connected workspace:
- React 👍 on an issue → award appears on GitLab; cache row has `gitlab_award_id`.
- Un-react → award disappears on GitLab; cache row gone.
- Subscribe to an issue → user (via their PAT) subscribed on GitLab; cache row added.
- Unsubscribe → vice versa.
- React on a comment → note-level award appears on GitLab.
- Agent reacts on an issue → ONLY Multica cache, no GitLab side effect (verify via GitLab UI).

---

## Self-Review Checklist

1. **Spec coverage.** Every endpoint has a task:
   - POST /issues/{id}/subscribe → Task 8 ✓
   - POST /issues/{id}/unsubscribe → Task 9 ✓
   - POST /issues/{id}/reactions → Task 4 ✓
   - DELETE /issues/{id}/reactions → Task 5 ✓
   - POST /comments/{id}/reactions → Task 6 ✓
   - DELETE /comments/{id}/reactions → Task 7 ✓
   - Plus migration + sqlc + client methods → Tasks 0, 1, 2, 3 ✓
   - Route unmount → Task 10 ✓

2. **Placeholder scan.** Task 6 and 7 reference the pattern from Task 4/5 with explicit "mirror Task N exactly" + pointers. Test bodies for the agent/error variants say "fill in the 3 skipped tests following Task N's exact pattern" — these are NOT placeholders because the pattern is fully shown in the referenced task. If an implementer is unsure, Task 4's full bodies provide the template.

3. **Type consistency.**
   - `CreateIssueAwardEmoji(ctx, token, projectID int64, issueIID int, name)` → used in Task 4
   - `DeleteIssueAwardEmoji(ctx, token, projectID int64, issueIID int, awardID int64)` → used in Task 5
   - `CreateNoteAwardEmoji(ctx, token, projectID int64, issueIID int, noteID int64, name)` → used in Task 6
   - `DeleteNoteAwardEmoji(..., noteID int64, awardID int64)` → used in Task 7
   - `Subscribe(ctx, token, projectID int64, issueIID int)` → used in Task 8
   - `Unsubscribe(...)` → used in Task 9
   - `UpsertIssueReactionFromGitlabParams` → Task 4 (exists)
   - `UpsertCommentReactionFromGitlabParams` → Task 6 (added in Task 0)
   - `GetIssueReactionByKey`, `DeleteIssueReactionByID` → Task 5 (added if missing)
   - `GetCommentReactionByKey`, `DeleteCommentReactionByID` → Task 7 (added if missing)
   - `GetIssueReactionByGitlabAwardID` → Task 4 (add if missing)
   - `GetCommentReactionByGitlabAwardID` → Task 6 (add if missing)

4. **Hard rules enforced.**
   - Write-through authoritative (502 on GitLab error, no fallthrough) → Tasks 4, 5, 6, 7, 8, 9 ✓
   - Agent reactions/subscribe stay Multica-only → Tasks 4, 5, 6, 7, 8, 9 have agent-stays-local tests ✓
   - 304 idempotent for subscribe/unsubscribe → Tasks 3, 8, 9 ✓
   - 404 idempotent for DELETE award_emoji → Tasks 1, 2 ✓
   - `gitlab_award_id` captured on POST and used on DELETE → Tasks 4, 5, 6, 7 ✓
   - `ResolveTokenForWrite` for every outbound call → all handler tasks ✓

5. **TDD discipline.** Every handler task writes failing tests first, implements, runs PASS ✓.

6. **No frontend touches.** Phase 3d is backend-only ✓.

7. **Migration safety.** Task 0's migration is additive (new columns + partial unique index). Rollback script present. No data loss risk.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-17-gitlab-issues-integration-phase-3d.md`.

**Two execution options:**

1. **Subagent-Driven (recommended)** — fresh subagent per task, two-stage review, parallelize across different files.
2. **Inline Execution** — batch execution with checkpoints.

**Which approach?**
