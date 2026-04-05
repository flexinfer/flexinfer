package webhook

import "time"

// GitLabPipelineEvent represents a GitLab pipeline webhook payload (subset).
type GitLabPipelineEvent struct {
	ObjectKind       string `json:"object_kind"`
	ObjectAttributes struct {
		ID        int    `json:"id"`
		Ref       string `json:"ref"`
		Status    string `json:"status"`
		Source    string `json:"source"`
		DetailURL string `json:"detailed_status"`
	} `json:"object_attributes"`
	MergeRequest *struct {
		IID          int    `json:"iid"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		Title        string `json:"title"`
	} `json:"merge_request"`
	Project struct {
		PathWithNamespace string `json:"path_with_namespace"`
		WebURL            string `json:"web_url"`
	} `json:"project"`
}

// GitHubCheckSuiteEvent represents a GitHub check_suite webhook payload (subset).
type GitHubCheckSuiteEvent struct {
	Action     string `json:"action"`
	CheckSuite struct {
		ID         int    `json:"id"`
		HeadBranch string `json:"head_branch"`
		HeadSHA    string `json:"head_sha"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"check_suite"`
	Repository struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
}

// GitHubPullRequestEvent represents a GitHub pull_request webhook payload (subset).
type GitHubPullRequestEvent struct {
	Action      string `json:"action"`
	PullRequest struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Head   struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	} `json:"pull_request"`
	Repository struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
}

// WebhookEvent is a log entry for the in-memory event ring buffer.
type WebhookEvent struct {
	Timestamp  time.Time `json:"timestamp"`
	Source     string    `json:"source"` // "gitlab" or "github"
	EventType  string    `json:"event_type"`
	Project    string    `json:"project"`
	Ref        string    `json:"ref"`
	Status     string    `json:"status"`
	Action     string    `json:"action,omitempty"`
	SpawnID    string    `json:"spawn_id,omitempty"`
	Error      string    `json:"error,omitempty"`
	RawSummary string    `json:"raw_summary,omitempty"`
}
