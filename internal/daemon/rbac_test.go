package daemon

import (
	"fmt"
	"log/slog"
	"sync"
	"testing"
)

func TestRBAC_DisabledReturnsNil(t *testing.T) {
	cfg := DefaultRBACConfig()
	e := NewRBACEnforcer(cfg, slog.Default())
	if e != nil {
		t.Fatal("expected nil enforcer when RBAC is disabled")
	}
}

func TestRBAC_Check(t *testing.T) {
	cfg := RBACConfig{
		Enabled:       true,
		DefaultPolicy: "deny",
		Roles: map[string]RBACRole{
			"admin": {
				Allow: []string{"*"},
			},
			"developer": {
				Allow: []string{"*"},
				Deny: []string{
					"k8s_apps_k3s__k8s_apply",
					"k8s_apps_k3s__k8s_exec",
					"server_mgmt__server_execSafe",
					"server_mgmt__server_sshCommand",
				},
			},
			"readonly": {
				Allow: []string{"*__list_*", "*__get_*", "*__search*", "*__query*"},
			},
		},
		Bindings: []RBACBinding{
			{AgentID: "admin-bot", Role: "admin"},
			{AgentID: "claude-code", Role: "developer"},
			{AgentType: "codex", Role: "readonly"},
			{AgentID: "*", Role: "developer"},
		},
	}

	e := NewRBACEnforcer(cfg, slog.Default())
	if e == nil {
		t.Fatal("expected non-nil enforcer when RBAC is enabled")
	}

	tests := []struct {
		name      string
		agentID   string
		agentType string
		server    string
		tool      string
		allowed   bool
		wantRole  string
	}{
		{
			name:     "admin allows everything",
			agentID:  "admin-bot",
			server:   "k8s_apps_k3s",
			tool:     "k8s_apply",
			allowed:  true,
			wantRole: "admin",
		},
		{
			name:     "developer allowed normal tool",
			agentID:  "claude-code",
			server:   "git",
			tool:     "git_status",
			allowed:  true,
			wantRole: "developer",
		},
		{
			name:     "developer denied k8s_apply",
			agentID:  "claude-code",
			server:   "k8s_apps_k3s",
			tool:     "k8s_apply",
			allowed:  false,
			wantRole: "developer",
		},
		{
			name:     "developer denied server_execSafe",
			agentID:  "claude-code",
			server:   "server_mgmt",
			tool:     "server_execSafe",
			allowed:  false,
			wantRole: "developer",
		},
		{
			name:      "codex type binding gets readonly",
			agentID:   "codex-instance-1",
			agentType: "codex",
			server:    "gitlab",
			tool:      "list_issues",
			allowed:   true,
			wantRole:  "readonly",
		},
		{
			name:      "readonly denies write tool",
			agentID:   "codex-instance-1",
			agentType: "codex",
			server:    "gitlab",
			tool:      "create_issue",
			allowed:   false,
			wantRole:  "readonly",
		},
		{
			name:      "readonly allows get tool",
			agentID:   "codex-instance-1",
			agentType: "codex",
			server:    "gitlab",
			tool:      "get_issue",
			allowed:   true,
			wantRole:  "readonly",
		},
		{
			name:      "readonly allows search tool",
			agentID:   "codex-instance-1",
			agentType: "codex",
			server:    "github",
			tool:      "search_repos",
			allowed:   true,
			wantRole:  "readonly",
		},
		{
			name:      "readonly allows query tool",
			agentID:   "codex-instance-1",
			agentType: "codex",
			server:    "prometheus",
			tool:      "query_range",
			allowed:   true,
			wantRole:  "readonly",
		},
		{
			name:     "wildcard binding matches unknown agent",
			agentID:  "gemini-instance",
			server:   "git",
			tool:     "git_log",
			allowed:  true,
			wantRole: "developer",
		},
		{
			name:     "wildcard binding still enforces deny",
			agentID:  "gemini-instance",
			server:   "k8s_apps_k3s",
			tool:     "k8s_exec",
			allowed:  false,
			wantRole: "developer",
		},
		{
			name:      "exact agent_id takes priority over agent_type",
			agentID:   "admin-bot",
			agentType: "codex",
			server:    "k8s_apps_k3s",
			tool:      "k8s_apply",
			allowed:   true,
			wantRole:  "admin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := e.Check(tt.agentID, tt.agentType, tt.server, tt.tool)
			if d.Allowed != tt.allowed {
				t.Errorf("allowed: got %v, want %v (reason: %s)", d.Allowed, tt.allowed, d.Reason)
			}
			if d.Role != tt.wantRole {
				t.Errorf("role: got %q, want %q", d.Role, tt.wantRole)
			}
		})
	}
}

