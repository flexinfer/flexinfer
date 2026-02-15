package panels

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHealthPanelUpdateWindowSize(t *testing.T) {
	p := NewHealthPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if p.width != 100 || p.height != 30 {
		t.Errorf("size = (%d, %d), want (100, 30)", p.width, p.height)
	}
}

func TestHealthPanelUpdateData(t *testing.T) {
	p := NewHealthPanel()
	msg := MsgHealthData{
		Servers: []ServerData{
			{Name: "server-1", Running: true, Healthy: true, Latency: 42},
			{Name: "server-2", Running: true, Healthy: false, Latency: 500},
			{Name: "server-3", Running: false, Healthy: false},
		},
	}
	p, _ = p.Update(msg)

	if len(p.servers) != 3 {
		t.Errorf("servers = %d, want 3", len(p.servers))
	}
}

func TestHealthPanelViewNoServers(t *testing.T) {
	p := NewHealthPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	v := p.View()
	if v == "" {
		t.Error("expected non-empty view")
	}
}

func TestHealthPanelViewWithServers(t *testing.T) {
	p := NewHealthPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	p, _ = p.Update(MsgHealthData{
		Servers: []ServerData{
			{Name: "mcp-time", Running: true, Healthy: true, Latency: 5, LatencyHistory: []float64{5, 4, 6, 5}},
			{Name: "mcp-git", Running: true, Healthy: false, Latency: 350, Error: "timeout"},
			{Name: "mcp-docker", Running: false},
		},
	})
	v := p.View()
	if v == "" {
		t.Error("expected non-empty view with servers")
	}
}

func TestHealthPanelViewCompact(t *testing.T) {
	p := NewHealthPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	p, _ = p.Update(MsgHealthData{
		Servers: []ServerData{
			{Name: "s1", Running: true, Healthy: true, Latency: 10},
		},
	})
	v := p.View()
	if v == "" {
		t.Error("expected non-empty compact view")
	}
}
