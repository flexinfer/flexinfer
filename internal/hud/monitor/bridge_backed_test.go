package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

type mockHandlers struct {
	mu      sync.RWMutex
	methods map[string]func(json.RawMessage) (any, error)
}

func (m *mockHandlers) handle(method string, fn func(json.RawMessage) (any, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.methods[method] = fn
}

func (m *mockHandlers) handlerFor(method string) (func(json.RawMessage) (any, error), bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	fn, ok := m.methods[method]
	return fn, ok
}

func mockDaemon(t *testing.T) (string, *mockHandlers) {
	t.Helper()

	dir, err := os.MkdirTemp("", "loom-monitor-test-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, "d.sock")
	lc := net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	handlers := &mockHandlers{methods: make(map[string]func(json.RawMessage) (any, error))}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				transport := mcp.NewStdioTransport(c, c)
				for {
					msg, err := transport.Recv(context.Background())
					if err != nil {
						return
					}
					if !msg.IsRequest() {
						continue
					}
					fn, ok := handlers.handlerFor(msg.Method)
					if !ok {
						_ = transport.Send(context.Background(), mcp.NewErrorResponse(msg.ID, mcp.MethodNotFound, fmt.Sprintf("unknown method: %s", msg.Method)))
						continue
					}
					result, err := fn(msg.Params)
					if err != nil {
						_ = transport.Send(context.Background(), mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error()))
						continue
					}
					resp, err := mcp.NewResponse(msg.ID, result)
					if err != nil {
						_ = transport.Send(context.Background(), mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error()))
						continue
					}
					_ = transport.Send(context.Background(), resp)
				}
			}(conn)
		}
	}()

	return sockPath, handlers
}

func newBridges(t *testing.T, sockPath string) (*bridge.DaemonClient, *bridge.AgentBridge) {
	t.Helper()
	client := bridge.NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, bridge.NewAgentBridge(client)
}

func toolEnvelope(payload any) map[string]any {
	b, _ := json.Marshal(payload)
	return map[string]any{
		"isError": false,
		"content": []map[string]any{
			{"type": "text", "text": string(b)},
		},
	}
}

func TestMemoryMonitor_BridgeBackedRefreshAndMutations(t *testing.T) {
	sockPath, handlers := mockDaemon(t)
	_, agent := newBridges(t, sockPath)

	var statsCalls int
	var promoteCalls int
	var demoteCalls int

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		switch req.Name {
		case "agent_context__agent_memory_stats":
			statsCalls++
			totalItems := 9
			if statsCalls > 1 {
				totalItems = 10
			}
			return toolEnvelope(map[string]any{
				"working_memory":    map[string]any{"item_count": 2, "token_count": 20},
				"short_term_memory": map[string]any{"item_count": 3, "token_count": 30},
				"long_term_memory":  map[string]any{"item_count": 4, "token_count": 40},
				"total_items":       totalItems,
				"total_tokens":      90,
			}), nil
		case "agent_context__agent_memory_recall":
			return toolEnvelope(map[string]any{
				"items": []map[string]any{
					{"id": "mem-1", "title": "first", "tier": "working", "importance": "medium", "importance_score": 0.5, "original_tokens": 10},
				},
			}), nil
		case "agent_context__agent_memory_promote":
			promoteCalls++
			return toolEnvelope(map[string]any{"ok": true}), nil
		case "agent_context__agent_memory_demote":
			demoteCalls++
			return toolEnvelope(map[string]any{"ok": true}), nil
		default:
			return nil, fmt.Errorf("unexpected tool: %s", req.Name)
		}
	})

	monitor := NewMemoryMonitor(agent, nil)
	var refreshCount int
	monitor.OnRefresh(func(_ *bridge.MemoryStatsResult) { refreshCount++ })

	if err := monitor.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if stats := monitor.Stats(); stats == nil || stats.TotalItems != 9 {
		t.Fatalf("unexpected stats after first refresh: %#v", stats)
	}
	statsCopy := monitor.Stats()
	statsCopy.TotalItems = 999
	if stats := monitor.Stats(); stats.TotalItems != 9 {
		t.Fatalf("expected stats copy isolation, got: %#v", stats)
	}

	items, err := monitor.Recall("working", "first", 5)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(items) != 1 || items[0].ID != "mem-1" {
		t.Fatalf("unexpected recall items: %#v", items)
	}

	if err := monitor.Promote("mem-1"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if err := monitor.Demote("mem-1"); err != nil {
		t.Fatalf("demote: %v", err)
	}

	if promoteCalls != 1 || demoteCalls != 1 {
		t.Fatalf("unexpected mutation call counts: promote=%d demote=%d", promoteCalls, demoteCalls)
	}
	if stats := monitor.Stats(); stats == nil || stats.TotalItems != 10 {
		t.Fatalf("expected refreshed stats after mutations, got: %#v", stats)
	}
	if refreshCount < 3 {
		t.Fatalf("expected multiple refresh callbacks, got %d", refreshCount)
	}

	monitor.Stop()
	monitor.Stop()
}

