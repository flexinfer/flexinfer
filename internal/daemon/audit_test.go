package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAudit_DisabledReturnsNil(t *testing.T) {
	cfg := DefaultAuditConfig()
	a, err := NewAuditLogger(cfg, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a != nil {
		t.Fatal("expected nil audit logger when disabled")
	}
}

func TestAudit_WritesJSONL(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")

	cfg := AuditConfig{
		Enabled: true,
		LogPath: logPath,
	}

	a, err := NewAuditLogger(cfg, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer a.Close()

	now := time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)

	entries := []AuditEntry{
		{
			Timestamp:  now,
			AgentID:    "claude-code",
			Server:     "git",
			Tool:       "git_status",
			DurationMs: 42,
			Status:     "success",
			Target:     "local",
		},
		{
			Timestamp:  now.Add(time.Second),
			AgentID:    "codex",
			AgentType:  "codex",
			Server:     "k8s_apps_k3s",
			Tool:       "k8s_apply",
			DurationMs: 0,
			Status:     "denied",
			Error:      "RBAC denied",
		},
		{
			Timestamp:  now.Add(2 * time.Second),
			AgentID:    "claude-code",
			Server:     "gitlab",
			Tool:       "list_issues",
			DurationMs: 350,
			Status:     "error",
			Error:      "connection refused",
			Target:     "hub",
		},
	}

	for _, e := range entries {
		a.Log(e)
	}

	decoded, err := a.ReadEntries(AuditReadOptions{})
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}

	if len(decoded) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(decoded))
	}

	// Verify first entry
	if decoded[0].AgentID != "claude-code" {
		t.Errorf("entry[0] agent_id: got %q, want %q", decoded[0].AgentID, "claude-code")
	}
	if decoded[0].Status != "success" {
		t.Errorf("entry[0] status: got %q, want %q", decoded[0].Status, "success")
	}
	if decoded[0].DurationMs != 42 {
		t.Errorf("entry[0] duration_ms: got %d, want 42", decoded[0].DurationMs)
	}

	// Verify denied entry
	if decoded[1].Status != "denied" {
		t.Errorf("entry[1] status: got %q, want %q", decoded[1].Status, "denied")
	}
	if decoded[1].Error != "RBAC denied" {
		t.Errorf("entry[1] error: got %q, want %q", decoded[1].Error, "RBAC denied")
	}

	// Verify error entry
	if decoded[2].Status != "error" {
		t.Errorf("entry[2] status: got %q, want %q", decoded[2].Status, "error")
	}
	if decoded[2].Target != "hub" {
		t.Errorf("entry[2] target: got %q, want %q", decoded[2].Target, "hub")
	}
}

func TestAudit_AppendBehavior(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")

	cfg := AuditConfig{
		Enabled: true,
		LogPath: logPath,
	}

	// Write one entry, close
	a1, err := NewAuditLogger(cfg, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a1.Log(AuditEntry{AgentID: "first", Server: "s", Tool: "t", Status: "success"})
	a1.Close()

	// Reopen and write another entry
	a2, err := NewAuditLogger(cfg, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a2.Log(AuditEntry{AgentID: "second", Server: "s", Tool: "t", Status: "success"})
	a2.Close()

	entries, err := ReadAuditEntries(logPath, AuditReadOptions{})
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries (append), got %d", len(entries))
	}
}

func TestAudit_DefaultTimestamp(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")

	cfg := AuditConfig{
		Enabled: true,
		LogPath: logPath,
	}

	a, err := NewAuditLogger(cfg, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer a.Close()

	before := time.Now().UTC()
	a.Log(AuditEntry{AgentID: "test", Server: "s", Tool: "t", Status: "success"})
	after := time.Now().UTC()

	f, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()

	var entry AuditEntry
	if err := json.NewDecoder(f).Decode(&entry); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if entry.Timestamp.Before(before) || entry.Timestamp.After(after) {
		t.Errorf("timestamp %v not between %v and %v", entry.Timestamp, before, after)
	}
}

func TestAudit_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "nested", "deep", "audit.jsonl")

	cfg := AuditConfig{
		Enabled: true,
		LogPath: logPath,
	}

	a, err := NewAuditLogger(cfg, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer a.Close()

	a.Log(AuditEntry{AgentID: "test", Server: "s", Tool: "t", Status: "success"})

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Fatal("expected log file to exist in nested directory")
	}
}

func TestAudit_ConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit-concurrent.jsonl")

	cfg := AuditConfig{
		Enabled: true,
		LogPath: logPath,
	}

	a, err := NewAuditLogger(cfg, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer a.Close()

	const goroutines = 50
	const entriesPerGoroutine = 10
	const totalEntries = goroutines * entriesPerGoroutine

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < entriesPerGoroutine; j++ {
				a.Log(AuditEntry{
					AgentID: fmt.Sprintf("agent-%d", n),
					Server:  "test-server",
					Tool:    fmt.Sprintf("tool-%d", j),
					Status:  "success",
				})
			}
		}(i)
	}
	wg.Wait()

	entries, err := a.ReadEntries(AuditReadOptions{})
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if len(entries) != totalEntries {
		t.Errorf("expected %d entries, got %d", totalEntries, len(entries))
	}
}

func TestAudit_SummaryUsesStatusAndAgentFields(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit-summary.jsonl")

	a, err := NewAuditLogger(AuditConfig{Enabled: true, LogPath: logPath}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer a.Close()

	now := time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC)
	a.Log(AuditEntry{
		Timestamp:  now,
		AgentID:    "codex",
		AgentType:  "worker",
		Server:     "git",
		Tool:       "status",
		DurationMs: 12,
		Status:     "success",
	})
	a.Log(AuditEntry{
		Timestamp: now.Add(time.Second),
		AgentID:   "codex",
		Server:    "gitlab",
		Tool:      "push",
		Status:    "error",
		Error:     "denied",
	})
	a.Log(AuditEntry{
		Timestamp: now.Add(2 * time.Second),
		AgentID:   "claude",
		Server:    "github",
		Tool:      "list_prs",
		Status:    "success",
	})

	summary, err := a.Summary(AuditReadOptions{Limit: 2})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}

	if summary.TotalEvents != 2 {
		t.Fatalf("summary total_events = %d, want 2", summary.TotalEvents)
	}
	if summary.ByEventType["success"] != 1 || summary.ByEventType["error"] != 1 {
		t.Fatalf("unexpected by_event_type: %+v", summary.ByEventType)
	}
	if summary.ByActorID["codex"] != 1 || summary.ByActorID["claude"] != 1 {
		t.Fatalf("unexpected by_actor_id: %+v", summary.ByActorID)
	}
	if summary.OldestTimestamp == nil || *summary.OldestTimestamp != now.Add(time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("unexpected oldest timestamp: %+v", summary.OldestTimestamp)
	}
	if summary.NewestTimestamp == nil || *summary.NewestTimestamp != now.Add(2*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("unexpected newest timestamp: %+v", summary.NewestTimestamp)
	}
}

func TestAudit_CloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit-close.jsonl")

	cfg := AuditConfig{
		Enabled: true,
		LogPath: logPath,
	}

	a, err := NewAuditLogger(cfg, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a.Log(AuditEntry{AgentID: "test", Server: "s", Tool: "t", Status: "success"})

	// First close should succeed.
	if err := a.Close(); err != nil {
		t.Fatalf("first Close() failed: %v", err)
	}

	// Second close may return an error but must not panic.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("second Close() panicked: %v", r)
			}
		}()
		_ = a.Close() // error is acceptable, panic is not
	}()
}
