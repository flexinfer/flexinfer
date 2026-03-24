package mobile

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/monitor"
)

type pipelineOpsCaller struct {
	t *testing.T
}

type recentOnlyPipelineOpsCaller struct{}

func (c *pipelineOpsCaller) Call(string, any) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected Call")
}

func (c *pipelineOpsCaller) CallWithTimeout(string, any, time.Duration) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected CallWithTimeout")
}

func (c *pipelineOpsCaller) CallTool(name string, args map[string]any) (json.RawMessage, error) {
	return c.CallToolWithTimeout(name, args, 0)
}

func (c *pipelineOpsCaller) CallToolWithTimeout(name string, args map[string]any, _ time.Duration) (json.RawMessage, error) {
	project, _ := args["project"].(string)
	switch name {
	case "gitlab__list_pipelines":
		status, _ := args["status"].(string)
		switch project {
		case "services/loom-core":
			if status == "" {
				return mobileToolTextResult(`{"pipelines":[{"id":55,"ref":"renovate/minor","status":"failed","created_at":"2026-03-23T20:05:00Z","web_url":"https://gitlab.example/pipelines/55"},{"id":42,"ref":"codex/mobile-parity","status":"failed","created_at":"2026-03-23T20:04:00Z","web_url":"https://gitlab.example/pipelines/42"},{"id":41,"ref":"main","status":"success","created_at":"2026-03-23T20:03:00Z","web_url":"https://gitlab.example/pipelines/41"}]}`), nil
			}
			if status == "running" {
				return mobileToolTextResult(`{"pipelines":[{"id":42,"ref":"main","status":"running","created_at":"2026-03-23T20:00:00Z","web_url":"https://gitlab.example/pipelines/42"}]}`), nil
			}
		}
		return mobileToolTextResult(`{"pipelines":[]}`), nil
	case "gitlab__get_pipeline":
		return mobileToolTextResult(`{"id":42,"ref":"main","status":"running","created_at":"2026-03-23T20:00:00Z","web_url":"https://gitlab.example/pipelines/42"}`), nil
	case "gitlab__list_pipeline_jobs":
		return mobileToolTextResult(`{"jobs":[{"id":7,"name":"build","status":"success","stage":"build"},{"id":8,"name":"test","status":"running","stage":"test"}]}`), nil
	default:
		return nil, fmt.Errorf("unexpected tool %s", name)
	}
}

func (c *pipelineOpsCaller) CircuitOpen() bool { return false }
func (c *pipelineOpsCaller) Close() error      { return nil }

func (c *recentOnlyPipelineOpsCaller) Call(string, any) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected Call")
}

func (c *recentOnlyPipelineOpsCaller) CallWithTimeout(string, any, time.Duration) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected CallWithTimeout")
}

func (c *recentOnlyPipelineOpsCaller) CallTool(name string, args map[string]any) (json.RawMessage, error) {
	return c.CallToolWithTimeout(name, args, 0)
}

func (c *recentOnlyPipelineOpsCaller) CallToolWithTimeout(name string, args map[string]any, _ time.Duration) (json.RawMessage, error) {
	project, _ := args["project"].(string)
	switch name {
	case "gitlab__list_pipelines":
		status, _ := args["status"].(string)
		if project == "services/loom-core" && status == "" {
			return mobileToolTextResult(`{"pipelines":[{"id":55,"ref":"renovate/minor","status":"failed","created_at":"2026-03-23T20:05:00Z","web_url":"https://gitlab.example/pipelines/55"},{"id":42,"ref":"codex/mobile-parity","status":"failed","created_at":"2026-03-23T20:04:00Z","web_url":"https://gitlab.example/pipelines/42"},{"id":41,"ref":"main","status":"success","created_at":"2026-03-23T20:03:00Z","web_url":"https://gitlab.example/pipelines/41"}]}`), nil
		}
		return mobileToolTextResult(`{"pipelines":[]}`), nil
	case "gitlab__get_pipeline":
		return mobileToolTextResult(`{"id":42,"ref":"codex/mobile-parity","status":"failed","created_at":"2026-03-23T20:04:00Z","web_url":"https://gitlab.example/pipelines/42"}`), nil
	case "gitlab__list_pipeline_jobs":
		return mobileToolTextResult(`{"jobs":[{"id":7,"name":"build","status":"failed","stage":"build"}]}`), nil
	default:
		return nil, fmt.Errorf("unexpected tool %s", name)
	}
}

