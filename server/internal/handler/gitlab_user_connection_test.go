package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// seedWorkspaceGitlabConnection inserts a minimal workspace_gitlab_connection
// row so that handlers gated on "workspace is GitLab-connected" can run. Most
// per-user PAT tests don't care about the service-level connection details, so
// we keep the fixture small and register cleanup via t.Cleanup.
func seedWorkspaceGitlabConnection(t *testing.T, h *Handler) {
	t.Helper()
	ctx := context.Background()
	if err := h.Queries.DeleteWorkspaceGitlabConnection(ctx, parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("clean workspace_gitlab_connection: %v", err)
	}
	if _, err := h.Queries.CreateWorkspaceGitlabConnection(ctx, db.CreateWorkspaceGitlabConnectionParams{
		WorkspaceID:           parseUUID(testWorkspaceID),
		GitlabProjectID:       42,
		GitlabProjectPath:     "team/app",
		ServiceTokenEncrypted: []byte("x"),
		ServiceTokenUserID:    1,
		ConnectionStatus:      "connected",
	}); err != nil {
		t.Fatalf("seed workspace_gitlab_connection: %v", err)
	}
	t.Cleanup(func() {
		h.Queries.DeleteWorkspaceGitlabConnection(context.Background(), parseUUID(testWorkspaceID))
	})
}

func TestConnectUserGitlab_Success(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/user" {
			t.Errorf("path = %s, want /api/v4/user", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": 999, "username": "alice", "name": "Alice"}`))
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	seedWorkspaceGitlabConnection(t, h)
	defer h.Queries.DeleteUserGitlabConnection(context.Background(), db.DeleteUserGitlabConnectionParams{
		UserID:      parseUUID(testUserID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})

	body, _ := json.Marshal(map[string]string{"token": "glpat-user-abc"})
	req := httptest.NewRequest(http.MethodPost, "/api/me/gitlab/connect", bytes.NewReader(body))
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rr := httptest.NewRecorder()

	h.ConnectUserGitlab(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got["gitlab_username"] != "alice" {
		t.Errorf("gitlab_username = %v", got["gitlab_username"])
	}
	if _, hasTok := got["pat_encrypted"]; hasTok {
		t.Errorf("response leaks pat_encrypted: %+v", got)
	}
}

func TestConnectUserGitlab_BadToken(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"401 Unauthorized"}`))
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	seedWorkspaceGitlabConnection(t, h)

	body, _ := json.Marshal(map[string]string{"token": "bad"})
	req := httptest.NewRequest(http.MethodPost, "/api/me/gitlab/connect", bytes.NewReader(body))
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rr := httptest.NewRecorder()

	h.ConnectUserGitlab(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestGetUserGitlabConnection_Connected(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": 999, "username": "alice"}`))
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	seedWorkspaceGitlabConnection(t, h)
	defer h.Queries.DeleteUserGitlabConnection(context.Background(), db.DeleteUserGitlabConnectionParams{
		UserID:      parseUUID(testUserID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})

	// Seed via the connect handler.
	body, _ := json.Marshal(map[string]string{"token": "glpat-x"})
	connReq := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	connReq.Header.Set("X-User-ID", testUserID)
	connReq.Header.Set("X-Workspace-ID", testWorkspaceID)
	h.ConnectUserGitlab(httptest.NewRecorder(), connReq)

	req := httptest.NewRequest(http.MethodGet, "/api/me/gitlab/connect", nil)
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rr := httptest.NewRecorder()
	h.GetUserGitlabConnection(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var got map[string]any
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got["connected"] != true || got["gitlab_username"] != "alice" {
		t.Errorf("got %+v", got)
	}
}

func TestGetUserGitlabConnection_NotConnected(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	h.Queries.DeleteUserGitlabConnection(context.Background(), db.DeleteUserGitlabConnectionParams{
		UserID:      parseUUID(testUserID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/me/gitlab/connect", nil)
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rr := httptest.NewRecorder()
	h.GetUserGitlabConnection(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var got map[string]any
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got["connected"] != false {
		t.Errorf("connected = %v, want false", got["connected"])
	}
}

// M1: registering a personal PAT on a workspace that isn't connected to GitLab
// must be rejected. Otherwise the PAT gets encrypted and stored as dead weight
// (the write-through resolver short-circuits on no workspace connection).
func TestConnectUserGitlab_RejectsWhenWorkspaceNotConnected(t *testing.T) {
	var calls atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": 999, "username": "alice"}`))
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	// Make sure no workspace_gitlab_connection row exists for this workspace.
	if err := h.Queries.DeleteWorkspaceGitlabConnection(context.Background(), parseUUID(testWorkspaceID)); err != nil {
		t.Fatalf("clean workspace_gitlab_connection: %v", err)
	}
	// Defensive: ensure no stale user row either, and clean up after.
	h.Queries.DeleteUserGitlabConnection(context.Background(), db.DeleteUserGitlabConnectionParams{
		UserID:      parseUUID(testUserID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	t.Cleanup(func() {
		h.Queries.DeleteUserGitlabConnection(context.Background(), db.DeleteUserGitlabConnectionParams{
			UserID:      parseUUID(testUserID),
			WorkspaceID: parseUUID(testWorkspaceID),
		})
	})

	body, _ := json.Marshal(map[string]string{"token": "glpat-user-abc"})
	req := httptest.NewRequest(http.MethodPost, "/api/me/gitlab/connect", bytes.NewReader(body))
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rr := httptest.NewRecorder()

	h.ConnectUserGitlab(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rr.Code, rr.Body.String())
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("fake GitLab was called %d times; want 0 (token validation must short-circuit before calling /user)", n)
	}

	// Must not have persisted a user_gitlab_connection row.
	_, err := h.Queries.GetUserGitlabConnection(context.Background(), db.GetUserGitlabConnectionParams{
		UserID:      parseUUID(testUserID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err == nil {
		t.Errorf("user_gitlab_connection row was inserted despite 409 response")
	}
}

func TestDisconnectUserGitlab_Success(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": 999, "username": "alice"}`))
	}))
	defer fake.Close()

	h := buildHandlerWithGitlab(t, fake.URL)
	seedWorkspaceGitlabConnection(t, h)
	body, _ := json.Marshal(map[string]string{"token": "glpat-x"})
	connReq := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	connReq.Header.Set("X-User-ID", testUserID)
	connReq.Header.Set("X-Workspace-ID", testWorkspaceID)
	h.ConnectUserGitlab(httptest.NewRecorder(), connReq)

	delReq := httptest.NewRequest(http.MethodDelete, "/", nil)
	delReq.Header.Set("X-User-ID", testUserID)
	delReq.Header.Set("X-Workspace-ID", testWorkspaceID)
	rr := httptest.NewRecorder()
	h.DisconnectUserGitlab(rr, delReq)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rr.Code)
	}

	// GET should now show disconnected.
	getReq := httptest.NewRequest(http.MethodGet, "/", nil)
	getReq.Header.Set("X-User-ID", testUserID)
	getReq.Header.Set("X-Workspace-ID", testWorkspaceID)
	getRr := httptest.NewRecorder()
	h.GetUserGitlabConnection(getRr, getReq)
	var got map[string]any
	json.Unmarshal(getRr.Body.Bytes(), &got)
	if got["connected"] != false {
		t.Errorf("connected = %v after delete, want false", got["connected"])
	}
}
