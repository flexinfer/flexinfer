package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crb2nu/loom/pkg/httpclient"
)

func setupAMMock(t *testing.T, handler http.HandlerFunc) (*alertmanagerServer, func()) {
	t.Helper()
	ts := httptest.NewServer(handler)
	am := &alertmanagerServer{
		url:    ts.URL,
		client: httpclient.NewDefault(),
	}
	return am, ts.Close
}

func amSuccess(data any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	}
}

func TestHandleListAlerts_HappyPath(t *testing.T) {
	am, cleanup := setupAMMock(t, amSuccess([]any{
		map[string]any{"labels": map[string]any{"alertname": "TestAlert"}, "status": map[string]any{"state": "active"}},
	}))
	defer cleanup()

	result, err := am.handleListAlerts(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
}

func TestHandleListSilences_HappyPath(t *testing.T) {
	am, cleanup := setupAMMock(t, amSuccess([]any{}))
	defer cleanup()

	result, err := am.handleListSilences(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
}

func TestHandleCreateSilence_MissingParams(t *testing.T) {
	am, cleanup := setupAMMock(t, amSuccess(nil))
	defer cleanup()

	result, err := am.handleCreateSilence(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

func TestHandleDeleteSilence_MissingParams(t *testing.T) {
	am, cleanup := setupAMMock(t, amSuccess(nil))
	defer cleanup()

	result, err := am.handleDeleteSilence(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing silence_id")
	}
}

func TestHandleStatus_HappyPath(t *testing.T) {
	am, cleanup := setupAMMock(t, amSuccess(map[string]any{
		"cluster":     map[string]any{"status": "ready"},
		"versionInfo": map[string]any{"version": "0.27.0"},
	}))
	defer cleanup()

	result, err := am.handleStatus(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
}

func TestHandleListAlerts_APIError(t *testing.T) {
	am, cleanup := setupAMMock(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]any{"error": "internal"})
	})
	defer cleanup()

	result, err := am.handleListAlerts(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for API error")
	}
}