func (c *recentOnlyPipelineOpsCaller) CircuitOpen() bool { return false }
func (c *recentOnlyPipelineOpsCaller) Close() error      { return nil }

type failingPipelineOpsCaller struct{}

func (c *failingPipelineOpsCaller) Call(string, any) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected Call")
}

func (c *failingPipelineOpsCaller) CallWithTimeout(string, any, time.Duration) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected CallWithTimeout")
}

func (c *failingPipelineOpsCaller) CallTool(name string, args map[string]any) (json.RawMessage, error) {
	return c.CallToolWithTimeout(name, args, 0)
}

func (c *failingPipelineOpsCaller) CallToolWithTimeout(string, map[string]any, time.Duration) (json.RawMessage, error) {
	return nil, fmt.Errorf("backend unavailable")
}

func (c *failingPipelineOpsCaller) CircuitOpen() bool { return false }
func (c *failingPipelineOpsCaller) Close() error      { return nil }

func mobileToolTextResult(payload string) json.RawMessage {
	return json.RawMessage(`{"content":[{"type":"text","text":` + strconv.Quote(payload) + `}]}`)
}

func TestBuildMobilePipelineResponses_EnrichesDetailAndCorrelation(t *testing.T) {
	pipelines := []bridge.PipelineInfo{
		{
			ID:        42,
			Project:   "services/loom-core",
			Ref:       "feature/mobile-parity",
			Status:    "running",
			Source:    "push",
			CreatedAt: "2026-03-23T20:00:00Z",
			WebURL:    "https://gitlab.example/pipelines/42",
		},
	}

	branchAgents := map[string]mobilePipelineAgentRef{
		"feature/mobile-parity": {ID: "codex-1", Type: "codex"},
	}

	callCount := 0
	results := buildMobilePipelineResponses(pipelines, branchAgents, func(project string, pipelineID int) (*bridge.PipelineDetail, error) {
		callCount++
		if project != "services/loom-core" {
			t.Fatalf("expected project to be propagated, got %q", project)
		}
		if pipelineID != 42 {
			t.Fatalf("expected pipeline ID 42, got %d", pipelineID)
		}
		return &bridge.PipelineDetail{
			PipelineInfo:    pipelines[0],
			Stages:          []bridge.PipelineStage{{Name: "build", Status: "success"}, {Name: "test", Status: "running", Jobs: []bridge.PipelineJob{{ID: 7, Name: "test", Status: "running", Stage: "test"}}}},
			CompletedStages: 1,
			TotalStages:     2,
			CurrentStage:    "test",
			FailedJobCount:  1,
		}, nil
	})

	if callCount != 1 {
		t.Fatalf("expected one detail lookup, got %d", callCount)
	}
	if len(results) != 1 {
		t.Fatalf("expected one pipeline response, got %#v", results)
	}

	got := results[0]
	if got.CurrentStage != "test" {
		t.Fatalf("expected current stage test, got %#v", got.CurrentStage)
	}
	if got.CompletedStages != 1 || got.TotalStages != 2 || got.FailedJobCount != 1 {
		t.Fatalf("unexpected stage counts: %#v", got)
	}
	if got.AgentID != "codex-1" || got.AgentType != "codex" || got.Correlation != "branch_match" {
		t.Fatalf("expected branch correlation to be attached, got %#v", got)
	}
	if len(got.Stages) != 2 {
		t.Fatalf("expected stage breakdown, got %#v", got.Stages)
	}
	if len(got.Stages[1].Jobs) != 1 || got.Stages[1].Jobs[0].Name != "test" {
		t.Fatalf("expected job detail to be preserved, got %#v", got.Stages[1])
	}
}

