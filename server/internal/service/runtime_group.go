package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RuntimeGroupTxStarter is the minimal interface RuntimeGroupService needs
// from a pool-or-conn for transactional work. Satisfied by *pgxpool.Pool.
type RuntimeGroupTxStarter interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

type RuntimeGroupService struct {
	Queries *db.Queries
	Tx      RuntimeGroupTxStarter
}

func NewRuntimeGroupService(q *db.Queries, tx RuntimeGroupTxStarter) *RuntimeGroupService {
	return &RuntimeGroupService{Queries: q, Tx: tx}
}

// CreateGroup inserts a new group + member rows in a single transaction.
// Validates every runtime belongs to the same workspace as the group.
func (s *RuntimeGroupService) CreateGroup(ctx context.Context, wsID pgtype.UUID, name, description string, userID pgtype.UUID, runtimeIDs []pgtype.UUID) (db.RuntimeGroup, error) {
	if name == "" {
		return db.RuntimeGroup{}, fmt.Errorf("name is required")
	}
	for _, rid := range runtimeIDs {
		if _, err := s.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
			ID:          rid,
			WorkspaceID: wsID,
		}); err != nil {
			return db.RuntimeGroup{}, fmt.Errorf("runtime does not belong to workspace")
		}
	}

	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return db.RuntimeGroup{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := s.Queries.WithTx(tx)

	group, err := qtx.CreateRuntimeGroup(ctx, db.CreateRuntimeGroupParams{
		WorkspaceID: wsID,
		Name:        name,
		Description: description,
		CreatedBy:   userID,
	})
	if err != nil {
		return db.RuntimeGroup{}, fmt.Errorf("create group: %w", err)
	}
	for _, rid := range runtimeIDs {
		if err := qtx.AddRuntimeGroupMember(ctx, db.AddRuntimeGroupMemberParams{
			GroupID:   group.ID,
			RuntimeID: rid,
		}); err != nil {
			return db.RuntimeGroup{}, fmt.Errorf("add member: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return db.RuntimeGroup{}, fmt.Errorf("commit: %w", err)
	}
	return group, nil
}

// UpdateGroup optionally updates name/description and replaces member set.
// runtimeIDs == nil means "leave members unchanged". Validates each runtime
// belongs to the workspace when a member replacement is requested.
func (s *RuntimeGroupService) UpdateGroup(ctx context.Context, groupID pgtype.UUID, name, description *string, runtimeIDs []pgtype.UUID, workspaceID pgtype.UUID) (db.RuntimeGroup, error) {
	if runtimeIDs != nil {
		for _, rid := range runtimeIDs {
			if _, err := s.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
				ID:          rid,
				WorkspaceID: workspaceID,
			}); err != nil {
				return db.RuntimeGroup{}, fmt.Errorf("runtime does not belong to workspace")
			}
		}
	}

	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return db.RuntimeGroup{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := s.Queries.WithTx(tx)

	params := db.UpdateRuntimeGroupParams{ID: groupID}
	if name != nil {
		params.Name = pgtype.Text{String: *name, Valid: true}
	}
	if description != nil {
		params.Description = pgtype.Text{String: *description, Valid: true}
	}
	group, err := qtx.UpdateRuntimeGroup(ctx, params)
	if err != nil {
		return db.RuntimeGroup{}, fmt.Errorf("update group: %w", err)
	}

	if runtimeIDs != nil {
		// Add new members (ON CONFLICT preserves created_at), then prune those
		// no longer in the set.
		for _, rid := range runtimeIDs {
			if err := qtx.AddRuntimeGroupMember(ctx, db.AddRuntimeGroupMemberParams{
				GroupID:   groupID,
				RuntimeID: rid,
			}); err != nil {
				return db.RuntimeGroup{}, fmt.Errorf("add member: %w", err)
			}
		}
		if err := qtx.RemoveRuntimeGroupMembersNotIn(ctx, db.RemoveRuntimeGroupMembersNotInParams{
			GroupID:    groupID,
			RuntimeIds: runtimeIDs,
		}); err != nil {
			return db.RuntimeGroup{}, fmt.Errorf("prune members: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return db.RuntimeGroup{}, fmt.Errorf("commit: %w", err)
	}
	return group, nil
}

// ErrRuntimeGroupNotFound is returned by SetOverride when the group has been
// deleted between the handler's pre-check and the service transaction.
var ErrRuntimeGroupNotFound = errors.New("runtime group not found")

// ErrRuntimeNotGroupMember is returned by SetOverride when the requested
// runtime is not a current member of the group.
var ErrRuntimeNotGroupMember = errors.New("runtime is not a member of this group")

// SetOverride clips any currently-active override on the group, then inserts
// a new one. Enforces "override runtime must be a group member" at the DB
// layer via the composite FK; violations surface as pgx error code 23503
// and are translated to ErrRuntimeNotGroupMember.
func (s *RuntimeGroupService) SetOverride(ctx context.Context, groupID, runtimeID pgtype.UUID, endsAt pgtype.Timestamptz, userID pgtype.UUID) (db.RuntimeGroupOverride, error) {
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return db.RuntimeGroupOverride{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := s.Queries.WithTx(tx)

	if _, err := qtx.GetRuntimeGroup(ctx, groupID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.RuntimeGroupOverride{}, ErrRuntimeGroupNotFound
		}
		return db.RuntimeGroupOverride{}, fmt.Errorf("get group: %w", err)
	}

	if err := qtx.ClipActiveRuntimeGroupOverride(ctx, groupID); err != nil {
		return db.RuntimeGroupOverride{}, fmt.Errorf("clip active: %w", err)
	}
	override, err := qtx.InsertRuntimeGroupOverride(ctx, db.InsertRuntimeGroupOverrideParams{
		GroupID:   groupID,
		RuntimeID: runtimeID,
		EndsAt:    endsAt,
		CreatedBy: userID,
	})
	if err != nil {
		// FK violation on the composite (group_id, runtime_id) → member means
		// the runtime is not in the group. Any other error bubbles up as-is.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return db.RuntimeGroupOverride{}, ErrRuntimeNotGroupMember
		}
		return db.RuntimeGroupOverride{}, fmt.Errorf("insert override: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return db.RuntimeGroupOverride{}, fmt.Errorf("commit: %w", err)
	}
	return override, nil
}

// ClearOverride soft-cancels the active override on the group (if any).
func (s *RuntimeGroupService) ClearOverride(ctx context.Context, groupID pgtype.UUID) error {
	return s.Queries.ClearRuntimeGroupOverride(ctx, groupID)
}
