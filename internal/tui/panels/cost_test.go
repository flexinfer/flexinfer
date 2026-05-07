package panels

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestCostPanel_Disabled(t *testing.T) {
	p := NewCostPanel()
	p, _ = p.Update(MsgCostData{Enabled: false})
	out := p.View()
	if !strings.Contains(out, "disabled") {
		t.Errorf("expected disabled notice, got: %q", out)
	}
}

func TestCostPanel_Empty(t *testing.T) {
	p := NewCostPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	p, _ = p.Update(MsgCostData{Enabled: true})
	out := p.View()
	if !strings.Contains(out, "no agent activity") {
		t.Errorf("expected empty agents, got: %q", out)
	}
	if !strings.Contains(out, "no server activity") {
		t.Errorf("expected empty servers, got: %q", out)
	}
}

func TestCostPanel_WithData(t *testing.T) {
	p := NewCostPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	p, _ = p.Update(MsgCostData{
		Enabled:     true,
		TotalCalls:  1000,
		TotalErrors: 5,
		TotalDenied: 2,
		TotalCached: 250,
		ByAgent: []CostAgentRow{
			{AgentID: "claude-code-1", CallCount: 600, Errors: 3, Denied: 1, Cached: 150},
			{AgentID: "codex-cli", CallCount: 400, Errors: 2, Denied: 1, Cached: 100},
		},
		ByServer: []CostServerRow{
			{Server: "github", CallCount: 500, Errors: 1},
			{Server: "gitlab", CallCount: 500, Errors: 4},
		},
	})

	out := p.View()
	for _, want := range []string{
		"COST & USAGE",
		"1000 calls",
		"claude-code-1",
		"codex-cli",
		"github",
		"gitlab",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %q", want, out)
		}
	}
}