func TestRBAC_DefaultPolicyAllow(t *testing.T) {
	cfg := RBACConfig{
		Enabled:       true,
		DefaultPolicy: "allow",
		Roles:         map[string]RBACRole{},
		Bindings:      nil, // No bindings
	}

	e := NewRBACEnforcer(cfg, slog.Default())
	d := e.Check("unknown-agent", "", "git", "git_status")
	if !d.Allowed {
		t.Errorf("expected allowed with default_policy=allow, got denied: %s", d.Reason)
	}
	if d.Role != "" {
		t.Errorf("expected empty role, got %q", d.Role)
	}
}

func TestRBAC_DefaultPolicyDeny(t *testing.T) {
	cfg := RBACConfig{
		Enabled:       true,
		DefaultPolicy: "deny",
		Roles:         map[string]RBACRole{},
		Bindings:      nil,
	}

	e := NewRBACEnforcer(cfg, slog.Default())
	d := e.Check("unknown-agent", "", "git", "git_status")
	if d.Allowed {
		t.Errorf("expected denied with default_policy=deny, got allowed: %s", d.Reason)
	}
}

func TestRBAC_EmptyAgentID(t *testing.T) {
	cfg := RBACConfig{
		Enabled:       true,
		DefaultPolicy: "deny",
		Roles: map[string]RBACRole{
			"developer": {Allow: []string{"*"}},
		},
		Bindings: []RBACBinding{
			{AgentID: "*", Role: "developer"},
		},
	}

	e := NewRBACEnforcer(cfg, slog.Default())
	// Empty agent_id still matches wildcard.
	d := e.Check("", "", "git", "git_status")
	if !d.Allowed {
		t.Errorf("expected wildcard to match empty agent_id, got denied: %s", d.Reason)
	}
}

func TestRBAC_UndefinedRole(t *testing.T) {
	cfg := RBACConfig{
		Enabled:       true,
		DefaultPolicy: "allow",
		Roles:         map[string]RBACRole{},
		Bindings: []RBACBinding{
			{AgentID: "test", Role: "nonexistent"},
		},
	}

	e := NewRBACEnforcer(cfg, slog.Default())
	d := e.Check("test", "", "git", "git_status")
	if d.Allowed {
		t.Errorf("expected denied when role is undefined, got allowed: %s", d.Reason)
	}
}

func TestRBAC_DenyWinsOverAllow(t *testing.T) {
	cfg := RBACConfig{
		Enabled:       true,
		DefaultPolicy: "deny",
		Roles: map[string]RBACRole{
			"mixed": {
				Allow: []string{"*"},
				Deny:  []string{"*__dangerous_*"},
			},
		},
		Bindings: []RBACBinding{
			{AgentID: "agent", Role: "mixed"},
		},
	}

	e := NewRBACEnforcer(cfg, slog.Default())

	// Allowed tool
	d := e.Check("agent", "", "server", "safe_tool")
	if !d.Allowed {
		t.Errorf("expected safe_tool allowed: %s", d.Reason)
	}

	// Denied tool (deny wins even though "*" would match)
	d = e.Check("agent", "", "server", "dangerous_action")
	if d.Allowed {
		t.Errorf("expected dangerous_action denied: %s", d.Reason)
	}
}

func TestRBAC_GlobalDenyWinsOverRoleAllow(t *testing.T) {
	cfg := RBACConfig{
		Enabled:       true,
		DefaultPolicy: "allow",
		GlobalDeny: []string{
			"gitlab__delete_*",
			"server_mgmt__*",
		},
		Roles: map[string]RBACRole{
			"admin": {Allow: []string{"*"}},
		},
		Bindings: []RBACBinding{
			{AgentID: "admin-bot", Role: "admin"},
		},
	}

	e := NewRBACEnforcer(cfg, slog.Default())
	if e == nil {
		t.Fatal("expected non-nil enforcer")
	}

	denied := e.Check("admin-bot", "", "gitlab", "delete_branch")
	if denied.Allowed {
		t.Fatalf("expected global deny to block delete tool, got allowed (reason: %s)", denied.Reason)
	}
	if !contains(denied.Reason, "global policy") {
		t.Fatalf("expected global policy reason, got %q", denied.Reason)
	}

	allowed := e.Check("admin-bot", "", "gitlab", "list_issues")
	if !allowed.Allowed {
		t.Fatalf("expected non-denied tool to be allowed, got denied (reason: %s)", allowed.Reason)
	}
}

func TestRBAC_GlobalDenyAppliesWithoutBindings(t *testing.T) {
	cfg := RBACConfig{
		Enabled:       true,
		DefaultPolicy: "allow",
		GlobalDeny:    []string{"github__delete_*"},
	}

	e := NewRBACEnforcer(cfg, slog.Default())
	if e == nil {
		t.Fatal("expected non-nil enforcer")
	}

	denied := e.Check("unknown-agent", "", "github", "delete_repo")
	if denied.Allowed {
		t.Fatalf("expected delete_repo denied by global policy, got allowed")
	}

	allowed := e.Check("unknown-agent", "", "github", "list_repos")
	if !allowed.Allowed {
		t.Fatalf("expected list_repos allowed by default policy, got denied: %s", allowed.Reason)
	}
}

