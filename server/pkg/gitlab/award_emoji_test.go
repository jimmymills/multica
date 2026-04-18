package gitlab

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListAwardEmoji_PerIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/7/issues/42/award_emoji" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]AwardEmoji{
			{ID: 1, Name: "thumbsup", User: User{ID: 100, Username: "alice"}, UpdatedAt: "2026-04-17T10:00:00Z"},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	awards, err := c.ListAwardEmoji(context.Background(), "tok", 7, 42)
	if err != nil {
		t.Fatalf("ListAwardEmoji: %v", err)
	}
	if len(awards) != 1 || awards[0].Name != "thumbsup" {
		t.Errorf("unexpected: %+v", awards)
	}
}

func TestCreateIssueAwardEmoji_SendsPOST(t *testing.T) {
	var capturedMethod, capturedPath, capturedToken string
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		capturedToken = r.Header.Get("PRIVATE-TOKEN")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9901,"name":"thumbsup","user":{"id":7},"created_at":"2026-04-17T12:00:00Z","updated_at":"2026-04-17T12:00:00Z"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	award, err := c.CreateIssueAwardEmoji(context.Background(), "tok", 42, 7, "thumbsup")
	if err != nil {
		t.Fatalf("CreateIssueAwardEmoji: %v", err)
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", capturedMethod)
	}
	if capturedPath != "/api/v4/projects/42/issues/7/award_emoji" {
		t.Errorf("path = %s", capturedPath)
	}
	if capturedToken != "tok" {
		t.Errorf("token = %s", capturedToken)
	}
	if capturedBody["name"] != "thumbsup" {
		t.Errorf("body name = %v, want thumbsup", capturedBody["name"])
	}
	if award.ID != 9901 || award.Name != "thumbsup" {
		t.Errorf("award = %+v", award)
	}
}

func TestCreateIssueAwardEmoji_PropagatesNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"already awarded"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	_, err := c.CreateIssueAwardEmoji(context.Background(), "tok", 1, 1, "thumbsup")
	if err == nil {
		t.Fatal("expected error on 409")
	}
	if !strings.Contains(err.Error(), "409") && !strings.Contains(err.Error(), "already") && !strings.Contains(err.Error(), "conflict") {
		t.Errorf("error = %v, want 409/conflict/already", err)
	}
}

func TestDeleteIssueAwardEmoji_SendsDELETE(t *testing.T) {
	var capturedMethod, capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.DeleteIssueAwardEmoji(context.Background(), "tok", 42, 7, 9901); err != nil {
		t.Fatalf("DeleteIssueAwardEmoji: %v", err)
	}
	if capturedMethod != http.MethodDelete {
		t.Errorf("method = %s", capturedMethod)
	}
	if capturedPath != "/api/v4/projects/42/issues/7/award_emoji/9901" {
		t.Errorf("path = %s", capturedPath)
	}
}

func TestDeleteIssueAwardEmoji_404IsIdempotentSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.DeleteIssueAwardEmoji(context.Background(), "tok", 1, 1, 1); err != nil {
		t.Fatalf("expected 404 as success, got %v", err)
	}
}

func TestCreateNoteAwardEmoji_SendsPOST(t *testing.T) {
	var capturedMethod, capturedPath string
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9902,"name":"heart","user":{"id":7},"created_at":"2026-04-17T12:00:00Z","updated_at":"2026-04-17T12:00:00Z"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	award, err := c.CreateNoteAwardEmoji(context.Background(), "tok", 42, 7, 555, "heart")
	if err != nil {
		t.Fatalf("CreateNoteAwardEmoji: %v", err)
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("method = %s", capturedMethod)
	}
	if capturedPath != "/api/v4/projects/42/issues/7/notes/555/award_emoji" {
		t.Errorf("path = %s", capturedPath)
	}
	if capturedBody["name"] != "heart" {
		t.Errorf("body name = %v", capturedBody["name"])
	}
	if award.ID != 9902 || award.Name != "heart" {
		t.Errorf("award = %+v", award)
	}
}

func TestDeleteNoteAwardEmoji_SendsDELETE(t *testing.T) {
	var capturedMethod, capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.DeleteNoteAwardEmoji(context.Background(), "tok", 42, 7, 555, 9902); err != nil {
		t.Fatalf("DeleteNoteAwardEmoji: %v", err)
	}
	if capturedMethod != http.MethodDelete {
		t.Errorf("method = %s", capturedMethod)
	}
	if capturedPath != "/api/v4/projects/42/issues/7/notes/555/award_emoji/9902" {
		t.Errorf("path = %s", capturedPath)
	}
}

func TestDeleteNoteAwardEmoji_404IsIdempotentSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.DeleteNoteAwardEmoji(context.Background(), "tok", 1, 1, 1, 1); err != nil {
		t.Fatalf("expected 404 as success, got %v", err)
	}
}
