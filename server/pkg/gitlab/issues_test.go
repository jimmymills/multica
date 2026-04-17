package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateIssue_PostsCorrectBody(t *testing.T) {
	var got CreateIssueInput
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v4/projects/7/issues" {
			t.Errorf("path = %s, want /api/v4/projects/7/issues", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Issue{
			ID: 9001, IID: 11, Title: got.Title, State: "opened",
			Labels: got.Labels, UpdatedAt: "2026-04-17T15:00:00Z",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	out, err := c.CreateIssue(context.Background(), "tok", 7, CreateIssueInput{
		Title:       "hi",
		Description: "body",
		Labels:      []string{"status::todo", "priority::high"},
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if got.Title != "hi" || got.Description != "body" {
		t.Errorf("server received %+v", got)
	}
	if len(got.Labels) != 2 {
		t.Errorf("labels = %v", got.Labels)
	}
	if out.IID != 11 {
		t.Errorf("returned IID = %d, want 11", out.IID)
	}
}

func TestListIssues_DefaultsToStateAll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != "all" {
			t.Errorf("state query = %q, want all", r.URL.Query().Get("state"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]Issue{
			{IID: 1, Title: "one", State: "opened", Labels: []string{"status::todo"}},
			{IID: 2, Title: "two", State: "closed", Labels: []string{"status::done"}},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	issues, err := c.ListIssues(context.Background(), "tok", 7, ListIssuesParams{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 2 || issues[0].IID != 1 {
		t.Errorf("unexpected: %+v", issues)
	}
}

func TestListIssues_UpdatedAfterPropagated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("updated_after") != "2026-04-17T00:00:00Z" {
			t.Errorf("updated_after = %q", r.URL.Query().Get("updated_after"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	_, err := c.ListIssues(context.Background(), "tok", 7, ListIssuesParams{
		UpdatedAfter: "2026-04-17T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
}
