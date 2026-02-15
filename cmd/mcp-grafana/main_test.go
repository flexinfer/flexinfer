package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/portforward"
)

// setupGrafanaMock starts an httptest server and overrides package globals.
func setupGrafanaMock(t *testing.T, handler http.HandlerFunc) func() {
	t.Helper()
	ts := httptest.NewServer(handler)
	origURL := grafanaURL
	origToken := grafanaToken
	origPF := portForwarder
	grafanaURL = ts.URL
	grafanaToken = "test-token"
	portForwarder = portforward.New(portforward.Config{}, false)
	return func() {
		ts.Close()
		grafanaURL = origURL
		grafanaToken = origToken
		portForwarder = origPF
	}
}

// grafanaList returns a handler that responds with a JSON array.
func grafanaList(items []any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(items)
	}
}

// grafanaObject returns a handler that responds with a JSON object.
func grafanaObject(obj map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(obj)
	}
}

func grafanaError(code int, message string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		w.Write([]byte(message))
	}
}

// ---------------------------------------------------------------------------
// Validation error tests
// ---------------------------------------------------------------------------

func TestHandleGetDashboard_MissingUID(t *testing.T) {
	defer setupGrafanaMock(t, grafanaList(nil))()
	result, err := handleGetDashboard(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing uid")
	}
}

func TestHandleGetDatasource_MissingUID(t *testing.T) {
	defer setupGrafanaMock(t, grafanaList(nil))()
	result, err := handleGetDatasource(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing uid")
	}
}

func TestHandleCreateAnnotation_MissingText(t *testing.T) {
	defer setupGrafanaMock(t, grafanaObject(nil))()
	result, err := handleCreateAnnotation(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing text")
	}
}

// ---------------------------------------------------------------------------
// Happy-path tests with mock HTTP server
// ---------------------------------------------------------------------------

func TestHandleSearch_HappyPath(t *testing.T) {
	defer setupGrafanaMock(t, grafanaList([]any{
		map[string]any{"uid": "dash-1", "title": "CPU Overview", "type": "dash-db"},
		map[string]any{"uid": "dash-2", "title": "Memory", "type": "dash-db"},
	}))()

	result, err := handleSearch(context.Background(), map[string]any{
		"query": "CPU",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "results") {
		t.Errorf("expected results in output, got: %s", text)
	}
}

func TestHandleSearch_LimitClamp(t *testing.T) {
	var capturedQuery string
	defer setupGrafanaMock(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		grafanaList([]any{})(w, r)
	})()

	_, err := handleSearch(context.Background(), map[string]any{
		"limit": float64(9999),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedQuery, "limit=500") {
		t.Errorf("expected limit clamped to 500, got query: %s", capturedQuery)
	}
}

func TestHandleGetDashboard_HappyPath(t *testing.T) {
	defer setupGrafanaMock(t, grafanaObject(map[string]any{
		"dashboard": map[string]any{
			"uid":   "dash-1",
			"title": "Test Dashboard",
		},
		"meta": map[string]any{"isStarred": false},
	}))()

	result, err := handleGetDashboard(context.Background(), map[string]any{
		"uid": "dash-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "dashboard") {
		t.Errorf("expected dashboard in output, got: %s", text)
	}
}

func TestHandleListDatasources_HappyPath(t *testing.T) {
	defer setupGrafanaMock(t, grafanaList([]any{
		map[string]any{"uid": "ds-1", "name": "Prometheus", "type": "prometheus"},
	}))()

	result, err := handleListDatasources(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "datasources") {
		t.Errorf("expected datasources in output, got: %s", text)
	}
}

func TestHandleGetDatasource_HappyPath(t *testing.T) {
	defer setupGrafanaMock(t, grafanaObject(map[string]any{
		"uid":  "ds-1",
		"name": "Prometheus",
		"type": "prometheus",
	}))()

	result, err := handleGetDatasource(context.Background(), map[string]any{
		"uid": "ds-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "datasource") {
		t.Errorf("expected datasource in output, got: %s", text)
	}
}

func TestHandleListAlerts_HappyPath(t *testing.T) {
	defer setupGrafanaMock(t, grafanaList([]any{
		map[string]any{"uid": "alert-1", "title": "High CPU"},
	}))()

	result, err := handleListAlerts(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "alerts") {
		t.Errorf("expected alerts in output, got: %s", text)
	}
}

func TestHandleListAlertInstances_HappyPath(t *testing.T) {
	defer setupGrafanaMock(t, grafanaList([]any{}))()

	result, err := handleListAlertInstances(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
}

func TestHandleListAnnotations_HappyPath(t *testing.T) {
	defer setupGrafanaMock(t, grafanaList([]any{
		map[string]any{"id": 1, "text": "deploy v1.2"},
	}))()

	result, err := handleListAnnotations(context.Background(), map[string]any{
		"dashboard_uid": "dash-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
}

func TestHandleCreateAnnotation_HappyPath(t *testing.T) {
	defer setupGrafanaMock(t, grafanaObject(map[string]any{
		"id":      42,
		"message": "Annotation added",
	}))()

	result, err := handleCreateAnnotation(context.Background(), map[string]any{
		"text": "deployment v1.2.3",
		"tags": []any{"deploy", "prod"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "Annotation created") {
		t.Errorf("expected 'Annotation created', got: %s", text)
	}
}

func TestHandleListFolders_HappyPath(t *testing.T) {
	defer setupGrafanaMock(t, grafanaList([]any{
		map[string]any{"uid": "folder-1", "title": "Production"},
	}))()

	result, err := handleListFolders(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "folders") {
		t.Errorf("expected folders in output, got: %s", text)
	}
}

// ---------------------------------------------------------------------------
// API error test
// ---------------------------------------------------------------------------

func TestHandleSearch_APIError(t *testing.T) {
	defer setupGrafanaMock(t, grafanaError(401, "Unauthorized"))()

	result, err := handleSearch(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for API error")
	}
}
