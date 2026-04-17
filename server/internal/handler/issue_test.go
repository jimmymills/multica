package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	gitlabsync "github.com/multica-ai/multica/server/internal/gitlab"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestCreateIssue_WriteThroughHumanWithoutPATUsesServicePAT(t *testing.T) {
	var capturedToken string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v4/projects/42/issues":
			if r.Method == http.MethodPost {
				capturedToken = r.Header.Get("PRIVATE-TOKEN")
				w.Write([]byte(`{"id":9901,"iid":99,"title":"From Multica","state":"opened",
					"labels":["status::todo","priority::medium"],"updated_at":"2026-04-17T15:00:00Z"}`))
				return
			}
		}
		w.Write([]byte(`{}`))
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	defer h.Queries.DeleteWorkspaceGitlabConnection(context.Background(), parseUUID(testWorkspaceID))
	defer testPool.Exec(context.Background(), `DELETE FROM issue WHERE workspace_id = $1::uuid AND gitlab_iid = 99`, testWorkspaceID)

	// Seed a workspace_gitlab_connection so the handler takes write-through.
	encrypted, _ := h.Secrets.Encrypt([]byte("svc-token-xyz"))
	testPool.Exec(context.Background(), `
		INSERT INTO workspace_gitlab_connection (
			workspace_id, gitlab_project_id, gitlab_project_path,
			service_token_encrypted, service_token_user_id, connection_status
		) VALUES ($1, 42, 'g/a', $2, 1, 'connected')
		ON CONFLICT (workspace_id) DO UPDATE SET
			gitlab_project_id = EXCLUDED.gitlab_project_id,
			service_token_encrypted = EXCLUDED.service_token_encrypted,
			service_token_user_id = EXCLUDED.service_token_user_id
	`, testWorkspaceID, encrypted)

	// Wire a real resolver on the handler so the write-through branch works.
	h.SetGitlabResolver(gitlabsync.NewResolver(h.Queries, func(_ context.Context, b []byte) (string, error) {
		plain, err := h.Secrets.Decrypt(b)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	}))

	body, _ := json.Marshal(map[string]any{
		"title":    "From Multica",
		"status":   "todo",
		"priority": "medium",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rr := httptest.NewRecorder()

	h.CreateIssue(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if capturedToken != "svc-token-xyz" {
		t.Errorf("PRIVATE-TOKEN sent to gitlab = %q, want svc-token-xyz (service PAT)", capturedToken)
	}

	// Verify the cache row exists with the GitLab IID.
	var iid int
	testPool.QueryRow(context.Background(),
		`SELECT gitlab_iid FROM issue WHERE workspace_id = $1::uuid AND title = 'From Multica'`,
		testWorkspaceID).Scan(&iid)
	if iid != 99 {
		t.Errorf("cached gitlab_iid = %d, want 99", iid)
	}
}

func TestCreateIssue_WriteThroughHumanWithPATUsesUserPAT(t *testing.T) {
	var capturedToken string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v4/user":
			w.Write([]byte(`{"id":555,"username":"alice"}`))
		case "/api/v4/projects/42/issues":
			if r.Method == http.MethodPost {
				capturedToken = r.Header.Get("PRIVATE-TOKEN")
				w.Write([]byte(`{"id":9902,"iid":100,"title":"From Alice","state":"opened",
					"labels":["status::todo","priority::medium"],"updated_at":"2026-04-17T15:00:00Z"}`))
				return
			}
		}
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	defer h.Queries.DeleteWorkspaceGitlabConnection(context.Background(), parseUUID(testWorkspaceID))
	defer testPool.Exec(context.Background(), `DELETE FROM issue WHERE workspace_id = $1::uuid AND gitlab_iid = 100`, testWorkspaceID)
	defer h.Queries.DeleteUserGitlabConnection(context.Background(), db.DeleteUserGitlabConnectionParams{
		UserID:      parseUUID(testUserID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})

	svcEnc, _ := h.Secrets.Encrypt([]byte("svc-token"))
	usrEnc, _ := h.Secrets.Encrypt([]byte("user-token-alice"))
	testPool.Exec(context.Background(), `
		INSERT INTO workspace_gitlab_connection (
			workspace_id, gitlab_project_id, gitlab_project_path,
			service_token_encrypted, service_token_user_id, connection_status
		) VALUES ($1, 42, 'g/a', $2, 1, 'connected')
		ON CONFLICT (workspace_id) DO UPDATE SET
			service_token_encrypted = EXCLUDED.service_token_encrypted
	`, testWorkspaceID, svcEnc)
	h.Queries.UpsertUserGitlabConnection(context.Background(), db.UpsertUserGitlabConnectionParams{
		UserID:         parseUUID(testUserID),
		WorkspaceID:    parseUUID(testWorkspaceID),
		GitlabUserID:   555,
		GitlabUsername: "alice",
		PatEncrypted:   usrEnc,
	})

	h.SetGitlabResolver(gitlabsync.NewResolver(h.Queries, func(_ context.Context, b []byte) (string, error) {
		plain, err := h.Secrets.Decrypt(b)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	}))

	body, _ := json.Marshal(map[string]any{"title": "From Alice", "status": "todo", "priority": "medium"})
	req := httptest.NewRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rr := httptest.NewRecorder()

	h.CreateIssue(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d", rr.Code)
	}
	if capturedToken != "user-token-alice" {
		t.Errorf("PRIVATE-TOKEN = %q, want user-token-alice", capturedToken)
	}
}

// seedGitlabWriteThroughFixture prepares a workspace_gitlab_connection row and
// attaches a real resolver to the handler so CreateIssue takes the
// write-through branch. Shared by the parent/project/attachments blocker tests.
func seedGitlabWriteThroughFixture(t *testing.T, h *Handler) {
	t.Helper()
	encrypted, _ := h.Secrets.Encrypt([]byte("svc-token-xyz"))
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO workspace_gitlab_connection (
			workspace_id, gitlab_project_id, gitlab_project_path,
			service_token_encrypted, service_token_user_id, connection_status
		) VALUES ($1, 42, 'g/a', $2, 1, 'connected')
		ON CONFLICT (workspace_id) DO UPDATE SET
			gitlab_project_id = EXCLUDED.gitlab_project_id,
			service_token_encrypted = EXCLUDED.service_token_encrypted,
			service_token_user_id = EXCLUDED.service_token_user_id
	`, testWorkspaceID, encrypted); err != nil {
		t.Fatalf("seed workspace_gitlab_connection: %v", err)
	}
	h.SetGitlabResolver(gitlabsync.NewResolver(h.Queries, func(_ context.Context, b []byte) (string, error) {
		plain, err := h.Secrets.Decrypt(b)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	}))
}

func TestCreateIssue_WriteThroughThreadsParentIssueID(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v4/projects/42/issues" && r.Method == http.MethodPost {
			w.Write([]byte(`{"id":9910,"iid":110,"title":"Sub-issue","state":"opened",
				"labels":["status::todo","priority::medium"],"updated_at":"2026-04-17T15:00:00Z"}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	defer h.Queries.DeleteWorkspaceGitlabConnection(context.Background(), parseUUID(testWorkspaceID))
	defer testPool.Exec(context.Background(), `DELETE FROM issue WHERE workspace_id = $1::uuid AND gitlab_iid IN (110)`, testWorkspaceID)

	// Seed a native parent issue in the same workspace.
	var parentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'blocker-parent', 'todo', 'none', $2, 'member', 9001, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&parentID); err != nil {
		t.Fatalf("seed parent issue: %v", err)
	}
	defer testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, parentID)

	seedGitlabWriteThroughFixture(t, h)

	body, _ := json.Marshal(map[string]any{
		"title":           "Sub-issue",
		"status":          "todo",
		"priority":        "medium",
		"parent_issue_id": parentID,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rr := httptest.NewRecorder()

	h.CreateIssue(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	// The cache row must have parent_issue_id set to the pre-seeded parent.
	var gotParent string
	if err := testPool.QueryRow(context.Background(),
		`SELECT parent_issue_id FROM issue WHERE workspace_id = $1::uuid AND gitlab_iid = 110`,
		testWorkspaceID).Scan(&gotParent); err != nil {
		t.Fatalf("query cache row parent_issue_id: %v", err)
	}
	if gotParent != parentID {
		t.Errorf("cache row parent_issue_id = %q, want %q", gotParent, parentID)
	}
}

func TestCreateIssue_WriteThroughThreadsProjectID(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v4/projects/42/issues" && r.Method == http.MethodPost {
			w.Write([]byte(`{"id":9911,"iid":111,"title":"Issue with project","state":"opened",
				"labels":["status::todo","priority::medium"],"updated_at":"2026-04-17T15:00:00Z"}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	defer h.Queries.DeleteWorkspaceGitlabConnection(context.Background(), parseUUID(testWorkspaceID))
	defer testPool.Exec(context.Background(), `DELETE FROM issue WHERE workspace_id = $1::uuid AND gitlab_iid = 111`, testWorkspaceID)

	// Seed a native project in the same workspace.
	var projectID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO project (workspace_id, title)
		VALUES ($1, 'blocker-project')
		RETURNING id
	`, testWorkspaceID).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	defer testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)

	seedGitlabWriteThroughFixture(t, h)

	body, _ := json.Marshal(map[string]any{
		"title":      "Issue with project",
		"status":     "todo",
		"priority":   "medium",
		"project_id": projectID,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rr := httptest.NewRecorder()

	h.CreateIssue(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var gotProject string
	if err := testPool.QueryRow(context.Background(),
		`SELECT project_id FROM issue WHERE workspace_id = $1::uuid AND gitlab_iid = 111`,
		testWorkspaceID).Scan(&gotProject); err != nil {
		t.Fatalf("query cache row project_id: %v", err)
	}
	if gotProject != projectID {
		t.Errorf("cache row project_id = %q, want %q", gotProject, projectID)
	}
}

func TestCreateIssue_WriteThroughLinksAttachments(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v4/projects/42/issues" && r.Method == http.MethodPost {
			w.Write([]byte(`{"id":9912,"iid":112,"title":"Issue with attachment","state":"opened",
				"labels":["status::todo","priority::medium"],"updated_at":"2026-04-17T15:00:00Z"}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	defer h.Queries.DeleteWorkspaceGitlabConnection(context.Background(), parseUUID(testWorkspaceID))
	defer testPool.Exec(context.Background(), `DELETE FROM issue WHERE workspace_id = $1::uuid AND gitlab_iid = 112`, testWorkspaceID)

	// Pre-upload an unattached attachment (issue_id IS NULL).
	attachmentUUID := uuid.New().String()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO attachment (id, workspace_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1::uuid, $2, 'member', $3, 'note.txt', 'https://cdn.example.com/note.txt', 'text/plain', 11)
	`, attachmentUUID, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("seed attachment: %v", err)
	}
	defer testPool.Exec(context.Background(), `DELETE FROM attachment WHERE id = $1::uuid`, attachmentUUID)

	seedGitlabWriteThroughFixture(t, h)

	body, _ := json.Marshal(map[string]any{
		"title":          "Issue with attachment",
		"status":         "todo",
		"priority":       "medium",
		"attachment_ids": []string{attachmentUUID},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rr := httptest.NewRecorder()

	h.CreateIssue(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	// Fetch the newly created cache row id.
	var issueID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM issue WHERE workspace_id = $1::uuid AND gitlab_iid = 112`,
		testWorkspaceID).Scan(&issueID); err != nil {
		t.Fatalf("query cache row id: %v", err)
	}

	// The attachment must now point at the new issue.
	var linkedIssueID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT issue_id FROM attachment WHERE id = $1::uuid`,
		attachmentUUID).Scan(&linkedIssueID); err != nil {
		t.Fatalf("query attachment issue_id: %v", err)
	}
	if linkedIssueID != issueID {
		t.Errorf("attachment issue_id = %q, want %q", linkedIssueID, issueID)
	}
}

// TestCreateIssue_WriteThroughEnqueuesAgentTask verifies that creating an
// agent-assigned issue on a GitLab-connected workspace enqueues an agent task
// — matching the legacy path's behaviour. The write-through branch must not
// silently swallow this side-effect.
func TestCreateIssue_WriteThroughEnqueuesAgentTask(t *testing.T) {
	ctx := context.Background()

	// Look up the seeded test agent. Its slug (lowercased name with hyphens)
	// determines the agent::<slug> label the fake GitLab server must echo back.
	var agentID string
	if err := testPool.QueryRow(ctx,
		`SELECT id FROM agent WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, "Handler Test Agent",
	).Scan(&agentID); err != nil {
		t.Fatalf("look up test agent: %v", err)
	}

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v4/projects/42/issues" && r.Method == http.MethodPost {
			// Echo back the agent::handler-test-agent label so TranslateIssue
			// resolves the agent assignee on the cache row.
			w.Write([]byte(`{"id":9913,"iid":113,"title":"Agent-assigned","state":"opened",
				"labels":["status::todo","priority::medium","agent::handler-test-agent"],
				"updated_at":"2026-04-17T15:00:00Z"}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	defer h.Queries.DeleteWorkspaceGitlabConnection(context.Background(), parseUUID(testWorkspaceID))
	defer testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE agent_id = $1::uuid`, agentID)
	defer testPool.Exec(context.Background(), `DELETE FROM issue WHERE workspace_id = $1::uuid AND gitlab_iid = 113`, testWorkspaceID)

	seedGitlabWriteThroughFixture(t, h)

	body, _ := json.Marshal(map[string]any{
		"title":         "Agent-assigned",
		"status":        "todo",
		"priority":      "medium",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rr := httptest.NewRecorder()

	h.CreateIssue(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	// Grab the cache row — it must be persisted with the agent assignee.
	var issueID, gotAssigneeType, gotAssigneeID string
	if err := testPool.QueryRow(ctx,
		`SELECT id, assignee_type, assignee_id FROM issue WHERE workspace_id = $1::uuid AND gitlab_iid = 113`,
		testWorkspaceID).Scan(&issueID, &gotAssigneeType, &gotAssigneeID); err != nil {
		t.Fatalf("query cache row: %v", err)
	}
	if gotAssigneeType != "agent" || gotAssigneeID != agentID {
		t.Fatalf("cache row assignee = (%q, %q), want (agent, %q)", gotAssigneeType, gotAssigneeID, agentID)
	}

	// The write-through path must enqueue an agent task — same side effect
	// the legacy path produces at CreateIssue's tail.
	var taskCount int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1::uuid AND agent_id = $2::uuid AND status = 'queued'`,
		issueID, agentID,
	).Scan(&taskCount); err != nil {
		t.Fatalf("count queued tasks: %v", err)
	}
	if taskCount != 1 {
		t.Fatalf("expected 1 queued task for agent-assigned write-through issue, got %d", taskCount)
	}
}

func TestCreateIssue_LegacyPathWhenNoGitlabConnection(t *testing.T) {
	// No workspace_gitlab_connection row → handler takes the legacy direct-DB
	// path. (Same behaviour as pre-Phase-3a.)
	h := buildHandlerWithGitlab(t, "http://unused")
	h.Queries.DeleteWorkspaceGitlabConnection(context.Background(), parseUUID(testWorkspaceID))

	body, _ := json.Marshal(map[string]any{"title": "Legacy", "status": "todo", "priority": "medium"})
	req := httptest.NewRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rr := httptest.NewRecorder()

	h.CreateIssue(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}
