package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/mcperror"
)

func TestMain(m *testing.M) {
	// Force JSON output format so handler results are parseable JSON.
	os.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	os.Exit(m.Run())
}

// rewriteTransport rewrites outbound requests from the hardcoded
// https://api.github.com base to a local httptest server URL.
type rewriteTransport struct {
	targetURL string
	base      http.RoundTripper
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Replace the hardcoded GitHub API URL prefix with the test server URL.
	newURL := strings.Replace(req.URL.String(), "https://api.github.com", t.targetURL, 1)
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, newURL, req.Body)
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header
	return t.base.RoundTrip(newReq)
}

// newTestGitHubServer creates a githubServer wired to the given httptest server URL.
func newTestGitHubServer(testURL string) *githubServer {
	client := httpclient.NewDefault()
	client.HTTP().Transport = &rewriteTransport{
		targetURL: testURL,
		base:      http.DefaultTransport,
	}
	return &githubServer{
		token:      "test-token",
		httpClient: client,
	}
}

// --------------------------------------------------------------------------
// Error handling tests
// --------------------------------------------------------------------------

func TestGitHubRequest_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"Not Found","documentation_url":"https://docs.github.com/rest"}`)
	}))
	defer ts.Close()

	gh := newTestGitHubServer(ts.URL)

	_, err := gh.request(context.Background(), "GET", "/repos/owner/nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for 404 response, got nil")
	}

	mcpErr, ok := err.(*mcperror.Error)
	if !ok {
		t.Fatalf("expected *mcperror.Error, got %T: %v", err, err)
	}

	if mcpErr.Code != mcperror.CodeNotFound {
		t.Errorf("expected error code %q, got %q", mcperror.CodeNotFound, mcpErr.Code)
	}

	if !strings.Contains(mcpErr.Message, "GitHub") {
		t.Errorf("expected error message to contain 'GitHub', got %q", mcpErr.Message)
	}

	if !strings.Contains(mcpErr.Message, "not found") {
		t.Errorf("expected error message to contain 'not found', got %q", mcpErr.Message)
	}
}

func TestGitHubRequest_Unauthorized(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"message":"Bad credentials"}`)
	}))
	defer ts.Close()

	gh := newTestGitHubServer(ts.URL)

	_, err := gh.request(context.Background(), "GET", "/user/repos", nil)
	if err == nil {
		t.Fatal("expected error for 401 response, got nil")
	}

	mcpErr, ok := err.(*mcperror.Error)
	if !ok {
		t.Fatalf("expected *mcperror.Error, got %T: %v", err, err)
	}

	if mcpErr.Code != mcperror.CodeUnauthorized {
		t.Errorf("expected error code %q, got %q", mcperror.CodeUnauthorized, mcpErr.Code)
	}

	if !strings.Contains(mcpErr.Message, "authentication failed") {
		t.Errorf("expected error message to mention authentication failure, got %q", mcpErr.Message)
	}
}

// --------------------------------------------------------------------------
// Pagination link parsing
// --------------------------------------------------------------------------

func TestParseGitHubPagination(t *testing.T) {
	tests := []struct {
		name     string
		link     string
		wantKeys []string
	}{
		{
			name:     "next and last",
			link:     `<https://api.github.com/repos/owner/repo/issues?page=2>; rel="next", <https://api.github.com/repos/owner/repo/issues?page=5>; rel="last"`,
			wantKeys: []string{"next_url", "next_page", "last_url", "last_page"},
		},
		{
			name:     "prev and next",
			link:     `<https://api.github.com/repos/owner/repo/issues?page=1>; rel="prev", <https://api.github.com/repos/owner/repo/issues?page=3>; rel="next"`,
			wantKeys: []string{"prev_url", "prev_page", "next_url", "next_page"},
		},
		{
			name:     "empty link",
			link:     "",
			wantKeys: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			headers := http.Header{}
			if tc.link != "" {
				headers.Set("Link", tc.link)
			}
			result := parseGitHubPagination(headers)

			if tc.wantKeys == nil {
				if result != nil {
					t.Fatalf("expected nil pagination, got %v", result)
				}
				return
			}

			if result == nil {
				t.Fatal("expected pagination map, got nil")
			}

			for _, key := range tc.wantKeys {
				if _, ok := result[key]; !ok {
					t.Errorf("expected key %q in pagination, got keys: %v", key, result)
				}
			}
		})
	}

	// Verify page numbers are parsed correctly.
	headers := http.Header{}
	headers.Set("Link", `<https://api.github.com/repos/o/r/issues?page=3>; rel="next", <https://api.github.com/repos/o/r/issues?page=10>; rel="last"`)
	result := parseGitHubPagination(headers)
	if result == nil {
		t.Fatal("expected non-nil pagination")
	}

	if nextPage, ok := result["next_page"].(int); !ok || nextPage != 3 {
		t.Errorf("expected next_page=3, got %v", result["next_page"])
	}
	if lastPage, ok := result["last_page"].(int); !ok || lastPage != 10 {
		t.Errorf("expected last_page=10, got %v", result["last_page"])
	}
}

