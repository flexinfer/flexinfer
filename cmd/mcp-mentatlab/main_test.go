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
