package service

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func setupRuntimeGroupServiceTest(t *testing.T) (*RuntimeGroupService, testContext) {
	t.Helper()
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	q := db.New(testPool)
	svc := NewRuntimeGroupService(q, testPool)
	tc := testContext{
		ctx:         ctx,
		workspaceID: testWorkspaceID,
	}
	tc.cleanup = func() {
		bgCtx := context.Background()
		// Runtime group tables — must run before agent_runtime / agent.
		testPool.Exec(bgCtx, `
			DELETE FROM runtime_group_override
			WHERE group_id IN (SELECT id FROM runtime_group WHERE workspace_id = $1)
		`, testWorkspaceID)
		testPool.Exec(bgCtx, `
			DELETE FROM runtime_group_member
			WHERE group_id IN (SELECT id FROM runtime_group WHERE workspace_id = $1)
		`, testWorkspaceID)
		testPool.Exec(bgCtx, `
			DELETE FROM agent_runtime_group
			WHERE agent_id IN (
				SELECT id FROM agent WHERE workspace_id = $1 AND name LIKE 'test-agent-%'
			)
		`, testWorkspaceID)
		testPool.Exec(bgCtx, `
			DELETE FROM runtime_group WHERE workspace_id = $1
		`, testWorkspaceID)
		testPool.Exec(bgCtx, `
			DELETE FROM agent_runtime WHERE workspace_id = $1 AND name LIKE 'test-rt-%'
		`, testWorkspaceID)
	}
	return svc, tc
}

func TestRuntimeGroupService_CreateGroup_AddsMembers(t *testing.T) {
	svc, tc := setupRuntimeGroupServiceTest(t)
	ctx := context.Background()
	defer tc.cleanup()

	rt1 := tc.createRuntime(t, "online")
	rt2 := tc.createRuntime(t, "online")

	group, err := svc.CreateGroup(ctx, testWorkspaceID, "test-grp", "desc", testUserID, []pgtype.UUID{rt1, rt2})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	members, err := svc.Queries.ListRuntimeGroupMembers(ctx, group.ID)
	if err != nil {
		t.Fatalf("ListRuntimeGroupMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
}

func TestRuntimeGroupService_CreateGroup_RejectsCrossWorkspaceRuntime(t *testing.T) {
	svc, tc := setupRuntimeGroupServiceTest(t)
	ctx := context.Background()
	defer tc.cleanup()

	foreignRt := tc.createRuntimeInOtherWorkspace(t)

	_, err := svc.CreateGroup(ctx, testWorkspaceID, "xwsg", "", testUserID, []pgtype.UUID{foreignRt})
	if err == nil {
		t.Fatal("expected cross-workspace rejection, got nil")
	}
}

func TestRuntimeGroupService_SetOverride_ReplacesExisting(t *testing.T) {
	svc, tc := setupRuntimeGroupServiceTest(t)
	ctx := context.Background()
	defer tc.cleanup()

	rt1 := tc.createRuntime(t, "online")
	rt2 := tc.createRuntime(t, "online")
	group, err := svc.CreateGroup(ctx, testWorkspaceID, "replg", "", testUserID, []pgtype.UUID{rt1, rt2})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	ends1 := pgtype.Timestamptz{Time: time.Now().Add(1 * time.Hour), Valid: true}
	if _, err := svc.SetOverride(ctx, group.ID, rt1, ends1, testUserID); err != nil {
		t.Fatalf("first SetOverride: %v", err)
	}

	ends2 := pgtype.Timestamptz{Time: time.Now().Add(2 * time.Hour), Valid: true}
	if _, err := svc.SetOverride(ctx, group.ID, rt2, ends2, testUserID); err != nil {
		t.Fatalf("second SetOverride: %v", err)
	}

	active, err := svc.Queries.GetActiveRuntimeGroupOverride(ctx, group.ID)
	if err != nil {
		t.Fatalf("GetActiveRuntimeGroupOverride: %v", err)
	}
	if active.RuntimeID.Bytes != rt2.Bytes {
		t.Fatalf("expected active override to be rt2, got different runtime")
	}
}

func TestRuntimeGroupService_SetOverride_RejectsNonMember(t *testing.T) {
	svc, tc := setupRuntimeGroupServiceTest(t)
	ctx := context.Background()
	defer tc.cleanup()

	rt1 := tc.createRuntime(t, "online")
	rtOther := tc.createRuntime(t, "online")
	group, err := svc.CreateGroup(ctx, testWorkspaceID, "reject-nonmem", "", testUserID, []pgtype.UUID{rt1})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	ends := pgtype.Timestamptz{Time: time.Now().Add(1 * time.Hour), Valid: true}
	_, err = svc.SetOverride(ctx, group.ID, rtOther, ends, testUserID)
	if err != ErrRuntimeNotGroupMember {
		t.Fatalf("expected ErrRuntimeNotGroupMember, got %v", err)
	}
}

func TestRuntimeGroupService_RemovingMemberCascadesOverride(t *testing.T) {
	svc, tc := setupRuntimeGroupServiceTest(t)
	ctx := context.Background()
	defer tc.cleanup()

	rt1 := tc.createRuntime(t, "online")
	rt2 := tc.createRuntime(t, "online")
	group, err := svc.CreateGroup(ctx, testWorkspaceID, "cascade-test", "", testUserID, []pgtype.UUID{rt1, rt2})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	ends := pgtype.Timestamptz{Time: time.Now().Add(1 * time.Hour), Valid: true}
	if _, err := svc.SetOverride(ctx, group.ID, rt1, ends, testUserID); err != nil {
		t.Fatalf("SetOverride: %v", err)
	}

	// Remove rt1 from the group → override should auto-delete via FK cascade.
	if _, err := svc.UpdateGroup(ctx, group.ID, nil, nil, []pgtype.UUID{rt2}, testWorkspaceID); err != nil {
		t.Fatalf("UpdateGroup: %v", err)
	}

	_, err = svc.Queries.GetActiveRuntimeGroupOverride(ctx, group.ID)
	if err == nil {
		t.Fatal("expected no active override after cascade, got one")
	}
}
