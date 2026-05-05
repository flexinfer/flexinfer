package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestAgentBridge_ListActivePipelines_UsesBatchedToolAndParsesWrappedResults(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	callCount := 0
	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name != "gitlab__list_active_pipelines" {
			t.Fatalf("expected batched tool, got: %s", req.Name)
		}
		projects, _ := req.Arguments["projects"].([]any)
		if len(projects) != 1 {
			t.Fatalf("expected 1 project in batched arg, got %#v", projects)
		}
		if got, _ := projects[0].(string); got != "services/loom-core" {
			t.Fatalf("expected project services/loom-core, got %#v", projects[0])
		}
		callCount++

		// Batched tool returns a flattened list; each pipeline carries its
		// originating project so the bridge needs no per-call backfill.
		payload := `{"pipelines":[` +
			`{"id":101,"ref":"main","status":"running","created_at":"2026-03-19T12:00:00Z","project":"services/loom-core"},` +
			`{"id":102,"ref":"feature","status":"pending","created_at":"2026-03-19T12:01:00Z","project":"services/loom-core"}` +
			`],"count":2,"errors":{}}`

		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": payload}},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	pipelines, err := bridge.ListActivePipelines([]string{"services/loom-core"})
	if err != nil {
		t.Fatalf("ListActivePipelines: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 batched call, got %d", callCount)
	}
	if len(pipelines) != 2 {
		t.Fatalf("expected 2 pipelines, got %#v", pipelines)
	}
	if pipelines[0].Project != "services/loom-core" || pipelines[1].Project != "services/loom-core" {
		t.Fatalf("expected project field to come from batched payload, got %#v", pipelines)
	}
	if pipelines[0].ID != 101 || pipelines[1].ID != 102 {
		t.Fatalf("unexpected pipelines: %#v", pipelines)
	}
}

func TestAgentBridge_GetPipelineDetail_UsesProjectArgAndParsesWrappedJobs(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if _, ok := req.Arguments["project_id"]; ok {
			t.Fatalf("unexpected legacy project_id arg in %s: %#v", req.Name, req.Arguments)
		}
		if got, _ := req.Arguments["project"].(string); got != "services/loom-core" {
			t.Fatalf("expected project arg to be services/loom-core, got %#v", req.Arguments["project"])
		}
		if got, _ := req.Arguments["pipeline_id"].(float64); int(got) != 4242 {
			t.Fatalf("expected pipeline_id 4242, got %#v", req.Arguments["pipeline_id"])
		}

		var payload string
		switch req.Name {
		case "gitlab__get_pipeline":
			payload = `{"id":4242,"ref":"main","status":"running","created_at":"2026-03-19T12:00:00Z","web_url":"https://gitlab.example/p/4242"}`
		case "gitlab__list_pipeline_jobs":
			payload = `{"jobs":[{"id":1,"name":"build","status":"success","stage":"build","duration":12.5},{"id":2,"name":"test","status":"running","stage":"test","duration":3.25}],"count":2}`
		default:
			return nil, fmt.Errorf("unexpected tool name: %s", req.Name)
		}

		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": payload}},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	detail, err := bridge.GetPipelineDetail("services/loom-core", 4242)
	if err != nil {
		t.Fatalf("GetPipelineDetail: %v", err)
	}
	if detail.Project != "services/loom-core" {
		t.Fatalf("expected project to be backfilled, got %#v", detail.Project)
	}
	if detail.TotalStages != 2 {
		t.Fatalf("expected 2 stages, got %#v", detail)
	}
	if detail.CompletedStages != 1 {
		t.Fatalf("expected 1 completed stage, got %#v", detail)
	}
	if detail.CurrentStage != "test" {
		t.Fatalf("expected current stage test, got %#v", detail.CurrentStage)
	}
	if detail.Stages[1].Jobs[0].Duration != 3.25 {
		t.Fatalf("expected fractional duration to survive unmarshal, got %#v", detail.Stages[1].Jobs[0].Duration)
	}
}

