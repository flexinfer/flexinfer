package bridge

import (
	"net/url"
	"strings"
	"testing"
)

func TestContextInspectRequestPath(t *testing.T) {
	path, err := (ContextInspectRequest{
		AgentID:   " codex-1 ",
		SessionID: " sess-1 ",
		Detail:    true,
		Limit:     250,
	}).Path()
	if err != nil {
		t.Fatalf("Path() error: %v", err)
	}

	parsed, err := url.Parse(path)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	if parsed.Path != AgentContextInspectEndpoint {
		t.Fatalf("expected path %q, got %q", AgentContextInspectEndpoint, parsed.Path)
	}
	q := parsed.Query()
	if got := q.Get("agent_id"); got != "codex-1" {
		t.Fatalf("expected trimmed agent_id, got %q", got)
	}
	if got := q.Get("session_id"); got != "sess-1" {
		t.Fatalf("expected trimmed session_id, got %q", got)
	}
	if got := q.Get("detail"); got != "true" {
		t.Fatalf("expected detail=true, got %q", got)
	}
	if got := q.Get("limit"); got != "250" {
		t.Fatalf("expected limit=250, got %q", got)
	}
}

func TestParseContextInspectRequest(t *testing.T) {
	values := url.Values{}
	values.Set("agent_id", " codex-1 ")
	values.Set("detail", "yes")
	values.Set("limit", "150")

	parsed, err := ParseContextInspectRequest(values)
	if err != nil {
		t.Fatalf("ParseContextInspectRequest() error: %v", err)
	}
	if parsed.AgentID != "codex-1" {
		t.Fatalf("expected trimmed agent_id, got %q", parsed.AgentID)
	}
	if !parsed.Detail {
		t.Fatalf("expected detail=true")
	}
	if parsed.Limit != 150 {
		t.Fatalf("expected limit=150, got %d", parsed.Limit)
	}
}

func TestParseContextInspectRequestErrors(t *testing.T) {
	_, err := ParseContextInspectRequest(url.Values{})
	if err == nil || !strings.Contains(err.Error(), "agent_id or session_id") {
		t.Fatalf("expected missing identifier error, got %v", err)
	}

	values := url.Values{}
	values.Set("agent_id", "codex-1")
	values.Set("limit", "0")
	_, err = ParseContextInspectRequest(values)
	if err == nil || !strings.Contains(err.Error(), "limit must be a positive integer") {
		t.Fatalf("expected invalid limit error, got %v", err)
	}
}

func TestSessionRequestPath(t *testing.T) {
	path, err := (SessionRequest{AgentID: " codex-1 "}).Path()
	if err != nil {
		t.Fatalf("Path() error: %v", err)
	}
	if !strings.HasPrefix(path, AgentSessionEndpoint+"?") {
		t.Fatalf("expected session path prefix %q, got %q", AgentSessionEndpoint+"?", path)
	}
	parsed, err := url.Parse(path)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	if got := parsed.Query().Get("agent_id"); got != "codex-1" {
		t.Fatalf("expected trimmed agent_id, got %q", got)
	}
}

func TestSessionListRequestParams(t *testing.T) {
	params, err := (SessionListRequest{
		AgentID:   " codex-1 ",
		Namespace: " loom-core/main ",
		Status:    " active ",
	}).Params()
	if err != nil {
		t.Fatalf("Params() error: %v", err)
	}
	if got := params["agent_id"]; got != "codex-1" {
		t.Fatalf("expected trimmed agent_id, got %#v", got)
	}
	if got := params["namespace"]; got != "loom-core/main" {
		t.Fatalf("expected trimmed namespace, got %#v", got)
	}
	if got := params["status"]; got != "active" {
		t.Fatalf("expected trimmed status, got %#v", got)
	}
	if got := params["limit"]; got != DefaultSessionListLimit {
		t.Fatalf("expected default limit=%d, got %#v", DefaultSessionListLimit, got)
	}
}

func TestSessionPruneRequestParams(t *testing.T) {
	params, err := (SessionPruneRequest{DryRun: true}).Params()
	if err != nil {
		t.Fatalf("Params() error: %v", err)
	}
	if got := params["max_age_hours"]; got != 72 {
		t.Fatalf("expected default max_age_hours=72, got %#v", got)
	}
	if got := params["status"]; got != DefaultSessionPruneStatus {
		t.Fatalf("expected default status=%q, got %#v", DefaultSessionPruneStatus, got)
	}
	if got := params["dry_run"]; got != true {
		t.Fatalf("expected dry_run=true, got %#v", got)
	}
}

