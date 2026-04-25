package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findWorkspaceRegistryPath walks up from the test working directory to find
// the canonical mcp/context/skills-registry.yaml. Tests in this package run
// under the package directory, so we walk parents until we hit the repo root
// (the directory that contains an mcp/context tree). Returns the path and
// true on success.
func findWorkspaceRegistryPath() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	dir := cwd
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "mcp", "context", "skills-registry.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

// loadWorkspaceRegistry parses the registry located by findWorkspaceRegistryPath.
// Skips the test (rather than failing) if the registry cannot be located, so
// out-of-tree consumers vendoring this package don't get false negatives.
func loadWorkspaceRegistry(t *testing.T) *Registry {
	t.Helper()
	path, ok := findWorkspaceRegistryPath()
	if !ok {
		cwd, _ := os.Getwd()
		t.Skipf("skills registry not found walking up from %s", cwd)
	}
	reg, err := Load(path)
	if err != nil {
		t.Fatalf("load registry at %s: %v", path, err)
	}
	return reg
}

func findSkill(reg *Registry, name string) *Skill {
	for _, s := range reg.Skills {
		if s != nil && s.Name == name {
			return s
		}
	}
	return nil
}

// TestRegistry_SessionRetro_Present verifies the session-retro skill is
// declared in the canonical workspace registry. This is a parity test:
// if it disappears, the postSessionEnd_retrospective hook stops generating
// useful instructions for any platform.
func TestRegistry_SessionRetro_Present(t *testing.T) {
	reg := loadWorkspaceRegistry(t)
	skill := findSkill(reg, "session-retro")
	if skill == nil {
		t.Fatal("session-retro skill missing from registry; expected entry under skills:")
	}
	if skill.Common == nil {
		t.Fatal("session-retro.common is nil; expected description+instructions block")
	}
	if strings.TrimSpace(skill.Common.Description) == "" {
		t.Error("session-retro.common.description is empty")
	}
	if strings.TrimSpace(skill.Common.Instructions) == "" {
		t.Error("session-retro.common.instructions is empty")
	}
}

// TestRegistry_SessionRetro_TargetsCoverPlatforms enforces the cross-platform
// surface for session-retro. The hook + skill is only useful if the major
// agent platforms (Claude, Codex, Gemini, Kilocode) all have it enabled —
// otherwise loom sync skips generation for the missing platforms.
func TestRegistry_SessionRetro_TargetsCoverPlatforms(t *testing.T) {
	reg := loadWorkspaceRegistry(t)
	skill := findSkill(reg, "session-retro")
	if skill == nil {
		t.Skip("session-retro absent (covered by TestRegistry_SessionRetro_Present)")
	}

	required := []string{"claude", "codex", "gemini", "kilocode"}
	for _, target := range required {
		if !skill.IsEnabled(target) {
			t.Errorf("session-retro must be enabled for target %q", target)
		}
	}
}

// TestRegistry_SessionRetro_DeclaresScript pins the script reference so the
// hook command (which calls scripts/session-retro.sh by hard-coded path) cannot
// silently desync from the registry.
func TestRegistry_SessionRetro_DeclaresScript(t *testing.T) {
	reg := loadWorkspaceRegistry(t)
	skill := findSkill(reg, "session-retro")
	if skill == nil || skill.Common == nil {
		t.Skip("session-retro absent (covered by TestRegistry_SessionRetro_Present)")
	}

	var found bool
	for _, s := range skill.Common.Scripts {
		if s == nil {
			continue
		}
		if strings.Contains(s.Path, "session-retro.sh") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("session-retro.common.scripts must declare session-retro.sh; got %+v", skill.Common.Scripts)
	}
}

// TestRegistry_SessionRetro_SkillMDPresent ensures the in-tree SKILL.md
// stays alongside the scripts. The published bundles are generated, but
// the source SKILL.md serves as in-tree documentation.
func TestRegistry_SessionRetro_SkillMDPresent(t *testing.T) {
	// Use the same walk logic as the registry to ensure we land in the
	// active worktree, not whatever path FindRegistry picks (which can
	// resolve to ~/workspace/services/loom-core when running from a
	// .claude/worktrees/... checkout).
	path, ok := findWorkspaceRegistryPath()
	if !ok {
		t.Skip("workspace registry not found; cannot resolve source dir")
	}
	srcDir := FindSkillsSourceDir(path)
	skillMD := filepath.Join(srcDir, "session-retro", "SKILL.md")
	if _, err := os.Stat(skillMD); err != nil {
		t.Errorf("expected SKILL.md at %s: %v", skillMD, err)
	}
}
