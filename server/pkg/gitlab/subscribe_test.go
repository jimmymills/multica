package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubscribe_SendsPOST(t *testing.T) {
	var capturedMethod, capturedPath, capturedToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		capturedToken = r.Header.Get("PRIVATE-TOKEN")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9001,"iid":7,"subscribed":true}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.Subscribe(context.Background(), "tok", 42, 7); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", capturedMethod)
	}
	if capturedPath != "/api/v4/projects/42/issues/7/subscribe" {
		t.Errorf("path = %s", capturedPath)
	}
	if capturedToken != "tok" {
		t.Errorf("token = %s", capturedToken)
	}
}

func TestSubscribe_304IsIdempotentSuccess(t *testing.T) {
	// GitLab returns 304 when the user is already subscribed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.Subscribe(context.Background(), "tok", 1, 1); err != nil {
		t.Fatalf("expected 304 as success, got %v", err)
	}
}

func TestSubscribe_PropagatesNon2xxNon304(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.Subscribe(context.Background(), "tok", 1, 1); err == nil {
		t.Fatal("expected error for 403")
	}
}

func TestUnsubscribe_SendsPOST(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9001,"iid":7,"subscribed":false}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.Unsubscribe(context.Background(), "tok", 42, 7); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if capturedPath != "/api/v4/projects/42/issues/7/unsubscribe" {
		t.Errorf("path = %s", capturedPath)
	}
}

func TestUnsubscribe_304IsIdempotentSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if err := c.Unsubscribe(context.Background(), "tok", 1, 1); err != nil {
		t.Fatalf("expected 304 as success, got %v", err)
	}
}