func TestTaskUpdateRequestToParams(t *testing.T) {
	params, err := (TaskUpdateRequest{
		TaskID:     " task-1 ",
		Status:     " completed ",
		Resolution: " done ",
	}).ToParams()
	if err != nil {
		t.Fatalf("ToParams() error: %v", err)
	}
	if params.ID != "task-1" {
		t.Fatalf("expected trimmed task_id, got %q", params.ID)
	}
	if params.Status != "completed" {
		t.Fatalf("expected trimmed status, got %q", params.Status)
	}
	if params.Resolution != "done" {
		t.Fatalf("expected trimmed resolution, got %q", params.Resolution)
	}
}

func TestDispatchTaskRequestToParamsNormalizes(t *testing.T) {
	params, err := (DispatchTaskRequest{
		TargetAgentID: " codex-1 ",
		Title:         "  Fix it  ",
		Priority:      " HIGH ",
		Tags:          []string{" team ", "", "team", "gitops"},
		BlockedBy:     []string{" task-1 ", "task-1", " task-2 "},
		LineNumber:    -4,
	}).ToParams()
	if err != nil {
		t.Fatalf("ToParams() error: %v", err)
	}
	if params.TargetAgentID != "codex-1" {
		t.Fatalf("expected trimmed target_agent_id, got %q", params.TargetAgentID)
	}
	if params.Title != "Fix it" {
		t.Fatalf("expected trimmed title, got %q", params.Title)
	}
	if params.Priority != "high" {
		t.Fatalf("expected normalized priority=high, got %q", params.Priority)
	}
	if len(params.Tags) != 2 || params.Tags[0] != "team" || params.Tags[1] != "gitops" {
		t.Fatalf("unexpected normalized tags: %#v", params.Tags)
	}
	if len(params.BlockedBy) != 2 || params.BlockedBy[0] != "task-1" || params.BlockedBy[1] != "task-2" {
		t.Fatalf("unexpected normalized blocked_by: %#v", params.BlockedBy)
	}
	if params.LineNumber != 0 {
		t.Fatalf("expected negative line number clamped to 0, got %d", params.LineNumber)
	}
}

func TestNudgeQueuePolicyMutationNormalizeValidate(t *testing.T) {
	capValue := 32
	debounce := 25
	drop := " drop_new "
	m := NudgeQueuePolicyMutation{
		Cap:          &capValue,
		DebounceMs:   &debounce,
		DropPolicy:   &drop,
		LanePriority: []string{" control ", " ", "handoff"},
		UpdatedBy:    " admin ",
	}.Normalize()

	if m.DropPolicy == nil || *m.DropPolicy != "drop_new" {
		t.Fatalf("expected normalized drop_policy=drop_new, got %#v", m.DropPolicy)
	}
	if got := len(m.LanePriority); got != 2 {
		t.Fatalf("expected 2 lanes after normalization, got %d", got)
	}
	if m.UpdatedBy != "admin" {
		t.Fatalf("expected updated_by trimmed, got %q", m.UpdatedBy)
	}
	if !m.HasMutation() {
		t.Fatalf("expected HasMutation=true")
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
}

func TestNudgeQueuePolicyMutationValidateErrors(t *testing.T) {
	m := (NudgeQueuePolicyMutation{LanePriority: []string{""}}).Normalize()
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "lane_priority") {
		t.Fatalf("expected lane_priority validation error, got %v", err)
	}
}

func TestParseLanePriorityCSVAndStatusPath(t *testing.T) {
	lanes, err := ParseLanePriorityCSV(" control, handoff ,advice ")
	if err != nil {
		t.Fatalf("ParseLanePriorityCSV() error: %v", err)
	}
	if len(lanes) != 3 {
		t.Fatalf("expected 3 lanes, got %d", len(lanes))
	}

	path, err := NudgeQueueStatusPath(" codex-1 ")
	if err != nil {
		t.Fatalf("NudgeQueueStatusPath() error: %v", err)
	}
	if !strings.HasPrefix(path, AgentNudgeQueueEndpoint+"?") {
		t.Fatalf("expected status path prefix %q, got %q", AgentNudgeQueueEndpoint+"?", path)
	}
}

func TestNormalizeStringList(t *testing.T) {
	values := NormalizeStringList([]string{" alpha ", "", "beta", "alpha", " beta "})
	if len(values) != 2 || values[0] != "alpha" || values[1] != "beta" {
		t.Fatalf("unexpected normalized list: %#v", values)
	}
}