func TestAgentBridge_ListActivePipelines_UsesBatchTimeoutPath(t *testing.T) {
	caller := &recordingCaller{
		t: t,
		callToolWithTimeoutFn: func(name string, args map[string]any, timeout time.Duration) (json.RawMessage, error) {
			if name != "gitlab__list_active_pipelines" {
				t.Fatalf("expected batched tool, got %q", name)
			}
			if timeout != pipelineBatchToolTimeout {
				t.Fatalf("expected batched timeout %v, got %v", pipelineBatchToolTimeout, timeout)
			}
			return toolTextResult(`{"pipelines":[` +
				`{"id":101,"ref":"main","status":"running","created_at":"2026-03-19T12:00:00Z","project":"services/loom-core"},` +
				`{"id":102,"ref":"feature","status":"pending","created_at":"2026-03-19T12:01:00Z","project":"services/loom-core"}` +
				`],"errors":{}}`), nil
		},
	}

	bridge := NewAgentBridge(caller)
	pipelines, err := bridge.ListActivePipelines([]string{"services/loom-core"})
	if err != nil {
		t.Fatalf("ListActivePipelines: %v", err)
	}
	if caller.callToolCount != 0 {
		t.Fatalf("expected CallTool to be unused, got %d calls", caller.callToolCount)
	}
	if caller.callToolWithTimeoutCount != 1 {
		t.Fatalf("expected 1 batched CallToolWithTimeout call, got %d", caller.callToolWithTimeoutCount)
	}
	if len(pipelines) != 2 {
		t.Fatalf("expected 2 pipelines, got %d", len(pipelines))
	}
}

func TestAgentBridge_ListActivePipelines_BatchedPreservesProjectsAcrossEntries(t *testing.T) {
	caller := &recordingCaller{
		t: t,
		callToolWithTimeoutFn: func(name string, args map[string]any, timeout time.Duration) (json.RawMessage, error) {
			if name != "gitlab__list_active_pipelines" {
				t.Fatalf("expected batched tool, got %q", name)
			}
			projects, _ := args["projects"].([]any)
			if len(projects) != 2 {
				t.Fatalf("expected 2 projects in batched arg, got %#v", projects)
			}
			// The batched server-side tool returns a flattened payload tagged
			// per pipeline; the bridge must preserve the per-entry project.
			return toolTextResult(`{"pipelines":[` +
				`{"id":11,"ref":"main","status":"running","created_at":"2026-03-19T12:00:00Z","project":"services/loom-core"},` +
				`{"id":22,"ref":"feature","status":"pending","created_at":"2026-03-19T12:01:00Z","project":"services/loom-core-hud"}` +
				`]}`), nil
		},
	}

	bridge := NewAgentBridge(caller)
	pipelines, err := bridge.ListActivePipelines([]string{"services/loom-core", "services/loom-core-hud"})
	if err != nil {
		t.Fatalf("ListActivePipelines: %v", err)
	}
	if len(pipelines) != 2 {
		t.Fatalf("expected 2 pipelines, got %#v", pipelines)
	}

	seen := map[string]bool{}
	for _, p := range pipelines {
		seen[p.Project] = true
	}
	if !seen["services/loom-core"] || !seen["services/loom-core-hud"] {
		t.Fatalf("expected both projects to be preserved, got %#v", pipelines)
	}
}

// TestAgentBridge_ListActivePipelines_FallsBackToPerProjectOnBatchedError
// covers the rollout-window safety net: when mcp-gitlab predates the batched
// tool the call returns an error and the bridge transparently fans out per
// project via the legacy path.
func TestAgentBridge_ListActivePipelines_FallsBackToPerProjectOnBatchedError(t *testing.T) {
	caller := &recordingCaller{
		t: t,
		callToolWithTimeoutFn: func(name string, args map[string]any, timeout time.Duration) (json.RawMessage, error) {
			if name == "gitlab__list_active_pipelines" {
				return nil, errors.New("tool not found: list_active_pipelines")
			}
			if name != "gitlab__list_pipelines" {
				t.Fatalf("expected legacy fallback to use gitlab__list_pipelines, got %q", name)
			}
			project, _ := args["project"].(string)
			status, _ := args["status"].(string)
			if project != "services/loom-core" {
				t.Fatalf("unexpected project %q", project)
			}
			switch status {
			case "running":
				return toolTextResult(`{"pipelines":[{"id":201,"ref":"main","status":"running","created_at":"2026-03-19T12:00:00Z"}]}`), nil
			case "pending":
				return toolTextResult(`{"pipelines":[]}`), nil
			default:
				t.Fatalf("unexpected status %q", status)
				return nil, nil
			}
		},
	}

	bridge := NewAgentBridge(caller)
	pipelines, err := bridge.ListActivePipelines([]string{"services/loom-core"})
	if err != nil {
		t.Fatalf("ListActivePipelines fallback: %v", err)
	}
	if len(pipelines) != 1 || pipelines[0].ID != 201 {
		t.Fatalf("unexpected pipelines after fallback: %#v", pipelines)
	}
	// 1 batched attempt + 2 per-project legacy calls (running + pending).
	if caller.callToolWithTimeoutCount != 3 {
		t.Fatalf("expected 3 CallToolWithTimeout calls (1 batched + 2 fallback), got %d", caller.callToolWithTimeoutCount)
	}
}

