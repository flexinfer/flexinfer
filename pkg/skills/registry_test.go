package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

func TestSkill_IsEnabled_NoTargets(t *testing.T) {
	s := &Skill{Name: "test", Targets: nil}

	if !s.IsEnabled("codex") {
		t.Error("expected enabled when no targets specified")
	}
	if !s.IsEnabled("claude") {
		t.Error("expected enabled when no targets specified")
	}
}

func TestSkill_IsEnabled_TargetNotSpecified(t *testing.T) {
	s := &Skill{
		Name: "test",
		Targets: map[string]*TargetSpec{
			"codex": {Enabled: boolPtr(true)},
		},
	}

	// "claude" is not in targets, should default to enabled.
	if !s.IsEnabled("claude") {
		t.Error("expected enabled when target not in map")
	}
}

func TestSkill_IsEnabled_ExplicitTrue(t *testing.T) {
	s := &Skill{
		Name: "test",
		Targets: map[string]*TargetSpec{
			"codex": {Enabled: boolPtr(true)},
		},
	}

	if !s.IsEnabled("codex") {
		t.Error("expected enabled when explicitly set to true")
	}
}

func TestSkill_IsEnabled_ExplicitFalse(t *testing.T) {
	s := &Skill{
		Name: "test",
		Targets: map[string]*TargetSpec{
			"codex": {Enabled: boolPtr(false)},
		},
	}

	if s.IsEnabled("codex") {
		t.Error("expected disabled when explicitly set to false")
	}
}

func TestSkill_IsEnabled_NilEnabledField(t *testing.T) {
	s := &Skill{
		Name: "test",
		Targets: map[string]*TargetSpec{
			"codex": {OutputFormat: "markdown"},
		},
	}

	// Enabled is nil, should default to true.
	if !s.IsEnabled("codex") {
		t.Error("expected enabled when Enabled field is nil")
	}
}

func TestSkill_GetOutputFormat_NoTargets(t *testing.T) {
	s := &Skill{Name: "test", Targets: nil}

	if got := s.GetOutputFormat("codex"); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestSkill_GetOutputFormat_TargetNotFound(t *testing.T) {
	s := &Skill{
		Name: "test",
		Targets: map[string]*TargetSpec{
			"codex": {OutputFormat: "markdown"},
		},
	}

	if got := s.GetOutputFormat("claude"); got != "" {
		t.Errorf("expected empty string for missing target, got %q", got)
	}
}

func TestSkill_GetOutputFormat_ReturnsValue(t *testing.T) {
	s := &Skill{
		Name: "test",
		Targets: map[string]*TargetSpec{
			"claude": {OutputFormat: "markdown"},
		},
	}

	if got := s.GetOutputFormat("claude"); got != "markdown" {
		t.Errorf("expected 'markdown', got %q", got)
	}
}

func TestSkill_GetType_PlatformDefaults(t *testing.T) {
	s := &Skill{Name: "test", Targets: nil}

	tests := []struct {
		target string
		want   string
	}{
		{"claude", "command"},
		{"codex", "skill"},
		{"kilocode", "rule"},
		{"gemini", "skill"},
		{"unknown", "skill"},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			if got := s.GetType(tt.target); got != tt.want {
				t.Errorf("GetType(%q) = %q, want %q", tt.target, got, tt.want)
			}
		})
	}
}

func TestSkill_GetType_ExplicitOverride(t *testing.T) {
	s := &Skill{
		Name: "test",
		Targets: map[string]*TargetSpec{
			"claude": {Type: "rule"},
		},
	}

	if got := s.GetType("claude"); got != "rule" {
		t.Errorf("expected 'rule' override, got %q", got)
	}

	// Unspecified target should still use default.
	if got := s.GetType("codex"); got != "skill" {
		t.Errorf("expected default 'skill', got %q", got)
	}
}

