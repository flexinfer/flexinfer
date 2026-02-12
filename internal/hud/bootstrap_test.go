package hud

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func TestBootstrapWorkflowDefinitions_LoadsYAMLAndYMLWithFullPayload(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	workflowsDir := filepath.Join(".agents", "workflows")
	if err := os.MkdirAll(workflowsDir, 0755); err != nil {
		t.Fatalf("mkdir workflows: %v", err)
	}

	const yamlWorkflow = `name: build-and-deploy
description: Build and deploy service
namespace: team/platform
steps:
  - id: build
    name: Build
    step_type: tool
    tool_name: devbox_exec
    server_name: devbox
    tool_args:
      project: loom-core
      command: make test
  - id: approve
    name: Approval
    step_type: approval
    depends_on: ["build"]
    requires_approval: true
    approval_message: Continue deploy?
`
	const ymlWorkflow = `name: smoke-check
steps:
  - id: ping
    name: Ping
    step_type: tool
    tool_name: time
`

	if err := os.WriteFile(filepath.Join(workflowsDir, "build.yaml"), []byte(yamlWorkflow), 0644); err != nil {
		t.Fatalf("write build.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workflowsDir, "smoke.yml"), []byte(ymlWorkflow), 0644); err != nil {
		t.Fatalf("write smoke.yml: %v", err)
	}

	sockPath, handlers := newMockDaemonForApp(t)
	var (
		mu      sync.Mutex
		defined []map[string]any
	)
	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name == "agent_context__agent_workflow_define" {
			mu.Lock()
			defined = append(defined, req.Arguments)
			mu.Unlock()
		}
		return map[string]any{
			"isError": false,
			"content": []map[string]any{
				{"type": "text", "text": `{"ok":true,"definition_id":"def-1","name":"ok","step_count":1}`},
			},
		}, nil
	})

	client := bridge.NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect to mock daemon: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	app := &App{
		agent:  bridge.NewAgentBridge(client),
		logger: slog.Default(),
	}
	app.bootstrapWorkflowDefinitions()

	mu.Lock()
	defer mu.Unlock()
	if len(defined) != 2 {
		t.Fatalf("expected 2 workflow definitions to be registered, got %d", len(defined))
	}

	var buildDef map[string]any
	for _, d := range defined {
		if name, _ := d["name"].(string); name == "build-and-deploy" {
			buildDef = d
			break
		}
	}
	if buildDef == nil {
		t.Fatalf("expected build-and-deploy definition in registered payloads: %#v", defined)
	}

	steps, ok := buildDef["steps"].([]any)
	if !ok || len(steps) != 2 {
		t.Fatalf("expected 2 steps in build definition, got %#v", buildDef["steps"])
	}

	step0, _ := steps[0].(map[string]any)
	if got, _ := step0["tool_name"].(string); got != "devbox_exec" {
		t.Fatalf("expected tool_name to be preserved, got %q", got)
	}
	if got, _ := step0["server_name"].(string); got != "devbox" {
		t.Fatalf("expected server_name to be preserved, got %q", got)
	}

	step1, _ := steps[1].(map[string]any)
	if got, _ := step1["requires_approval"].(bool); !got {
		t.Fatalf("expected requires_approval=true, got %#v", step1["requires_approval"])
	}
	dependsOn, _ := step1["depends_on"].([]any)
	if len(dependsOn) != 1 || dependsOn[0] != "build" {
		t.Fatalf("expected depends_on=[build], got %#v", step1["depends_on"])
	}
}
