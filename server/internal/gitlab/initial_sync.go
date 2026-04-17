package gitlab

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	gitlabapi "github.com/multica-ai/multica/server/pkg/gitlab"
)

// SyncDeps is the set of plumbing the sync orchestrator needs.
type SyncDeps struct {
	Queries *db.Queries
	Client  *gitlabapi.Client
}

// RunInitialSyncInput is the per-call input.
type RunInitialSyncInput struct {
	WorkspaceID string
	ProjectID   int64
	Token       string
}

// RunInitialSync orchestrates a one-shot pull of GitLab project state into
// Multica's cache tables for one workspace.
func RunInitialSync(ctx context.Context, deps SyncDeps, in RunInitialSyncInput) error {
	wsUUID, err := pgUUID(in.WorkspaceID)
	if err != nil {
		return fmt.Errorf("initial sync: workspace_id: %w", err)
	}

	// 1. Bootstrap canonical scoped labels (idempotent).
	if err := BootstrapScopedLabels(ctx, deps.Client, in.Token, in.ProjectID); err != nil {
		return fmt.Errorf("initial sync: bootstrap labels: %w", err)
	}

	// 2. Fetch + upsert all labels.
	labels, err := deps.Client.ListLabels(ctx, in.Token, in.ProjectID)
	if err != nil {
		return fmt.Errorf("initial sync: list labels: %w", err)
	}
	now := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	for _, l := range labels {
		if _, err := deps.Queries.UpsertGitlabLabel(ctx, db.UpsertGitlabLabelParams{
			WorkspaceID:       wsUUID,
			GitlabLabelID:     l.ID,
			Name:              l.Name,
			Color:             l.Color,
			Description:       l.Description,
			ExternalUpdatedAt: now,
		}); err != nil {
			return fmt.Errorf("initial sync: upsert label %q: %w", l.Name, err)
		}
	}

	// 3. Fetch + upsert project members.
	members, err := deps.Client.ListProjectMembers(ctx, in.Token, in.ProjectID)
	if err != nil {
		return fmt.Errorf("initial sync: list members: %w", err)
	}
	for _, m := range members {
		if _, err := deps.Queries.UpsertGitlabProjectMember(ctx, db.UpsertGitlabProjectMemberParams{
			WorkspaceID:       wsUUID,
			GitlabUserID:      m.ID,
			Username:          m.Username,
			Name:              m.Name,
			AvatarUrl:         m.AvatarURL,
			ExternalUpdatedAt: now,
		}); err != nil {
			return fmt.Errorf("initial sync: upsert member %q: %w", m.Username, err)
		}
	}

	// 4. Issues + notes + awards (Task 12 implements this).
	if err := syncAllIssues(ctx, deps, in, wsUUID); err != nil {
		return fmt.Errorf("initial sync: issues: %w", err)
	}

	return nil
}

// pgUUID converts a string UUID to pgtype.UUID, returning an error for
// invalid input.
func pgUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return u, err
	}
	return u, nil
}

// syncAllIssues is implemented in Task 12. Stub here so the file compiles.
func syncAllIssues(_ context.Context, _ SyncDeps, _ RunInitialSyncInput, _ pgtype.UUID) error {
	return nil
}
