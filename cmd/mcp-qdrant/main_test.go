package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func setupQdrantMock(t *testing.T, handler http.HandlerFunc) func() {
	t.Helper()
	ts := httptest.NewServer(handler)
	origURL := qdrantURL
	qdrantURL = ts.URL
	return func() {
		ts.Close()
		qdrantURL = origURL
	}
}

func qdrantSuccess(data any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"result": data,
		})
	}
}

func TestHandleListCollections_HappyPath(t *testing.T) {
	defer setupQdrantMock(t, qdrantSuccess(map[string]any{
		"collections": []any{
			map[string]any{"name": "test-collection"},
		},
	}))()

	result, err := handleListCollections(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "collections") {
		t.Errorf("expected 'collections' in output, got: %s", text)
	}
}

func TestHandleCreateCollection_MissingParams(t *testing.T) {
	result, err := handleCreateCollection(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

func TestHandleDeleteCollection_MissingCollection(t *testing.T) {
	result, err := handleDeleteCollection(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing collection")
	}
}

func TestHandleGetCollection_MissingCollection(t *testing.T) {
	result, err := handleGetCollection(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing collection")
	}
}

func TestHandleSearch_MissingParams(t *testing.T) {
	result, err := handleSearch(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

func TestHandleScroll_MissingCollection(t *testing.T) {
	result, err := handleScroll(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing collection")
	}
}

func TestHandleUpsert_MissingParams(t *testing.T) {
	result, err := handleUpsert(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

func TestHandleDelete_MissingCollection(t *testing.T) {
	result, err := handleDelete(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing collection")
	}
}

func TestHandleDelete_NoPointsOrFilter(t *testing.T) {
	defer setupQdrantMock(t, qdrantSuccess(nil))()

	result, err := handleDelete(context.Background(), map[string]any{
		"collection": "test",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when neither points nor filter provided")
	}
}

func TestHandleGetCollection_HappyPath(t *testing.T) {
	defer setupQdrantMock(t, qdrantSuccess(map[string]any{
		"status":        "green",
		"vectors_count": 1000,
	}))()

	result, err := handleGetCollection(context.Background(), map[string]any{
		"collection": "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
}

func TestHandleListCollections_APIError(t *testing.T) {
	defer setupQdrantMock(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]any{"error": "internal server error"})
	})()

	result, err := handleListCollections(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for API error")
	}
}
