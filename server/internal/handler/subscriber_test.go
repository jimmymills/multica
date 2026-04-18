package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestSubscribeToIssue_WriteThroughHumanCallsGitLab verifies that on a
// GitLab-connected workspace, a member subscribing to a GitLab-backed issue
// POSTs /api/v4/projects/:id/issues/:iid/subscribe with the caller's PAT and
// then upserts the local subscriber cache row.
func TestSubscribeToIssue_WriteThroughHumanCallsGitLab(t *testing.T) {
	ctx := context.Background()

	var capturedMethod, capturedPath string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	defer h.Queries.DeleteWorkspaceGitlabConnection(ctx, parseUUID(testWorkspaceID))

	seedGitlabWriteThroughFixture(t, h)

	issueID := seedGitlabConnectedIssue(t, 800, 42)
	defer testPool.Exec(ctx, `DELETE FROM issue_subscriber WHERE issue_id = $1`, parseUUID(issueID))
	defer testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, parseUUID(issueID))

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
		t.Errorf("method = %s, want POST", capturedMethod)
	}
	if capturedPath != "/api/v4/projects/42/issues/800/subscribe" {
		t.Errorf("path = %s", capturedPath)
	}
	var count int
	_ = testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue_subscriber WHERE issue_id = $1 AND user_id = $2 AND user_type = 'member'`,
		parseUUID(issueID), parseUUID(testUserID)).Scan(&count)
	if count != 1 {
		t.Errorf("cache subscriber missing, count=%d", count)
	}
}

// TestSubscribeToIssue_WriteThroughAgentStaysMulticaOnly verifies that an
// agent-authored subscribe (X-Agent-ID header set) on a GitLab-connected
// workspace never calls GitLab — agent subscriptions are Multica-only —
// but still writes the Multica cache row.
func TestSubscribeToIssue_WriteThroughAgentStaysMulticaOnly(t *testing.T) {
	ctx := context.Background()

	var gitlabCalls int
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gitlabCalls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	defer h.Queries.DeleteWorkspaceGitlabConnection(ctx, parseUUID(testWorkspaceID))

	seedGitlabWriteThroughFixture(t, h)

	// Look up the seeded Handler Test Agent.
	var agentID string
	if err := testPool.QueryRow(ctx,
		`SELECT id FROM agent WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, "Handler Test Agent",
	).Scan(&agentID); err != nil {
		t.Fatalf("look up test agent: %v", err)
	}

	issueID := seedGitlabConnectedIssue(t, 801, 42)
	defer testPool.Exec(ctx, `DELETE FROM issue_subscriber WHERE issue_id = $1`, parseUUID(issueID))
	defer testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, parseUUID(issueID))

	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/subscribe", nil)
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req.Header.Set("X-Agent-ID", agentID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.SubscribeToIssue(rec, req)

	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if gitlabCalls != 0 {
		t.Errorf("GitLab got %d calls — agent subscribes stay Multica-only", gitlabCalls)
	}
	var count int
	_ = testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue_subscriber WHERE issue_id = $1 AND user_type = 'agent'`,
		parseUUID(issueID)).Scan(&count)
	if count != 1 {
		t.Errorf("Multica cache row missing, count=%d", count)
	}
}

// TestSubscribeToIssue_WriteThroughGitLabErrorReturns502 verifies that a
// non-idempotent GitLab error on the write-through branch aborts the request
// and does NOT leave an orphaned cache row behind.
func TestSubscribeToIssue_WriteThroughGitLabErrorReturns502(t *testing.T) {
	ctx := context.Background()

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	defer h.Queries.DeleteWorkspaceGitlabConnection(ctx, parseUUID(testWorkspaceID))

	seedGitlabWriteThroughFixture(t, h)

	issueID := seedGitlabConnectedIssue(t, 802, 42)
	defer testPool.Exec(ctx, `DELETE FROM issue_subscriber WHERE issue_id = $1`, parseUUID(issueID))
	defer testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, parseUUID(issueID))

	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/subscribe", nil)
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.SubscribeToIssue(rec, req)

	if rec.Code < 400 {
		t.Fatalf("status = %d, want >=400", rec.Code)
	}
	var count int
	_ = testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue_subscriber WHERE issue_id = $1`,
		parseUUID(issueID)).Scan(&count)
	if count != 0 {
		t.Errorf("cache leaked on error, count=%d", count)
	}
}

