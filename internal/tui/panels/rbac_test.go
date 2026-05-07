package panels

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestRBACPanel_Disabled(t *testing.T) {
	p := NewRBACPanel()
	p, _ = p.Update(MsgRBACData{Enabled: false})
	out := p.View()
	if !strings.Contains(out, "disabled") {
		t.Errorf("expected disabled notice, got: %q", out)
	}
}

func TestRBACPanel_NoDenials(t *testing.T) {
	p := NewRBACPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	p, _ = p.Update(MsgRBACData{
		Enabled:       true,
		AuditEnabled:  true,
		DefaultPolicy: "deny",
		RoleCount:     3,
		BindingCount:  5,
	})
	out := p.View()
	if !strings.Contains(out, "no recent denials") {
		t.Errorf("expected empty denials notice, got: %q", out)
	}
	if !strings.Contains(out, "audit on") {
		t.Errorf("expected audit on, got: %q", out)
	}
}

func TestRBACPanel_WithDenials(t *testing.T) {
	p := NewRBACPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	p, _ = p.Update(MsgRBACData{
		Enabled:        true,
		AuditEnabled:   true,
		DefaultPolicy:  "allow",
		RoleCount:      2,
		BindingCount:   4,
		DeniedCount24h: 7,
		RecentDenied: []RBACDeniedRow{
			{AgentID: "claude-code-1", Server: "github", Tool: "create_pull_request", Reason: "role lacks scope"},
			{AgentID: "codex-cli", Server: "k8s", Tool: "delete_namespace", Reason: "globally denied"},
		},
	})
	out := p.View()
	for _, want := range []string{
		"RBAC POSTURE",
		"7 denied",
		"claude-code-1",
		"create_pull_request",
		"globally denied",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %q", want, out)
		}
	}
}

func TestEmptyDashRBAC(t *testing.T) {
	if got := emptyDashRBAC(""); got != "-" {
		t.Errorf("expected dash for empty, got %q", got)
	}
	if got := emptyDashRBAC("allow"); got != "allow" {
		t.Errorf("expected pass-through, got %q", got)
	}
}
