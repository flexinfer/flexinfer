package daemon

import (
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/pool"
)

func TestDefaultHubDelegateServers(t *testing.T) {
	servers := DefaultHubDelegateServers()
	if len(servers) != 2 {
		t.Fatalf("expected 2 default servers, got %d", len(servers))
	}
	want := map[string]bool{"agent_context": true, "devbox": true}
	for _, s := range servers {
		if !want[s] {
			t.Errorf("unexpected default server: %q", s)
		}
	}
}

func TestHubDelegateEligible_InListAndHubHealthy(t *testing.T) {
	d := &Daemon{
		hubPool:   pool.New(pool.Config{MaxIdle: 1, MaxOpen: 1}),
		hubClient: mcp.NewWebSocketClient(mcp.WebSocketConfig{URL: "wss://test"}),
		fileCfg: FileConfig{
			HubDelegate: HubDelegateConfig{
				Servers: []string{"agent_context", "devbox"},
			},
		},
	}

	if !d.hubDelegateEligible("agent_context") {
		t.Error("expected agent_context to be eligible for hub delegation")
	}
	if !d.hubDelegateEligible("devbox") {
		t.Error("expected devbox to be eligible for hub delegation")
	}
}

func TestHubDelegateEligible_NotInList(t *testing.T) {
	d := &Daemon{
		hubPool:   pool.New(pool.Config{MaxIdle: 1, MaxOpen: 1}),
		hubClient: mcp.NewWebSocketClient(mcp.WebSocketConfig{URL: "wss://test"}),
		fileCfg: FileConfig{
			HubDelegate: HubDelegateConfig{
				Servers: []string{"agent_context", "devbox"},
			},
		},
	}

	if d.hubDelegateEligible("git") {
		t.Error("expected git to NOT be eligible for hub delegation")
	}
	if d.hubDelegateEligible("filesystem") {
		t.Error("expected filesystem to NOT be eligible for hub delegation")
	}
}

func TestHubDelegateEligible_HubPoolNil(t *testing.T) {
	d := &Daemon{
		hubPool:   nil,
		hubClient: mcp.NewWebSocketClient(mcp.WebSocketConfig{URL: "wss://test"}),
		fileCfg: FileConfig{
			HubDelegate: HubDelegateConfig{
				Servers: []string{"agent_context"},
			},
		},
	}

	if d.hubDelegateEligible("agent_context") {
		t.Error("expected ineligible when hubPool is nil")
	}
}

func TestHubDelegateEligible_HubClientNil(t *testing.T) {
	d := &Daemon{
		hubPool:   pool.New(pool.Config{MaxIdle: 1, MaxOpen: 1}),
		hubClient: nil,
		fileCfg: FileConfig{
			HubDelegate: HubDelegateConfig{
				Servers: []string{"agent_context"},
			},
		},
	}

	if d.hubDelegateEligible("agent_context") {
		t.Error("expected ineligible when hubClient is nil")
	}
}

func TestHubDelegateEligible_HubAuthDisabled(t *testing.T) {
	d := &Daemon{
		hubPool:         pool.New(pool.Config{MaxIdle: 1, MaxOpen: 1}),
		hubClient:       mcp.NewWebSocketClient(mcp.WebSocketConfig{URL: "wss://test"}),
		hubAuthDisabled: true,
		fileCfg: FileConfig{
			HubDelegate: HubDelegateConfig{
				Servers: []string{"agent_context"},
			},
		},
	}

	if d.hubDelegateEligible("agent_context") {
		t.Error("expected ineligible when hubAuthDisabled is true")
	}
}

func TestHubDelegateEligible_EmptyDelegateList(t *testing.T) {
	d := &Daemon{
		hubPool:   pool.New(pool.Config{MaxIdle: 1, MaxOpen: 1}),
		hubClient: mcp.NewWebSocketClient(mcp.WebSocketConfig{URL: "wss://test"}),
		fileCfg: FileConfig{
			HubDelegate: HubDelegateConfig{
				Servers: nil,
			},
		},
	}

	if d.hubDelegateEligible("agent_context") {
		t.Error("expected ineligible when delegate list is empty")
	}
}

func TestHubDelegateEligible_WarmedServerSkipped(t *testing.T) {
	d := &Daemon{
		cfg: Config{
			WarmOnStart: []string{"agent_context"},
		},
		hubPool:   pool.New(pool.Config{MaxIdle: 1, MaxOpen: 1}),
		hubClient: mcp.NewWebSocketClient(mcp.WebSocketConfig{URL: "wss://test"}),
		fileCfg: FileConfig{
			HubDelegate: HubDelegateConfig{
				Servers: []string{"agent_context", "devbox"},
			},
		},
	}

	if d.hubDelegateEligible("agent_context") {
		t.Error("expected agent_context ineligible when warmed")
	}
	if !d.hubDelegateEligible("devbox") {
		t.Error("expected devbox still eligible (not warmed)")
	}
}

func TestHubDelegateConfig_DefaultInFileConfig(t *testing.T) {
	cfg := DefaultFileConfig()
	servers := cfg.HubDelegate.Servers
	if len(servers) != 2 {
		t.Fatalf("expected 2 default delegate servers, got %d", len(servers))
	}
	want := map[string]bool{"agent_context": true, "devbox": true}
	for _, s := range servers {
		if !want[s] {
			t.Errorf("unexpected default delegate server: %q", s)
		}
	}
}
