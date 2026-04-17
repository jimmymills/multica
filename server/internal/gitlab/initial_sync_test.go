package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	gitlabapi "github.com/multica-ai/multica/server/pkg/gitlab"
)

// connectTestPool connects to the worktree DB. Test is skipped if unreachable.
func connectTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil || pool.Ping(context.Background()) != nil {
		t.Skip("database not reachable")
	}
	return pool
}

func makeWorkspace(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO workspace (name, slug, description)
		VALUES ('GL Sync Test', 'gl-sync-test-'||substr(gen_random_uuid()::text, 1, 8), '')
		RETURNING id
	`).Scan(&id); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, id)
	})
	return id
}

func mustPGUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		t.Fatalf("scan uuid: %v", err)
	}
	return u
}

func TestInitialSync_LabelsAndMembers(t *testing.T) {
	pool := connectTestPool(t)
	defer pool.Close()
	wsID := makeWorkspace(t, pool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v4/projects/7/labels":
			if r.Method == http.MethodGet {
				json.NewEncoder(w).Encode([]gitlabapi.Label{
					{ID: 1, Name: "bug", Color: "#ff0000"},
				})
			} else {
				w.Write([]byte(`{"id":99,"name":"x","color":"#000"}`))
			}
		case "/api/v4/projects/7/members/all":
			json.NewEncoder(w).Encode([]gitlabapi.ProjectMember{
				{ID: 100, Username: "alice", Name: "Alice", AvatarURL: "https://x"},
			})
		case "/api/v4/projects/7/issues":
			json.NewEncoder(w).Encode([]gitlabapi.Issue{})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	queries := db.New(pool)
	deps := SyncDeps{Queries: queries, Client: gitlabapi.NewClient(srv.URL, srv.Client())}
	err := RunInitialSync(context.Background(), deps, RunInitialSyncInput{
		WorkspaceID: wsID,
		ProjectID:   7,
		Token:       "tok",
	})
	if err != nil {
		t.Fatalf("RunInitialSync: %v", err)
	}

	rows, _ := queries.ListGitlabLabels(context.Background(), mustPGUUID(t, wsID))
	if len(rows) == 0 {
		t.Errorf("no gitlab_label rows after sync")
	}

	members, _ := queries.ListGitlabProjectMembers(context.Background(), mustPGUUID(t, wsID))
	if len(members) != 1 || members[0].Username != "alice" {
		t.Errorf("members = %+v, want one alice", members)
	}
}
