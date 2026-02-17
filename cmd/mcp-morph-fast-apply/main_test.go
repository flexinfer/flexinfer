package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleEditFile_MissingParams(t *testing.T) {
	result, err := handleEditFile(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

func TestGetConfigDefaultsAndValidation(t *testing.T) {
	t.Setenv("MORPH_API_KEY", "")
	t.Setenv("MORPH_BASE_URL", "")
	t.Setenv("MORPH_MODEL", "")

	_, _, _, err := getConfig()
	if err == nil {
		t.Fatal("expected missing MORPH_API_KEY to fail")
	}

	t.Setenv("MORPH_API_KEY", "test-key")
	baseURL, apiKey, model, err := getConfig()
	if err != nil {
		t.Fatalf("unexpected config error: %v", err)
	}
	if baseURL != "https://api.morphllm.com/v1" {
		t.Fatalf("unexpected default base URL: %s", baseURL)
	}
	if model != "morph-v3-large" {
		t.Fatalf("unexpected default model: %s", model)
	}
	if apiKey != "test-key" {
		t.Fatalf("unexpected api key: %s", apiKey)
	}
}

func TestResolvePathWorkspaceAndTraversal(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("MORPH_WORKSPACE_ROOT", workspace)
	t.Setenv("WORKSPACE_ROOT", "")

	resolved, err := resolvePath("nested/file.go")
	if err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}
	expected := filepath.Join(workspace, "nested", "file.go")
	if resolved != expected {
		t.Fatalf("resolved path mismatch: got %q want %q", resolved, expected)
	}

	if _, err := resolvePath("../escape.go"); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
}

func TestHandleEditFileSuccess(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("MORPH_WORKSPACE_ROOT", workspace)
	t.Setenv("WORKSPACE_ROOT", "")
	t.Setenv("MORPH_API_KEY", "test-key")
	t.Setenv("MORPH_MODEL", "morph-test")

	filePath := filepath.Join(workspace, "test.go")
	if err := os.WriteFile(filePath, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected auth header: %s", got)
		}
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		msgs, _ := reqBody["messages"].([]any)
		if len(msgs) == 0 {
			t.Fatal("expected at least one message")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"package main\n\nfunc main(){}\n"}}],"usage":{"total_tokens":42}}`))
	}))
	defer server.Close()
	t.Setenv("MORPH_BASE_URL", server.URL)

	result, err := handleEditFile(context.Background(), map[string]any{
		"path":        "test.go",
		"instruction": "add main",
		"update":      "func main(){}",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].Text)
	}
	updated, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read updated file: %v", err)
	}
	if !strings.Contains(string(updated), "func main(){}") {
		t.Fatalf("expected edited content to be written, got: %s", string(updated))
	}
}

func TestHandleEditFileAPIErrors(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("MORPH_WORKSPACE_ROOT", workspace)
	t.Setenv("WORKSPACE_ROOT", "")
	t.Setenv("MORPH_API_KEY", "test-key")

	filePath := filepath.Join(workspace, "test.go")
	if err := os.WriteFile(filePath, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()
	t.Setenv("MORPH_BASE_URL", server.URL)

	result, err := handleEditFile(context.Background(), map[string]any{
		"path":        "test.go",
		"instruction": "edit",
		"update":      "edit",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected API error result")
	}
	if !strings.Contains(result.Content[0].Text, "UNAUTHORIZED") {
		t.Fatalf("expected auth error in text, got: %s", result.Content[0].Text)
	}
}

func TestHandleEditFileNoChoices(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("MORPH_WORKSPACE_ROOT", workspace)
	t.Setenv("WORKSPACE_ROOT", "")
	t.Setenv("MORPH_API_KEY", "test-key")

	filePath := filepath.Join(workspace, "test.go")
	if err := os.WriteFile(filePath, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[],"usage":{"total_tokens":1}}`))
	}))
	defer server.Close()
	t.Setenv("MORPH_BASE_URL", server.URL)

	result, err := handleEditFile(context.Background(), map[string]any{
		"path":        "test.go",
		"instruction": "edit",
		"update":      "edit",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected no-choices error")
	}
	if !strings.Contains(result.Content[0].Text, "no response from Morph API") {
		t.Fatalf("expected no-response error, got: %s", result.Content[0].Text)
	}
}

func TestHandleEditFileRejectsInvalidPath(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("MORPH_WORKSPACE_ROOT", workspace)
	t.Setenv("WORKSPACE_ROOT", "")
	t.Setenv("MORPH_API_KEY", "test-key")

	result, err := handleEditFile(context.Background(), map[string]any{
		"path":        "../escape.go",
		"instruction": "edit",
		"update":      "edit",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected invalid-path error")
	}
	if !strings.Contains(result.Content[0].Text, "invalid path") {
		t.Fatalf("expected invalid path error, got: %s", result.Content[0].Text)
	}
}

func TestHandleEditFile_MissingInstruction(t *testing.T) {
	result, err := handleEditFile(context.Background(), map[string]any{
		"path": "test.go",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing instruction")
	}
}

func TestHandleEditFile_MissingUpdate(t *testing.T) {
	result, err := handleEditFile(context.Background(), map[string]any{
		"path":        "test.go",
		"instruction": "fix the bug",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing update")
	}
}