func TestRBAC_MatchesPattern(t *testing.T) {
	tests := []struct {
		pattern string
		tool    string
		want    bool
	}{
		{"*", "anything__here", true},
		{"*__list_*", "gitlab__list_issues", true},
		{"*__list_*", "gitlab__get_issue", false},
		{"*__get_*", "github__get_repo", true},
		{"*__search*", "github__search_repos", true},
		{"*__query*", "prometheus__query_range", true},
		{"k8s_apps_k3s__k8s_apply", "k8s_apps_k3s__k8s_apply", true},
		{"k8s_apps_k3s__k8s_apply", "k8s_apps_k3s__k8s_exec", false},
		{"", "anything", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.tool, func(t *testing.T) {
			got := matchesPattern(tt.pattern, tt.tool)
			if got != tt.want {
				t.Errorf("matchesPattern(%q, %q) = %v, want %v", tt.pattern, tt.tool, got, tt.want)
			}
		})
	}
}

func TestRBAC_ConcurrentAccess(t *testing.T) {
	cfg := RBACConfig{
		Enabled:       true,
		DefaultPolicy: "deny",
		Roles: map[string]RBACRole{
			"admin": {
				Allow: []string{"*"},
			},
			"developer": {
				Allow: []string{"*"},
				Deny:  []string{"k8s_apps_k3s__k8s_apply", "server_mgmt__server_execSafe"},
			},
		},
		Bindings: []RBACBinding{
			{AgentID: "admin-bot", Role: "admin"},
			{AgentID: "*", Role: "developer"},
		},
	}

	e := NewRBACEnforcer(cfg, slog.Default())
	if e == nil {
		t.Fatal("expected non-nil enforcer")
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			agentID := fmt.Sprintf("agent-%d", n%10)
			server := fmt.Sprintf("server-%d", n%5)
			tool := fmt.Sprintf("tool-%d", n%3)
			_ = e.Check(agentID, "", server, tool)
		}(i)
	}
	wg.Wait()
	// Test passes if no panic occurred during concurrent reads.
}

func TestRBAC_WildcardOnlyBindings(t *testing.T) {
	cfg := RBACConfig{
		Enabled:       true,
		DefaultPolicy: "deny",
		Roles: map[string]RBACRole{
			"viewer": {
				Allow: []string{"*__list_*", "*__get_*"},
			},
		},
		Bindings: []RBACBinding{
			{AgentID: "*", Role: "viewer"},
		},
	}

	e := NewRBACEnforcer(cfg, slog.Default())
	if e == nil {
		t.Fatal("expected non-nil enforcer")
	}

	// Any random agent_id should get the viewer role.
	d := e.Check("random-agent-xyz", "", "gitlab", "list_issues")
	if !d.Allowed {
		t.Errorf("expected list tool allowed for wildcard viewer, got denied: %s", d.Reason)
	}
	if d.Role != "viewer" {
		t.Errorf("role: got %q, want %q", d.Role, "viewer")
	}

	d = e.Check("another-agent", "", "github", "get_repo")
	if !d.Allowed {
		t.Errorf("expected get tool allowed for wildcard viewer, got denied: %s", d.Reason)
	}

	// Write tools should be denied.
	d = e.Check("random-agent-xyz", "", "gitlab", "create_issue")
	if d.Allowed {
		t.Errorf("expected write tool denied for viewer role, got allowed")
	}

	d = e.Check("some-agent", "", "k8s_apps_k3s", "k8s_apply")
	if d.Allowed {
		t.Errorf("expected k8s_apply denied for viewer role, got allowed")
	}
}

func TestRBAC_EmptyRolesMap(t *testing.T) {
	cfg := RBACConfig{
		Enabled:       true,
		DefaultPolicy: "deny",
		Roles:         map[string]RBACRole{},
		Bindings: []RBACBinding{
			{AgentID: "test-agent", Role: "ghost-role"},
		},
	}

	e := NewRBACEnforcer(cfg, slog.Default())
	if e == nil {
		t.Fatal("expected non-nil enforcer")
	}

	d := e.Check("test-agent", "", "git", "git_status")
	if d.Allowed {
		t.Error("expected denied when bound role is not defined in empty roles map")
	}
	if d.Reason == "" {
		t.Error("expected non-empty reason for denial")
	}
	// The reason should indicate the role is not defined.
	found := false
	for _, substr := range []string{"not defined", "undefined", "unknown"} {
		if contains(d.Reason, substr) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected reason to mention role not defined, got: %q", d.Reason)
	}
}

// contains checks if s contains substr (case-insensitive-ish, simple check).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
