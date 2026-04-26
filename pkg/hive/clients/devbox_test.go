package clients

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/hive/pipeline"
)

// devboxStubResult builds a CallTool envelope whose first content block
// is the JSON of a qualityGateResult.
func devboxStubResult(t *testing.T, body devboxQualityGateResult) []byte {
	t.Helper()
	bodyJSON, _ := json.Marshal(body)
	res := mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: string(bodyJSON)}},
	}
	out, _ := json.Marshal(res)
	return out
}

func TestDevboxClient_HappyPathPropagatesPassedAndChecks(t *testing.T) {
	body := devboxQualityGateResult{
		Language:        "go",
		Passed:          true,
		TotalDurationMs: 5000,
		Checks: []devboxQualityCheckRow{
			{Name: "fmt", Passed: true, DurationMs: 200},
			{Name: "lint", Passed: true, DurationMs: 1500},
			{Name: "test", Passed: true, DurationMs: 3300},
		},
	}
	ft := &fakeTransport{
		responses: map[string][]byte{
			"initialize": []byte(`{"protocolVersion":"2024-11-05","serverInfo":{"name":"x","version":"1"}}`),
			"tools/call": devboxStubResult(t, body),
		},
	}
	hub := newTestHubClient(t, ft)
	dc := NewDevboxClient(hub)
	resp, err := dc.QualityGate(context.Background(), pipeline.DevboxRequest{
		Project: "loom-core",
		AgentID: "claude-code",
	})
	if err != nil {
		t.Fatalf("quality gate: %v", err)
	}
	if !resp.Passed {
		t.Errorf("expected Passed=true")
	}
	if resp.Language != "go" {
		t.Errorf("language = %q", resp.Language)
	}
	if len(resp.Checks) != 3 {
		t.Errorf("checks len = %d, want 3", len(resp.Checks))
	}
	if resp.Checks[1].Name != "lint" {
		t.Errorf("check 1 name = %q", resp.Checks[1].Name)
	}
	if resp.Checks[2].Duration < 3.0 {
		t.Errorf("check 2 duration should be in seconds: %v", resp.Checks[2].Duration)
	}
}

func TestDevboxClient_PassesProjectAndAgentIDArgs(t *testing.T) {
	body := devboxQualityGateResult{Language: "go", Passed: true}
	ft := &fakeTransport{
		responses: map[string][]byte{
			"initialize": []byte(`{}`),
			"tools/call": devboxStubResult(t, body),
		},
	}
	hub := newTestHubClient(t, ft)
	dc := NewDevboxClient(hub)
	if _, err := dc.QualityGate(context.Background(), pipeline.DevboxRequest{
		Project: "platform/gitops",
		AgentID: "loom-hive",
	}); err != nil {
		t.Fatalf("call: %v", err)
	}
	sent := ft.sentMessages()
	var callMsg mcp.Message
	for _, m := range sent {
		if m.Method == "tools/call" {
			callMsg = m
		}
	}
	var params mcp.CallToolParams
	if err := json.Unmarshal(callMsg.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.Name != "devbox_quality_gate" {
		t.Errorf("tool name = %q", params.Name)
	}
	if params.Arguments["project"] != "platform/gitops" {
		t.Errorf("project arg = %v", params.Arguments["project"])
	}
	if params.Arguments["agent_id"] != "loom-hive" {
		t.Errorf("agent_id arg = %v", params.Arguments["agent_id"])
	}
}

func TestDevboxClient_FailedGateBuildsLogTail(t *testing.T) {
	body := devboxQualityGateResult{
		Language: "python",
		Passed:   false,
		Checks: []devboxQualityCheckRow{
			{Name: "fmt", Passed: true, DurationMs: 100},
			{Name: "lint", Passed: false, DurationMs: 500, OutputTail: "ruff: E501 line too long\n"},
			{Name: "test", Passed: false, DurationMs: 800, ExitCode: 1, OutputTail: "1 failed, 5 passed\n"},
		},
	}
	ft := &fakeTransport{
		responses: map[string][]byte{
			"initialize": []byte(`{}`),
			"tools/call": devboxStubResult(t, body),
		},
	}
	hub := newTestHubClient(t, ft)
	dc := NewDevboxClient(hub)
	resp, err := dc.QualityGate(context.Background(), pipeline.DevboxRequest{Project: "loom-core"})
	if err != nil {
		t.Fatalf("call should succeed even when gate fails: %v", err)
	}
	if resp.Passed {
		t.Errorf("Passed should be false")
	}
	if !strings.Contains(resp.LogTail, "FAIL lint") {
		t.Errorf("log tail missing FAIL marker: %q", resp.LogTail)
	}
	if !strings.Contains(resp.LogTail, "ruff: E501") {
		t.Errorf("log tail missing failed-check output: %q", resp.LogTail)
	}
	if !strings.Contains(resp.LogTail, "PASS fmt") {
		t.Errorf("log tail missing PASS marker for passing check: %q", resp.LogTail)
	}
}

func TestDevboxClient_RequiresProject(t *testing.T) {
	hub := newTestHubClient(t, &fakeTransport{})
	if _, err := NewDevboxClient(hub).QualityGate(context.Background(), pipeline.DevboxRequest{}); err == nil {
		t.Error("expected error when Project empty")
	}
}

func TestDevboxClient_NilHubErrors(t *testing.T) {
	dc := &DevboxClient{}
	if _, err := dc.QualityGate(context.Background(), pipeline.DevboxRequest{Project: "x"}); err == nil {
		t.Error("expected error with nil hub")
	}
}

func TestDevboxClient_DecodeFailureSurfacesRawBody(t *testing.T) {
	res := mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: "not-json"}},
	}
	resBytes, _ := json.Marshal(res)
	ft := &fakeTransport{
		responses: map[string][]byte{
			"initialize": []byte(`{}`),
			"tools/call": resBytes,
		},
	}
	hub := newTestHubClient(t, ft)
	_, err := NewDevboxClient(hub).QualityGate(context.Background(), pipeline.DevboxRequest{Project: "x"})
	if err == nil {
		t.Error("expected decode error")
	}
	if !strings.Contains(err.Error(), "not-json") {
		t.Errorf("error should expose raw body: %v", err)
	}
}
