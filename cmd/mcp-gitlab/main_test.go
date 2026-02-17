package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/mcperror"
)

func TestMain(m *testing.M) {
	// Force JSON output format so CallToolResult text is parseable JSON.
	os.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	os.Exit(m.Run())
}

// newTestServer creates a gitlabServer pointing at the given httptest server URL.
func newTestServer(ts *httptest.Server) *gitlabServer {
	return &gitlabServer{
		token:      "test-token",
		apiURL:     ts.URL,
		httpClient: httpclient.NewDefault(),
	}
}

// mustParseJSON extracts the JSON from a CallToolResult's first content block.
func mustParseJSON(t *testing.T, result any) map[string]any {
	t.Helper()
	// result is *mcp.CallToolResult
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var wrapper struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(b, &wrapper); err != nil {
		t.Fatalf("unmarshal wrapper: %v", err)
	}
	if len(wrapper.Content) == 0 {
		t.Fatal("no content blocks in result")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(wrapper.Content[0].Text), &out); err != nil {
		t.Fatalf("unmarshal result JSON: %v (raw: %s)", err, wrapper.Content[0].Text)
	}
	return out
}

// =====================================================================
// Error handling tests
// =====================================================================

func TestGitLabRequest_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"404 Project Not Found"}`)
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	_, err := gl.request(context.Background(), "GET", "/projects/999", nil)
	if err == nil {
		t.Fatal("expected error for 404 response, got nil")
	}

	mcpErr, ok := err.(*mcperror.Error)
	if !ok {
		t.Fatalf("expected *mcperror.Error, got %T: %v", err, err)
	}
	if mcpErr.Code != mcperror.CodeNotFound {
		t.Fatalf("expected error code %q, got %q", mcperror.CodeNotFound, mcpErr.Code)
	}
	if !strings.Contains(mcpErr.Message, "GitLab") {
		t.Fatalf("error message should mention GitLab: %q", mcpErr.Message)
	}
}

func TestGitLabRequest_Unauthorized(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"message":"401 Unauthorized"}`)
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	_, err := gl.request(context.Background(), "GET", "/user", nil)
	if err == nil {
		t.Fatal("expected error for 401 response, got nil")
	}

	mcpErr, ok := err.(*mcperror.Error)
	if !ok {
		t.Fatalf("expected *mcperror.Error, got %T: %v", err, err)
	}
	if mcpErr.Code != mcperror.CodeUnauthorized {
		t.Fatalf("expected error code %q, got %q", mcperror.CodeUnauthorized, mcpErr.Code)
	}
	if !strings.Contains(mcpErr.Message, "authentication") {
		t.Fatalf("error message should mention authentication: %q", mcpErr.Message)
	}
}

