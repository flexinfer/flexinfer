package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/mcperror"
)

func TestMain(m *testing.M) {
	os.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	os.Exit(m.Run())
}

// rewriteTransport rewrites all requests to point at a local httptest server.
type rewriteTransport struct {
	target *url.URL
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = t.target.Scheme
	req.URL.Host = t.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

// newTestServer creates an actionsServer whose HTTP client routes to the given test server.
func newTestActionsServer(ts *httptest.Server) *actionsServer {
	client := httpclient.NewDefault()
	target, _ := url.Parse(ts.URL)
	client.HTTP().Transport = &rewriteTransport{target: target}
	return &actionsServer{
		token:      "test-token",
		httpClient: client,
	}
}

// =====================================================================
// request tests
// =====================================================================

func TestRequest_SetsHeaders(t *testing.T) {
	var gotAuth, gotAccept, gotAPIVersion string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotAPIVersion = r.Header.Get("X-GitHub-Api-Version")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{}`)
	}))
	defer ts.Close()

	srv := newTestActionsServer(ts)

	_, err := srv.request(context.Background(), "GET", "/user", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("expected Authorization header, got %q", gotAuth)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Fatalf("expected Accept header, got %q", gotAccept)
	}
	if gotAPIVersion != "2022-11-28" {
		t.Fatalf("expected API version header, got %q", gotAPIVersion)
	}
}

func TestRequest_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"Not Found"}`)
	}))
	defer ts.Close()

	srv := newTestActionsServer(ts)

	_, err := srv.request(context.Background(), "GET", "/repos/x/y/actions/workflows", nil)
	if err == nil {
		t.Fatal("expected error for 404 response")
	}

	mcpErr, ok := err.(*mcperror.Error)
	if !ok {
		t.Fatalf("expected *mcperror.Error, got %T: %v", err, err)
	}
	if mcpErr.Code != mcperror.CodeNotFound {
		t.Fatalf("expected code %q, got %q", mcperror.CodeNotFound, mcpErr.Code)
	}
}

func TestRequest_SendsBody(t *testing.T) {
	var gotBody map[string]any
	var gotContentType string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	srv := newTestActionsServer(ts)

	_, err := srv.request(context.Background(), "POST", "/test", map[string]any{
		"ref": "main",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotContentType != "application/json" {
		t.Fatalf("expected Content-Type json, got %q", gotContentType)
	}
	if gotBody["ref"] != "main" {
		t.Fatalf("expected ref=main in body, got %v", gotBody)
	}
}

// =====================================================================
// handleListWorkflows tests
// =====================================================================

func TestHandleListWorkflows_MissingRequired(t *testing.T) {
	srv := &actionsServer{token: "x", httpClient: httpclient.NewDefault()}

	result, err := srv.handleListWorkflows(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("expected nil error for validation failure, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected IsError=true for missing required params")
	}
}

func TestHandleListWorkflows_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/repos/octocat/hello/actions/workflows") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"total_count": 1,
			"workflows": []map[string]any{
				{
					"id":    42,
					"name":  "CI",
					"path":  ".github/workflows/ci.yml",
					"state": "active",
				},
			},
		})
	}))
	defer ts.Close()

	srv := newTestActionsServer(ts)

	result, err := srv.handleListWorkflows(context.Background(), map[string]any{
		"owner": "octocat",
		"repo":  "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatal("expected success result")
	}
	// Result is text, not JSON
	text := result.Content[0].Text
	if !strings.Contains(text, "CI") {
		t.Fatalf("expected workflow name in output, got %q", text)
	}
	if !strings.Contains(text, "total_count: 1") {
		t.Fatalf("expected total_count in output, got %q", text)
	}
}

// =====================================================================
// handleGetWorkflow tests
// =====================================================================

func TestHandleGetWorkflow_MissingRequired(t *testing.T) {
	srv := &actionsServer{token: "x", httpClient: httpclient.NewDefault()}

	result, err := srv.handleGetWorkflow(context.Background(), map[string]any{
		"owner": "octocat",
		"repo":  "hello",
	})
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected IsError=true for missing workflow_id")
	}
}

func TestHandleGetWorkflow_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/actions/workflows/ci.yml") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    42,
			"name":  "CI",
			"state": "active",
		})
	}))
	defer ts.Close()

	srv := newTestActionsServer(ts)

	result, err := srv.handleGetWorkflow(context.Background(), map[string]any{
		"owner":       "octocat",
		"repo":        "hello",
		"workflow_id": "ci.yml",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatal("expected success result")
	}
}

