package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/mills/pipeline"
)

// GitLabConfig captures the connection settings for the GitLab REST API.
// The operator reads these from env at startup; the same token is used
// by both the merge-request flow and the issue-creation escalation path.
type GitLabConfig struct {
	// APIURL is the GitLab REST API base, e.g. "https://gitlab.flexinfer.ai/api/v4".
	APIURL string
	// Token is a personal access token or project access token with
	// api scope. Sent as the GitLab PRIVATE-TOKEN header.
	Token string
	// Project is the URL-encoded project path or numeric id (e.g.
	// "services%2Floom-core" or "47"). All MR/pipeline calls scope to
	// this project.
	Project string
	// PollInterval is how often PollPipeline checks the pipeline state.
	// Default 5s. Capped to 2s minimum to avoid hammering the API.
	PollInterval time.Duration
	// PollDeadline caps the total wait for a pipeline to terminate.
	// Default 30 minutes.
	PollDeadline time.Duration
	// MergeMethod is "merge", "rebase", or "ff" — defaults to "merge".
	MergeMethod string
	// Timeout caps any individual HTTP call. Default 30s.
	Timeout time.Duration
}

// GitLabClient implements pipeline.GitLabClient + pipeline.IssueClient
// against the GitLab REST API.
type GitLabClient struct {
	cfg  GitLabConfig
	http *httpclient.Client
}