func TestSkill_GetType_EmptyTypeUsesDefault(t *testing.T) {
	s := &Skill{
		Name: "test",
		Targets: map[string]*TargetSpec{
			"claude": {Type: ""},
		},
	}

	// Empty type in target spec should fall through to platform default.
	if got := s.GetType("claude"); got != "command" {
		t.Errorf("expected default 'command', got %q", got)
	}
}

func TestSkill_ResolveInstructions_Codex(t *testing.T) {
	s := &Skill{
		Name: "deploy",
		Common: &SkillSpec{
			Instructions: "Run ${SKILL_PATH}/scripts/deploy.sh from ${CODEX_HOME}",
		},
	}

	got := s.ResolveInstructions("codex", "/home/user/.codex", "/src/skills/deploy")

	if got != "Run $CODEX_HOME/skills/deploy/scripts/deploy.sh from $CODEX_HOME" {
		t.Errorf("unexpected codex resolution:\n%s", got)
	}
}

func TestSkill_ResolveInstructions_Claude(t *testing.T) {
	s := &Skill{
		Name: "deploy",
		Common: &SkillSpec{
			Instructions: "Run ${SKILL_PATH}/scripts/deploy.sh",
		},
	}

	got := s.ResolveInstructions("claude", "/home/.codex", "/workspace/skills/deploy")

	if got != "Run /workspace/skills/deploy/scripts/deploy.sh" {
		t.Errorf("unexpected claude resolution:\n%s", got)
	}
}

func TestSkill_ResolveInstructions_Gemini(t *testing.T) {
	s := &Skill{
		Name: "lint",
		Common: &SkillSpec{
			Instructions: "Execute ${SKILL_PATH}/run.sh",
		},
	}

	got := s.ResolveInstructions("gemini", "", "/skills/lint")

	if got != "Execute /skills/lint/run.sh" {
		t.Errorf("unexpected gemini resolution:\n%s", got)
	}
}

func TestSkill_ResolveInstructions_NoVariables(t *testing.T) {
	s := &Skill{
		Name: "simple",
		Common: &SkillSpec{
			Instructions: "Just do the thing, no variables here.",
		},
	}

	got := s.ResolveInstructions("claude", "", "")

	if got != "Just do the thing, no variables here." {
		t.Errorf("expected unchanged instructions, got:\n%s", got)
	}
}

