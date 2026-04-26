package clients

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"context"

	"github.com/crb2nu/loom/pkg/hive/pipeline"
)

// recordingTransport captures every request the client makes and serves
// canned responses keyed on (method, path-prefix). It's the test
// substrate for every GitLab REST verb we exercise.
type recordingTransport struct {
	mu       sync.Mutex
	requests []recordedRequest
	routes   map[string]func(*http.Request) (int, any)
}

type recordedRequest struct {
	Method string
	Path   string
	Body   string
	Token  string
}

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	body := ""
	if req.Body != nil {
		buf, _ := io.ReadAll(req.Body)
		body = string(buf)
	}
	// Use RawPath when the URL contains percent-encoded segments
	// (Go's URL.Path decodes %2F → /, which loses information GitLab
	// project paths depend on). Tests assert on the encoded form.
	matchPath := req.URL.Path
	if req.URL.RawPath != "" {
		matchPath = req.URL.RawPath
	}
	rt.requests = append(rt.requests, recordedRequest{
		Method: req.Method,
		Path:   matchPath,
		Body:   body,
		Token:  req.Header.Get("PRIVATE-TOKEN"),
	})
	for prefix, handler := range rt.routes {
		method, path, _ := strings.Cut(prefix, " ")
		if req.Method == method && strings.HasPrefix(matchPath, path) {
			status, payload := handler(req)
			buf, _ := json.Marshal(payload)
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(bytes.NewReader(buf)),
				Header:     make(http.Header),
			}, nil
		}
	}
	return &http.Response{
		StatusCode: 404,
		Body:       io.NopCloser(strings.NewReader(`{"message":"not found"}`)),
		Header:     make(http.Header),
	}, nil
}

