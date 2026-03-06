package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/httpclient"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	os.Exit(m.Run())
}

func decodeToolResult(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if len(res.Content) == 0 {
		t.Fatal("expected content in result")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("result content is not JSON: %v", err)
	}
	return decoded
}

func newTestMentatlabServer(baseURL string) *mentatlabServer {
	return &mentatlabServer{
		baseURL:          baseURL,
		token:            "test-token",
		maxResponseBytes: 1024 * 1024,
		httpClient: httpclient.New(httpclient.Config{
			Timeout:          5 * time.Second,
			MaxRetries:       0,
			MaxResponseBytes: 1024 * 1024,
		}),
	}
}

func TestNewMentatlabServerFromEnvRequiresBaseURL(t *testing.T) {
	t.Setenv("MENTATLAB_BASE_URL", "")
	t.Setenv("ORCHESTRATOR_BASE_URL", "")

	_, err := newMentatlabServerFromEnv()
	if err == nil {
		t.Fatal("expected missing base URL to return an error")
	}
}

// --- Run handler tests ---

func TestHandleListRunsCallsRunsEndpoint(t *testing.T) {
	var seenMethod string
	var seenPath string
	var seenAuth string
	var seenQuery map[string]string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		seenAuth = r.Header.Get("Authorization")
		seenQuery = map[string]string{
			"limit":  r.URL.Query().Get("limit"),
			"cursor": r.URL.Query().Get("cursor"),
			"owner":  r.URL.Query().Get("owner"),
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"runs":["run-1"],"total":1}`))
	}))
	defer ts.Close()

	srv := newTestMentatlabServer(ts.URL)
	res, err := srv.handleListRuns(context.Background(), map[string]any{
		"limit":  25,
		"cursor": "next-cursor",
		"owner":  "alice",
	})
	if err != nil {
		t.Fatalf("handleListRuns returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error payload: %s", res.Content[0].Text)
	}

	if seenMethod != http.MethodGet {
		t.Fatalf("method=%s, want GET", seenMethod)
	}
	if seenPath != "/api/v1/runs" {
		t.Fatalf("path=%s, want /api/v1/runs", seenPath)
	}
	if seenAuth != "Bearer test-token" {
		t.Fatalf("authorization header mismatch: %q", seenAuth)
	}
	if seenQuery["limit"] != "25" || seenQuery["cursor"] != "next-cursor" || seenQuery["owner"] != "alice" {
		t.Fatalf("unexpected query params: %+v", seenQuery)
	}

	decoded := decodeToolResult(t, res)
	if decoded["ok"] != true {
		t.Fatalf("ok=%v, want true", decoded["ok"])
	}
	data, ok := decoded["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data object, got %T", decoded["data"])
	}
	if got := data["total"]; got != float64(1) {
		t.Fatalf("data.total=%v, want 1", got)
	}
}

func TestHandleCreateRunPostsPayload(t *testing.T) {
	var seenBody map[string]any
	var seenMethod string
	var seenPath string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&seenBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"runId":"run-created","status":"created"}`))
	}))
	defer ts.Close()

	srv := newTestMentatlabServer(ts.URL)
	res, err := srv.handleCreateRun(context.Background(), map[string]any{
		"name":       "integration-run",
		"auto_start": true,
		"plan": map[string]any{
			"nodes": []any{
				map[string]any{"id": "n1", "type": "agent"},
			},
			"edges": []any{},
		},
	})
	if err != nil {
		t.Fatalf("handleCreateRun returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error payload: %s", res.Content[0].Text)
	}

	if seenMethod != http.MethodPost {
		t.Fatalf("method=%s, want POST", seenMethod)
	}
	if seenPath != "/api/v1/runs" {
		t.Fatalf("path=%s, want /api/v1/runs", seenPath)
	}
	if seenBody["name"] != "integration-run" {
		t.Fatalf("body.name=%v, want integration-run", seenBody["name"])
	}
	if seenBody["auto_start"] != true {
		t.Fatalf("body.auto_start=%v, want true", seenBody["auto_start"])
	}
	if _, ok := seenBody["plan"].(map[string]any); !ok {
		t.Fatalf("expected body.plan object, got %T", seenBody["plan"])
	}
}

func TestHandleGetRunReturnsErrorOnNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"run not found"}`))
	}))
	defer ts.Close()

	srv := newTestMentatlabServer(ts.URL)
	res, err := srv.handleGetRun(context.Background(), map[string]any{"run_id": "missing"})
	if err != nil {
		t.Fatalf("handleGetRun returned error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error result for not found response, got success: %s", res.Content[0].Text)
	}
}

func TestHandleCancelRunCallsCancelEndpoint(t *testing.T) {
	var seenMethod string
	var seenPath string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"cancelled"}`))
	}))
	defer ts.Close()

	srv := newTestMentatlabServer(ts.URL)
	res, err := srv.handleCancelRun(context.Background(), map[string]any{"run_id": "run-1"})
	if err != nil {
		t.Fatalf("handleCancelRun returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error payload: %s", res.Content[0].Text)
	}

	if seenMethod != http.MethodPost {
		t.Fatalf("method=%s, want POST", seenMethod)
	}
	if seenPath != "/api/v1/runs/run-1/cancel" {
		t.Fatalf("path=%s, want /api/v1/runs/run-1/cancel", seenPath)
	}
}

func TestHandleCloneRunCallsCloneEndpoint(t *testing.T) {
	var seenMethod string
	var seenPath string
	var seenBody map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&seenBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"runId":"run-cloned","status":"created"}`))
	}))
	defer ts.Close()

	srv := newTestMentatlabServer(ts.URL)
	res, err := srv.handleCloneRun(context.Background(), map[string]any{
		"run_id":     "run-1",
		"auto_start": true,
	})
	if err != nil {
		t.Fatalf("handleCloneRun returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %s", res.Content[0].Text)
	}

	if seenMethod != http.MethodPost {
		t.Fatalf("method=%s, want POST", seenMethod)
	}
	if seenPath != "/api/v1/runs/run-1/clone" {
		t.Fatalf("path=%s, want /api/v1/runs/run-1/clone", seenPath)
	}
	if seenBody["auto_start"] != true {
		t.Fatalf("body.auto_start=%v, want true", seenBody["auto_start"])
	}
}

func TestHandleApproveGateCallsApproveEndpoint(t *testing.T) {
	var seenMethod string
	var seenPath string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"approved"}`))
	}))
	defer ts.Close()

	srv := newTestMentatlabServer(ts.URL)
	res, err := srv.handleApproveGate(context.Background(), map[string]any{
		"run_id":  "run-1",
		"node_id": "gate-a",
	})
	if err != nil {
		t.Fatalf("handleApproveGate returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %s", res.Content[0].Text)
	}

	if seenMethod != http.MethodPost {
		t.Fatalf("method=%s, want POST", seenMethod)
	}
	if seenPath != "/api/v1/runs/run-1/nodes/gate-a/approve" {
		t.Fatalf("path=%s, want /api/v1/runs/run-1/nodes/gate-a/approve", seenPath)
	}
}