func TestAgentBridge_GetPipelineDetail_UsesTimeoutPathAndFallsBackOnSlowJobs(t *testing.T) {
	caller := &recordingCaller{
		t: t,
		callToolWithTimeoutFn: func(name string, args map[string]any, timeout time.Duration) (json.RawMessage, error) {
			if timeout != pipelineDetailToolTimeout {
				t.Fatalf("unexpected timeout %v", timeout)
			}
			switch name {
			case "gitlab__get_pipeline":
				return toolTextResult(`{"id":4242,"ref":"main","status":"running","created_at":"2026-03-19T12:00:00Z","web_url":"https://gitlab.example/p/4242"}`), nil
			case "gitlab__list_pipeline_jobs":
				return nil, context.DeadlineExceeded
			default:
				t.Fatalf("unexpected tool name %q", name)
				return nil, nil
			}
		},
	}

	bridge := NewAgentBridge(caller)
	detail, err := bridge.GetPipelineDetail("services/loom-core", 4242)
	if err != nil {
		t.Fatalf("GetPipelineDetail: %v", err)
	}
	if caller.callToolCount != 0 {
		t.Fatalf("expected CallTool to be unused, got %d calls", caller.callToolCount)
	}
	if caller.callToolWithTimeoutCount != 2 {
		t.Fatalf("expected 2 CallToolWithTimeout calls, got %d", caller.callToolWithTimeoutCount)
	}
	if detail.Project != "services/loom-core" {
		t.Fatalf("expected project to be backfilled, got %q", detail.Project)
	}
	if detail.TotalStages != 0 {
		t.Fatalf("expected no stage data on timeout, got %#v", detail)
	}
}

type recordingCaller struct {
	t                        *testing.T
	callToolCount            int
	callToolWithTimeoutCount int
	callToolWithTimeoutFn    func(name string, args map[string]any, timeout time.Duration) (json.RawMessage, error)
}

func (c *recordingCaller) Call(string, any) (json.RawMessage, error) {
	c.t.Fatal("Call should not be used in this test")
	return nil, nil
}

func (c *recordingCaller) CallWithTimeout(string, any, time.Duration) (json.RawMessage, error) {
	c.t.Fatal("CallWithTimeout should not be used in this test")
	return nil, nil
}

func (c *recordingCaller) CallTool(name string, args map[string]any) (json.RawMessage, error) {
	c.callToolCount++
	return nil, fmt.Errorf("unexpected CallTool for %s with %#v", name, args)
}

func (c *recordingCaller) CallToolWithTimeout(name string, args map[string]any, timeout time.Duration) (json.RawMessage, error) {
	c.callToolWithTimeoutCount++
	if c.callToolWithTimeoutFn == nil {
		return nil, fmt.Errorf("unexpected CallToolWithTimeout for %s", name)
	}
	return c.callToolWithTimeoutFn(name, args, timeout)
}

func (c *recordingCaller) CircuitOpen() bool { return false }

func (c *recordingCaller) Close() error { return nil }