func TestParseGitHubPagination_NilHeaders(t *testing.T) {
	result := parseGitHubPagination(nil)
	if result != nil {
		t.Fatalf("expected nil for nil headers, got %v", result)
	}
}

// --------------------------------------------------------------------------
// Handler tests
// --------------------------------------------------------------------------

func TestHandleListRepos(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/users/testuser/repos") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Errorf("unexpected auth header: %s", auth)
		}

		baseURL := "http://" + r.Host
		w.Header().Set("Link", fmt.Sprintf(
			`<%s/users/testuser/repos?page=2>; rel="next", <%s/users/testuser/repos?page=3>; rel="last"`,
			baseURL, baseURL,
		))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "name": "repo-a", "full_name": "testuser/repo-a"},
			{"id": 2, "name": "repo-b", "full_name": "testuser/repo-b"},
		})
	}))
	defer ts.Close()

	gh := newTestGitHubServer(ts.URL)

	result, err := gh.handleListRepos(context.Background(), map[string]any{
		"owner":    "testuser",
		"per_page": 10,
		"page":     1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("result is error: %s", result.Content[0].Text)
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &data); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	repos, ok := data["repositories"].([]any)
	if !ok {
		t.Fatalf("expected repositories array, got %T", data["repositories"])
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}

	count, ok := data["count"].(float64)
	if !ok || int(count) != 2 {
		t.Errorf("expected count=2, got %v", data["count"])
	}
}

func TestHandleGetRepo(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/repos/octocat/hello-world" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":        1296269,
			"name":      "hello-world",
			"full_name": "octocat/hello-world",
			"private":   false,
			"owner": map[string]any{
				"login": "octocat",
				"id":    1,
			},
			"description":      "My first repository on GitHub!",
			"stargazers_count": 80,
		})
	}))
	defer ts.Close()

	gh := newTestGitHubServer(ts.URL)

	result, err := gh.handleGetRepo(context.Background(), map[string]any{
		"owner": "octocat",
		"repo":  "hello-world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("result is error: %s", result.Content[0].Text)
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &data); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	if data["full_name"] != "octocat/hello-world" {
		t.Errorf("expected full_name 'octocat/hello-world', got %v", data["full_name"])
	}
	if data["description"] != "My first repository on GitHub!" {
		t.Errorf("unexpected description: %v", data["description"])
	}
}

func TestHandleGetRepo_MissingRequired(t *testing.T) {
	gh := &githubServer{token: "test-token", httpClient: httpclient.NewDefault()}

	result, err := gh.handleGetRepo(context.Background(), map[string]any{
		"owner": "octocat",
		// missing "repo"
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing required param")
	}
}

func TestHandleSearchRepos(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/search/repositories") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query().Get("q")
		if q != "golang mcp" {
			t.Errorf("unexpected query param q=%q", q)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"total_count":        2,
			"incomplete_results": false,
			"items": []map[string]any{
				{"id": 100, "name": "mcp-server", "full_name": "user/mcp-server"},
				{"id": 101, "name": "go-mcp", "full_name": "user/go-mcp"},
			},
		})
	}))
	defer ts.Close()

	gh := newTestGitHubServer(ts.URL)

	result, err := gh.handleSearchRepos(context.Background(), map[string]any{
		"query":    "golang mcp",
		"per_page": 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("result is error: %s", result.Content[0].Text)
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &data); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	totalCount, ok := data["total_count"].(float64)
	if !ok || int(totalCount) != 2 {
		t.Errorf("expected total_count=2, got %v", data["total_count"])
	}

	items, ok := data["items"].([]any)
	if !ok {
		t.Fatalf("expected items array, got %T", data["items"])
	}
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
}

