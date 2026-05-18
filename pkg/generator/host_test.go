package generator

import (
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/registry"
)

// regWithHostOverrides returns a registry whose claude + codex platform
// permissions carry a code-server host override.
func regWithHostOverrides() *registry.Registry {
	return &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"claude": {
				AdditionalDirectories: []string{"~/workspace"},
				Settings: map[string]any{
					"default_mode": "acceptEdits",
					"host_overrides": map[string]any{
						"code-server": map[string]any{
							"additional_directories": []any{"/home/coder"},
						},
					},
				},
			},
			"codex": {
				Settings: map[string]any{
					"approval_policy": "never",
					"sandbox_mode":    "workspace-write",
					"host_overrides": map[string]any{
						"code-server": map[string]any{
							"sandbox_mode": "danger-full-access",
						},
					},
				},
			},
		},
	}
}

func TestHostOverride_NoEnv_ReturnsNil(t *testing.T) {
	t.Setenv("LOOM_HOST", "")
	reg := regWithHostOverrides()
	if got := hostOverride(reg.PlatformPermissions["claude"]); got != nil {
		t.Fatalf("hostOverride() with empty LOOM_HOST = %v, want nil", got)
	}
}

func TestHostOverride_UnknownHost_ReturnsNil(t *testing.T) {
	t.Setenv("LOOM_HOST", "ghost")
	reg := regWithHostOverrides()
	if got := hostOverride(reg.PlatformPermissions["claude"]); got != nil {
		t.Fatalf("hostOverride() with unknown host = %v, want nil", got)
	}
}

func TestHostOverride_MatchedHost_ReturnsBlock(t *testing.T) {
	t.Setenv("LOOM_HOST", "code-server")
	reg := regWithHostOverrides()
	got := hostOverride(reg.PlatformPermissions["claude"])
	if got == nil {
		t.Fatal("hostOverride() returned nil for matched host")
	}
	dirs := hostOverrideStringSlice(got, "additional_directories")
	if len(dirs) != 1 || dirs[0] != "/home/coder" {
		t.Fatalf("additional_directories = %v, want [/home/coder]", dirs)
	}
}

func TestClaudePermissions_HostOverrideReplacesAdditionalDirectories(t *testing.T) {
	t.Setenv("LOOM_HOST", "code-server")
	reg := regWithHostOverrides()
	perms := claudePermissions(reg)
	got, ok := perms["additionalDirectories"].([]string)
	if !ok {
		t.Fatalf("additionalDirectories type %T, want []string", perms["additionalDirectories"])
	}
	if len(got) != 1 || got[0] != "/home/coder" {
		t.Fatalf("additionalDirectories = %v, want [/home/coder]", got)
	}
}

func TestClaudePermissions_NoHost_KeepsBase(t *testing.T) {
	t.Setenv("LOOM_HOST", "")
	reg := regWithHostOverrides()
	perms := claudePermissions(reg)
	got, ok := perms["additionalDirectories"].([]string)
	if !ok {
		t.Fatalf("additionalDirectories type %T, want []string", perms["additionalDirectories"])
	}
	if len(got) != 1 || got[0] != "~/workspace" {
		t.Fatalf("additionalDirectories = %v, want [~/workspace]", got)
	}
}

func TestCodexContext_HostOverrideFlipsSandboxMode(t *testing.T) {
	t.Setenv("LOOM_HOST", "code-server")
	reg := regWithHostOverrides()
	ctx := buildCodexContext(reg, "/home/coder", "loom")
	if ctx.SandboxMode != "danger-full-access" {
		t.Fatalf("SandboxMode = %q, want danger-full-access", ctx.SandboxMode)
	}
}

func TestCodexContext_NoHost_KeepsBaseSandbox(t *testing.T) {
	t.Setenv("LOOM_HOST", "")
	reg := regWithHostOverrides()
	ctx := buildCodexContext(reg, "/home/coder", "loom")
	if ctx.SandboxMode != "workspace-write" {
		t.Fatalf("SandboxMode = %q, want workspace-write", ctx.SandboxMode)
	}
}

func TestCodexContext_HostOverrideReplacesWritableRoots(t *testing.T) {
	t.Setenv("LOOM_HOST", "code-server")
	reg := &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"codex": {
				Settings: map[string]any{
					"sandbox_mode": "workspace-write",
					"host_overrides": map[string]any{
						"code-server": map[string]any{
							"writable_roots": []any{"/home/coder"},
						},
					},
				},
			},
		},
	}
	ctx := buildCodexContext(reg, "/Users/macuser/workspace", "loom")
	if ctx.WorkspaceRoot != "/home/coder" {
		t.Fatalf("WorkspaceRoot = %q, want /home/coder", ctx.WorkspaceRoot)
	}
}

func TestCodexTemplate_HostOverrideRendersDangerFullAccess(t *testing.T) {
	t.Setenv("LOOM_HOST", "code-server")
	reg := regWithHostOverrides()
	ctx := buildCodexContext(reg, "/home/coder", "loom")
	rendered, err := renderCodexTemplate(ctx)
	if err != nil {
		t.Fatalf("renderCodexTemplate() error: %v", err)
	}
	if !strings.Contains(rendered, `sandbox_mode = "danger-full-access"`) {
		t.Fatalf("rendered config missing danger-full-access:\n%s", rendered)
	}
}
