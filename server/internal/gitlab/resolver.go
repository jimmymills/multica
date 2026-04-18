package gitlab

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// resolverQueries is the narrow surface of *db.Queries the resolver needs.
// Defined as an interface so tests can stub without a DB.
type resolverQueries interface {
	GetWorkspaceGitlabConnection(ctx context.Context, workspaceID pgtype.UUID) (db.WorkspaceGitlabConnection, error)
	GetUserGitlabConnection(ctx context.Context, arg db.GetUserGitlabConnectionParams) (db.UserGitlabConnection, error)
	GetUserGitlabConnectionByGitlabUserID(ctx context.Context, arg db.GetUserGitlabConnectionByGitlabUserIDParams) (db.UserGitlabConnection, error)
	GetGitlabProjectMember(ctx context.Context, arg db.GetGitlabProjectMemberParams) (db.GitlabProjectMember, error)
}

// Resolver picks the right GitLab token for a write request.
//
// Construction takes a TokenDecrypter so tests can stub it; production wires
// in the secrets.Cipher's Decrypt method.
type Resolver struct {
	queries resolverQueries
	decrypt TokenDecrypter
}

// NewResolver constructs a Resolver. queries can be *db.Queries (production)
// or any stub implementing the resolverQueries interface (tests).
func NewResolver(queries resolverQueries, decrypt TokenDecrypter) *Resolver {
	return &Resolver{queries: queries, decrypt: decrypt}
}

// ResolveTokenForWrite returns the plaintext token to use for a GitLab API
// write call, plus a "source" string ("user" or "service") so the caller
// can attribute the cache row correctly.
//
// Rules:
//   - actorType="member", user PAT registered → user PAT, "user"
//   - actorType="member", no PAT             → workspace service PAT, "service"
//   - actorType="agent"                      → workspace service PAT, "service"
//
// Any other actorType returns an error — an unknown actor type (e.g. an
// empty string or a typo) must NOT silently fall back to the service PAT,
// because that would misattribute writes to the service account.
//
// Returns an error when the workspace itself has no GitLab connection
// (writes shouldn't have been routed here in that case).
func (r *Resolver) ResolveTokenForWrite(ctx context.Context, workspaceID, actorType, actorID string) (token string, source string, err error) {
	wsUUID, err := pgUUID(workspaceID)
	if err != nil {
		return "", "", fmt.Errorf("resolver: workspace_id: %w", err)
	}
	wsConn, err := r.queries.GetWorkspaceGitlabConnection(ctx, wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", fmt.Errorf("resolver: workspace has no gitlab connection")
		}
		return "", "", fmt.Errorf("resolver: workspace lookup: %w", err)
	}

	switch actorType {
	case "member":
		userUUID, err := pgUUID(actorID)
		if err == nil {
			userConn, err := r.queries.GetUserGitlabConnection(ctx, db.GetUserGitlabConnectionParams{
				UserID:      userUUID,
				WorkspaceID: wsUUID,
			})
			if err == nil {
				token, err := r.decrypt(ctx, userConn.PatEncrypted)
				if err != nil {
					return "", "", fmt.Errorf("resolver: decrypt user pat: %w", err)
				}
				return token, "user", nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return "", "", fmt.Errorf("resolver: user lookup: %w", err)
			}
			// no user PAT → fall through to service PAT
		}
	case "agent":
		// Agents don't have GitLab identities; always use the service PAT.
	default:
		return "", "", fmt.Errorf("resolver: unknown actor type %q", actorType)
	}

	token, err = r.decrypt(ctx, wsConn.ServiceTokenEncrypted)
	if err != nil {
		return "", "", fmt.Errorf("resolver: decrypt service pat: %w", err)
	}
	return token, "service", nil
}

// ResolveMulticaUserFromGitlabUserID reverse-resolves a GitLab user ID to a
// Multica user reference. Preference order:
//  1. user_gitlab_connection — a human who connected their personal PAT is
//     authoritative over a cached project-member row.
//  2. gitlab_project_member  — GitLab user cached but no Multica mapping.
//  3. Unmapped — returns all empty strings and a nil error; the caller
//     decides how to handle it.
//
// Returns:
//
//	userType    — "member" | "gitlab_user" | ""
//	userID      — Multica user UUID when userType="member", empty otherwise
//	memberRowID — gitlab_project_member.id UUID when userType="gitlab_user",
//	              empty otherwise
func (r *Resolver) ResolveMulticaUserFromGitlabUserID(ctx context.Context, workspaceID string, gitlabUserID int64) (userType, userID, memberRowID string, err error) {
	wsUUID, err := pgUUID(workspaceID)
	if err != nil {
		return "", "", "", fmt.Errorf("resolver: workspace_id: %w", err)
	}

	conn, connErr := r.queries.GetUserGitlabConnectionByGitlabUserID(ctx, db.GetUserGitlabConnectionByGitlabUserIDParams{
		WorkspaceID:  wsUUID,
		GitlabUserID: gitlabUserID,
	})
	if connErr == nil {
		return "member", util.UUIDToString(conn.UserID), "", nil
	}
	if !errors.Is(connErr, pgx.ErrNoRows) {
		return "", "", "", fmt.Errorf("resolver: user connection lookup: %w", connErr)
	}

	member, memErr := r.queries.GetGitlabProjectMember(ctx, db.GetGitlabProjectMemberParams{
		WorkspaceID:  wsUUID,
		GitlabUserID: gitlabUserID,
	})
	if memErr == nil {
		return "gitlab_user", "", util.UUIDToString(member.ID), nil
	}
	if !errors.Is(memErr, pgx.ErrNoRows) {
		return "", "", "", fmt.Errorf("resolver: project member lookup: %w", memErr)
	}

	return "", "", "", nil
}