func toolTextResult(payload string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"content":[{"type":"text","text":%q}]}`, payload))
}

func TestAgentBridge_ListPipelineProjects_PaginatesAndDedupes(t *testing.T) {
	// Disable the default cap so this test exercises pagination + dedupe
	// without the top-N short-circuit interfering.
	t.Setenv("HUD_PIPELINE_MAX_PROJECTS", "0")

	var pagesRequested []int
	caller := &recordingCaller{
		callToolWithTimeoutFn: func(name string, args map[string]any, timeout time.Duration) (json.RawMessage, error) {
			if name != "gitlab__list_projects" {
				t.Fatalf("unexpected tool: %s", name)
			}
			if got, _ := args["membership"].(bool); !got {
				t.Fatalf("expected membership=true, got %#v", args["membership"])
			}
			if got, _ := args["order_by"].(string); got != "last_activity_at" {
				t.Fatalf("expected order_by=last_activity_at, got %#v", args["order_by"])
			}
			if got, _ := args["sort"].(string); got != "desc" {
				t.Fatalf("expected sort=desc, got %#v", args["sort"])
			}
			page, _ := args["page"].(int)
			pagesRequested = append(pagesRequested, page)
			switch page {
			case 1:
				return toolTextResult(`{"projects":[` +
					pageProjects(100, 0, "services/") +
					`],"count":100}`), nil
			case 2:
				return toolTextResult(`{"projects":[` +
					`{"path_with_namespace":"services/p0"},` + // duplicate of page 1 entry
					`{"path_with_namespace":"libs/banner-kit"}` +
					`],"count":2}`), nil
			}
			t.Fatalf("unexpected page %d", page)
			return nil, nil
		},
	}
	bridge := NewAgentBridge(caller)
	projects, err := bridge.ListPipelineProjects(context.Background(), 5)
	if err != nil {
		t.Fatalf("ListPipelineProjects: %v", err)
	}
	// 100 from page 1 + 1 new from page 2 (services/p0 is dropped as duplicate).
	if len(projects) != 101 {
		t.Fatalf("expected 101 deduped projects, got %d", len(projects))
	}
	// Stops after a short page.
	if len(pagesRequested) != 2 {
		t.Fatalf("expected 2 pages (early stop on partial), got %d", len(pagesRequested))
	}
	// Output must be sorted alphabetically.
	for i := 1; i < len(projects); i++ {
		if projects[i-1] > projects[i] {
			t.Fatalf("results not sorted: %s > %s", projects[i-1], projects[i])
		}
	}
}

func TestAgentBridge_ListPipelineProjects_DefaultCapAt20(t *testing.T) {
	var pagesRequested []int
	caller := &recordingCaller{
		callToolWithTimeoutFn: func(name string, args map[string]any, timeout time.Duration) (json.RawMessage, error) {
			page, _ := args["page"].(int)
			pagesRequested = append(pagesRequested, page)
			// Return 100 projects on page 1 so the cap (20) short-circuits before page 2.
			return toolTextResult(`{"projects":[` +
				pageProjects(100, 0, "services/") +
				`],"count":100}`), nil
		},
	}
	bridge := NewAgentBridge(caller)
	projects, err := bridge.ListPipelineProjects(context.Background(), 5)
	if err != nil {
		t.Fatalf("ListPipelineProjects: %v", err)
	}
	if len(projects) != 20 {
		t.Fatalf("expected default cap of 20 projects, got %d", len(projects))
	}
	if len(pagesRequested) != 1 {
		t.Fatalf("expected cap to short-circuit after 1 page, got %d pages", len(pagesRequested))
	}
}

func TestAgentBridge_ListPipelineProjects_CapOverrideViaEnv(t *testing.T) {
	t.Setenv("HUD_PIPELINE_MAX_PROJECTS", "5")
	caller := &recordingCaller{
		callToolWithTimeoutFn: func(name string, args map[string]any, timeout time.Duration) (json.RawMessage, error) {
			return toolTextResult(`{"projects":[` +
				pageProjects(100, 0, "services/") +
				`],"count":100}`), nil
		},
	}
	bridge := NewAgentBridge(caller)
	projects, err := bridge.ListPipelineProjects(context.Background(), 5)
	if err != nil {
		t.Fatalf("ListPipelineProjects: %v", err)
	}
	if len(projects) != 5 {
		t.Fatalf("expected env-overridden cap of 5, got %d", len(projects))
	}
}

func TestAgentBridge_ListPipelineProjects_FirstPageErrorPropagates(t *testing.T) {
	caller := &recordingCaller{
		callToolWithTimeoutFn: func(name string, args map[string]any, timeout time.Duration) (json.RawMessage, error) {
			return nil, fmt.Errorf("mcp unreachable")
		},
	}
	bridge := NewAgentBridge(caller)
	_, err := bridge.ListPipelineProjects(context.Background(), 3)
	if err == nil {
		t.Fatalf("expected error when gitlab MCP is unreachable")
	}
}

// pageProjects produces a JSON array of n entries "services/p{offset}"…"services/p{offset+n-1}".
func pageProjects(n, offset int, prefix string) string {
	out := make([]byte, 0, n*50)
	for i := 0; i < n; i++ {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, []byte(fmt.Sprintf(`{"path_with_namespace":%q}`, fmt.Sprintf("%sp%d", prefix, offset+i)))...)
	}
	return string(out)
}