func TestHandleMobilePipelines_ColdStartRefreshesAndReturnsDetail(t *testing.T) {
	deps := newTestMockDeps()
	deps.agent = bridge.NewAgentBridge(&pipelineOpsCaller{t: t})
	deps.monitors = Monitors{
		Fleet:    &monitor.FleetMonitor{},
		Pipeline: monitor.NewPipelineMonitor(deps.agent, []string{"services/loom-core"}, slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mobile/v1/pipelines", d.handleMobilePipelines)

	req := newAuthRequest("GET", "/api/mobile/v1/pipelines")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var env Envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data map, got %T", env.Data)
	}
	if available, _ := data["available"].(bool); !available {
		t.Fatal("expected pipelines to be available on cold start")
	}
	pipelines, ok := data["pipelines"].([]any)
	if !ok {
		t.Fatalf("expected pipelines array, got %T", data["pipelines"])
	}
	if len(pipelines) != 1 {
		t.Fatalf("expected one pipeline, got %#v", pipelines)
	}
	pipeline, ok := pipelines[0].(map[string]any)
	if !ok {
		t.Fatalf("expected pipeline object, got %T", pipelines[0])
	}
	if pipeline["current_stage"] != "test" {
		t.Fatalf("expected current_stage=test, got %#v", pipeline["current_stage"])
	}
	if pipeline["completed_stages"] != float64(1) || pipeline["total_stages"] != float64(2) || pipeline["failed_job_count"] != float64(0) {
		t.Fatalf("unexpected stage summary: %#v", pipeline)
	}
	stages, ok := pipeline["stages"].([]any)
	if !ok || len(stages) != 2 {
		t.Fatalf("expected stage detail, got %#v", pipeline["stages"])
	}
}

func TestHandleMobilePipelines_FallsBackToRecentRelevantPipelinesWhenNoActiveOnesExist(t *testing.T) {
	deps := newTestMockDeps()
	deps.agent = bridge.NewAgentBridge(&recentOnlyPipelineOpsCaller{})
	deps.monitors = Monitors{
		Fleet:    &monitor.FleetMonitor{},
		Pipeline: monitor.NewPipelineMonitor(deps.agent, []string{"services/loom-core"}, slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mobile/v1/pipelines", d.handleMobilePipelines)

	req := newAuthRequest("GET", "/api/mobile/v1/pipelines")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var env Envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data map, got %T", env.Data)
	}
	pipelines, ok := data["pipelines"].([]any)
	if !ok {
		t.Fatalf("expected pipelines array, got %T", data["pipelines"])
	}
	if len(pipelines) == 0 {
		t.Fatal("expected recent pipeline fallback to return results")
	}
	first, ok := pipelines[0].(map[string]any)
	if !ok {
		t.Fatalf("expected pipeline object, got %T", pipelines[0])
	}
	if first["ref"] != "codex/mobile-parity" {
		t.Fatalf("expected codex pipeline to outrank renovate fallback, got %#v", first["ref"])
	}
}

func TestHandleMobilePipelines_ReturnsUpstreamErrorWhenNoPipelineDataIsAvailable(t *testing.T) {
	deps := newTestMockDeps()
	deps.agent = bridge.NewAgentBridge(&failingPipelineOpsCaller{})
	deps.monitors = Monitors{
		Fleet:    &monitor.FleetMonitor{},
		Pipeline: monitor.NewPipelineMonitor(deps.agent, []string{"services/loom-core"}, slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mobile/v1/pipelines", d.handleMobilePipelines)

	req := newAuthRequest("GET", "/api/mobile/v1/pipelines")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var env Envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.OK {
		t.Fatal("expected ok=false")
	}
	errBody, ok := env.Error.(map[string]any)
	if !ok {
		t.Fatalf("expected error map, got %T", env.Error)
	}
	if errBody["code"] != "upstream_unavailable" {
		t.Fatalf("expected upstream_unavailable error, got %#v", errBody)
	}
}
