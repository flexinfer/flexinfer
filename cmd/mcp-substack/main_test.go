package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crb2nu/loom/pkg/httpclient"
)

func setupSubstackMock(t *testing.T, handler http.HandlerFunc) (*substackServer, func()) {
	t.Helper()
	ts := httptest.NewServer(handler)
	client := httpclient.NewDefault()
	s := &substackServer{
		baseURL:    ts.URL,
		userID:     12345,
		httpClient: client,
	}
	return s, ts.Close
}

func substackSuccess(data any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	}
}

func TestHandleListDrafts_HappyPath(t *testing.T) {
	s, cleanup := setupSubstackMock(t, substackSuccess([]any{}))
	defer cleanup()

	result, err := s.handleListDrafts(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
}

func TestHandleCreateDraft_MissingTitle(t *testing.T) {
	s, cleanup := setupSubstackMock(t, substackSuccess(nil))
	defer cleanup()

	result, err := s.handleCreateDraft(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing title")
	}
}

func TestHandleUpdateDraft_MissingDraftID(t *testing.T) {
	s, cleanup := setupSubstackMock(t, substackSuccess(nil))
	defer cleanup()

	result, err := s.handleUpdateDraft(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing draft_id")
	}
}

func TestHandlePublish_MissingDraftID(t *testing.T) {
	s, cleanup := setupSubstackMock(t, substackSuccess(nil))
	defer cleanup()

	result, err := s.handlePublish(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing draft_id")
	}
}

func TestHandleListPosts_HappyPath(t *testing.T) {
	s, cleanup := setupSubstackMock(t, substackSuccess([]any{
		map[string]any{"id": 1, "title": "Test Post"},
	}))
	defer cleanup()

	result, err := s.handleListPosts(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
}

func TestHandleGetPost_MissingSlug(t *testing.T) {
	s, cleanup := setupSubstackMock(t, substackSuccess(nil))
	defer cleanup()

	result, err := s.handleGetPost(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing slug")
	}
}