func TestHandleRejectGateCallsRejectEndpoint(t *testing.T) {
	var seenMethod string
	var seenPath string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"rejected"}`))
	}))
	defer ts.Close()

	srv := newTestMentatlabServer(ts.URL)
	res, err := srv.handleRejectGate(context.Background(), map[string]any{
		"run_id":  "run-1",
		"node_id": "gate-b",
	})
	if err != nil {
		t.Fatalf("handleRejectGate returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %s", res.Content[0].Text)
	}

	if seenMethod != http.MethodPost {
		t.Fatalf("method=%s, want POST", seenMethod)
	}
	if seenPath != "/api/v1/runs/run-1/nodes/gate-b/reject" {
		t.Fatalf("path=%s, want /api/v1/runs/run-1/nodes/gate-b/reject", seenPath)
	}
}

// --- Agent handler tests ---

func TestHandleListAgentsCallsAgentsEndpoint(t *testing.T) {
	var seenMethod string
	var seenPath string
	var seenQuery map[string]string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		seenQuery = map[string]string{
			"limit":        r.URL.Query().Get("limit"),
			"offset":       r.URL.Query().Get("offset"),
			"capabilities": r.URL.Query().Get("capabilities"),
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"agents":[{"id":"a1","name":"echo"}],"total":1}`))
	}))
	defer ts.Close()

	srv := newTestMentatlabServer(ts.URL)
	res, err := srv.handleListAgents(context.Background(), map[string]any{
		"limit":        10,
		"capabilities": "text,code",
	})
	if err != nil {
		t.Fatalf("handleListAgents returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %s", res.Content[0].Text)
	}

	if seenMethod != http.MethodGet {
		t.Fatalf("method=%s, want GET", seenMethod)
	}
	if seenPath != "/api/v1/agents" {
		t.Fatalf("path=%s, want /api/v1/agents", seenPath)
	}
	if seenQuery["limit"] != "10" {
		t.Fatalf("query.limit=%s, want 10", seenQuery["limit"])
	}
	if seenQuery["capabilities"] != "text,code" {
		t.Fatalf("query.capabilities=%s, want text,code", seenQuery["capabilities"])
	}
}

func TestHandleGetAgentCallsAgentEndpoint(t *testing.T) {
	var seenMethod string
	var seenPath string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"agent-1","name":"echo"}`))
	}))
	defer ts.Close()

	srv := newTestMentatlabServer(ts.URL)
	res, err := srv.handleGetAgent(context.Background(), map[string]any{"agent_id": "agent-1"})
	if err != nil {
		t.Fatalf("handleGetAgent returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %s", res.Content[0].Text)
	}

	if seenMethod != http.MethodGet {
		t.Fatalf("method=%s, want GET", seenMethod)
	}
	if seenPath != "/api/v1/agents/agent-1" {
		t.Fatalf("path=%s, want /api/v1/agents/agent-1", seenPath)
	}
}

func TestHandleGetAgentReturnsErrorOnNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"agent not found"}`))
	}))
	defer ts.Close()

	srv := newTestMentatlabServer(ts.URL)
	res, err := srv.handleGetAgent(context.Background(), map[string]any{"agent_id": "missing"})
	if err != nil {
		t.Fatalf("handleGetAgent returned error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error result for not found response, got success: %s", res.Content[0].Text)
	}
}

func TestHandleRegisterAgentPostsPayload(t *testing.T) {
	var seenMethod string
	var seenPath string
	var seenBody map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&seenBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"new-agent","name":"my-agent"}`))
	}))
	defer ts.Close()

	srv := newTestMentatlabServer(ts.URL)
	res, err := srv.handleRegisterAgent(context.Background(), map[string]any{
		"name":         "my-agent",
		"image":        "registry.example.com/agent:v1",
		"description":  "Test agent",
		"capabilities": []any{"text", "code"},
	})
	if err != nil {
		t.Fatalf("handleRegisterAgent returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %s", res.Content[0].Text)
	}

	if seenMethod != http.MethodPost {
		t.Fatalf("method=%s, want POST", seenMethod)
	}
	if seenPath != "/api/v1/agents" {
		t.Fatalf("path=%s, want /api/v1/agents", seenPath)
	}
	if seenBody["name"] != "my-agent" {
		t.Fatalf("body.name=%v, want my-agent", seenBody["name"])
	}
	if seenBody["image"] != "registry.example.com/agent:v1" {
		t.Fatalf("body.image=%v, want registry.example.com/agent:v1", seenBody["image"])
	}
	if seenBody["description"] != "Test agent" {
		t.Fatalf("body.description=%v, want Test agent", seenBody["description"])
	}
}

// --- Flow handler tests ---

func TestHandleListFlowsCallsFlowsEndpoint(t *testing.T) {
	var seenMethod string
	var seenPath string
	var seenQuery map[string]string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		seenQuery = map[string]string{
			"limit":  r.URL.Query().Get("limit"),
			"offset": r.URL.Query().Get("offset"),
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"flows":[{"id":"f1","name":"test-flow"}],"total":1}`))
	}))
	defer ts.Close()

	srv := newTestMentatlabServer(ts.URL)
	res, err := srv.handleListFlows(context.Background(), map[string]any{"limit": 20})
	if err != nil {
		t.Fatalf("handleListFlows returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %s", res.Content[0].Text)
	}

	if seenMethod != http.MethodGet {
		t.Fatalf("method=%s, want GET", seenMethod)
	}
	if seenPath != "/api/v1/flows" {
		t.Fatalf("path=%s, want /api/v1/flows", seenPath)
	}
	if seenQuery["limit"] != "20" {
		t.Fatalf("query.limit=%s, want 20", seenQuery["limit"])
	}
}

