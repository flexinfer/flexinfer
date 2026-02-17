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
