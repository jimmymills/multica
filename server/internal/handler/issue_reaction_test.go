package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAddIssueReaction_WriteThroughHumanCallsGitLab verifies that a
// human-authored reaction on a GitLab-connected workspace POSTs to the GitLab
// award_emoji endpoint and that the returned award is upserted into the cache
// keyed by gitlab_award_id.
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
	defer h.Queries.DeleteWorkspaceGitlabConnection(ctx, parseUUID(testWorkspaceID))

	seedGitlabWriteThroughFixture(t, h)

	issueID := seedGitlabConnectedIssue(t, 600, 42)
	defer testPool.Exec(ctx, `DELETE FROM issue_reaction WHERE issue_id = $1`, issueID)
	defer testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/reactions", strings.NewReader(`{"emoji":"thumbsup"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.AddIssueReaction(rec, req)

	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", capturedMethod)
	}
	if capturedPath != "/api/v4/projects/42/issues/600/award_emoji" {
		t.Errorf("path = %s", capturedPath)
	}
	if capturedBody["name"] != "thumbsup" {
		t.Errorf("body name = %v, want thumbsup", capturedBody["name"])
	}

	var count int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue_reaction WHERE issue_id = $1 AND gitlab_award_id = 9901`,
		issueID).Scan(&count); err != nil {
		t.Fatalf("query cache row: %v", err)
	}
	if count != 1 {
		t.Errorf("cache row missing with gitlab_award_id=9901, count=%d", count)
	}
}

// TestAddIssueReaction_WriteThroughAgentStaysMulticaOnly verifies that an
// agent-authored reaction (X-Agent-ID header) on a GitLab-connected workspace
// does NOT call GitLab and falls through to the legacy Multica-only path.
// GitLab's award_emoji endpoint doesn't support attributing awards to agents,
// so agent reactions stay local.
func TestAddIssueReaction_WriteThroughAgentStaysMulticaOnly(t *testing.T) {
	ctx := context.Background()

	// Look up the seeded Handler Test Agent so the X-Agent-ID header resolves.
	var agentID string
	if err := testPool.QueryRow(ctx,
		`SELECT id FROM agent WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, "Handler Test Agent",
	).Scan(&agentID); err != nil {
		t.Fatalf("look up test agent: %v", err)
	}

	var gitlabCalls int
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gitlabCalls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	defer h.Queries.DeleteWorkspaceGitlabConnection(ctx, parseUUID(testWorkspaceID))

	seedGitlabWriteThroughFixture(t, h)

	issueID := seedGitlabConnectedIssue(t, 601, 42)
	defer testPool.Exec(ctx, `DELETE FROM issue_reaction WHERE issue_id = $1`, issueID)
	defer testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/reactions", strings.NewReader(`{"emoji":"rocket"}`))
	req.Header.Set("Content-Type", "application/json")
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
	var count int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue_reaction WHERE issue_id = $1 AND actor_type = 'agent' AND emoji = 'rocket'`,
		issueID).Scan(&count); err != nil {
		t.Fatalf("query cache row: %v", err)
	}
	if count != 1 {
		t.Errorf("Multica cache row missing, count=%d", count)
	}
}

// TestAddIssueReaction_WriteThroughGitLabErrorReturns502 verifies that a
// GitLab error aborts the request and does NOT leave an orphaned cache row.
func TestAddIssueReaction_WriteThroughGitLabErrorReturns502(t *testing.T) {
	ctx := context.Background()

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"forbidden"}`))
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	defer h.Queries.DeleteWorkspaceGitlabConnection(ctx, parseUUID(testWorkspaceID))

	seedGitlabWriteThroughFixture(t, h)

	issueID := seedGitlabConnectedIssue(t, 602, 42)
	defer testPool.Exec(ctx, `DELETE FROM issue_reaction WHERE issue_id = $1`, issueID)
	defer testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/reactions", strings.NewReader(`{"emoji":"thumbsup"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.AddIssueReaction(rec, req)

	if rec.Code < 400 {
		t.Fatalf("status = %d, want >=400", rec.Code)
	}
	var count int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue_reaction WHERE issue_id = $1`,
		issueID).Scan(&count); err != nil {
		t.Fatalf("query cache row count: %v", err)
	}
	if count != 0 {
		t.Errorf("cache leaked on error, count = %d", count)
	}
}
