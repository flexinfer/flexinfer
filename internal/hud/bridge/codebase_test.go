package bridge

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// stubCaller is a minimal Caller that routes CallTool to a configurable function.
type stubCaller struct {
	callToolFn func(name string, args map[string]any) (json.RawMessage, error)
}

func (s *stubCaller) Call(string, any) (json.RawMessage, error) { return nil, nil }
func (s *stubCaller) CallWithTimeout(string, any, time.Duration) (json.RawMessage, error) {
	return nil, nil
}
func (s *stubCaller) CallTool(name string, args map[string]any) (json.RawMessage, error) {
	if s.callToolFn != nil {
		return s.callToolFn(name, args)
	}
	return nil, fmt.Errorf("unexpected CallTool for %s", name)
}
func (s *stubCaller) CallToolWithTimeout(name string, args map[string]any, _ time.Duration) (json.RawMessage, error) {
	return s.CallTool(name, args)
}
func (s *stubCaller) CircuitOpen() bool { return false }
func (s *stubCaller) Close() error      { return nil }

func mcpTextResult(payload string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"content":[{"type":"text","text":%s}]}`, payload))
}

func TestCodebaseStats(t *testing.T) {
	caller := &stubCaller{
		callToolFn: func(name string, _ map[string]any) (json.RawMessage, error) {
			if name != "codebase_memory__codebase_stats" {
				t.Fatalf("unexpected tool name: %s", name)
			}
			return mcpTextResult(`"{\"total_files\":150,\"total_symbols\":3200,\"languages\":{\"go\":120,\"svelte\":30},\"last_indexed\":\"2026-03-24T10:00:00Z\",\"index_status\":\"ready\"}"`), nil
		},
	}
	b := NewAgentBridge(caller)
	stats, err := b.CodebaseStats()
	if err != nil {
		t.Fatalf("CodebaseStats: %v", err)
	}
	if stats.TotalFiles != 150 {
		t.Fatalf("expected 150 files, got %d", stats.TotalFiles)
	}
	if stats.TotalSymbols != 3200 {
		t.Fatalf("expected 3200 symbols, got %d", stats.TotalSymbols)
	}
	if stats.Languages["go"] != 120 {
		t.Fatalf("expected 120 go files, got %d", stats.Languages["go"])
	}
	if stats.IndexStatus != "ready" {
		t.Fatalf("expected index_status 'ready', got %q", stats.IndexStatus)
	}
}

func TestCodebaseSearch(t *testing.T) {
	caller := &stubCaller{
		callToolFn: func(name string, args map[string]any) (json.RawMessage, error) {
			if name != "codebase_memory__codebase_search" {
				t.Fatalf("unexpected tool name: %s", name)
			}
			if q, _ := args["query"].(string); q != "AgentBridge" {
				t.Fatalf("expected query 'AgentBridge', got %q", q)
			}
			return mcpTextResult(`"{\"results\":[{\"file_path\":\"internal/hud/bridge/agent.go\",\"symbol\":\"AgentBridge\",\"kind\":\"struct\",\"line\":31,\"score\":0.95,\"snippet\":\"type AgentBridge struct\"}]}"`), nil
		},
	}
	b := NewAgentBridge(caller)
	results, err := b.CodebaseSearch("AgentBridge", 10)
	if err != nil {
		t.Fatalf("CodebaseSearch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Symbol != "AgentBridge" {
		t.Fatalf("expected symbol 'AgentBridge', got %q", results[0].Symbol)
	}
	if results[0].Score != 0.95 {
		t.Fatalf("expected score 0.95, got %f", results[0].Score)
	}
}

func TestCodebaseTextSearch(t *testing.T) {
	caller := &stubCaller{
		callToolFn: func(name string, args map[string]any) (json.RawMessage, error) {
			if name != "codebase_memory__codebase_text_search" {
				t.Fatalf("unexpected tool name: %s", name)
			}
			if q, _ := args["query"].(string); q != "callTool" {
				t.Fatalf("expected query 'callTool', got %q", q)
			}
			return mcpTextResult(`"{\"results\":[{\"file_path\":\"internal/hud/bridge/agent.go\",\"symbol\":\"\",\"kind\":\"text\",\"line\":114,\"score\":1.0,\"snippet\":\"a.client.CallTool\"}]}"`), nil
		},
	}
	b := NewAgentBridge(caller)
	results, err := b.CodebaseTextSearch("callTool", 5)
	if err != nil {
		t.Fatalf("CodebaseTextSearch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].FilePath != "internal/hud/bridge/agent.go" {
		t.Fatalf("expected file_path, got %q", results[0].FilePath)
	}
}

func TestCodebaseIndexStart(t *testing.T) {
	caller := &stubCaller{
		callToolFn: func(name string, args map[string]any) (json.RawMessage, error) {
			if name != "codebase_memory__codebase_index_start" {
				t.Fatalf("unexpected tool name: %s", name)
			}
			if p, _ := args["path"].(string); p != "/workspace/loom-core" {
				t.Fatalf("expected path '/workspace/loom-core', got %q", p)
			}
			return mcpTextResult(`"{\"job_id\":\"idx-42\",\"status\":\"running\"}"`), nil
		},
	}
	b := NewAgentBridge(caller)
	job, err := b.CodebaseIndexStart("/workspace/loom-core")
	if err != nil {
		t.Fatalf("CodebaseIndexStart: %v", err)
	}
	if job.JobID != "idx-42" {
		t.Fatalf("expected job_id 'idx-42', got %q", job.JobID)
	}
	if job.Status != "running" {
		t.Fatalf("expected status 'running', got %q", job.Status)
	}
}

func TestCodebaseIndexPoll(t *testing.T) {
	caller := &stubCaller{
		callToolFn: func(name string, args map[string]any) (json.RawMessage, error) {
			if name != "codebase_memory__codebase_index_poll" {
				t.Fatalf("unexpected tool name: %s", name)
			}
			if jid, _ := args["job_id"].(string); jid != "idx-42" {
				t.Fatalf("expected job_id 'idx-42', got %q", jid)
			}
			return mcpTextResult(`"{\"job_id\":\"idx-42\",\"status\":\"completed\"}"`), nil
		},
	}
	b := NewAgentBridge(caller)
	job, err := b.CodebaseIndexPoll("idx-42")
	if err != nil {
		t.Fatalf("CodebaseIndexPoll: %v", err)
	}
	if job.JobID != "idx-42" {
		t.Fatalf("expected job_id 'idx-42', got %q", job.JobID)
	}
	if job.Status != "completed" {
		t.Fatalf("expected status 'completed', got %q", job.Status)
	}
}

func TestCodebaseSearch_DefaultLimit(t *testing.T) {
	caller := &stubCaller{
		callToolFn: func(name string, args map[string]any) (json.RawMessage, error) {
			if name != "codebase_memory__codebase_search" {
				t.Fatalf("unexpected tool name: %s", name)
			}
			lim, _ := args["limit"].(int)
			if lim != 20 {
				t.Fatalf("expected default limit 20, got %v", args["limit"])
			}
			return mcpTextResult(`"{\"results\":[]}"`), nil
		},
	}
	b := NewAgentBridge(caller)
	results, err := b.CodebaseSearch("test", 0) // 0 should default to 20
	if err != nil {
		t.Fatalf("CodebaseSearch: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestCodebaseStats_Error(t *testing.T) {
	caller := &stubCaller{
		callToolFn: func(string, map[string]any) (json.RawMessage, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}
	b := NewAgentBridge(caller)
	_, err := b.CodebaseStats()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
