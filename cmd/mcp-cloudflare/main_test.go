package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// setupCFMock starts an httptest server, overrides package-level globals,
// and returns a cleanup function.
func setupCFMock(t *testing.T, handler http.HandlerFunc) func() {
	t.Helper()
	ts := httptest.NewServer(handler)
	origToken := cfAPIToken
	origBase := cfAPIBase
	cfAPIToken = "test-token-abc"
	cfAPIBase = ts.URL
	return func() {
		ts.Close()
		cfAPIToken = origToken
		cfAPIBase = origBase
	}
}

func cfSuccess(result any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result":  result,
		})
	}
}

func cfError(code int, errors []any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"errors":  errors,
		})
	}
}

// ---------------------------------------------------------------------------
// Missing token test
// ---------------------------------------------------------------------------

func TestHandleVerifyToken_MissingToken(t *testing.T) {
	origToken := cfAPIToken
	cfAPIToken = ""
	defer func() { cfAPIToken = origToken }()

	result, err := handleVerifyToken(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when token is empty")
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "CF_API_TOKEN") {
		t.Errorf("expected CF_API_TOKEN in error, got: %s", text)
	}
}

// ---------------------------------------------------------------------------
// Validation error tests
// ---------------------------------------------------------------------------

func TestHandleListDNSRecords_MissingZoneID(t *testing.T) {
	result, err := handleListDNSRecords(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing zone_id")
	}
}

func TestHandleCreateDNSRecord_MissingParams(t *testing.T) {
	result, err := handleCreateDNSRecord(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

func TestHandleUpdateDNSRecord_MissingParams(t *testing.T) {
	result, err := handleUpdateDNSRecord(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

func TestHandleDeleteDNSRecord_MissingParams(t *testing.T) {
	result, err := handleDeleteDNSRecord(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

func TestHandlePurgeCache_MissingZoneID(t *testing.T) {
	result, err := handlePurgeCache(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing zone_id")
	}
}

func TestHandlePurgeCache_NoPurgeOption(t *testing.T) {
	defer setupCFMock(t, cfSuccess(nil))()
	result, err := handlePurgeCache(context.Background(), map[string]any{
		"zone_id": "zone-1",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no purge option specified")
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "must specify") {
		t.Errorf("expected 'must specify' in error, got: %s", text)
	}
}

func TestHandleListTunnels_MissingAccountID(t *testing.T) {
	defer setupCFMock(t, cfSuccess(nil))()
	origAccount := cfAccountID
	cfAccountID = ""
	defer func() { cfAccountID = origAccount }()

	result, err := handleListTunnels(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when account ID is empty")
	}
}

// ---------------------------------------------------------------------------
// Happy-path tests with mock HTTP server
// ---------------------------------------------------------------------------

func TestHandleVerifyToken_HappyPath(t *testing.T) {
	defer setupCFMock(t, cfSuccess(map[string]any{
		"id":       "tok-123",
		"status":   "active",
		"policies": []any{},
	}))()

	result, err := handleVerifyToken(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "active") {
		t.Errorf("expected 'active' in output, got: %s", text)
	}
}

func TestHandleListZones_HappyPath(t *testing.T) {
	defer setupCFMock(t, cfSuccess([]any{
		map[string]any{"id": "zone-1", "name": "example.com"},
		map[string]any{"id": "zone-2", "name": "test.com"},
	}))()

	result, err := handleListZones(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "count") {
		t.Errorf("expected count in output, got: %s", text)
	}
}

func TestHandleListZones_WithPagination(t *testing.T) {
	var capturedPath string
	defer setupCFMock(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.RawQuery
		cfSuccess([]any{})(w, r)
	})()

	_, err := handleListZones(context.Background(), map[string]any{
		"per_page": float64(10),
		"page":     float64(2),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedPath, "per_page=10") {
		t.Errorf("expected per_page=10 in query, got: %s", capturedPath)
	}
	if !strings.Contains(capturedPath, "page=2") {
		t.Errorf("expected page=2 in query, got: %s", capturedPath)
	}
}

func TestHandleListDNSRecords_HappyPath(t *testing.T) {
	defer setupCFMock(t, cfSuccess([]any{
		map[string]any{"id": "rec-1", "type": "A", "name": "www.example.com", "content": "1.2.3.4"},
	}))()

	result, err := handleListDNSRecords(context.Background(), map[string]any{
		"zone_id": "zone-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "records") {
		t.Errorf("expected records in output, got: %s", text)
	}
}

func TestHandleCreateDNSRecord_HappyPath(t *testing.T) {
	defer setupCFMock(t, cfSuccess(map[string]any{
		"id":      "rec-new",
		"type":    "A",
		"name":    "www.example.com",
		"content": "1.2.3.4",
	}))()

	result, err := handleCreateDNSRecord(context.Background(), map[string]any{
		"zone_id": "zone-1",
		"type":    "a",
		"name":    "www.example.com",
		"content": "1.2.3.4",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "created") {
		t.Errorf("expected 'created' in output, got: %s", text)
	}
}

func TestHandleDeleteDNSRecord_HappyPath(t *testing.T) {
	defer setupCFMock(t, cfSuccess(map[string]any{"id": "rec-1"}))()

	result, err := handleDeleteDNSRecord(context.Background(), map[string]any{
		"zone_id":   "zone-1",
		"record_id": "rec-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "deleted") {
		t.Errorf("expected 'deleted' in output, got: %s", text)
	}
}

func TestHandlePurgeCache_HappyPath(t *testing.T) {
	defer setupCFMock(t, cfSuccess(map[string]any{"id": "purge-1"}))()

	result, err := handlePurgeCache(context.Background(), map[string]any{
		"zone_id":   "zone-1",
		"purge_all": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "purge") || !strings.Contains(text, "initiated") {
		t.Errorf("expected purge message, got: %s", text)
	}
}

// ---------------------------------------------------------------------------
// API error test
// ---------------------------------------------------------------------------

func TestHandleListZones_APIError(t *testing.T) {
	defer setupCFMock(t, cfError(403, []any{
		map[string]any{"code": 9109, "message": "Invalid access token"},
	}))()

	result, err := handleListZones(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for API error")
	}
}