func TestStreamMonitor_BridgeBackedDedupAndWatermark(t *testing.T) {
	sockPath, handlers := mockDaemon(t)
	_, agent := newBridges(t, sockPath)

	var streamCalls int
	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		if req.Name != "agent_context__agent_context_search" {
			return nil, fmt.Errorf("unexpected tool: %s", req.Name)
		}
		streamCalls++
		switch streamCalls {
		case 1:
			return toolEnvelope(map[string]any{
				"results": []map[string]any{
					{"score": 0.9, "entry": map[string]any{"id": "e1", "entry_type": "decision", "agent_id": "codex", "namespace": "loom-core", "title": "one", "content": "a", "timestamp": "2026-02-17T00:00:01Z"}},
					{"score": 0.8, "entry": map[string]any{"id": "e2", "entry_type": "finding", "agent_id": "codex", "namespace": "loom-core", "title": "two", "content": "b", "timestamp": "2026-02-17T00:00:02Z"}},
					{"score": 0.7, "entry": map[string]any{"id": "e1", "entry_type": "decision", "agent_id": "codex", "namespace": "loom-core", "title": "dup", "content": "c", "timestamp": "2026-02-17T00:00:03Z"}},
					{"score": 0.5, "entry": map[string]any{"id": "", "entry_type": "note", "agent_id": "codex", "namespace": "loom-core", "title": "ignored", "timestamp": "2026-02-17T00:00:04Z"}},
				},
			}), nil
		default:
			return toolEnvelope(map[string]any{
				"results": []map[string]any{
					{"score": 0.6, "entry": map[string]any{"id": "e2", "entry_type": "finding", "agent_id": "codex", "namespace": "loom-core", "title": "dup2", "content": "x", "timestamp": "2026-02-17T00:00:05Z"}},
					{"score": 0.6, "entry": map[string]any{"id": "e3", "entry_type": "note", "agent_id": "codex", "namespace": "loom-core", "title": "three", "content": "d", "timestamp": "2026-02-17T00:00:06Z"}},
				},
			}), nil
		}
	})

	monitor := NewStreamMonitor(agent, nil)
	var deltaSizes []int
	monitor.OnRefresh(func(delta []StreamEntry) { deltaSizes = append(deltaSizes, len(delta)) })

	if err := monitor.Refresh(); err != nil {
		t.Fatalf("refresh #1: %v", err)
	}
	if monitor.lastPoll.IsZero() {
		t.Fatal("expected watermark to be updated after refresh")
	}
	if got := monitor.Entries(); len(got) != 2 {
		t.Fatalf("expected 2 unique entries after first refresh, got %d", len(got))
	}

	if err := monitor.Refresh(); err != nil {
		t.Fatalf("refresh #2: %v", err)
	}
	got := monitor.Entries()
	if len(got) != 3 {
		t.Fatalf("expected 3 total entries after second refresh, got %d", len(got))
	}
	if got[0].ID != "e3" {
		t.Fatalf("expected newest entry prepended, got first ID %s", got[0].ID)
	}
	if len(deltaSizes) != 2 || deltaSizes[0] != 2 || deltaSizes[1] != 1 {
		t.Fatalf("unexpected delta callback sizes: %#v", deltaSizes)
	}

	monitor.Stop()
	monitor.Stop()
}

