package bridge

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestAgentBridge_ListActivePipelines_UsesProjectArgAndParsesWrappedResults(t *testing.T) {
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
		if req.Name != "gitlab__list_pipelines" {
			t.Fatalf("unexpected tool name: %s", req.Name)
		}
		if _, ok := req.Arguments["project_id"]; ok {
			t.Fatalf("unexpected legacy project_id arg: %#v", req.Arguments)
		}
		if got, _ := req.Arguments["project"].(string); got != "services/loom-core" {
			t.Fatalf("expected project arg to be services/loom-core, got %#v", req.Arguments["project"])
		}
		status, _ := req.Arguments["status"].(string)
		callCount++

		var payload string
		switch status {
		case "running":
			payload = `{"pipelines":[{"id":101,"ref":"main","status":"running","created_at":"2026-03-19T12:00:00Z"}],"count":1}`
		case "pending":
			payload = `{"pipelines":[{"id":102,"ref":"feature","status":"pending","created_at":"2026-03-19T12:01:00Z"}],"count":1}`
		default:
			t.Fatalf("unexpected status arg: %q", status)
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
	pipelines, err := bridge.ListActivePipelines([]string{"services/loom-core"})
	if err != nil {
		t.Fatalf("ListActivePipelines: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 gitlab__list_pipelines calls, got %d", callCount)
	}
	if len(pipelines) != 2 {
		t.Fatalf("expected 2 pipelines, got %#v", pipelines)
	}
	if pipelines[0].Project != "services/loom-core" || pipelines[1].Project != "services/loom-core" {
		t.Fatalf("expected project field to be backfilled, got %#v", pipelines)
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
			payload = `{"jobs":[{"id":1,"name":"build","status":"success","stage":"build"},{"id":2,"name":"test","status":"running","stage":"test"}],"count":2}`
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
}