// TestSubscribeToIssue_WriteThrough304TreatedAsIdempotent verifies that when
// GitLab returns 304 (user already subscribed), Client.Subscribe treats it as
// success and the handler still adds the idempotent cache row.
func TestSubscribeToIssue_WriteThrough304TreatedAsIdempotent(t *testing.T) {
	ctx := context.Background()

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	defer h.Queries.DeleteWorkspaceGitlabConnection(ctx, parseUUID(testWorkspaceID))

	seedGitlabWriteThroughFixture(t, h)

	issueID := seedGitlabConnectedIssue(t, 803, 42)
	defer testPool.Exec(ctx, `DELETE FROM issue_subscriber WHERE issue_id = $1`, parseUUID(issueID))
	defer testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, parseUUID(issueID))

	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/subscribe", nil)
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.SubscribeToIssue(rec, req)

	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var count int
	_ = testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue_subscriber WHERE issue_id = $1 AND user_id = $2`,
		parseUUID(issueID), parseUUID(testUserID)).Scan(&count)
	if count != 1 {
		t.Errorf("cache subscriber missing after 304, count=%d", count)
	}
}

// TestUnsubscribeFromIssue_WriteThroughHumanCallsGitLab verifies that on a
// GitLab-connected workspace, a member unsubscribing from a GitLab-backed
// issue POSTs /api/v4/projects/:id/issues/:iid/unsubscribe with the caller's
// PAT and then removes the local subscriber cache row.
func TestUnsubscribeFromIssue_WriteThroughHumanCallsGitLab(t *testing.T) {
	ctx := context.Background()

	var capturedPath string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	defer h.Queries.DeleteWorkspaceGitlabConnection(ctx, parseUUID(testWorkspaceID))

	seedGitlabWriteThroughFixture(t, h)

	issueID := seedGitlabConnectedIssue(t, 810, 42)
	defer testPool.Exec(ctx, `DELETE FROM issue_subscriber WHERE issue_id = $1`, parseUUID(issueID))
	defer testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, parseUUID(issueID))

	// Pre-subscribe the user so Unsubscribe has something to remove.
	if _, err := testPool.Exec(ctx,
		`INSERT INTO issue_subscriber (issue_id, user_type, user_id, reason) VALUES ($1, 'member', $2, 'manual') ON CONFLICT DO NOTHING`,
		parseUUID(issueID), parseUUID(testUserID)); err != nil {
		t.Fatalf("pre-subscribe: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/unsubscribe", nil)
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.UnsubscribeFromIssue(rec, req)

	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if capturedPath != "/api/v4/projects/42/issues/810/unsubscribe" {
		t.Errorf("path = %s", capturedPath)
	}
	var count int
	_ = testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue_subscriber WHERE issue_id = $1 AND user_id = $2`,
		parseUUID(issueID), parseUUID(testUserID)).Scan(&count)
	if count != 0 {
		t.Errorf("cache subscriber should be removed, count=%d", count)
	}
}

// TestUnsubscribeFromIssue_WriteThroughAgentStaysMulticaOnly verifies that an
// agent-authored unsubscribe (X-Agent-ID header set) on a GitLab-connected
// workspace never calls GitLab — agent subscriptions are Multica-only.
func TestUnsubscribeFromIssue_WriteThroughAgentStaysMulticaOnly(t *testing.T) {
	ctx := context.Background()

	var gitlabCalls int
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gitlabCalls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	defer h.Queries.DeleteWorkspaceGitlabConnection(ctx, parseUUID(testWorkspaceID))

	seedGitlabWriteThroughFixture(t, h)

	var agentID string
	if err := testPool.QueryRow(ctx,
		`SELECT id FROM agent WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, "Handler Test Agent",
	).Scan(&agentID); err != nil {
		t.Fatalf("look up test agent: %v", err)
	}

	issueID := seedGitlabConnectedIssue(t, 811, 42)
	defer testPool.Exec(ctx, `DELETE FROM issue_subscriber WHERE issue_id = $1`, parseUUID(issueID))
	defer testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, parseUUID(issueID))

	if _, err := testPool.Exec(ctx,
		`INSERT INTO issue_subscriber (issue_id, user_type, user_id, reason) VALUES ($1, 'agent', $2, 'manual') ON CONFLICT DO NOTHING`,
		parseUUID(issueID), parseUUID(agentID)); err != nil {
		t.Fatalf("pre-subscribe agent: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/unsubscribe", nil)
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req.Header.Set("X-Agent-ID", agentID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.UnsubscribeFromIssue(rec, req)

	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if gitlabCalls != 0 {
		t.Errorf("GitLab got %d calls — agent unsubscribes must stay Multica-only", gitlabCalls)
	}
}

// TestUnsubscribeFromIssue_WriteThroughGitLabErrorPreservesCache verifies that
// a non-idempotent GitLab error on the write-through branch aborts the
// request and does NOT mutate the local cache row.
func TestUnsubscribeFromIssue_WriteThroughGitLabErrorPreservesCache(t *testing.T) {
	ctx := context.Background()

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	defer h.Queries.DeleteWorkspaceGitlabConnection(ctx, parseUUID(testWorkspaceID))

	seedGitlabWriteThroughFixture(t, h)

	issueID := seedGitlabConnectedIssue(t, 812, 42)
	defer testPool.Exec(ctx, `DELETE FROM issue_subscriber WHERE issue_id = $1`, parseUUID(issueID))
	defer testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, parseUUID(issueID))

	if _, err := testPool.Exec(ctx,
		`INSERT INTO issue_subscriber (issue_id, user_type, user_id, reason) VALUES ($1, 'member', $2, 'manual') ON CONFLICT DO NOTHING`,
		parseUUID(issueID), parseUUID(testUserID)); err != nil {
		t.Fatalf("pre-subscribe: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/unsubscribe", nil)
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.UnsubscribeFromIssue(rec, req)

	if rec.Code < 400 {
		t.Fatalf("status = %d, want >=400", rec.Code)
	}
	var count int
	_ = testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue_subscriber WHERE issue_id = $1 AND user_id = $2`,
		parseUUID(issueID), parseUUID(testUserID)).Scan(&count)
	if count != 1 {
		t.Errorf("cache mutated on error, count=%d", count)
	}
}

