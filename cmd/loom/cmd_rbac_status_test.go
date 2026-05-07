package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	rbac "github.com/crb2nu/loom/internal/visibility/contracts/rbac"
)

func sampleRBACConfig() *bridge.RBACConfigResult {
	return &bridge.RBACConfigResult{
		Enabled:       true,
		AuditEnabled:  true,
		DefaultPolicy: "deny",
		DeniedCount:   3,
		RecentDenied: []bridge.RBACDeniedEntry{
			{
				AgentID:   "claude-code",
				Server:    "github",
				Tool:      "delete_repo",
				Role:      "reader",
				Reason:    "tool not in allow-list",
				Timestamp: "2026-05-06T11:30:00Z",
			},
			{
				AgentID:   "",
				Server:    "gitlab",
				Tool:      "merge_mr",
				Role:      "writer",
				Reason:    "rate limit exceeded",
				Timestamp: "2026-05-06T11:35:00Z",
			},
			{
				AgentID:   "codex",
				Server:    "",
				Tool:      "",
				Role:      "",
				Reason:    "policy missing",
				Timestamp: "",
			},
		},
	}
}

func TestAdaptRBACConfigToSnapshot_NilSafe(t *testing.T) {
	t.Parallel()

	got := adaptRBACConfigToSnapshot(nil)
	if got.AuditEnabled || got.DeniedCount24h != 0 || len(got.RecentDenials) != 0 {
		t.Errorf("expected zero snapshot, got %+v", got)
	}
}

func TestAdaptRBACConfigToSnapshot_LiftsFields(t *testing.T) {
	t.Parallel()

	cfg := sampleRBACConfig()
	got := adaptRBACConfigToSnapshot(cfg)

	if !got.AuditEnabled {
		t.Errorf("audit_enabled = false, want true")
	}
	if got.DeniedCount24h != 3 {
		t.Errorf("denied_count_24h = %d, want 3", got.DeniedCount24h)
	}
	if len(got.RecentDenials) != 3 {
		t.Fatalf("recent_denials len = %d, want 3", len(got.RecentDenials))
	}

	// First entry: agent_id present, server+tool present.
	d0 := got.RecentDenials[0]
	if d0.Actor != "claude-code" {
		t.Errorf("denial[0].actor = %q, want claude-code", d0.Actor)
	}
	if d0.Resource != "github__delete_repo" {
		t.Errorf("denial[0].resource = %q, want github__delete_repo", d0.Resource)
	}
	if d0.Time.IsZero() {
		t.Errorf("denial[0].time should be parsed, got zero")
	}

	// Second entry: agent_id empty, role fallback.
	d1 := got.RecentDenials[1]
	if d1.Actor != "role:writer" {
		t.Errorf("denial[1].actor = %q, want role:writer", d1.Actor)
	}

	// Third entry: nothing — actor=unknown, resource=unknown, time=zero.
	d2 := got.RecentDenials[2]
	if d2.Actor != "codex" {
		t.Errorf("denial[2].actor = %q, want codex", d2.Actor)
	}
	if d2.Resource != "unknown" {
		t.Errorf("denial[2].resource = %q, want unknown", d2.Resource)
	}
	if !d2.Time.IsZero() {
		t.Errorf("denial[2].time = %v, want zero", d2.Time)
	}
}

func TestRunRBACStatusCommand_JSONRoundTrips(t *testing.T) {
	t.Parallel()

	fetch := func(_ string) (*bridge.RBACConfigResult, error) { return sampleRBACConfig(), nil }

	var buf bytes.Buffer
	if err := runRBACStatusCommand(&buf, "/dev/null", true, fetch); err != nil {
		t.Fatalf("runRBACStatusCommand: %v", err)
	}

	var got rbac.Snapshot
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal output: %v\noutput: %s", err, buf.String())
	}
	if !got.AuditEnabled {
		t.Errorf("audit_enabled = false")
	}
	if got.DeniedCount24h != 3 {
		t.Errorf("denied_count_24h = %d, want 3", got.DeniedCount24h)
	}
	if len(got.RecentDenials) != 3 {
		t.Errorf("recent_denials len = %d, want 3", len(got.RecentDenials))
	}
}

func TestRunRBACStatusCommand_TextSummary(t *testing.T) {
	t.Parallel()

	fetch := func(_ string) (*bridge.RBACConfigResult, error) { return sampleRBACConfig(), nil }
	var buf bytes.Buffer
	if err := runRBACStatusCommand(&buf, "/dev/null", false, fetch); err != nil {
		t.Fatalf("runRBACStatusCommand: %v", err)
	}
	got := buf.String()

	wants := []string{
		"RBAC posture",
		"audit_enabled:    true",
		"denied_count_24h: 3",
		"Recent denials:",
		"github__delete_repo",
		"role:writer",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q\nfull output:\n%s", w, got)
		}
	}
}

func TestRunRBACStatusCommand_NoDenials(t *testing.T) {
	t.Parallel()

	fetch := func(_ string) (*bridge.RBACConfigResult, error) {
		return &bridge.RBACConfigResult{Enabled: true, AuditEnabled: true}, nil
	}
	var buf bytes.Buffer
	if err := runRBACStatusCommand(&buf, "/dev/null", false, fetch); err != nil {
		t.Fatalf("runRBACStatusCommand: %v", err)
	}
	if !strings.Contains(buf.String(), "Recent denials: none") {
		t.Errorf("expected 'Recent denials: none' in output, got:\n%s", buf.String())
	}
}

func TestRunRBACStatusCommand_FetchErrorIsNonZero(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("connection refused")
	fetch := func(_ string) (*bridge.RBACConfigResult, error) { return nil, wantErr }

	var buf bytes.Buffer
	err := runRBACStatusCommand(&buf, "/dev/null", false, fetch)
	if err == nil {
		t.Fatalf("expected error from fetch failure")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error chain missing fetch error: %v", err)
	}
	if !strings.Contains(err.Error(), "daemon unreachable") {
		t.Errorf("error missing daemon-unreachable hint: %v", err)
	}
}

func TestParseRBACTimestamp(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in       string
		wantZero bool
	}{
		{"", true},
		{"  ", true},
		{"not-a-time", true},
		{"2026-05-06T11:30:00Z", false},
		{"2026-05-06T11:30:00.123456789Z", false},
	}
	for _, tc := range cases {
		got := parseRBACTimestamp(tc.in)
		if got.IsZero() != tc.wantZero {
			t.Errorf("parseRBACTimestamp(%q) zero=%v, want zero=%v", tc.in, got.IsZero(), tc.wantZero)
		}
	}
}

func TestRBACFormatTime(t *testing.T) {
	t.Parallel()

	if got := rbacFormatTime(time.Time{}); got != "-" {
		t.Errorf("rbacFormatTime(zero) = %q, want -", got)
	}
	stamp := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	if got := rbacFormatTime(stamp); got != "2026-05-06T12:00:00Z" {
		t.Errorf("rbacFormatTime(stamp) = %q, want 2026-05-06T12:00:00Z", got)
	}
}
