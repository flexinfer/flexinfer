package skills

import (
	"strings"
	"testing"
)

// newTestGenerator returns a minimal Generator suitable for unit tests.
func newTestGenerator() *Generator {
	return &Generator{
		SourceDir: "/tmp/skills",
		CodexHome: "/tmp/codex",
	}
}

// newTestSkill creates a Skill with the given name, description, and optional resources.
func newTestSkill(name, description string) *Skill {
	return &Skill{
		Name: name,
		Common: &SkillSpec{
			Description:  description,
			Instructions: "# " + name + "\n\nDo the thing.",
		},
	}
}

func TestGenerateCodexSkillMD_BasicFrontmatter(t *testing.T) {
	g := newTestGenerator()
	skill := newTestSkill("my-skill", "A simple skill")

	got := g.generateCodexSkillMD(skill)

	if !strings.HasPrefix(got, "---\n") {
		t.Error("output should start with YAML frontmatter delimiter")
	}
	if !strings.Contains(got, "name: my-skill\n") {
		t.Error("output should contain name field")
	}
	if !strings.Contains(got, `description: "A simple skill"`) {
		t.Errorf("output should contain quoted description, got:\n%s", got)
	}
	if !strings.Contains(got, "\n---\n") {
		t.Error("output should contain closing frontmatter delimiter")
	}
}

func TestGenerateCodexSkillMD_DescriptionWithDoubleQuotes(t *testing.T) {
	g := newTestGenerator()
	skill := newTestSkill("quoted", `Use the "foo" tool for "bar"`)

	got := g.generateCodexSkillMD(skill)

	// Should produce valid YAML: description: "Use the \"foo\" tool for \"bar\""
	expected := `description: "Use the \"foo\" tool for \"bar\""`
	if !strings.Contains(got, expected) {
		t.Errorf("double quotes should be escaped once.\nwant: %s\ngot:\n%s", expected, got)
	}

	// Must NOT contain double-escaped quotes (the original bug)
	if strings.Contains(got, `\\\"`) {
		t.Error("description contains double-escaped quotes (\\\\\\\")")
	}
}

func TestGenerateCodexSkillMD_DescriptionWithBackslashes(t *testing.T) {
	g := newTestGenerator()
	skill := newTestSkill("backslash", `Path is C:\Users\test`)

	got := g.generateCodexSkillMD(skill)

	expected := `description: "Path is C:\\Users\\test"`
	if !strings.Contains(got, expected) {
		t.Errorf("backslashes should be escaped.\nwant: %s\ngot:\n%s", expected, got)
	}
}

func TestGenerateCodexSkillMD_DescriptionWithColons(t *testing.T) {
	g := newTestGenerator()
	skill := newTestSkill("colons", "key: value pair")

	got := g.generateCodexSkillMD(skill)

	// Colons are safe inside double-quoted YAML strings
	expected := `description: "key: value pair"`
	if !strings.Contains(got, expected) {
		t.Errorf("colons should be preserved in quoted string.\nwant: %s\ngot:\n%s", expected, got)
	}
}

func TestGenerateCodexSkillMD_MultilineDescriptionCollapsed(t *testing.T) {
	g := newTestGenerator()
	skill := newTestSkill("multiline", "Line one.\nLine two.\nLine three.")

	got := g.generateCodexSkillMD(skill)

	// Newlines should be replaced with spaces
	if strings.Contains(got, "Line one.\n") {
		t.Error("multi-line description should be collapsed to single line")
	}
	expected := `description: "Line one. Line two. Line three."`
	if !strings.Contains(got, expected) {
		t.Errorf("multi-line description not collapsed correctly.\nwant: %s\ngot:\n%s", expected, got)
	}
}

func TestGenerateCodexSkillMD_EmptyBundledResourcesOmitted(t *testing.T) {
	g := newTestGenerator()
	skill := newTestSkill("no-resources", "Skill without resources")

	got := g.generateCodexSkillMD(skill)

	if strings.Contains(got, "## Bundled Resources") {
		t.Error("Bundled Resources section should be omitted when no resources exist")
	}
}

func TestGenerateCodexSkillMD_BundledResourcesIncluded(t *testing.T) {
	g := newTestGenerator()
	skill := newTestSkill("with-resources", "Skill with resources")
	skill.Common.Scripts = []*Script{
		{Name: "run", Path: "scripts/run.sh", Description: "Run the thing"},
	}
	skill.Common.References = []string{"guide.md"}
	skill.Common.Assets = []string{"templates/default.yaml"}

	got := g.generateCodexSkillMD(skill)

	if !strings.Contains(got, "## Bundled Resources") {
		t.Error("Bundled Resources section should be present when resources exist")
	}
	if !strings.Contains(got, "- `scripts/run.sh`") {
		t.Error("should list script path")
	}
	if !strings.Contains(got, "- `references/guide.md`") {
		t.Error("should list reference path")
	}
	if !strings.Contains(got, "- `assets/templates/default.yaml`") {
		t.Error("should list asset path")
	}
}

func TestGenerateCodexSkillMD_OnlyScriptsShowsResources(t *testing.T) {
	g := newTestGenerator()
	skill := newTestSkill("scripts-only", "Just scripts")
	skill.Common.Scripts = []*Script{
		{Name: "check", Path: "scripts/check.sh"},
	}

	got := g.generateCodexSkillMD(skill)

	if !strings.Contains(got, "## Bundled Resources") {
		t.Error("Bundled Resources should appear when only scripts exist")
	}
}

func TestGenerateCodexSkillMD_IncludesTargetInstructionsAppend(t *testing.T) {
	g := newTestGenerator()
	skill := newTestSkill("append-skill", "Skill with target append")
	skill.Targets = map[string]*TargetSpec{
		"codex": {
			InstructionsAppend: "## Codex Addendum\n\nUse ${SKILL_PATH}/assets/extra.md",
		},
	}

	got := g.generateCodexSkillMD(skill)
	if !strings.Contains(got, "## Codex Addendum") {
		t.Fatalf("expected codex append block in generated skill:\n%s", got)
	}
	if !strings.Contains(got, "$CODEX_HOME/skills/append-skill/assets/extra.md") {
		t.Fatalf("expected path substitutions in append block:\n%s", got)
	}
}

func TestEscapeYAMLString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "hello", "hello"},
		{"double quotes", `say "hi"`, `say \"hi\"`},
		{"backslash", `a\b`, `a\\b`},
		{"both", `a "b\" c`, `a \"b\\\" c`},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := escapeYAMLString(tt.input)
			if got != tt.want {
				t.Errorf("escapeYAMLString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