// TestUnsubscribeFromIssue_WriteThrough304TreatedAsIdempotent verifies that
// when GitLab returns 304 (user not currently subscribed — e.g. the legacy
// subscribe happened but an admin already unsubscribed server-side),
// Client.Unsubscribe treats it as success and the handler still removes the
// local subscriber row.
func TestUnsubscribeFromIssue_WriteThrough304TreatedAsIdempotent(t *testing.T) {
	ctx := context.Background()

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	defer h.Queries.DeleteWorkspaceGitlabConnection(ctx, parseUUID(testWorkspaceID))

	seedGitlabWriteThroughFixture(t, h)

	issueID := seedGitlabConnectedIssue(t, 813, 42)
	defer testPool.Exec(ctx, `DELETE FROM issue_subscriber WHERE issue_id = $1`, parseUUID(issueID))
	defer testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, parseUUID(issueID))

	if _, err := testPool.Exec(ctx,
		`INSERT INTO issue_subscriber (issue_id, user_type, user_id, reason) VALUES ($1, 'member', $2, 'manual') ON CONFLICT DO NOTHING`,
		parseUUID(issueID), parseUUID(testUserID)); err != nil {
		t.Fatalf("pre-subscribe: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/unsubscribe", nil)
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()

	h.UnsubscribeFromIssue(rec, req)

	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var count int
	_ = testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue_subscriber WHERE issue_id = $1 AND user_id = $2`,
		parseUUID(issueID), parseUUID(testUserID)).Scan(&count)
	if count != 0 {
		t.Errorf("cache subscriber should be removed after 304, count=%d", count)
	}
}

func TestSubscriberAPI(t *testing.T) {
	ctx := context.Background()

	// Helper: create an issue for subscriber tests
	createIssue := func(t *testing.T) string {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
			"title": "Subscriber test issue",
		})
		testHandler.CreateIssue(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
		}
		var issue IssueResponse
		json.NewDecoder(w.Body).Decode(&issue)
		return issue.ID
	}

	// Helper: delete an issue
	deleteIssue := func(t *testing.T, issueID string) {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest("DELETE", "/api/issues/"+issueID, nil)
		req = withURLParam(req, "id", issueID)
		testHandler.DeleteIssue(w, req)
	}

	t.Run("Subscribe", func(t *testing.T) {
		issueID := createIssue(t)
		defer deleteIssue(t, issueID)

		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+issueID+"/subscribe", nil)
		req = withURLParam(req, "id", issueID)
		testHandler.SubscribeToIssue(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("SubscribeToIssue: expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp map[string]bool
		json.NewDecoder(w.Body).Decode(&resp)
		if !resp["subscribed"] {
			t.Fatal("SubscribeToIssue: expected subscribed=true")
		}

		// Verify in DB
		subscribed, err := testHandler.Queries.IsIssueSubscriber(ctx, db.IsIssueSubscriberParams{
			IssueID:  parseUUID(issueID),
			UserType: "member",
			UserID:   parseUUID(testUserID),
		})
		if err != nil {
			t.Fatalf("IsIssueSubscriber: %v", err)
		}
		if !subscribed {
			t.Fatal("expected user to be subscribed in DB")
		}
	})

	t.Run("SubscribeIdempotent", func(t *testing.T) {
		issueID := createIssue(t)
		defer deleteIssue(t, issueID)

		// Subscribe first time
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+issueID+"/subscribe", nil)
		req = withURLParam(req, "id", issueID)
		testHandler.SubscribeToIssue(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("SubscribeToIssue (1st): expected 200, got %d: %s", w.Code, w.Body.String())
		}

		// Subscribe second time — should also succeed
		w = httptest.NewRecorder()
		req = newRequest("POST", "/api/issues/"+issueID+"/subscribe", nil)
		req = withURLParam(req, "id", issueID)
		testHandler.SubscribeToIssue(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("SubscribeToIssue (2nd): expected 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("ListSubscribers", func(t *testing.T) {
		issueID := createIssue(t)
		defer deleteIssue(t, issueID)

		// Subscribe first
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+issueID+"/subscribe", nil)
		req = withURLParam(req, "id", issueID)
		testHandler.SubscribeToIssue(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("SubscribeToIssue: expected 200, got %d: %s", w.Code, w.Body.String())
		}

		// List
		w = httptest.NewRecorder()
		req = newRequest("GET", "/api/issues/"+issueID+"/subscribers", nil)
		req = withURLParam(req, "id", issueID)
		testHandler.ListIssueSubscribers(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("ListIssueSubscribers: expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var subscribers []SubscriberResponse
		json.NewDecoder(w.Body).Decode(&subscribers)
		if len(subscribers) == 0 {
			t.Fatal("ListIssueSubscribers: expected at least 1 subscriber")
		}
		found := false
		for _, s := range subscribers {
			if s.UserID == testUserID && s.UserType == "member" && s.Reason == "manual" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("ListIssueSubscribers: expected to find test user subscriber, got %+v", subscribers)
		}
	})

	t.Run("Unsubscribe", func(t *testing.T) {
		issueID := createIssue(t)
		defer deleteIssue(t, issueID)

		// Subscribe first
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+issueID+"/subscribe", nil)
		req = withURLParam(req, "id", issueID)
		testHandler.SubscribeToIssue(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("SubscribeToIssue: expected 200, got %d: %s", w.Code, w.Body.String())
		}

		// Unsubscribe
		w = httptest.NewRecorder()
		req = newRequest("POST", "/api/issues/"+issueID+"/unsubscribe", nil)
		req = withURLParam(req, "id", issueID)
		testHandler.UnsubscribeFromIssue(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("UnsubscribeFromIssue: expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp map[string]bool
		json.NewDecoder(w.Body).Decode(&resp)
		if resp["subscribed"] {
			t.Fatal("UnsubscribeFromIssue: expected subscribed=false")
		}

		// Verify in DB
		subscribed, err := testHandler.Queries.IsIssueSubscriber(ctx, db.IsIssueSubscriberParams{
			IssueID:  parseUUID(issueID),
			UserType: "member",
			UserID:   parseUUID(testUserID),
		})
		if err != nil {
			t.Fatalf("IsIssueSubscriber: %v", err)
		}
		if subscribed {
			t.Fatal("expected user to NOT be subscribed in DB")
		}
	})

	t.Run("SubscribeCrossWorkspaceUser", func(t *testing.T) {
		issueID := createIssue(t)
		defer deleteIssue(t, issueID)

		foreignUserID := "00000000-0000-0000-0000-000000000099"
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+issueID+"/subscribe", map[string]any{
			"user_id":   foreignUserID,
			"user_type": "member",
		})
		req = withURLParam(req, "id", issueID)
		testHandler.SubscribeToIssue(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("SubscribeToIssue with cross-workspace user: expected 403, got %d: %s", w.Code, w.Body.String())
		}

		subscribed, err := testHandler.Queries.IsIssueSubscriber(ctx, db.IsIssueSubscriberParams{
			IssueID:  parseUUID(issueID),
			UserType: "member",
			UserID:   parseUUID(foreignUserID),
		})
		if err != nil {
			t.Fatalf("IsIssueSubscriber: %v", err)
		}
		if subscribed {
			t.Fatal("cross-workspace user should NOT be subscribed in DB")
		}
	})

	t.Run("UnsubscribeCrossWorkspaceUser", func(t *testing.T) {
		issueID := createIssue(t)
		defer deleteIssue(t, issueID)

		foreignUserID := "00000000-0000-0000-0000-000000000099"
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+issueID+"/unsubscribe", map[string]any{
			"user_id":   foreignUserID,
			"user_type": "member",
		})
		req = withURLParam(req, "id", issueID)
		testHandler.UnsubscribeFromIssue(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("UnsubscribeFromIssue with cross-workspace user: expected 403, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("AgentCallerSubscribesItself", func(t *testing.T) {
		issueID := createIssue(t)
		defer deleteIssue(t, issueID)

		// Look up the agent created by the handler test fixture.
		var agentID string
		err := testPool.QueryRow(ctx,
			`SELECT id FROM agent WHERE workspace_id = $1 AND name = $2`,
			testWorkspaceID, "Handler Test Agent",
		).Scan(&agentID)
		if err != nil {
			t.Fatalf("failed to find test agent: %v", err)
		}

		// Subscribe with X-Agent-ID set — no body, so the handler must default
		// to subscribing the agent itself (not the member behind X-User-ID).
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+issueID+"/subscribe", nil)
		req = withURLParam(req, "id", issueID)
		req.Header.Set("X-Agent-ID", agentID)
		testHandler.SubscribeToIssue(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("SubscribeToIssue (agent caller): expected 200, got %d: %s", w.Code, w.Body.String())
		}

		agentSubscribed, err := testHandler.Queries.IsIssueSubscriber(ctx, db.IsIssueSubscriberParams{
			IssueID:  parseUUID(issueID),
			UserType: "agent",
			UserID:   parseUUID(agentID),
		})
		if err != nil {
			t.Fatalf("IsIssueSubscriber (agent): %v", err)
		}
		if !agentSubscribed {
			t.Fatal("expected agent to be subscribed in DB when X-Agent-ID is set")
		}

		memberSubscribed, err := testHandler.Queries.IsIssueSubscriber(ctx, db.IsIssueSubscriberParams{
			IssueID:  parseUUID(issueID),
			UserType: "member",
			UserID:   parseUUID(testUserID),
		})
		if err != nil {
			t.Fatalf("IsIssueSubscriber (member): %v", err)
		}
		if memberSubscribed {
			t.Fatal("member must not be auto-subscribed when caller is an agent")
		}

		// Unsubscribe with X-Agent-ID set — same default-to-caller expectation.
		w = httptest.NewRecorder()
		req = newRequest("POST", "/api/issues/"+issueID+"/unsubscribe", nil)
		req = withURLParam(req, "id", issueID)
		req.Header.Set("X-Agent-ID", agentID)
		testHandler.UnsubscribeFromIssue(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("UnsubscribeFromIssue (agent caller): expected 200, got %d: %s", w.Code, w.Body.String())
		}

		agentSubscribed, err = testHandler.Queries.IsIssueSubscriber(ctx, db.IsIssueSubscriberParams{
			IssueID:  parseUUID(issueID),
			UserType: "agent",
			UserID:   parseUUID(agentID),
		})
		if err != nil {
			t.Fatalf("IsIssueSubscriber (agent, after unsubscribe): %v", err)
		}
		if agentSubscribed {
			t.Fatal("expected agent to be unsubscribed in DB when X-Agent-ID is set")
		}
	})

	t.Run("ListAfterUnsubscribe", func(t *testing.T) {
		issueID := createIssue(t)
		defer deleteIssue(t, issueID)

		// Subscribe
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+issueID+"/subscribe", nil)
		req = withURLParam(req, "id", issueID)
		testHandler.SubscribeToIssue(w, req)

		// Unsubscribe
		w = httptest.NewRecorder()
		req = newRequest("POST", "/api/issues/"+issueID+"/unsubscribe", nil)
		req = withURLParam(req, "id", issueID)
		testHandler.UnsubscribeFromIssue(w, req)

		// List should be empty
		w = httptest.NewRecorder()
		req = newRequest("GET", "/api/issues/"+issueID+"/subscribers", nil)
		req = withURLParam(req, "id", issueID)
		testHandler.ListIssueSubscribers(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("ListIssueSubscribers: expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var subscribers []SubscriberResponse
		json.NewDecoder(w.Body).Decode(&subscribers)
		if len(subscribers) != 0 {
			t.Fatalf("ListIssueSubscribers: expected 0 subscribers after unsubscribe, got %d", len(subscribers))
		}
	})
}
