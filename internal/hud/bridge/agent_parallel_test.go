package bridge

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// TestAgentBridge_SetTracer_DefaultIsNoop verifies the default tracer is non-nil (noop).
func TestAgentBridge_SetTracer_DefaultIsNoop(t *testing.T) {
	sockPath, _ := mockDaemon(t)
	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	b := NewAgentBridge(client)
	if b.tracer == nil {
		t.Fatal("expected default tracer to be non-nil")
	}
}

// TestAgentBridge_SetTracer_AcceptsCustomTracer verifies SetTracer replaces the tracer.
func TestAgentBridge_SetTracer_AcceptsCustomTracer(t *testing.T) {
	sockPath, _ := mockDaemon(t)
	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	b := NewAgentBridge(client)
	custom := noop.NewTracerProvider().Tracer("test")
	b.SetTracer(custom)
	if b.tracer != custom {
		t.Fatal("expected tracer to be replaced")
	}
}

// TestAgentBridge_SetTracer_NilRevertsToNoop verifies that passing nil to SetTracer
// reverts to a noop tracer rather than storing nil.
func TestAgentBridge_SetTracer_NilRevertsToNoop(t *testing.T) {
	sockPath, _ := mockDaemon(t)
	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	b := NewAgentBridge(client)
	b.SetTracer(nil)
	if b.tracer == nil {
		t.Fatal("expected tracer to be noop, not nil")
	}
}