func TestHandleListIssues(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/repos/octocat/hello-world/issues") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if state := r.URL.Query().Get("state"); state != "open" {
			t.Errorf("expected state=open, got %q", state)
		}

		// Include pagination headers.
		baseURL := "http://" + r.Host
		w.Header().Set("Link", fmt.Sprintf(
			`<%s/repos/octocat/hello-world/issues?state=open&page=2>; rel="next", <%s/repos/octocat/hello-world/issues?state=open&page=4>; rel="last"`,
			baseURL, baseURL,
		))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":     1,
				"number": 1347,
				"title":  "Found a bug",
				"state":  "open",
				"user":   map[string]any{"login": "octocat"},
			},
			{
				"id":     2,
				"number": 1348,
				"title":  "Feature request",
				"state":  "open",
				"user":   map[string]any{"login": "octocat"},
			},
		})
	}))
	defer ts.Close()

	gh := newTestGitHubServer(ts.URL)

	result, err := gh.handleListIssues(context.Background(), map[string]any{
		"owner":    "octocat",
		"repo":     "hello-world",
		"state":    "open",
		"per_page": 10,
		"page":     1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("result is error: %s", result.Content[0].Text)
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &data); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	issues, ok := data["issues"].([]any)
	if !ok {
		t.Fatalf("expected issues array, got %T", data["issues"])
	}
	if len(issues) != 2 {
		t.Errorf("expected 2 issues, got %d", len(issues))
	}

	count, ok := data["count"].(float64)
	if !ok || int(count) != 2 {
		t.Errorf("expected count=2, got %v", data["count"])
	}

	pagination, ok := data["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("expected pagination map, got %T", data["pagination"])
	}
	if _, ok := pagination["next_url"]; !ok {
		t.Error("expected next_url in pagination")
	}
	if _, ok := pagination["last_url"]; !ok {
		t.Error("expected last_url in pagination")
	}
	if nextPage, ok := pagination["next_page"].(float64); !ok || int(nextPage) != 2 {
		t.Errorf("expected next_page=2, got %v", pagination["next_page"])
	}
	if lastPage, ok := pagination["last_page"].(float64); !ok || int(lastPage) != 4 {
		t.Errorf("expected last_page=4, got %v", pagination["last_page"])
	}
}

func TestHandleListIssues_MissingRequired(t *testing.T) {
	gh := &githubServer{token: "test-token", httpClient: httpclient.NewDefault()}

	result, err := gh.handleListIssues(context.Background(), map[string]any{
		"owner": "octocat",
		// missing "repo"
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing required param")
	}
}

func TestHandleListIssues_WithLabels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		labels := r.URL.Query().Get("labels")
		if labels != "bug,enhancement" {
			t.Errorf("expected labels=bug,enhancement, got %q", labels)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "number": 10, "title": "Bug issue", "state": "open"},
		})
	}))
	defer ts.Close()

	gh := newTestGitHubServer(ts.URL)

	result, err := gh.handleListIssues(context.Background(), map[string]any{
		"owner":  "octocat",
		"repo":   "hello-world",
		"labels": "bug,enhancement",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("result is error: %s", result.Content[0].Text)
	}
}

// --------------------------------------------------------------------------
// Helper function tests
// --------------------------------------------------------------------------

func TestExtractPage(t *testing.T) {
	tests := []struct {
		url      string
		wantPage int
		wantOK   bool
	}{
		{"https://api.github.com/repos/o/r/issues?page=5", 5, true},
		{"https://api.github.com/repos/o/r/issues?page=1&state=open", 1, true},
		{"https://api.github.com/repos/o/r/issues", 0, false},
		{"https://api.github.com/repos/o/r/issues?page=0", 0, false},
		{"https://api.github.com/repos/o/r/issues?page=-1", 0, false},
		{"https://api.github.com/repos/o/r/issues?page=abc", 0, false},
		{"://bad-url", 0, false},
	}

	for _, tc := range tests {
		page, ok := extractPage(tc.url)
		if ok != tc.wantOK {
			t.Errorf("extractPage(%q) ok=%v, want %v", tc.url, ok, tc.wantOK)
		}
		if page != tc.wantPage {
			t.Errorf("extractPage(%q) page=%d, want %d", tc.url, page, tc.wantPage)
		}
	}
}

func TestNormalizePerPage(t *testing.T) {
	tests := []struct {
		input   int
		wantVal int
	}{
		{0, 30},    // below minimum uses default
		{-1, 30},   // negative uses default
		{50, 50},   // valid value passes through
		{100, 100}, // max value passes through
		{200, 100}, // above max is clamped
	}
	for _, tc := range tests {
		got := normalizePerPage(tc.input, 30)
		if got != tc.wantVal {
			t.Errorf("normalizePerPage(%d, 30) = %d, want %d", tc.input, got, tc.wantVal)
		}
	}
}

func TestNormalizePage(t *testing.T) {
	tests := []struct {
		input   int
		wantVal int
	}{
		{0, 1},
		{-1, 1},
		{1, 1},
		{5, 5},
	}
	for _, tc := range tests {
		got := normalizePage(tc.input)
		if got != tc.wantVal {
			t.Errorf("normalizePage(%d) = %d, want %d", tc.input, got, tc.wantVal)
		}
	}
}
