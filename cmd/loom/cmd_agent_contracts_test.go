package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func TestActiveSessionWithFallbackUsesSharedSessionRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LOOM_HUD_URL", "")
	t.Setenv("LOOM_HUD_CF_ACCESS_ID", "")
	t.Setenv("LOOM_HUD_CF_ACCESS_SECRET", "")
	hudConfigOnce.loaded = false
	defer func() { hudConfigOnce.loaded = false }()

	var gotPath string
	var gotQuery url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		_, _ = w.Write([]byte(`{"session":{"id":"sess-1"}}`))
	}))
	defer ts.Close()

	t.Setenv("LOOM_HUD_URL", ts.URL)

	if _, err := activeSessionWithFallback(nil, "3333", " codex-1 "); err != nil {
		t.Fatalf("activeSessionWithFallback() error: %v", err)
	}
	if gotPath != bridge.AgentSessionEndpoint {
		t.Fatalf("expected path %q, got %q", bridge.AgentSessionEndpoint, gotPath)
	}
	if got := gotQuery.Get("agent_id"); got != "codex-1" {
		t.Fatalf("expected trimmed agent_id, got %q", got)
	}
}

func TestTaskUpdateWithFallbackUsesSharedTaskUpdateRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LOOM_HUD_URL", "")
	t.Setenv("LOOM_HUD_CF_ACCESS_ID", "")
	t.Setenv("LOOM_HUD_CF_ACCESS_SECRET", "")
	hudConfigOnce.loaded = false
	defer func() { hudConfigOnce.loaded = false }()

	var gotPath string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"status":"updated"}`))
	}))
	defer ts.Close()

	t.Setenv("LOOM_HUD_URL", ts.URL)

	req := bridge.TaskUpdateRequest{
		TaskID:     " task-1 ",
		Status:     " completed ",
		Resolution: " done ",
	}
	if _, err := updateTaskWithFallback(nil, "3333", req); err != nil {
		t.Fatalf("updateTaskWithFallback() error: %v", err)
	}
	if gotPath != bridge.AgentTaskUpdateEndpoint {
		t.Fatalf("expected path %q, got %q", bridge.AgentTaskUpdateEndpoint, gotPath)
	}
	if got := gotBody["task_id"]; got != "task-1" {
		t.Fatalf("expected trimmed task_id in HUD payload, got %#v", got)
	}
	if got := gotBody["status"]; got != "completed" {
		t.Fatalf("expected trimmed status in HUD payload, got %#v", got)
	}
	if got := gotBody["resolution"]; got != "done" {
		t.Fatalf("expected trimmed resolution in HUD payload, got %#v", got)
	}
}

func TestAgentDispatchCmdUsesSharedDispatchRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LOOM_HUD_URL", "")
	t.Setenv("LOOM_HUD_CF_ACCESS_ID", "")
	t.Setenv("LOOM_HUD_CF_ACCESS_SECRET", "")
	hudConfigOnce.loaded = false
	defer func() { hudConfigOnce.loaded = false }()

	var gotPath string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer ts.Close()

	t.Setenv("LOOM_HUD_URL", ts.URL)

	cmd := newAgentDispatchCmd()
	cmd.SetArgs([]string{
		"--to", " codex-1 ",
		"--title", "  Fix it  ",
		"--context", "  notes  ",
		"--priority", "HIGH",
		"--tag", " team ",
		"--tag", "team",
		"--tag", "gitops",
		"--file", " pkg/task.go ",
		"--line", "-2",
		"--blocked-by", " task-1 ",
		"--blocked-by", "task-1",
		"--blocked-by", " task-2 ",
		"--quiet",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dispatch command error: %v", err)
	}

	if gotPath != bridge.AgentDispatchEndpoint {
		t.Fatalf("expected path %q, got %q", bridge.AgentDispatchEndpoint, gotPath)
	}
	if got := gotBody["target_agent_id"]; got != "codex-1" {
		t.Fatalf("expected trimmed target_agent_id, got %#v", got)
	}
	if got := gotBody["title"]; got != "Fix it" {
		t.Fatalf("expected trimmed title, got %#v", got)
	}
	if got := gotBody["context"]; got != "notes" {
		t.Fatalf("expected trimmed context, got %#v", got)
	}
	if got := gotBody["priority"]; got != "high" {
		t.Fatalf("expected normalized priority, got %#v", got)
	}
	if got := gotBody["file_path"]; got != "pkg/task.go" {
		t.Fatalf("expected trimmed file_path, got %#v", got)
	}
	if _, ok := gotBody["line_number"]; ok {
		t.Fatalf("expected line_number to be omitted after normalization, got %#v", gotBody["line_number"])
	}
	tags, ok := gotBody["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "team" || tags[1] != "gitops" {
		t.Fatalf("unexpected tags payload: %#v", gotBody["tags"])
	}
	blockedBy, ok := gotBody["blocked_by"].([]any)
	if !ok || len(blockedBy) != 2 || blockedBy[0] != "task-1" || blockedBy[1] != "task-2" {
		t.Fatalf("unexpected blocked_by payload: %#v", gotBody["blocked_by"])
	}
}
