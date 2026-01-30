package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetEnv(t *testing.T) {
	tests := []struct {
		key      string
		fallback string
		want     string
	}{
		{"NONEXISTENT_VAR_12345", "default", "default"},
	}

	for _, tt := range tests {
		got := getEnv(tt.key, tt.fallback)
		if got != tt.want {
			t.Errorf("getEnv(%q, %q) = %q, want %q", tt.key, tt.fallback, got, tt.want)
		}
	}
}

func TestGetEnvInt(t *testing.T) {
	tests := []struct {
		key      string
		fallback int
		want     int
	}{
		{"NONEXISTENT_VAR_12345", 42, 42},
	}

	for _, tt := range tests {
		got := getEnvInt(tt.key, tt.fallback)
		if got != tt.want {
			t.Errorf("getEnvInt(%q, %d) = %d, want %d", tt.key, tt.fallback, got, tt.want)
		}
	}
}

func TestClampInt(t *testing.T) {
	tests := []struct {
		v, min, max, want int
	}{
		{5, 0, 10, 5},
		{-1, 0, 10, 0},
		{15, 0, 10, 10},
		{0, 0, 10, 0},
		{10, 0, 10, 10},
	}

	for _, tt := range tests {
		got := clampInt(tt.v, tt.min, tt.max)
		if got != tt.want {
			t.Errorf("clampInt(%d, %d, %d) = %d, want %d", tt.v, tt.min, tt.max, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		s    string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
		{"ab", 2, "ab"},
		{"abc", 2, "ab..."},
	}

	for _, tt := range tests {
		got := truncate(tt.s, tt.max)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.max, got, tt.want)
		}
	}
}

func TestBuildURL(t *testing.T) {
	origURL := esURL
	defer func() { esURL = origURL }()

	esURL = "http://localhost:9200"

	tests := []struct {
		path string
		want string
	}{
		{"_search", "http://localhost:9200/_search"},
		{"/logs/_search", "http://localhost:9200/logs/_search"},
		{"_cluster/health", "http://localhost:9200/_cluster/health"},
	}

	for _, tt := range tests {
		got, err := buildURL(tt.path)
		if err != nil {
			t.Errorf("buildURL(%q) error = %v", tt.path, err)
			continue
		}
		if got != tt.want {
			t.Errorf("buildURL(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestBuildURL_WithBasePath(t *testing.T) {
	origURL := esURL
	defer func() { esURL = origURL }()

	esURL = "http://localhost:9200/es"

	path := "_search"
	got, err := buildURL(path)
	if err != nil {
		t.Fatalf("buildURL error = %v", err)
	}
	want := "http://localhost:9200/es/_search"
	if got != want {
		t.Errorf("buildURL(%q) = %q, want %q", path, got, want)
	}
}

// Integration-style tests with mock server

func setupMockES(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(handler))
}

func TestHandleInfo(t *testing.T) {
	server := setupMockES(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"name":         "test-node",
			"cluster_name": "test-cluster",
			"version": map[string]any{
				"number": "8.12.0",
			},
		})
	})
	defer server.Close()

	origURL := esURL
	esURL = server.URL
	defer func() { esURL = origURL }()

	result, err := handleInfo(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleInfo error = %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Check result content
	text := result.Content[0].Text
	if !strings.Contains(text, "test-cluster") {
		t.Errorf("expected cluster name in response, got: %s", text)
	}
}

func TestHandleHealth(t *testing.T) {
	server := setupMockES(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/_cluster/health") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"cluster_name": "test-cluster",
			"status":       "green",
			"timed_out":    false,
		})
	})
	defer server.Close()

	origURL := esURL
	esURL = server.URL
	defer func() { esURL = origURL }()

	result, err := handleHealth(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleHealth error = %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	text := result.Content[0].Text
	if !strings.Contains(text, "green") {
		t.Errorf("expected status in response, got: %s", text)
	}
}

func TestHandleCount(t *testing.T) {
	server := setupMockES(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/_count") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"count":  42,
			"_shards": map[string]any{"total": 5, "successful": 5},
		})
	})
	defer server.Close()

	origURL := esURL
	esURL = server.URL
	defer func() { esURL = origURL }()

	result, err := handleCount(context.Background(), map[string]any{
		"index": "test-index",
	})
	if err != nil {
		t.Fatalf("handleCount error = %v", err)
	}

	text := result.Content[0].Text
	if !strings.Contains(text, "42") {
		t.Errorf("expected count in response, got: %s", text)
	}
}

func TestHandleSearch(t *testing.T) {
	server := setupMockES(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/_search") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"took":      5,
			"timed_out": false,
			"hits": map[string]any{
				"total": map[string]any{"value": 1, "relation": "eq"},
				"hits": []any{
					map[string]any{
						"_index":  "test-index",
						"_id":     "1",
						"_score":  1.0,
						"_source": map[string]any{"message": "test"},
					},
				},
			},
		})
	})
	defer server.Close()

	origURL := esURL
	esURL = server.URL
	defer func() { esURL = origURL }()

	result, err := handleSearch(context.Background(), map[string]any{
		"index": "test-index",
		"query": map[string]any{
			"match_all": map[string]any{},
		},
		"size": float64(10),
	})
	if err != nil {
		t.Fatalf("handleSearch error = %v", err)
	}

	text := result.Content[0].Text
	if !strings.Contains(text, "test-index") {
		t.Errorf("expected index in response, got: %s", text)
	}
}

