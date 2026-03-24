package monitor

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// codebaseStubCaller implements bridge.Caller for codebase monitor tests.
type codebaseStubCaller struct {
	callToolFn func(name string, args map[string]any) (json.RawMessage, error)
}

func (s *codebaseStubCaller) Call(string, any) (json.RawMessage, error) { return nil, nil }
func (s *codebaseStubCaller) CallWithTimeout(string, any, time.Duration) (json.RawMessage, error) {
	return nil, nil
}
func (s *codebaseStubCaller) CallTool(name string, args map[string]any) (json.RawMessage, error) {
	if s.callToolFn != nil {
		return s.callToolFn(name, args)
	}
	return nil, fmt.Errorf("unexpected CallTool for %s", name)
}
func (s *codebaseStubCaller) CallToolWithTimeout(name string, args map[string]any, _ time.Duration) (json.RawMessage, error) {
	return s.CallTool(name, args)
}
func (s *codebaseStubCaller) CircuitOpen() bool { return false }
func (s *codebaseStubCaller) Close() error      { return nil }

func codebaseMCPResult(payload string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"content":[{"type":"text","text":%s}]}`, payload))
}

func TestCodebaseMonitor_Refresh(t *testing.T) {
	caller := &codebaseStubCaller{
		callToolFn: func(name string, _ map[string]any) (json.RawMessage, error) {
			if name != "codebase_memory__codebase_stats" {
				t.Fatalf("unexpected tool: %s", name)
			}
			return codebaseMCPResult(`"{\"total_files\":200,\"total_symbols\":5000,\"languages\":{\"go\":180,\"svelte\":20},\"last_indexed\":\"2026-03-24T12:00:00Z\",\"index_status\":\"ready\"}"`), nil
		},
	}

	agent := bridge.NewAgentBridge(caller)
	m := NewCodebaseMonitor(agent, slog.Default())

	if err := m.Refresh(); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	snap := m.Status()
	if snap.TotalFiles != 200 {
		t.Fatalf("expected 200 files, got %d", snap.TotalFiles)
	}
	if snap.TotalSymbols != 5000 {
		t.Fatalf("expected 5000 symbols, got %d", snap.TotalSymbols)
	}
	if snap.Languages["go"] != 180 {
		t.Fatalf("expected 180 go files, got %d", snap.Languages["go"])
	}
	if snap.IndexStatus != "ready" {
		t.Fatalf("expected index_status 'ready', got %q", snap.IndexStatus)
	}
	if snap.UpdatedAt.IsZero() {
		t.Fatal("expected non-zero UpdatedAt")
	}
}

func TestCodebaseMonitor_RefreshError(t *testing.T) {
	caller := &codebaseStubCaller{
		callToolFn: func(string, map[string]any) (json.RawMessage, error) {
			return nil, fmt.Errorf("transport closed")
		},
	}

	agent := bridge.NewAgentBridge(caller)
	m := NewCodebaseMonitor(agent, slog.Default())

	err := m.Refresh()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Snapshot should remain zero-valued.
	snap := m.Status()
	if snap.TotalFiles != 0 {
		t.Fatalf("expected 0 files after error, got %d", snap.TotalFiles)
	}
}

func TestCodebaseMonitor_OnRefreshCallback(t *testing.T) {
	caller := &codebaseStubCaller{
		callToolFn: func(string, map[string]any) (json.RawMessage, error) {
			return codebaseMCPResult(`"{\"total_files\":10,\"total_symbols\":100,\"languages\":{},\"last_indexed\":\"\",\"index_status\":\"indexing\"}"`), nil
		},
	}

	agent := bridge.NewAgentBridge(caller)
	m := NewCodebaseMonitor(agent, slog.Default())

	called := false
	m.OnRefresh(func(snap CodebaseSnapshot) {
		called = true
		if snap.IndexStatus != "indexing" {
			t.Errorf("expected 'indexing', got %q", snap.IndexStatus)
		}
	})

	if err := m.Refresh(); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !called {
		t.Fatal("OnRefresh callback was not invoked")
	}
}