func TestHandleGetFlowCallsFlowEndpoint(t *testing.T) {
	var seenMethod string
	var seenPath string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"flow-1","name":"my-flow"}`))
	}))
	defer ts.Close()

	srv := newTestMentatlabServer(ts.URL)
	res, err := srv.handleGetFlow(context.Background(), map[string]any{"flow_id": "flow-1"})
	if err != nil {
		t.Fatalf("handleGetFlow returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %s", res.Content[0].Text)
	}

	if seenMethod != http.MethodGet {
		t.Fatalf("method=%s, want GET", seenMethod)
	}
	if seenPath != "/api/v1/flows/flow-1" {
		t.Fatalf("path=%s, want /api/v1/flows/flow-1", seenPath)
	}
}

func TestHandleGetFlowReturnsErrorOnNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"flow not found"}`))
	}))
	defer ts.Close()

	srv := newTestMentatlabServer(ts.URL)
	res, err := srv.handleGetFlow(context.Background(), map[string]any{"flow_id": "missing"})
	if err != nil {
		t.Fatalf("handleGetFlow returned error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error result for not found response, got success: %s", res.Content[0].Text)
	}
}

func TestHandleCreateFlowPostsPayload(t *testing.T) {
	var seenMethod string
	var seenPath string
	var seenBody map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&seenBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"new-flow","name":"my-flow"}`))
	}))
	defer ts.Close()

	srv := newTestMentatlabServer(ts.URL)
	res, err := srv.handleCreateFlow(context.Background(), map[string]any{
		"name":        "my-flow",
		"description": "A test flow",
		"graph": map[string]any{
			"nodes": []any{
				map[string]any{"id": "n1", "type": "agent", "agent_id": "echo"},
			},
			"edges": []any{},
		},
	})
	if err != nil {
		t.Fatalf("handleCreateFlow returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %s", res.Content[0].Text)
	}

	if seenMethod != http.MethodPost {
		t.Fatalf("method=%s, want POST", seenMethod)
	}
	if seenPath != "/api/v1/flows" {
		t.Fatalf("path=%s, want /api/v1/flows", seenPath)
	}
	if seenBody["name"] != "my-flow" {
		t.Fatalf("body.name=%v, want my-flow", seenBody["name"])
	}
	if seenBody["description"] != "A test flow" {
		t.Fatalf("body.description=%v, want 'A test flow'", seenBody["description"])
	}
	if _, ok := seenBody["graph"].(map[string]any); !ok {
		t.Fatalf("expected body.graph object, got %T", seenBody["graph"])
	}
}

// --- Diagnostics handler tests ---

func TestHandleHealthCallsHealthEndpoint(t *testing.T) {
	var seenMethod string
	var seenPath string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()

	srv := newTestMentatlabServer(ts.URL)
	res, err := srv.handleHealth(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleHealth returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %s", res.Content[0].Text)
	}

	if seenMethod != http.MethodGet {
		t.Fatalf("method=%s, want GET", seenMethod)
	}
	if seenPath != "/health" {
		t.Fatalf("path=%s, want /health", seenPath)
	}

	decoded := decodeToolResult(t, res)
	if decoded["ok"] != true {
		t.Fatalf("ok=%v, want true", decoded["ok"])
	}
}