// =====================================================================
// handleListWorkflowRuns tests
// =====================================================================

func TestHandleListWorkflowRuns_WithFilters(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"total_count":   0,
			"workflow_runs": []any{},
		})
	}))
	defer ts.Close()

	srv := newTestActionsServer(ts)

	_, err := srv.handleListWorkflowRuns(context.Background(), map[string]any{
		"owner":       "octocat",
		"repo":        "hello",
		"workflow_id": "ci.yml",
		"branch":      "main",
		"status":      "completed",
		"conclusion":  "failure",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "workflows/ci.yml/runs") {
		t.Fatalf("expected workflow-specific runs path, got %q", gotPath)
	}
	if !strings.Contains(gotPath, "branch=main") {
		t.Fatalf("expected branch filter, got %q", gotPath)
	}
	if !strings.Contains(gotPath, "status=completed") {
		t.Fatalf("expected status filter, got %q", gotPath)
	}
	if !strings.Contains(gotPath, "conclusion=failure") {
		t.Fatalf("expected conclusion filter, got %q", gotPath)
	}
}

func TestHandleListWorkflowRuns_RepoLevel(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"total_count":   0,
			"workflow_runs": []any{},
		})
	}))
	defer ts.Close()

	srv := newTestActionsServer(ts)

	_, err := srv.handleListWorkflowRuns(context.Background(), map[string]any{
		"owner": "octocat",
		"repo":  "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/actions/runs") {
		t.Fatalf("expected repo-level runs path, got %q", gotPath)
	}
}

// =====================================================================
// handleTriggerWorkflow tests
// =====================================================================

func TestHandleTriggerWorkflow_MissingRequired(t *testing.T) {
	srv := &actionsServer{token: "x", httpClient: httpclient.NewDefault()}

	result, err := srv.handleTriggerWorkflow(context.Background(), map[string]any{
		"owner": "octocat",
		"repo":  "hello",
	})
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected IsError=true for missing required params")
	}
}

