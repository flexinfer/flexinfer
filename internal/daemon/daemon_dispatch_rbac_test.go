package daemon

import (
	"context"
	"encoding/json"
	"testing"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

func TestHandleMessage_RBACSimulate(t *testing.T) {
	d := &Daemon{
		rbac: NewRBACEnforcer(RBACConfig{
			Enabled:       true,
			DefaultPolicy: "deny",
			Roles: map[string]RBACRole{
				"readonly": {Allow: []string{"*__get_*"}},
			},
			Bindings: []RBACBinding{
				{AgentType: "codex", Role: "readonly"},
			},
		}, nil),
	}
	if d.rbac == nil {
		t.Fatal("expected rbac enforcer")
	}

	msg, err := mcp.NewRequest(1, "loom/rbac-simulate", map[string]any{
		"agent_id":   "agent-1",
		"agent_type": "codex",
		"server":     "github",
		"tool":       "get_repo",
		"dry_run":    true,
	})
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := d.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("expected successful response, got %+v", resp)
	}

	var out struct {
		Enabled  bool           `json:"enabled"`
		Decision AccessDecision `json:"decision"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !out.Enabled {
		t.Fatal("expected enabled=true")
	}
	if !out.Decision.Allowed {
		t.Fatalf("expected allowed decision, got denied: %s", out.Decision.Reason)
	}
	if !out.Decision.DryRun {
		t.Fatal("expected dry_run=true in decision")
	}
	if out.Decision.ReasonCode != "role_allow" {
		t.Fatalf("reason_code=%q want role_allow", out.Decision.ReasonCode)
	}
}

func TestHandleMessage_RBACSimulate_RequiresServerAndTool(t *testing.T) {
	d := &Daemon{
		rbac: NewRBACEnforcer(RBACConfig{Enabled: true, DefaultPolicy: "deny"}, nil),
	}

	msg, err := mcp.NewRequest(1, "loom/rbac-simulate", map[string]any{
		"agent_id": "agent-1",
	})
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := d.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatalf("expected invalid params error response, got %+v", resp)
	}
	if resp.Error.Code != mcp.InvalidParams {
		t.Fatalf("error code=%d want %d", resp.Error.Code, mcp.InvalidParams)
	}
}