func TestGitLabRequest_SetsAuthHeader(t *testing.T) {
	var gotToken string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("PRIVATE-TOKEN")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":1}`)
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	_, err := gl.request(context.Background(), "GET", "/user", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotToken != "test-token" {
		t.Fatalf("expected PRIVATE-TOKEN %q, got %q", "test-token", gotToken)
	}
}

func TestGitLabRequest_ArrayResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `[{"id":1},{"id":2}]`)
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	result, err := gl.request(context.Background(), "GET", "/projects", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	items, ok := result["items"].([]any)
	if !ok {
		t.Fatalf("expected items array, got %v", result)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

// =====================================================================
// Handler tests
// =====================================================================

func TestHandleListProjects(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/projects") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// Verify pagination params are set
		if r.URL.Query().Get("per_page") == "" {
			t.Error("expected per_page query param")
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Page", "1")
		w.Header().Set("X-Per-Page", "20")
		w.Header().Set("X-Total", "2")
		w.Header().Set("X-Total-Pages", "1")
		io.WriteString(w, `[{"id":1,"name":"project-a","path_with_namespace":"group/project-a"},{"id":2,"name":"project-b","path_with_namespace":"group/project-b"}]`)
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	result, err := gl.handleListProjects(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parsed := mustParseJSON(t, result)
	count, ok := parsed["count"].(float64)
	if !ok || count != 2 {
		t.Fatalf("expected count=2, got %v", parsed["count"])
	}
	projects, ok := parsed["projects"].([]any)
	if !ok || len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %v", parsed["projects"])
	}
	pagination, ok := parsed["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("expected pagination map, got %v", parsed["pagination"])
	}
	if pagination["total"] != float64(2) {
		t.Fatalf("expected pagination total=2, got %v", pagination["total"])
	}
}

func TestHandleListProjects_WithFilters(t *testing.T) {
	var gotOwned, gotMembership string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOwned = r.URL.Query().Get("owned")
		gotMembership = r.URL.Query().Get("membership")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `[{"id":1,"name":"my-project"}]`)
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	result, err := gl.handleListProjects(context.Background(), map[string]any{
		"owned":      true,
		"membership": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotOwned != "true" {
		t.Fatalf("expected owned=true query param, got %q", gotOwned)
	}
	if gotMembership != "true" {
		t.Fatalf("expected membership=true query param, got %q", gotMembership)
	}

	parsed := mustParseJSON(t, result)
	count, ok := parsed["count"].(float64)
	if !ok || count != 1 {
		t.Fatalf("expected count=1, got %v", parsed["count"])
	}
}

func TestHandleGetProject(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		// The path should include the URL-encoded project identifier
		if !strings.Contains(r.URL.Path, "/projects/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":42,"name":"my-project","path_with_namespace":"group/my-project","default_branch":"main","visibility":"private"}`)
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	result, err := gl.handleGetProject(context.Background(), map[string]any{
		"project": "group/my-project",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parsed := mustParseJSON(t, result)
	if parsed["id"] != float64(42) {
		t.Fatalf("expected project id=42, got %v", parsed["id"])
	}
	if parsed["name"] != "my-project" {
		t.Fatalf("expected name=my-project, got %v", parsed["name"])
	}
}

func TestHandleGetProject_MissingRequired(t *testing.T) {
	gl := &gitlabServer{token: "x", apiURL: "http://unused"}

	result, err := gl.handleGetProject(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("expected nil error (validation failure returns result, not error), got: %v", err)
	}
	if result == nil {
		t.Fatal("expected error result, got nil")
		return
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for missing required param")
	}
}

func TestHandleSearchRepos(t *testing.T) {
	var gotSearch string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSearch = r.URL.Query().Get("search")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total", "1")
		io.WriteString(w, `[{"id":10,"name":"loom-core","path_with_namespace":"infra/loom-core"}]`)
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	result, err := gl.handleSearchRepositories(context.Background(), map[string]any{
		"search": "loom",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotSearch != "loom" {
		t.Fatalf("expected search=loom, got %q", gotSearch)
	}

	parsed := mustParseJSON(t, result)
	count, ok := parsed["count"].(float64)
	if !ok || count != 1 {
		t.Fatalf("expected count=1, got %v", parsed["count"])
	}
	projects, ok := parsed["projects"].([]any)
	if !ok || len(projects) != 1 {
		t.Fatalf("expected 1 project, got %v", parsed["projects"])
	}
	proj := projects[0].(map[string]any)
	if proj["name"] != "loom-core" {
		t.Fatalf("expected name=loom-core, got %v", proj["name"])
	}
}

func TestHandleSearchRepos_MissingSearch(t *testing.T) {
	gl := &gitlabServer{token: "x", apiURL: "http://unused"}

	result, err := gl.handleSearchRepositories(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("expected nil error for validation failure, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result for missing search parameter")
	}
}

func TestHandleListPipelines(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/pipelines") {
			t.Errorf("expected /pipelines in path, got %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Page", "1")
		w.Header().Set("X-Per-Page", "20")
		w.Header().Set("X-Total", "3")
		io.WriteString(w, `[{"id":101,"status":"success","ref":"main"},{"id":102,"status":"failed","ref":"main"},{"id":103,"status":"running","ref":"develop"}]`)
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	result, err := gl.handleListPipelines(context.Background(), map[string]any{
		"project": "group/my-project",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parsed := mustParseJSON(t, result)
	count, ok := parsed["count"].(float64)
	if !ok || count != 3 {
		t.Fatalf("expected count=3, got %v", parsed["count"])
	}
	pipelines, ok := parsed["pipelines"].([]any)
	if !ok || len(pipelines) != 3 {
		t.Fatalf("expected 3 pipelines, got %v", parsed["pipelines"])
	}
}

func TestHandleListPipelines_WithFilters(t *testing.T) {
	var gotRef, gotStatus string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRef = r.URL.Query().Get("ref")
		gotStatus = r.URL.Query().Get("status")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `[{"id":101,"status":"success","ref":"main"}]`)
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	result, err := gl.handleListPipelines(context.Background(), map[string]any{
		"project": "group/my-project",
		"ref":     "main",
		"status":  "success",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotRef != "main" {
		t.Fatalf("expected ref=main, got %q", gotRef)
	}
	if gotStatus != "success" {
		t.Fatalf("expected status=success, got %q", gotStatus)
	}

	parsed := mustParseJSON(t, result)
	count, ok := parsed["count"].(float64)
	if !ok || count != 1 {
		t.Fatalf("expected count=1, got %v", parsed["count"])
	}
}

func TestHandleListPipelines_MissingProject(t *testing.T) {
	gl := &gitlabServer{token: "x", apiURL: "http://unused"}

	result, err := gl.handleListPipelines(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("expected nil error for validation failure, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result for missing project parameter")
	}
}

func TestHandleGetPipeline(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/pipelines/") {
			t.Errorf("expected /pipelines/ in path, got %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":101,"status":"success","ref":"main","sha":"abc123","created_at":"2026-01-15T10:00:00Z","duration":120}`)
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	result, err := gl.handleGetPipeline(context.Background(), map[string]any{
		"project":     "group/my-project",
		"pipeline_id": 101,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parsed := mustParseJSON(t, result)
	if parsed["id"] != float64(101) {
		t.Fatalf("expected id=101, got %v", parsed["id"])
	}
	if parsed["status"] != "success" {
		t.Fatalf("expected status=success, got %v", parsed["status"])
	}
}

func TestHandleVerifyToken_EmptyToken(t *testing.T) {
	gl := &gitlabServer{
		token:      "",
		apiURL:     "http://unused",
		httpClient: httpclient.NewDefault(),
	}

	_, err := gl.handleVerifyToken(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for empty token")
	}

	mcpErr, ok := err.(*mcperror.Error)
	if !ok {
		t.Fatalf("expected *mcperror.Error, got %T: %v", err, err)
	}
	if !strings.Contains(mcpErr.Message, "not configured") {
		t.Fatalf("error should mention 'not configured': %q", mcpErr.Message)
	}
}

func TestHandleVerifyToken_UnexpandedToken(t *testing.T) {
	gl := &gitlabServer{
		token:      "${GITLAB_TOKEN}",
		apiURL:     "http://unused",
		httpClient: httpclient.NewDefault(),
	}

	_, err := gl.handleVerifyToken(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for unexpanded token")
	}

	mcpErr, ok := err.(*mcperror.Error)
	if !ok {
		t.Fatalf("expected *mcperror.Error, got %T: %v", err, err)
	}
	if !strings.Contains(mcpErr.Message, "unexpanded") {
		t.Fatalf("error should mention 'unexpanded': %q", mcpErr.Message)
	}
}

func TestHandleVerifyToken_Success(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.Contains(r.URL.Path, "/personal_access_tokens/self"):
			io.WriteString(w, `{"id":1,"name":"my-token","scopes":["api","read_user"],"expires_at":"2027-01-01"}`)
		case r.URL.Path == "/user" || strings.HasSuffix(r.URL.Path, "/user"):
			io.WriteString(w, `{"id":42,"username":"testuser","name":"Test User"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"not found"}`)
		}
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	result, err := gl.handleVerifyToken(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parsed := mustParseJSON(t, result)
	if parsed["ok"] != true {
		t.Fatalf("expected ok=true, got %v", parsed["ok"])
	}
	user, ok := parsed["user"].(map[string]any)
	if !ok {
		t.Fatalf("expected user object, got %v", parsed["user"])
	}
	if user["username"] != "testuser" {
		t.Fatalf("expected username=testuser, got %v", user["username"])
	}
}

func TestHandleListPipelineJobs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/jobs") {
			t.Errorf("expected /jobs in path, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total", "2")
		io.WriteString(w, `[{"id":501,"name":"build","stage":"build","status":"success"},{"id":502,"name":"test","stage":"test","status":"failed"}]`)
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	result, err := gl.handleListPipelineJobs(context.Background(), map[string]any{
		"project":     "group/my-project",
		"pipeline_id": 101,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parsed := mustParseJSON(t, result)
	count, ok := parsed["count"].(float64)
	if !ok || count != 2 {
		t.Fatalf("expected count=2, got %v", parsed["count"])
	}
	jobs, ok := parsed["jobs"].([]any)
	if !ok || len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %v", parsed["jobs"])
	}
}

func TestHandleCreateIssue(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":1,"iid":7,"title":"Bug report","state":"opened","web_url":"https://gitlab.com/group/project/-/issues/7"}`)
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	result, err := gl.handleCreateIssue(context.Background(), map[string]any{
		"project":     "group/project",
		"title":       "Bug report",
		"description": "Something broke",
		"labels":      "bug,critical",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parsed := mustParseJSON(t, result)
	if parsed["title"] != "Bug report" {
		t.Fatalf("expected title=Bug report, got %v", parsed["title"])
	}

	// Verify request payload
	if gotBody["title"] != "Bug report" {
		t.Fatalf("expected request body title=Bug report, got %v", gotBody["title"])
	}
	if gotBody["labels"] != "bug,critical" {
		t.Fatalf("expected request body labels=bug,critical, got %v", gotBody["labels"])
	}
}

func TestHandleCreateIssue_WithAssigneeIDs(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":1,"iid":7,"title":"Bug report","state":"opened"}`)
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	_, err := gl.handleCreateIssue(context.Background(), map[string]any{
		"project":      "group/project",
		"title":        "Bug report",
		"assignee_ids": []any{float64(10), float64(11)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assigneeIDs, ok := gotBody["assignee_ids"].([]any)
	if !ok {
		t.Fatalf("expected assignee_ids array in request body, got %v", gotBody["assignee_ids"])
	}
	if len(assigneeIDs) != 2 || assigneeIDs[0] != float64(10) || assigneeIDs[1] != float64(11) {
		t.Fatalf("unexpected assignee_ids payload: %v", assigneeIDs)
	}
}

func TestHandleCreateIssue_InvalidAssigneeIDs(t *testing.T) {
	gl := &gitlabServer{token: "x", apiURL: "http://unused"}

	result, err := gl.handleCreateIssue(context.Background(), map[string]any{
		"project":      "group/project",
		"title":        "Bug report",
		"assignee_ids": []any{"not-an-int"},
	})
	if err != nil {
		t.Fatalf("expected nil error for validation failure, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result for invalid assignee_ids")
	}
}

func TestHandleUpdateIssue(t *testing.T) {
	var gotBody map[string]any
	var gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":1,"iid":7,"title":"Bug report","state":"closed","labels":["bug","resolved"]}`)
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	result, err := gl.handleUpdateIssue(context.Background(), map[string]any{
		"project":     "group/project",
		"issue_iid":   7,
		"labels":      "bug,resolved",
		"state_event": "close",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != "PUT" {
		t.Fatalf("expected PUT, got %s", gotMethod)
	}
	if !strings.Contains(gotPath, "/projects/group/project/issues/7") {
		t.Fatalf("unexpected path: %s", gotPath)
	}

	parsed := mustParseJSON(t, result)
	if parsed["state"] != "closed" {
		t.Fatalf("expected state=closed, got %v", parsed["state"])
	}
	if gotBody["labels"] != "bug,resolved" {
		t.Fatalf("expected request body labels=bug,resolved, got %v", gotBody["labels"])
	}
	if gotBody["state_event"] != "close" {
		t.Fatalf("expected request body state_event=close, got %v", gotBody["state_event"])
	}
}

func TestHandleUpdateIssue_MissingUpdateFields(t *testing.T) {
	gl := &gitlabServer{token: "x", apiURL: "http://unused"}

	result, err := gl.handleUpdateIssue(context.Background(), map[string]any{
		"project":   "group/project",
		"issue_iid": 7,
	})
	if err != nil {
		t.Fatalf("expected nil error for validation failure, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result when no update fields are provided")
	}
}

func TestHandleListMergeRequests(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total", "1")
		io.WriteString(w, `[{"id":200,"iid":5,"title":"Feature PR","state":"opened","source_branch":"feature","target_branch":"main"}]`)
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	result, err := gl.handleListMergeRequests(context.Background(), map[string]any{
		"project": "group/project",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parsed := mustParseJSON(t, result)
	count, ok := parsed["count"].(float64)
	if !ok || count != 1 {
		t.Fatalf("expected count=1, got %v", parsed["count"])
	}
	mrs, ok := parsed["merge_requests"].([]any)
	if !ok || len(mrs) != 1 {
		t.Fatalf("expected 1 merge request, got %v", parsed["merge_requests"])
	}
}

func TestHandleGetPipeline_InvalidPipelineID(t *testing.T) {
	gl := &gitlabServer{token: "x", apiURL: "http://unused"}

	result, err := gl.handleGetPipeline(context.Background(), map[string]any{
		"project":     "group/my-project",
		"pipeline_id": 0,
	})
	if err != nil {
		t.Fatalf("expected nil error for validation failure, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result for invalid pipeline_id")
	}
}

func TestHandleGetArtifacts_EncodesArtifactPath(t *testing.T) {
	var gotEscapedPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "artifact-content")
	}))
	defer ts.Close()

	gl := newTestServer(ts)

	result, err := gl.handleGetArtifacts(context.Background(), map[string]any{
		"project":       "group/project",
		"job_id":        99,
		"artifact_path": "reports/build#1 log.txt",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(gotEscapedPath, "/projects/group%2Fproject/jobs/99/artifacts/reports/build%231%20log.txt") {
		t.Fatalf("expected escaped artifact path, got %q", gotEscapedPath)
	}

	parsed := mustParseJSON(t, result)
	if parsed["encoding"] != "text" {
		t.Fatalf("expected text encoding, got %v", parsed["encoding"])
	}
}

func TestHandleGetArtifacts_InvalidJobID(t *testing.T) {
	gl := &gitlabServer{token: "x", apiURL: "http://unused"}

	result, err := gl.handleGetArtifacts(context.Background(), map[string]any{
		"project": "group/project",
		"job_id":  0,
	})
	if err != nil {
		t.Fatalf("expected nil error for validation failure, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result for invalid job_id")
	}
}

// =====================================================================
// Utility function tests
// =====================================================================

func TestParsePaginationHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-Page", "1")
	h.Set("X-Per-Page", "20")
	h.Set("X-Total", "42")
	h.Set("X-Total-Pages", "3")
	h.Set("X-Next-Page", "2")

	result := parsePaginationHeaders(h)
	if result == nil {
		t.Fatal("expected non-nil pagination")
	}
	if result["page"] != 1 {
		t.Fatalf("expected page=1, got %v", result["page"])
	}
	if result["total"] != 42 {
		t.Fatalf("expected total=42, got %v", result["total"])
	}
	if result["next_page"] != 2 {
		t.Fatalf("expected next_page=2, got %v", result["next_page"])
	}
}

func TestParsePaginationHeaders_Nil(t *testing.T) {
	result := parsePaginationHeaders(nil)
	if result != nil {
		t.Fatalf("expected nil for nil headers, got %v", result)
	}
}

func TestParsePaginationHeaders_Empty(t *testing.T) {
	result := parsePaginationHeaders(http.Header{})
	if result != nil {
		t.Fatalf("expected nil for empty headers, got %v", result)
	}
}

func TestEncodeProject(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"123", "123"},
		{"group/project", "group%2Fproject"},
		{"nested/group/project", "nested%2Fgroup%2Fproject"},
	}
	for _, tc := range cases {
		got := encodeProject(tc.input)
		if got != tc.want {
			t.Errorf("encodeProject(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNormalizePerPage(t *testing.T) {
	cases := []struct {
		input      int
		defaultVal int
		want       int
	}{
		{0, 20, 20},    // zero -> default
		{-1, 20, 20},   // negative -> default
		{50, 20, 50},   // valid
		{200, 20, 100}, // over max -> capped at 100
	}
	for _, tc := range cases {
		got := normalizePerPage(tc.input, tc.defaultVal)
		if got != tc.want {
			t.Errorf("normalizePerPage(%d, %d) = %d, want %d", tc.input, tc.defaultVal, got, tc.want)
		}
	}
}

func TestNormalizePage(t *testing.T) {
	cases := []struct {
		input int
		want  int
	}{
		{0, 1},  // zero -> 1
		{-1, 1}, // negative -> 1
		{1, 1},  // normal
		{5, 5},  // normal
	}
	for _, tc := range cases {
		got := normalizePage(tc.input)
		if got != tc.want {
			t.Errorf("normalizePage(%d) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		input string
		want  int // seconds
	}{
		{"", 0},
		{"abc", 0},
		{"-1", 0},
		{"0", 0},
		{"5", 5},
		{" 10 ", 10},
	}
	for _, tc := range cases {
		got := parseRetryAfter(tc.input)
		wantDur := 0
		if tc.want > 0 {
			wantDur = tc.want
		}
		gotSecs := int(got.Seconds())
		if gotSecs != wantDur {
			t.Errorf("parseRetryAfter(%q) = %v, want %ds", tc.input, got, tc.want)
		}
	}
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	future := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	got := parseRetryAfter(future)
	if got < 2*time.Second || got > 4*time.Second {
		t.Fatalf("expected ~3s delay from HTTP date, got %v", got)
	}

	past := time.Now().Add(-1 * time.Minute).UTC().Format(http.TimeFormat)
	if gotPast := parseRetryAfter(past); gotPast != 0 {
		t.Fatalf("expected 0 delay for past HTTP date, got %v", gotPast)
	}
}

func TestIsTerminalPipelineStatus(t *testing.T) {
	terminal := []string{"success", "failed", "canceled", "skipped", "manual"}
	for _, s := range terminal {
		if !isTerminalPipelineStatus(s) {
			t.Errorf("expected %q to be terminal", s)
		}
	}

	nonTerminal := []string{"running", "pending", "created", "waiting_for_resource", ""}
	for _, s := range nonTerminal {
		if isTerminalPipelineStatus(s) {
			t.Errorf("expected %q to be non-terminal", s)
		}
	}
}

func TestIsTextContent(t *testing.T) {
	cases := []struct {
		contentType string
		data        []byte
		want        bool
	}{
		{"text/plain", nil, true},
		{"text/html", nil, true},
		{"application/json", nil, true},
		{"application/xml", nil, true},
		{"application/octet-stream", []byte("hello world"), true},
		{"application/octet-stream", []byte{0x00, 0x01, 0x02}, false},
	}
	for _, tc := range cases {
		got := isTextContent(tc.contentType, tc.data)
		if got != tc.want {
			t.Errorf("isTextContent(%q, %v) = %v, want %v", tc.contentType, tc.data, got, tc.want)
		}
	}
}