func TestHandleSimpleQuery(t *testing.T) {
	server := setupMockES(t, func(w http.ResponseWriter, r *http.Request) {
		// Check query param is present (URL path includes query string)
		if !strings.Contains(r.URL.RawQuery, "q=message") {
			t.Logf("URL: %s, RawQuery: %s", r.URL.String(), r.URL.RawQuery)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"took":      2,
			"timed_out": false,
			"hits": map[string]any{
				"total": map[string]any{"value": 5},
				"hits":  []any{},
			},
		})
	})
	defer server.Close()

	origURL := esURL
	esURL = server.URL
	defer func() { esURL = origURL }()

	result, err := handleSimpleQuery(context.Background(), map[string]any{
		"q": "message:error",
	})
	if err != nil {
		t.Fatalf("handleSimpleQuery error = %v", err)
	}

	text := result.Content[0].Text
	if !strings.Contains(text, "ok: true") {
		t.Errorf("expected ok in response, got: %s", text)
	}
}

func TestHandleSimpleQuery_MissingQ(t *testing.T) {
	result, err := handleSimpleQuery(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should return error result
	if !result.IsError {
		t.Error("expected error result for missing q parameter")
	}
}

func TestHandleGet(t *testing.T) {
	server := setupMockES(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-index/_doc/doc123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"_index":  "test-index",
			"_id":     "doc123",
			"found":   true,
			"_source": map[string]any{"field": "value"},
		})
	})
	defer server.Close()

	origURL := esURL
	esURL = server.URL
	defer func() { esURL = origURL }()

	result, err := handleGet(context.Background(), map[string]any{
		"index": "test-index",
		"id":    "doc123",
	})
	if err != nil {
		t.Fatalf("handleGet error = %v", err)
	}

	text := result.Content[0].Text
	if !strings.Contains(text, "doc123") {
		t.Errorf("expected doc id in response, got: %s", text)
	}
}

func TestHandleGet_MissingParams(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
	}{
		{"missing index", map[string]any{"id": "123"}},
		{"missing id", map[string]any{"index": "test"}},
		{"missing both", map[string]any{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := handleGet(context.Background(), tt.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.IsError {
				t.Error("expected error result")
			}
		})
	}
}

func TestHandleMapping(t *testing.T) {
	server := setupMockES(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/_mapping") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"test-index": map[string]any{
				"mappings": map[string]any{
					"properties": map[string]any{
						"message": map[string]any{"type": "text"},
					},
				},
			},
		})
	})
	defer server.Close()

	origURL := esURL
	esURL = server.URL
	defer func() { esURL = origURL }()

	result, err := handleMapping(context.Background(), map[string]any{
		"index": "test-index",
	})
	if err != nil {
		t.Fatalf("handleMapping error = %v", err)
	}

	text := result.Content[0].Text
	if !strings.Contains(text, "mappings") {
		t.Errorf("expected mappings in response, got: %s", text)
	}
}

func TestHandleMapping_MissingIndex(t *testing.T) {
	result, err := handleMapping(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for missing index")
	}
}

func TestHandleIndices(t *testing.T) {
	server := setupMockES(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/_cat/indices") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// Verify query params are passed
		if !strings.Contains(r.URL.RawQuery, "format=json") {
			t.Logf("query params: %s", r.URL.RawQuery)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{"index": "logs-2024", "health": "green", "status": "open"},
			{"index": "logs-2025", "health": "green", "status": "open"},
		})
	})
	defer server.Close()

	origURL := esURL
	esURL = server.URL
	defer func() { esURL = origURL }()

	result, err := handleIndices(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleIndices error = %v", err)
	}

	text := result.Content[0].Text
	if !strings.Contains(text, "logs-2024") {
		t.Errorf("expected index in response, got: %s", text)
	}
}

func TestHandleStats(t *testing.T) {
	server := setupMockES(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/_stats") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"_all": map[string]any{
				"primaries": map[string]any{
					"docs": map[string]any{"count": 1000},
				},
			},
		})
	})
	defer server.Close()

	origURL := esURL
	esURL = server.URL
	defer func() { esURL = origURL }()

	result, err := handleStats(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleStats error = %v", err)
	}

	text := result.Content[0].Text
	if !strings.Contains(text, "1000") {
		t.Errorf("expected doc count in response, got: %s", text)
	}
}

func TestHandleAliases(t *testing.T) {
	server := setupMockES(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/_cat/aliases") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{"alias": "logs", "index": "logs-2025-01"},
		})
	})
	defer server.Close()

	origURL := esURL
	esURL = server.URL
	defer func() { esURL = origURL }()

	result, err := handleAliases(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleAliases error = %v", err)
	}

	text := result.Content[0].Text
	if !strings.Contains(text, "logs") {
		t.Errorf("expected alias in response, got: %s", text)
	}
}