// TestAgentBridge_CallAgentTool_ProducesSpan verifies that callAgentTool creates spans.
func TestAgentBridge_CallAgentTool_ProducesSpan(t *testing.T) {
	sockPath, handlers := mockDaemon(t)
	handlers.handle("tools/call", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": `{"ok":true}`},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	// Use a recording tracer to verify spans are started.
	tp := &recordingTracerProvider{}
	b := NewAgentBridge(client)
	b.SetTracer(tp.Tracer("bridge-test"))

	err := b.callAgentTool("agent_presence_heartbeat", map[string]any{
		"agent_id": "test",
	}, nil)
	if err != nil {
		t.Fatalf("callAgentTool: %v", err)
	}

	if tp.spanCount.Load() == 0 {
		t.Fatal("expected at least one span to be started")
	}
}

// TestAgentBridge_HandoffList_ConcurrentQueries verifies that HandoffList queries
// multiple agents' inboxes concurrently and deduplicates by handoff ID.
func TestAgentBridge_HandoffList_ConcurrentQueries(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	var inboxCallCount atomic.Int32

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Errorf("unmarshal params: %v", err)
			return nil, err
		}

		switch req.Name {
		case "agent_context__agent_presence_list":
			return map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": `{"agents":[
						{"agent_id":"agent-a","status":"active"},
						{"agent_id":"agent-b","status":"active"},
						{"agent_id":"agent-c","status":"offline"}
					]}`},
				},
			}, nil

		case "agent_context__agent_handoff_inbox":
			inboxCallCount.Add(1)
			agentID, _ := req.Arguments["agent_id"].(string)

			switch agentID {
			case "agent-a":
				return map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": `{"handoffs":[
							{"handoff_id":"h-1","source_agent":"agent-b","status":"pending","summary":"task 1"}
						]}`},
					},
				}, nil
			case "agent-b":
				// Return a duplicate h-1 plus a unique h-2.
				return map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": `{"handoffs":[
							{"handoff_id":"h-1","source_agent":"agent-a","status":"pending","summary":"task 1"},
							{"handoff_id":"h-2","source_agent":"agent-a","status":"pending","summary":"task 2"}
						]}`},
					},
				}, nil
			case "agent-c":
				return map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": `{"handoffs":[]}`},
					},
				}, nil
			default:
				t.Errorf("unexpected agent_id in handoff_inbox: %q", agentID)
				return nil, nil
			}

		default:
			t.Errorf("unexpected tool: %s", req.Name)
			return nil, nil
		}
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	b := NewAgentBridge(client)
	handoffs, err := b.HandoffList()
	if err != nil {
		t.Fatalf("HandoffList: %v", err)
	}

	// Should have called inbox for all 3 agents.
	if got := inboxCallCount.Load(); got != 3 {
		t.Fatalf("expected 3 inbox calls, got %d", got)
	}

	// Should deduplicate h-1, so we get h-1 and h-2.
	if len(handoffs) != 2 {
		t.Fatalf("expected 2 unique handoffs, got %d: %+v", len(handoffs), handoffs)
	}

	ids := map[string]bool{}
	for _, h := range handoffs {
		ids[h.ID] = true
	}
	if !ids["h-1"] || !ids["h-2"] {
		t.Fatalf("expected handoff IDs h-1 and h-2, got %v", ids)
	}
}

// TestAgentBridge_ListActivePipelines_BatchedSingleCall verifies that
// ListActivePipelines now collapses N×M per-project requests into one
// batched call (the fix that relieved the daemon's per-server call lock).
func TestAgentBridge_ListActivePipelines_BatchedSingleCall(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	var callCount atomic.Int32

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		callCount.Add(1)
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Errorf("unmarshal params: %v", err)
			return nil, err
		}

		if req.Name != "gitlab__list_active_pipelines" {
			t.Errorf("expected batched tool, got: %s", req.Name)
			return nil, nil
		}

		projects, _ := req.Arguments["projects"].([]any)
		if len(projects) != 2 {
			t.Errorf("expected 2 projects in batched arg, got %#v", projects)
		}

		// One mocked batched response carrying both projects' pipelines,
		// each tagged with its source project.
		payload := `{"pipelines":[` +
			`{"id":1,"ref":"main","status":"running","created_at":"2026-03-19T12:00:00Z","project":"proj-a"},` +
			`{"id":2,"ref":"feat","status":"pending","created_at":"2026-03-19T12:01:00Z","project":"proj-b"}` +
			`],"errors":{}}`

		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": payload}},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	b := NewAgentBridge(client)
	pipelines, err := b.ListActivePipelines([]string{"proj-a", "proj-b"})
	if err != nil {
		t.Fatalf("ListActivePipelines: %v", err)
	}

	// One batched call replaces the prior 2×2 = 4 per-project calls. This
	// is the whole point of the fix: collapse N×M lock acquisitions to 1.
	if got := callCount.Load(); got != 1 {
		t.Fatalf("expected 1 batched call, got %d", got)
	}

	if len(pipelines) != 2 {
		t.Fatalf("expected 2 pipelines, got %d: %+v", len(pipelines), pipelines)
	}

	projMap := map[string]bool{}
	for _, p := range pipelines {
		projMap[p.Project] = true
	}
	if !projMap["proj-a"] || !projMap["proj-b"] {
		t.Fatalf("expected both projects in batched payload, got %v", projMap)
	}
}

// TestAgentBridge_ResolveDispatchSession_ReusesExistingSession verifies that
// resolveDispatchSourceSessionID reuses an existing active session instead of
// creating a new one every time.
func TestAgentBridge_ResolveDispatchSession_ReusesExistingSession(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	var sessionListCount atomic.Int32
	var sessionStartCount atomic.Int32

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Errorf("unmarshal params: %v", err)
			return nil, err
		}

		switch req.Name {
		case "agent_context__agent_session_list":
			sessionListCount.Add(1)
			agentID, _ := req.Arguments["agent_id"].(string)
			if agentID == "hud-dispatcher" {
				return map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": `{"sessions":[{"id":"existing-sess","agent_id":"hud-dispatcher","status":"active","namespace":"loom-core/hud-dispatch"}]}`},
					},
				}, nil
			}
			return map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": `{"sessions":[]}`},
				},
			}, nil

		case "agent_context__agent_session_start":
			sessionStartCount.Add(1)
			return map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": `{"session_id":"new-sess"}`},
				},
			}, nil

		default:
			t.Errorf("unexpected tool: %s", req.Name)
			return nil, nil
		}
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	b := NewAgentBridge(client)

	// First call should find existing session.
	sid1, err := b.resolveDispatchSourceSessionID("")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if sid1 != "existing-sess" {
		t.Fatalf("expected existing-sess, got %q", sid1)
	}

	// Second call should use the cache, not make another session_list call.
	listCountBefore := sessionListCount.Load()
	sid2, err := b.resolveDispatchSourceSessionID("")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if sid2 != "existing-sess" {
		t.Fatalf("expected existing-sess on second call, got %q", sid2)
	}
	if sessionListCount.Load() != listCountBefore {
		t.Fatal("expected second call to use cache, not make session_list call")
	}

	// Should never have started a new session since one already existed.
	if sessionStartCount.Load() != 0 {
		t.Fatalf("expected 0 session starts, got %d", sessionStartCount.Load())
	}
}

// TestAgentBridge_ResolveDispatchSession_ExplicitIDPassthrough verifies that
// an explicit sourceSessionID is returned directly without any RPC.
func TestAgentBridge_ResolveDispatchSession_ExplicitIDPassthrough(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	var callCount atomic.Int32
	handlers.handle("tools/call", func(_ json.RawMessage) (any, error) {
		callCount.Add(1)
		return nil, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	b := NewAgentBridge(client)
	sid, err := b.resolveDispatchSourceSessionID("my-explicit-session")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sid != "my-explicit-session" {
		t.Fatalf("expected my-explicit-session, got %q", sid)
	}
	if callCount.Load() != 0 {
		t.Fatal("expected no RPC calls when explicit session ID is provided")
	}
}

// TestAgentBridge_HandoffList_PartialFailure verifies that HandoffList returns
// partial results when some agents fail.
func TestAgentBridge_HandoffList_PartialFailure(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		json.Unmarshal(params, &req) //nolint:errcheck // test helper; assertion failures catch issues

		switch req.Name {
		case "agent_context__agent_presence_list":
			return map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": `{"agents":[
						{"agent_id":"agent-ok","status":"active"},
						{"agent_id":"agent-fail","status":"active"}
					]}`},
				},
			}, nil

		case "agent_context__agent_handoff_inbox":
			agentID, _ := req.Arguments["agent_id"].(string)
			if agentID == "agent-fail" {
				return map[string]any{
					"isError": true,
					"content": []map[string]any{
						{"type": "text", "text": "inbox unavailable for agent-fail"},
					},
				}, nil
			}
			return map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": `{"handoffs":[
						{"handoff_id":"h-ok","source_agent":"someone","status":"pending","summary":"good"}
					]}`},
				},
			}, nil

		default:
			return nil, nil
		}
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	b := NewAgentBridge(client)
	handoffs, err := b.HandoffList()
	if err != nil {
		t.Fatalf("HandoffList with partial failure: %v", err)
	}
	// Should have at least the one from agent-ok.
	if len(handoffs) < 1 {
		t.Fatalf("expected at least 1 handoff from successful agent, got %d", len(handoffs))
	}
	found := false
	for _, h := range handoffs {
		if h.ID == "h-ok" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected handoff h-ok in results, got %+v", handoffs)
	}
}

// --- Recording tracer helpers ---

// recordingTracerProvider is a minimal TracerProvider that counts spans started.
type recordingTracerProvider struct {
	spanCount atomic.Int64
}

func (p *recordingTracerProvider) Tracer(name string, _ ...trace.TracerOption) trace.Tracer {
	return &recordingTracer{provider: p}
}

type recordingTracer struct {
	trace.Tracer
	provider *recordingTracerProvider
}

func (rt *recordingTracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	rt.provider.spanCount.Add(1)
	return noop.NewTracerProvider().Tracer("").Start(ctx, name, opts...)
}

// Note: recordingTracerProvider does not implement trace.TracerProvider
// (which has a private method) but its Tracer method returns a valid trace.Tracer
// suitable for SetTracer.