func newGitLabStub(t *testing.T, routes map[string]func(*http.Request) (int, any)) (*GitLabClient, *recordingTransport) {
	t.Helper()
	cli, err := NewGitLabClient(GitLabConfig{
		APIURL:       "https://gitlab.example/api/v4",
		Token:        "tok-123",
		Project:      "services/loom-core",
		PollInterval: 10 * time.Millisecond,
		PollDeadline: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	rt := &recordingTransport{routes: routes}
	cli.SetTransport(rt)
	return cli, rt
}

// ----- Config validation -----

func TestNewGitLabClient_RequiresFields(t *testing.T) {
	cases := []GitLabConfig{
		{},
		{APIURL: "x"},
		{APIURL: "x", Token: "y"},
	}
	for i, c := range cases {
		if _, err := NewGitLabClient(c); err == nil {
			t.Errorf("case %d: expected error for %+v", i, c)
		}
	}
}

func TestNewGitLabClient_AppliesDefaults(t *testing.T) {
	c, err := NewGitLabClient(GitLabConfig{
		APIURL: "x", Token: "y", Project: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.cfg.PollInterval < 2*time.Second {
		t.Errorf("PollInterval = %v, want >= 2s default", c.cfg.PollInterval)
	}
	if c.cfg.MergeMethod != "merge" {
		t.Errorf("MergeMethod = %q, want merge", c.cfg.MergeMethod)
	}
}

// ----- CreateMR -----

func TestCreateMR_PostsAndPropagatesIID(t *testing.T) {
	cli, rt := newGitLabStub(t, map[string]func(*http.Request) (int, any){
		"POST /api/v4/projects/services%2Floom-core/merge_requests": func(_ *http.Request) (int, any) {
			return 201, mrResponse{IID: 99, WebURL: "https://gitlab/services/loom-core/-/merge_requests/99"}
		},
	})
	resp, err := cli.CreateMR(context.Background(), pipeline.CreateMRRequest{
		BacklogID:    "BL-X",
		SourceBranch: "feat/x",
		TargetBranch: "main",
		Title:        "feat: x",
		Description:  "details",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.MRIID != 99 {
		t.Errorf("MRIID = %d", resp.MRIID)
	}
	if !strings.Contains(resp.URL, "/merge_requests/99") {
		t.Errorf("URL = %q", resp.URL)
	}
	if len(rt.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(rt.requests))
	}
	got := rt.requests[0]
	if got.Token != "tok-123" {
		t.Errorf("token header = %q", got.Token)
	}
	var body createMRBody
	if err := json.Unmarshal([]byte(got.Body), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.SourceBranch != "feat/x" || body.TargetBranch != "main" {
		t.Errorf("body branches wrong: %+v", body)
	}
	if !body.RemoveSourceBranch {
		t.Error("remove_source_branch should default true")
	}
}

func TestCreateMR_ServerError(t *testing.T) {
	cli, _ := newGitLabStub(t, map[string]func(*http.Request) (int, any){
		"POST /api/v4/projects": func(_ *http.Request) (int, any) {
			return 422, map[string]string{"message": "branch already has open MR"}
		},
	})
	if _, err := cli.CreateMR(context.Background(), pipeline.CreateMRRequest{SourceBranch: "x", TargetBranch: "main", Title: "x"}); err == nil {
		t.Error("expected error on 422")
	}
}

// ----- PollPipeline -----

func TestPollPipeline_TerminatesOnSuccess(t *testing.T) {
	var pollCount int32
	cli, _ := newGitLabStub(t, map[string]func(*http.Request) (int, any){
		"GET /api/v4/projects/services%2Floom-core/merge_requests/42": func(_ *http.Request) (int, any) {
			return 200, mrResponse{IID: 42, HeadPipeline: mrHeadPipe{ID: 1234, Status: "running"}}
		},
		"GET /api/v4/projects/services%2Floom-core/pipelines/1234": func(_ *http.Request) (int, any) {
			n := atomic.AddInt32(&pollCount, 1)
			status := "running"
			if n >= 2 {
				status = "success"
			}
			return 200, map[string]any{"id": 1234, "status": status}
		},
	})
	resp, err := cli.PollPipeline(context.Background(), pipeline.PollPipelineRequest{MRIID: 42})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if resp.Status != "success" {
		t.Errorf("status = %q, want success", resp.Status)
	}
	if !strings.Contains(resp.LogTail, "status=success") {
		t.Errorf("log tail missing terminal status: %q", resp.LogTail)
	}
	if atomic.LoadInt32(&pollCount) < 2 {
		t.Errorf("expected at least 2 poll calls, got %d", pollCount)
	}
}

func TestPollPipeline_FailedTerminal(t *testing.T) {
	cli, _ := newGitLabStub(t, map[string]func(*http.Request) (int, any){
		"GET /api/v4/projects/services%2Floom-core/merge_requests/77": func(_ *http.Request) (int, any) {
			return 200, mrResponse{IID: 77, HeadPipeline: mrHeadPipe{ID: 99, Status: "failed"}}
		},
		"GET /api/v4/projects/services%2Floom-core/pipelines/99": func(_ *http.Request) (int, any) {
			return 200, map[string]any{"id": 99, "status": "failed"}
		},
	})
	resp, err := cli.PollPipeline(context.Background(), pipeline.PollPipelineRequest{MRIID: 77})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if resp.Status != "failed" {
		t.Errorf("status = %q", resp.Status)
	}
}

func TestPollPipeline_TimeoutWhenNoHeadPipeline(t *testing.T) {
	cli, _ := newGitLabStub(t, map[string]func(*http.Request) (int, any){
		"GET /api/v4/projects/services%2Floom-core/merge_requests/55": func(_ *http.Request) (int, any) {
			return 200, mrResponse{IID: 55} // never gets a head_pipeline.id
		},
	})
	cli.cfg.PollDeadline = 50 * time.Millisecond
	cli.cfg.PollInterval = 10 * time.Millisecond
	resp, err := cli.PollPipeline(context.Background(), pipeline.PollPipelineRequest{MRIID: 55})
	if err == nil {
		t.Error("expected timeout error")
	}
	if resp.Status != "timeout" {
		t.Errorf("status = %q, want timeout", resp.Status)
	}
}

func TestPollPipeline_RequiresMRIID(t *testing.T) {
	cli, _ := newGitLabStub(t, nil)
	if _, err := cli.PollPipeline(context.Background(), pipeline.PollPipelineRequest{}); err == nil {
		t.Error("expected error for missing MRIID")
	}
}

// ----- Merge -----

func TestMerge_PropagatesSHA(t *testing.T) {
	cli, _ := newGitLabStub(t, map[string]func(*http.Request) (int, any){
		"PUT /api/v4/projects/services%2Floom-core/merge_requests/3/merge": func(_ *http.Request) (int, any) {
			return 200, mrResponse{IID: 3, SHA: "abc123def"}
		},
	})
	resp, err := cli.Merge(context.Background(), pipeline.MergeRequestArgs{MRIID: 3})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if resp.MergedSHA != "abc123def" {
		t.Errorf("sha = %q", resp.MergedSHA)
	}
}

func TestMerge_GitLabReportsMergeError(t *testing.T) {
	cli, _ := newGitLabStub(t, map[string]func(*http.Request) (int, any){
		"PUT /api/v4/projects/services%2Floom-core/merge_requests/4/merge": func(_ *http.Request) (int, any) {
			return 200, mrResponse{IID: 4, MergeError: "branch cannot be merged"}
		},
	})
	if _, err := cli.Merge(context.Background(), pipeline.MergeRequestArgs{MRIID: 4}); err == nil {
		t.Error("expected error from merge_error field")
	}
}

func TestMerge_RequiresMRIID(t *testing.T) {
	cli, _ := newGitLabStub(t, nil)
	if _, err := cli.Merge(context.Background(), pipeline.MergeRequestArgs{}); err == nil {
		t.Error("expected error for missing MRIID")
	}
}

// ----- Cleanup -----

func TestCleanup_DeletesSourceBranch(t *testing.T) {
	called := false
	cli, _ := newGitLabStub(t, map[string]func(*http.Request) (int, any){
		"DELETE /api/v4/projects/services%2Floom-core/repository/branches/feat%2Fx": func(_ *http.Request) (int, any) {
			called = true
			return 204, nil
		},
	})
	resp, err := cli.Cleanup(context.Background(), pipeline.CleanupRequest{BranchName: "feat/x"})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if !called {
		t.Error("DELETE not called")
	}
	if !strings.Contains(resp.LogTail, "deleted") {
		t.Errorf("log tail = %q", resp.LogTail)
	}
}

func TestCleanup_404IsSuccess(t *testing.T) {
	cli, _ := newGitLabStub(t, nil) // default 404 for everything
	resp, err := cli.Cleanup(context.Background(), pipeline.CleanupRequest{BranchName: "feat/gone"})
	if err != nil {
		t.Errorf("cleanup should swallow 404: %v", err)
	}
	if !strings.Contains(resp.LogTail, "already removed") {
		t.Errorf("log tail = %q", resp.LogTail)
	}
}

func TestCleanup_NoBranchIsNoOp(t *testing.T) {
	cli, rt := newGitLabStub(t, nil)
	if _, err := cli.Cleanup(context.Background(), pipeline.CleanupRequest{}); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if len(rt.requests) != 0 {
		t.Errorf("no branch should mean no HTTP call, got %d", len(rt.requests))
	}
}

// ----- CreateIssue -----

func TestCreateIssue_PostsAndJoinsLabels(t *testing.T) {
	cli, rt := newGitLabStub(t, map[string]func(*http.Request) (int, any){
		"POST /api/v4/projects/services%2Floom-core/issues": func(_ *http.Request) (int, any) {
			return 201, issueResponse{IID: 7, WebURL: "https://gitlab/services/loom-core/-/issues/7"}
		},
	})
	resp, err := cli.CreateIssue(context.Background(), pipeline.IssueRequest{
		Title:       "hive escalation",
		Description: "boom",
		Labels:      []string{"hive-escalation", "priority/P1"},
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if resp.IID != 7 {
		t.Errorf("iid = %d", resp.IID)
	}
	if len(rt.requests) != 1 {
		t.Fatalf("requests = %d", len(rt.requests))
	}
	var body createIssueBody
	_ = json.Unmarshal([]byte(rt.requests[0].Body), &body)
	if body.Labels != "hive-escalation,priority/P1" {
		t.Errorf("labels CSV wrong: %q", body.Labels)
	}
}

// ----- Numeric project id is passed through -----

func TestProjectPath_NumericIDPassthrough(t *testing.T) {
	c, err := NewGitLabClient(GitLabConfig{APIURL: "x", Token: "y", Project: "47"})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.projectPath(); got != "47" {
		t.Errorf("projectPath = %q, want 47", got)
	}
}

func TestProjectPath_SlugPathEncoded(t *testing.T) {
	c, err := NewGitLabClient(GitLabConfig{APIURL: "x", Token: "y", Project: "services/loom-core"})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.projectPath(); got != "services%2Floom-core" {
		t.Errorf("projectPath = %q", got)
	}
}

// ----- compile-time interface assertions -----

var _ pipeline.GitLabClient = (*GitLabClient)(nil)
var _ pipeline.IssueClient = (*GitLabClient)(nil)