func TestWorkflowMonitor_BridgeBackedCacheAndInvalidation(t *testing.T) {
	sockPath, handlers := mockDaemon(t)
	_, agent := newBridges(t, sockPath)

	var statusCalls int
	var eventCalls int
	var approveCalls int
	var listCalls int

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}

		switch req.Name {
		case "agent_context__agent_workflow_status":
			statusCalls++
			return toolEnvelope(map[string]any{
				"workflow_id":  "wf-1",
				"status":       "running",
				"current_step": "step-a",
				"created_at":   "2026-02-17T00:00:00Z",
			}), nil
		case "agent_context__agent_workflow_events":
			eventCalls++
			return toolEnvelope(map[string]any{
				"events": []map[string]any{
					{"id": "evt-1", "event_type": "step_started", "step_id": "step-a", "timestamp": "2026-02-17T00:00:01Z"},
				},
			}), nil
		case "agent_context__agent_workflow_approve":
			approveCalls++
			return toolEnvelope(map[string]any{"ok": true}), nil
		case "agent_context__agent_workflow_list":
			listCalls++
			if listCalls == 1 {
				return toolEnvelope(map[string]any{
					"workflows": []map[string]any{
						{"workflow_id": "wf-1", "name": "One", "status": "running", "current_step": "step-a", "created_at": "2026-02-17T00:00:00Z"},
					},
				}), nil
			}
			return toolEnvelope(map[string]any{
				"workflows": []map[string]any{
					{"workflow_id": "wf-2", "name": "Two", "status": "completed", "current_step": "done", "created_at": "2026-02-17T00:10:00Z"},
				},
			}), nil
		default:
			return nil, fmt.Errorf("unexpected tool: %s", req.Name)
		}
	})

	monitor := NewWorkflowMonitor(agent, nil)
	monitor.details["wf-stale"] = &cachedDetail{detail: &bridge.WorkflowDetail{ID: "wf-stale"}, fetchedAt: time.Now()}

	detail1, err := monitor.Detail("wf-1")
	if err != nil {
		t.Fatalf("detail #1: %v", err)
	}
	if detail1 == nil || detail1.ID != "wf-1" || len(detail1.Events) != 1 {
		t.Fatalf("unexpected detail #1: %#v", detail1)
	}
	if _, err := monitor.Detail("wf-1"); err != nil {
		t.Fatalf("detail #2 (cached): %v", err)
	}
	if statusCalls != 1 || eventCalls != 1 {
		t.Fatalf("expected cached second detail call, got status=%d events=%d", statusCalls, eventCalls)
	}

	if err := monitor.ApproveStep("wf-1", "step-a"); err != nil {
		t.Fatalf("approve step: %v", err)
	}
	if approveCalls != 1 {
		t.Fatalf("expected one approve call, got %d", approveCalls)
	}
	if _, err := monitor.Detail("wf-1"); err != nil {
		t.Fatalf("detail #3 after invalidation: %v", err)
	}
	if statusCalls != 2 || eventCalls != 2 {
		t.Fatalf("expected refetch after invalidation, got status=%d events=%d", statusCalls, eventCalls)
	}

	if err := monitor.Refresh(); err != nil {
		t.Fatalf("refresh #1: %v", err)
	}
	if len(monitor.Workflows()) != 1 || monitor.Workflows()[0].ID != "wf-1" {
		t.Fatalf("unexpected workflows after refresh #1: %#v", monitor.Workflows())
	}
	if err := monitor.Refresh(); err != nil {
		t.Fatalf("refresh #2: %v", err)
	}
	if _, ok := monitor.details["wf-stale"]; ok {
		t.Fatal("expected stale workflow cache entry to be pruned")
	}
}

