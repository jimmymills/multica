package gitlab

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeResolverQueries lets us simulate "found" / "not found" for both
// connection types without wiring a real DB.
type fakeResolverQueries struct {
	workspaceConn        *db.WorkspaceGitlabConnection
	userConn             *db.UserGitlabConnection
	userConnByGitlabUser *db.UserGitlabConnection
	projectMember        *db.GitlabProjectMember
}

func (f *fakeResolverQueries) GetWorkspaceGitlabConnection(_ context.Context, _ pgtype.UUID) (db.WorkspaceGitlabConnection, error) {
	if f.workspaceConn == nil {
		return db.WorkspaceGitlabConnection{}, pgx.ErrNoRows
	}
	return *f.workspaceConn, nil
}

func (f *fakeResolverQueries) GetUserGitlabConnection(_ context.Context, _ db.GetUserGitlabConnectionParams) (db.UserGitlabConnection, error) {
	if f.userConn == nil {
		return db.UserGitlabConnection{}, pgx.ErrNoRows
	}
	return *f.userConn, nil
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

// pgUUIDForTest parses a UUID string into pgtype.UUID for fake rows.
func pgUUIDForTest(s string) pgtype.UUID {
	var u pgtype.UUID
	_ = u.Scan(s)
	return u
}

// stubDecrypt returns the plaintext "{prefix}|{hex}" so tests can assert
// which encrypted column we resolved against.
func stubDecrypt(prefix string) TokenDecrypter {
	return func(_ context.Context, encrypted []byte) (string, error) {
		return prefix + "|" + string(encrypted), nil
	}
}

// Distinct workspace vs. user UUIDs so a future transposition bug in the
// resolver (passing the workspace UUID where the user UUID belongs, or vice
// versa) would surface rather than silently pass.
const (
	workspaceUUID = "00000000-0000-0000-0000-000000000001"
	userUUID      = "00000000-0000-0000-0000-000000000002"
	multicaUserID = "00000000-0000-0000-0000-000000000003"
	memberRowID   = "00000000-0000-0000-0000-000000000004"
)

func TestResolveTokenForWrite_HumanWithPATPicksUserPAT(t *testing.T) {
	q := &fakeResolverQueries{
		workspaceConn: &db.WorkspaceGitlabConnection{
			ServiceTokenEncrypted: []byte("svc"),
		},
		userConn: &db.UserGitlabConnection{
			PatEncrypted: []byte("usr"),
			GitlabUserID: 100,
		},
	}
	r := NewResolver(q, stubDecrypt("dec"))
	tok, src, err := r.ResolveTokenForWrite(context.Background(), workspaceUUID, "member", userUUID)
	if err != nil {
		t.Fatalf("ResolveTokenForWrite: %v", err)
	}
	if src != "user" {
		t.Errorf("source = %q, want user", src)
	}
	if tok != "dec|usr" {
		t.Errorf("token = %q, want dec|usr (the decrypted user PAT)", tok)
	}
}

func TestResolveTokenForWrite_HumanWithoutPATFallsBackToServicePAT(t *testing.T) {
	q := &fakeResolverQueries{
		workspaceConn: &db.WorkspaceGitlabConnection{
			ServiceTokenEncrypted: []byte("svc"),
		},
		userConn: nil, // user hasn't connected
	}
	r := NewResolver(q, stubDecrypt("dec"))
	tok, src, err := r.ResolveTokenForWrite(context.Background(), workspaceUUID, "member", userUUID)
	if err != nil {
		t.Fatalf("ResolveTokenForWrite: %v", err)
	}
	if src != "service" {
		t.Errorf("source = %q, want service", src)
	}
	if tok != "dec|svc" {
		t.Errorf("token = %q, want dec|svc", tok)
	}
}

func TestResolveTokenForWrite_AgentAlwaysUsesServicePAT(t *testing.T) {
	// Even if a "user_gitlab_connection" row somehow exists for the agent UUID,
	// the resolver MUST ignore it and pick the service PAT. Agents don't have
	// GitLab identities.
	q := &fakeResolverQueries{
		workspaceConn: &db.WorkspaceGitlabConnection{ServiceTokenEncrypted: []byte("svc")},
		userConn: &db.UserGitlabConnection{
			PatEncrypted: []byte("usr"),
			GitlabUserID: 100,
		},
	}
	r := NewResolver(q, stubDecrypt("dec"))
	tok, src, err := r.ResolveTokenForWrite(context.Background(), workspaceUUID, "agent", userUUID)
	if err != nil {
		t.Fatalf("ResolveTokenForWrite: %v", err)
	}
	if src != "service" {
		t.Errorf("source = %q, want service", src)
	}
	if tok != "dec|svc" {
		t.Errorf("token = %q, want dec|svc (service PAT)", tok)
	}
}

func TestResolveTokenForWrite_NoWorkspaceConnection(t *testing.T) {
	q := &fakeResolverQueries{} // both nil
	r := NewResolver(q, stubDecrypt("dec"))
	_, _, err := r.ResolveTokenForWrite(context.Background(), workspaceUUID, "member", userUUID)
	if err == nil {
		t.Fatalf("expected error when workspace has no connection")
	}
}

func TestResolveTokenForWrite_RejectsUnknownActorType(t *testing.T) {
	q := &fakeResolverQueries{
		workspaceConn: &db.WorkspaceGitlabConnection{
			ServiceTokenEncrypted: []byte("svc"),
		},
		userConn: &db.UserGitlabConnection{
			PatEncrypted: []byte("usr"),
			GitlabUserID: 100,
		},
	}
	r := NewResolver(q, stubDecrypt("dec"))

	cases := []struct {
		name      string
		actorType string
	}{
		{name: "empty", actorType: ""},
		{name: "typo", actorType: "manager"},
		{name: "unknown", actorType: "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := r.ResolveTokenForWrite(context.Background(), workspaceUUID, tc.actorType, userUUID)
			if err == nil {
				t.Fatalf("expected error for actorType %q, got nil", tc.actorType)
			}
			if !strings.Contains(err.Error(), "unknown actor type") {
				t.Errorf("error = %q, want to contain %q", err.Error(), "unknown actor type")
			}
		})
	}
}

func TestResolveMulticaUserFromGitlabUserID_UserGitlabConnectionWins(t *testing.T) {
	q := &fakeResolverQueries{
		userConnByGitlabUser: &db.UserGitlabConnection{
			UserID:       pgUUIDForTest(multicaUserID),
			WorkspaceID:  pgUUIDForTest(workspaceUUID),
			GitlabUserID: 7,
		},
		// Even with a project member match, user connection wins.
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
