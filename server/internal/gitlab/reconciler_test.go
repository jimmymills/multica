package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	gitlabapi "github.com/multica-ai/multica/server/pkg/gitlab"
)

func TestReconciler_PicksUpDriftAndAdvancesCursor(t *testing.T) {
	pool := connectTestPool(t)
	wsID := makeWorkspace(t, pool)
	queries := db.New(pool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v4/projects/7/issues":
			json.NewEncoder(w).Encode([]gitlabapi.Issue{
				{ID: 5001, IID: 11, Title: "from reconciler", State: "opened",
					Labels: []string{}, UpdatedAt: "2026-04-17T15:00:00Z"},
			})
		case "/api/v4/projects/7/labels":
			json.NewEncoder(w).Encode([]gitlabapi.Label{})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	// Seed a connection in the past.
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO workspace_gitlab_connection (
			workspace_id, gitlab_project_id, gitlab_project_path,
			service_token_encrypted, service_token_user_id, connection_status,
			last_sync_cursor
		) VALUES ($1, 7, 'g/a', $2, 1, 'connected', '2026-04-17T14:00:00Z')
	`, wsID, []byte{0x01, 0x02}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	// Inject a static decrypter so we don't have to wire the cipher.
	r := NewReconciler(queries, gitlabapi.NewClient(srv.URL, srv.Client()),
		func(ctx context.Context, encrypted []byte) (string, error) { return "tok", nil })
	if err := r.tickOne(context.Background()); err != nil {
		t.Fatalf("tickOne: %v", err)
	}

	row, err := queries.GetIssueByGitlabIID(context.Background(), db.GetIssueByGitlabIIDParams{
		WorkspaceID: mustPGUUID(t, wsID),
		GitlabIid:   pgtype.Int4{Int32: 11, Valid: true},
	})
	if err != nil {
		t.Fatalf("issue not picked up: %v", err)
	}
	if row.Title != "from reconciler" {
		t.Errorf("title = %q", row.Title)
	}

	// Cursor should have advanced to the issue's UpdatedAt.
	conn, _ := queries.GetWorkspaceGitlabConnection(context.Background(), mustPGUUID(t, wsID))
	expected, _ := time.Parse(time.RFC3339, "2026-04-17T15:00:00Z")
	if !conn.LastSyncCursor.Valid || !conn.LastSyncCursor.Time.Equal(expected) {
		t.Errorf("last_sync_cursor = %+v, want %v", conn.LastSyncCursor, expected)
	}
}