func TestFleetMonitor_BridgeBackedRefreshAndDebounce(t *testing.T) {
	sockPath, handlers := mockDaemon(t)
	client, agent := newBridges(t, sockPath)

	var statusCalls int
	toolCalls := make(map[string]int)

	handlers.handle("loom/status", func(_ json.RawMessage) (any, error) {
		statusCalls++
		return map[string]any{
			"running":     true,
			"servers":     42,
			"activeConns": 3,
			"idleConns":   1,
			"processes":   []string{"agent_context"},
		}, nil
	})

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		toolCalls[req.Name]++
		switch req.Name {
		case "agent_context__agent_session_list":
			return toolEnvelope(map[string]any{
				"sessions": []map[string]any{
					{"id": "s1", "agent_id": "a1", "status": "active", "total_tokens": 100},
					{"id": "s2", "agent_id": "a2", "status": "idle", "total_tokens": 50},
				},
			}), nil
		case "agent_context__agent_task_list":
			return toolEnvelope(map[string]any{
				"tasks": []map[string]any{
					{"id": "t1", "status": "pending"},
					{"id": "t2", "status": "in_progress"},
					{"id": "t3", "status": "blocked"},
				},
			}), nil
		case "agent_context__agent_memory_stats":
			return toolEnvelope(map[string]any{
				"working_memory":    map[string]any{"item_count": 1, "token_count": 10},
				"short_term_memory": map[string]any{"item_count": 2, "token_count": 20},
				"long_term_memory":  map[string]any{"item_count": 3, "token_count": 30},
				"total_items":       6,
				"total_tokens":      60,
			}), nil
		case "agent_context__agent_graph_stats":
			return toolEnvelope(map[string]any{
				"total_entities":  12,
				"total_relations": 34,
			}), nil
		case "agent_context__agent_workflow_list":
			return toolEnvelope(map[string]any{
				"workflows": []map[string]any{
					{"workflow_id": "wf1", "status": "running", "current_step": "x", "created_at": "2026-02-17T00:00:00Z"},
				},
			}), nil
		case "agent_context__agent_presence_list":
			return toolEnvelope(map[string]any{
				"agents": []map[string]any{
					{"agent_id": "a1", "status": "active"},
					{"agent_id": "a2", "status": "idle"},
					{"agent_id": "a3", "status": "offline"},
				},
			}), nil
		case "agent_context__agent_file_claim_list":
			return toolEnvelope(map[string]any{"claims": []map[string]any{}}), nil
		case "agent_context__agent_worktree_list":
			return toolEnvelope(map[string]any{
				"assignments": []map[string]any{
					{"assignment_id": "w1", "status": "active"},
				},
			}), nil
		case "agent_context__agent_handoff_list":
			return toolEnvelope(map[string]any{"handoffs": []map[string]any{}}), nil
		default:
			return nil, fmt.Errorf("unexpected tool: %s", req.Name)
		}
	})

	monitor := NewFleetMonitor(client, agent, nil)
	if err := monitor.Refresh(); err != nil {
		t.Fatalf("refresh #1: %v", err)
	}

	snap := monitor.Snapshot()
	if !snap.DaemonRunning || snap.ServerCount != 42 || snap.ActiveConns != 3 {
		t.Fatalf("unexpected daemon fields: %#v", snap)
	}
	if snap.TotalSessions != 2 || snap.ActiveSessions != 1 || snap.TotalTokens != 150 {
		t.Fatalf("unexpected session summary: %#v", snap)
	}
	if snap.TotalTasks != 3 || snap.PendingTasks != 1 || snap.ActiveTasks != 1 || snap.BlockedTasks != 1 {
		t.Fatalf("unexpected task summary: %#v", snap)
	}
	if snap.MemoryTotalItems != 6 || snap.MemoryTotalTokens != 60 {
		t.Fatalf("unexpected memory summary: %#v", snap)
	}
	if snap.EntityCount != 12 || snap.RelationCount != 34 {
		t.Fatalf("unexpected graph summary: %#v", snap)
	}
	if snap.RunningWorkflows != 1 || snap.PendingApprovals != 0 {
		t.Fatalf("unexpected workflow summary: %#v", snap)
	}
	if snap.ActiveAgents != 1 || snap.IdleAgents != 1 || snap.OfflineAgents != 1 {
		t.Fatalf("unexpected agent summary: %#v", snap)
	}
	if snap.ActiveWorktrees != 1 {
		t.Fatalf("unexpected worktree summary: %#v", snap)
	}

	prevStatusCalls := statusCalls
	prevSessionCalls := toolCalls["agent_context__agent_session_list"]
	if err := monitor.Refresh(); err != nil {
		t.Fatalf("refresh #2 (debounced): %v", err)
	}
	if statusCalls != prevStatusCalls || toolCalls["agent_context__agent_session_list"] != prevSessionCalls {
		t.Fatalf("expected second refresh to debounce without new RPC calls, status=%d->%d sessions=%d->%d", prevStatusCalls, statusCalls, prevSessionCalls, toolCalls["agent_context__agent_session_list"])
	}
}