// NewGitLabClient validates config and returns a ready client.
func NewGitLabClient(cfg GitLabConfig) (*GitLabClient, error) {
	if cfg.APIURL == "" {
		return nil, errors.New("gitlab: APIURL required")
	}
	if cfg.Token == "" {
		return nil, errors.New("gitlab: Token required")
	}
	if cfg.Project == "" {
		return nil, errors.New("gitlab: Project required")
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if cfg.PollDeadline == 0 {
		cfg.PollDeadline = 30 * time.Minute
	}
	if cfg.MergeMethod == "" {
		cfg.MergeMethod = "merge"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	hcfg := httpclient.DefaultConfig()
	hcfg.Timeout = cfg.Timeout
	c := httpclient.New(hcfg)
	return &GitLabClient{cfg: cfg, http: c}, nil
}

// SetTransport is for tests.
func (c *GitLabClient) SetTransport(rt http.RoundTripper) {
	c.http.HTTP().Transport = rt
}

// requestJSON is the shared call helper. It marshals body when non-nil,
// decodes the response into out, and surfaces non-2xx as an error with
// a truncated body for debugging.
func (c *GitLabClient) requestJSON(ctx context.Context, method, path string, body any, out any) error {
	full := strings.TrimRight(c.cfg.APIURL, "/") + path
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("gitlab: marshal: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, full, reqBody)
	if err != nil {
		return fmt.Errorf("gitlab: new request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("PRIVATE-TOKEN", c.cfg.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gitlab: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gitlab: %s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(buf)))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("gitlab: decode %s: %w", path, err)
		}
	}
	return nil
}

// projectPath returns the URL-encoded project segment.
func (c *GitLabClient) projectPath() string {
	// Numeric IDs are passed through; slug paths are URL-encoded.
	if _, err := strconv.Atoi(c.cfg.Project); err == nil {
		return c.cfg.Project
	}
	return url.PathEscape(c.cfg.Project)
}

// ----- CreateMR -----

type createMRBody struct {
	SourceBranch       string `json:"source_branch"`
	TargetBranch       string `json:"target_branch"`
	Title              string `json:"title"`
	Description        string `json:"description,omitempty"`
	RemoveSourceBranch bool   `json:"remove_source_branch"`
	Squash             bool   `json:"squash"`
}

type mrResponse struct {
	IID          int64      `json:"iid"`
	WebURL       string     `json:"web_url"`
	State        string     `json:"state"`
	HeadPipeline mrHeadPipe `json:"head_pipeline"`
	SHA          string     `json:"sha"`
	MergeError   string     `json:"merge_error"`
}

type mrHeadPipe struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

// CreateMR implements pipeline.GitLabClient.
func (c *GitLabClient) CreateMR(ctx context.Context, req pipeline.CreateMRRequest) (pipeline.CreateMRResponse, error) {
	body := createMRBody{
		SourceBranch:       req.SourceBranch,
		TargetBranch:       req.TargetBranch,
		Title:              req.Title,
		Description:        req.Description,
		RemoveSourceBranch: true,
		Squash:             false,
	}
	var got mrResponse
	path := fmt.Sprintf("/projects/%s/merge_requests", c.projectPath())
	if err := c.requestJSON(ctx, http.MethodPost, path, body, &got); err != nil {
		return pipeline.CreateMRResponse{}, err
	}
	return pipeline.CreateMRResponse{
		MRIID: got.IID,
		URL:   got.WebURL,
	}, nil
}

// ----- PollPipeline -----

// PollPipeline implements pipeline.GitLabClient. It looks up the MR's
// head pipeline and polls its state until terminal. The contract is
// blocking: the worker calls and the integration loops itself, so
// upstream code doesn't need its own polling state.
func (c *GitLabClient) PollPipeline(ctx context.Context, req pipeline.PollPipelineRequest) (pipeline.PollPipelineResponse, error) {
	if req.MRIID == 0 {
		return pipeline.PollPipelineResponse{}, errors.New("gitlab: MRIID required")
	}
	pollCtx, cancel := context.WithTimeout(ctx, c.cfg.PollDeadline)
	defer cancel()
	logTail := strings.Builder{}
	terminal := map[string]bool{
		"success": true, "failed": true, "canceled": true, "skipped": true,
	}
	for {
		if err := pollCtx.Err(); err != nil {
			return pipeline.PollPipelineResponse{
				Status:  "timeout",
				LogTail: logTail.String(),
			}, fmt.Errorf("gitlab: pipeline poll timed out after %s", c.cfg.PollDeadline)
		}

		mrPath := fmt.Sprintf("/projects/%s/merge_requests/%d", c.projectPath(), req.MRIID)
		var mr mrResponse
		if err := c.requestJSON(pollCtx, http.MethodGet, mrPath, nil, &mr); err != nil {
			if pollCtx.Err() != nil {
				return pipeline.PollPipelineResponse{Status: "timeout", LogTail: logTail.String()}, fmt.Errorf("gitlab: pipeline poll timed out after %s", c.cfg.PollDeadline)
			}
			return pipeline.PollPipelineResponse{}, err
		}
		if mr.HeadPipeline.ID == 0 {
			fmt.Fprintf(&logTail, "[%s] MR %d head pipeline pending\n", time.Now().Format(time.RFC3339), req.MRIID)
		} else {
			pipePath := fmt.Sprintf("/projects/%s/pipelines/%d", c.projectPath(), mr.HeadPipeline.ID)
			var pipe struct {
				ID     int64  `json:"id"`
				Status string `json:"status"`
				WebURL string `json:"web_url"`
			}
			if err := c.requestJSON(pollCtx, http.MethodGet, pipePath, nil, &pipe); err != nil {
				if pollCtx.Err() != nil {
					return pipeline.PollPipelineResponse{Status: "timeout", LogTail: logTail.String()}, fmt.Errorf("gitlab: pipeline poll timed out after %s", c.cfg.PollDeadline)
				}
				return pipeline.PollPipelineResponse{}, err
			}
			fmt.Fprintf(&logTail, "[%s] pipeline %d status=%s\n", time.Now().Format(time.RFC3339), pipe.ID, pipe.Status)
			if terminal[pipe.Status] {
				return pipeline.PollPipelineResponse{
					Status:  pipe.Status,
					LogTail: logTail.String(),
				}, nil
			}
		}

		select {
		case <-pollCtx.Done():
			return pipeline.PollPipelineResponse{Status: "timeout", LogTail: logTail.String()}, fmt.Errorf("gitlab: pipeline poll timed out after %s", c.cfg.PollDeadline)
		case <-time.After(c.cfg.PollInterval):
		}
	}
}

// ----- Merge -----

type mergeBody struct {
	MergeWhenPipelineSucceeds bool   `json:"merge_when_pipeline_succeeds,omitempty"`
	Squash                    bool   `json:"squash,omitempty"`
	SHA                       string `json:"sha,omitempty"`
}

// Merge implements pipeline.GitLabClient.
func (c *GitLabClient) Merge(ctx context.Context, req pipeline.MergeRequestArgs) (pipeline.MergeResponse, error) {
	if req.MRIID == 0 {
		return pipeline.MergeResponse{}, errors.New("gitlab: MRIID required")
	}
	path := fmt.Sprintf("/projects/%s/merge_requests/%d/merge", c.projectPath(), req.MRIID)
	var got mrResponse
	if err := c.requestJSON(ctx, http.MethodPut, path, mergeBody{}, &got); err != nil {
		return pipeline.MergeResponse{}, err
	}
	if got.MergeError != "" {
		return pipeline.MergeResponse{}, fmt.Errorf("gitlab: merge failed: %s", got.MergeError)
	}
	return pipeline.MergeResponse{MergedSHA: got.SHA}, nil
}

// ----- Cleanup -----

// Cleanup deletes the source branch when GitLab didn't auto-delete it
// (RemoveSourceBranch=true on create usually handles this, but a
// best-effort delete here is the documented contract).
func (c *GitLabClient) Cleanup(ctx context.Context, req pipeline.CleanupRequest) (pipeline.CleanupResponse, error) {
	logTail := strings.Builder{}
	if req.BranchName != "" {
		path := fmt.Sprintf("/projects/%s/repository/branches/%s", c.projectPath(), url.PathEscape(req.BranchName))
		if err := c.requestJSON(ctx, http.MethodDelete, path, nil, nil); err != nil {
			// 404 = branch already gone; treat as success.
			if !strings.Contains(err.Error(), "status 404") {
				return pipeline.CleanupResponse{LogTail: logTail.String()}, err
			}
			fmt.Fprintf(&logTail, "branch %s already removed\n", req.BranchName)
		} else {
			fmt.Fprintf(&logTail, "deleted branch %s\n", req.BranchName)
		}
	}
	return pipeline.CleanupResponse{LogTail: logTail.String()}, nil
}

// ----- Issue (escalation path) -----

type createIssueBody struct {
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	Labels       string `json:"labels,omitempty"` // GitLab takes a CSV
	Confidential bool   `json:"confidential,omitempty"`
}

type issueResponse struct {
	IID    int64  `json:"iid"`
	WebURL string `json:"web_url"`
}

// CreateIssue implements pipeline.IssueClient.
func (c *GitLabClient) CreateIssue(ctx context.Context, req pipeline.IssueRequest) (pipeline.IssueResponse, error) {
	body := createIssueBody{
		Title:       req.Title,
		Description: req.Description,
		Labels:      strings.Join(req.Labels, ","),
	}
	var got issueResponse
	path := fmt.Sprintf("/projects/%s/issues", c.projectPath())
	if err := c.requestJSON(ctx, http.MethodPost, path, body, &got); err != nil {
		return pipeline.IssueResponse{}, err
	}
	return pipeline.IssueResponse{IID: got.IID, URL: got.WebURL}, nil
}