func TestSkill_ResolveInstructions_WithTargetAppend(t *testing.T) {
	s := &Skill{
		Name: "deploy",
		Common: &SkillSpec{
			Instructions: "Run ${SKILL_PATH}/scripts/deploy.sh",
		},
		Targets: map[string]*TargetSpec{
			"codex": {
				InstructionsAppend: "Then verify from ${SKILL_PATH}/references/checklist.md",
			},
		},
	}

	got := s.ResolveInstructions("codex", "/home/.codex", "/workspace/skills/deploy")
	want := "Run $CODEX_HOME/skills/deploy/scripts/deploy.sh\n\nThen verify from $CODEX_HOME/skills/deploy/references/checklist.md"

	if got != want {
		t.Errorf("unexpected appended instructions:\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestSkill_ResolveInstructions_EscapedVariablesPreserved(t *testing.T) {
	s := &Skill{
		Name: "doc-skill",
		Common: &SkillSpec{
			Instructions: `Use \${SKILL_PATH} to reference the skill directory.
Actual path: ${SKILL_PATH}/scripts/run.sh
Literal codex home: \${CODEX_HOME}`,
		},
	}

	got := s.ResolveInstructions("claude", "/home/.codex", "/workspace/skills/doc-skill")

	// Escaped references should become literal ${...} in output.
	if !strings.Contains(got, "Use ${SKILL_PATH} to reference") {
		t.Errorf("escaped \\${SKILL_PATH} should become literal ${SKILL_PATH}, got:\n%s", got)
	}
	// Unescaped references should be substituted normally.
	if !strings.Contains(got, "Actual path: /workspace/skills/doc-skill/scripts/run.sh") {
		t.Errorf("unescaped ${SKILL_PATH} should be resolved, got:\n%s", got)
	}
	// Escaped ${CODEX_HOME} should become literal.
	if !strings.Contains(got, "Literal codex home: ${CODEX_HOME}") {
		t.Errorf("escaped \\${CODEX_HOME} should become literal, got:\n%s", got)
	}
}

func TestSkill_ResolveInstructions_EscapedCodex(t *testing.T) {
	s := &Skill{
		Name: "doc-skill",
		Common: &SkillSpec{
			Instructions: `Real: ${CODEX_HOME}/config  Literal: \${CODEX_HOME}/config`,
		},
	}

	got := s.ResolveInstructions("codex", "/home/.codex", "/src/skills/doc-skill")

	if !strings.Contains(got, "Real: $CODEX_HOME/config") {
		t.Errorf("unescaped ${CODEX_HOME} should resolve for codex, got:\n%s", got)
	}
	if !strings.Contains(got, "Literal: ${CODEX_HOME}/config") {
		t.Errorf("escaped \\${CODEX_HOME} should become literal, got:\n%s", got)
	}
}

func TestSkill_ResolveInstructions_NoEscapesUnchanged(t *testing.T) {
	s := &Skill{
		Name: "plain",
		Common: &SkillSpec{
			Instructions: "No template variables here at all.",
		},
	}

	got := s.ResolveInstructions("claude", "", "")
	if got != "No template variables here at all." {
		t.Errorf("expected unchanged, got:\n%s", got)
	}
}

func TestFindSkillsSourceDir(t *testing.T) {
	// Given a registry at mcp/context/skills-registry.yaml,
	// skills should be at mcp/skills/.
	regPath := "/workspace/platform/gitops/mcp/context/skills-registry.yaml"
	got := FindSkillsSourceDir(regPath)
	want := "/workspace/platform/gitops/mcp/skills"

	if got != want {
		t.Errorf("FindSkillsSourceDir(%q) = %q, want %q", regPath, got, want)
	}
}

func TestFindSkillsSourceDir_RelativePath(t *testing.T) {
	regPath := "mcp/context/skills-registry.yaml"
	got := FindSkillsSourceDir(regPath)
	want := filepath.Join("mcp", "skills")

	if got != want {
		t.Errorf("FindSkillsSourceDir(%q) = %q, want %q", regPath, got, want)
	}
}

func TestLoad_ValidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	registryPath := filepath.Join(tmpDir, "skills-registry.yaml")

	content := `version: 1
updated: "2026-02-16"
skills:
  - name: testing
    categories: [dev]
    common:
      description: "Test skill"
      instructions: "# Testing\n\nRun tests."
    targets:
      claude:
        enabled: true
        type: command
      codex:
        enabled: false
        instructions_append: "Codex-only append"
`
	if err := os.WriteFile(registryPath, []byte(content), 0644); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	reg, err := Load(registryPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if reg.Version != 1 {
		t.Errorf("expected version 1, got %d", reg.Version)
	}
	if len(reg.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(reg.Skills))
	}
	if reg.Skills[0].Name != "testing" {
		t.Errorf("expected name 'testing', got %q", reg.Skills[0].Name)
	}
	if !reg.Skills[0].IsEnabled("claude") {
		t.Error("expected testing enabled for claude")
	}
	if reg.Skills[0].IsEnabled("codex") {
		t.Error("expected testing disabled for codex")
	}
	if got := reg.Skills[0].Targets["codex"].InstructionsAppend; got != "Codex-only append" {
		t.Errorf("expected instructions_append to parse, got %q", got)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/skills-registry.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	registryPath := filepath.Join(tmpDir, "bad.yaml")

	// Use YAML with a mapping value where a sequence is expected to provoke unmarshal error.
	if err := os.WriteFile(registryPath, []byte("version: 1\nskills:\n  - name: [[[invalid"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := Load(registryPath)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}