func TestHandleTriggerWorkflow_Success(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	srv := newTestActionsServer(ts)

	result, err := srv.handleTriggerWorkflow(context.Background(), map[string]any{
		"owner":       "octocat",
		"repo":        "hello",
		"workflow_id": "deploy.yml",
		"ref":         "main",
		"inputs":      map[string]any{"env": "staging"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatal("expected success result")
	}
	if gotBody["ref"] != "main" {
		t.Fatalf("expected ref=main in body, got %v", gotBody["ref"])
	}
	inputs, ok := gotBody["inputs"].(map[string]any)
	if !ok || inputs["env"] != "staging" {
		t.Fatalf("expected inputs.env=staging, got %v", gotBody["inputs"])
	}
}

// =====================================================================
// handleCancelWorkflowRun tests
// =====================================================================

func TestHandleCancelWorkflowRun_Success(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	defer ts.Close()

	srv := newTestActionsServer(ts)

	result, err := srv.handleCancelWorkflowRun(context.Background(), map[string]any{
		"owner":  "octocat",
		"repo":   "hello",
		"run_id": float64(12345),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatal("expected success result")
	}
	if !strings.HasSuffix(gotPath, "/12345/cancel") {
		t.Fatalf("expected cancel path, got %q", gotPath)
	}
	if !strings.Contains(result.Content[0].Text, "12345") {
		t.Fatalf("expected run ID in result, got %q", result.Content[0].Text)
	}
}

// =====================================================================
// handleRerunWorkflow tests
// =====================================================================

func TestHandleRerunWorkflow_All(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	srv := newTestActionsServer(ts)

	result, err := srv.handleRerunWorkflow(context.Background(), map[string]any{
		"owner":  "octocat",
		"repo":   "hello",
		"run_id": float64(99),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatal("expected success result")
	}
	if !strings.HasSuffix(gotPath, "/99/rerun") {
		t.Fatalf("expected rerun path, got %q", gotPath)
	}
}

func TestHandleRerunWorkflow_FailedOnly(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	srv := newTestActionsServer(ts)

	result, err := srv.handleRerunWorkflow(context.Background(), map[string]any{
		"owner":       "octocat",
		"repo":        "hello",
		"run_id":      float64(99),
		"failed_only": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/99/rerun-failed-jobs") {
		t.Fatalf("expected rerun-failed-jobs path, got %q", gotPath)
	}
	if !strings.Contains(result.Content[0].Text, "failed jobs only") {
		t.Fatalf("expected 'failed jobs only' in message, got %q", result.Content[0].Text)
	}
}

// =====================================================================
// handleGetJobLogs tests
// =====================================================================

func TestHandleGetJobLogs_MissingRequired(t *testing.T) {
	srv := &actionsServer{token: "x", httpClient: httpclient.NewDefault()}

	result, err := srv.handleGetJobLogs(context.Background(), map[string]any{
		"owner": "octocat",
	})
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected IsError=true for missing required params")
	}
}

func TestHandleGetJobLogs_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/actions/jobs/777/logs") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		io.WriteString(w, "line1\nline2\nline3\n")
	}))
	defer ts.Close()

	srv := newTestActionsServer(ts)

	result, err := srv.handleGetJobLogs(context.Background(), map[string]any{
		"owner":  "octocat",
		"repo":   "hello",
		"job_id": float64(777),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatal("expected success result")
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "line1") {
		t.Fatalf("expected full logs, got %q", text)
	}
}

func TestHandleGetJobLogs_TailLines(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "line1\nline2\nline3\nline4\nline5\n")
	}))
	defer ts.Close()

	srv := newTestActionsServer(ts)

	result, err := srv.handleGetJobLogs(context.Background(), map[string]any{
		"owner":      "octocat",
		"repo":       "hello",
		"job_id":     float64(777),
		"tail_lines": float64(2),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := result.Content[0].Text
	if strings.Contains(text, "line1") {
		t.Fatalf("expected tail-only output, got %q", text)
	}
	if !strings.Contains(text, "line5") {
		t.Fatalf("expected last line present, got %q", text)
	}
}

func TestHandleGetJobLogs_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"Not Found"}`)
	}))
	defer ts.Close()

	srv := newTestActionsServer(ts)

	result, err := srv.handleGetJobLogs(context.Background(), map[string]any{
		"owner":  "octocat",
		"repo":   "hello",
		"job_id": float64(999),
	})
	if err != nil {
		t.Fatalf("expected nil error (handler returns error result), got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected IsError=true for 404 response")
	}
}

// =====================================================================
// handleListArtifacts tests
// =====================================================================

func TestHandleListArtifacts_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"total_count": 1,
			"artifacts": []map[string]any{
				{
					"id":            101,
					"name":          "build-output",
					"size_in_bytes": 1024,
					"expired":       false,
					"created_at":    "2024-01-01T00:00:00Z",
					"expires_at":    "2024-02-01T00:00:00Z",
				},
			},
		})
	}))
	defer ts.Close()

	srv := newTestActionsServer(ts)

	result, err := srv.handleListArtifacts(context.Background(), map[string]any{
		"owner":  "octocat",
		"repo":   "hello",
		"run_id": float64(555),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatal("expected success result")
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "build-output") {
		t.Fatalf("expected artifact name in output, got %q", text)
	}
	if !strings.Contains(text, "total_artifacts: 1") {
		t.Fatalf("expected total count in output, got %q", text)
	}
}

// =====================================================================
// handleListWorkflowJobs tests
// =====================================================================

func TestHandleListWorkflowJobs_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"total_count": 1,
			"jobs": []map[string]any{
				{
					"id":           201,
					"name":         "build",
					"status":       "completed",
					"conclusion":   "success",
					"started_at":   "2024-01-01T00:00:00Z",
					"completed_at": "2024-01-01T00:05:00Z",
					"steps": []map[string]any{
						{"number": 1, "name": "Checkout", "status": "completed", "conclusion": "success"},
						{"number": 2, "name": "Build", "status": "completed", "conclusion": "success"},
					},
				},
			},
		})
	}))
	defer ts.Close()

	srv := newTestActionsServer(ts)

	result, err := srv.handleListWorkflowJobs(context.Background(), map[string]any{
		"owner":  "octocat",
		"repo":   "hello",
		"run_id": float64(100),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatal("expected success result")
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "build") {
		t.Fatalf("expected job name in output, got %q", text)
	}
	if !strings.Contains(text, "Checkout") {
		t.Fatalf("expected step name in output, got %q", text)
	}
}
