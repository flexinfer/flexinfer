package skills

import (
	"os"
	"path/filepath"
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

func TestCodexManifestFiles_IncludeBundledResources(t *testing.T) {
	g := newTestGenerator()
	skill := newTestSkill("with-resources", "Skill with resources")
	skill.Common.Scripts = []*Script{
		{Name: "run", Path: "scripts/run.sh"},
	}
	skill.Common.References = []string{"guide.md"}
	skill.Common.Assets = []string{"templates/default.yaml"}

	got := g.codexManifestFiles(skill)

	want := []string{
		filepath.Join("skills", "with-resources", "SKILL.md"),
		filepath.Join("skills", "with-resources", "scripts", "run.sh"),
		filepath.Join("skills", "with-resources", "references", "guide.md"),
		filepath.Join("skills", "with-resources", "assets", "templates", "default.yaml"),
	}

	if len(got) != len(want) {
		t.Fatalf("expected %d manifest files, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("manifest file %d mismatch: want %q got %q", i, want[i], got[i])
		}
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

func TestGenerateForTarget_GeminiInstructionCreatesSkillBundle(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "mcp", "skills")
	sourceSkillDir := filepath.Join(sourceDir, "demo-skill")

	if err := os.MkdirAll(filepath.Join(sourceSkillDir, "scripts"), 0755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sourceSkillDir, "references"), 0755); err != nil {
		t.Fatalf("mkdir references: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sourceSkillDir, "assets", "templates"), 0755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceSkillDir, "scripts", "run.sh"), []byte("#!/usr/bin/env bash\necho ok\n"), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceSkillDir, "references", "guide.md"), []byte("# Guide\n"), 0644); err != nil {
		t.Fatalf("write reference: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceSkillDir, "assets", "templates", "default.yaml"), []byte("name: demo\n"), 0644); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	enabled := true
	skill := &Skill{
		Name: "demo-skill",
		Common: &SkillSpec{
			Description:  "Gemini demo skill",
			Instructions: "# Demo Skill\n\nRun ${SKILL_PATH}/scripts/run.sh",
			Scripts: []*Script{
				{Name: "run", Path: "scripts/run.sh"},
			},
			References: []string{"guide.md"},
			Assets:     []string{"templates/default.yaml"},
		},
		Targets: map[string]*TargetSpec{
			"gemini": {Enabled: &enabled, Type: "instruction"},
		},
	}

	g := &Generator{
		Registry:  &Registry{Skills: []*Skill{skill}},
		SourceDir: sourceDir,
		Target:    "gemini",
		RepoRoot:  tmpDir,
		CodexHome: "/tmp/codex",
	}

	if err := g.generateForTarget("gemini"); err != nil {
		t.Fatalf("generateForTarget(gemini): %v", err)
	}

	geminiDir := filepath.Join(tmpDir, ".gemini")
	skillMDPath := filepath.Join(geminiDir, "skills", "demo-skill", "SKILL.md")
	if _, err := os.Stat(skillMDPath); err != nil {
		t.Fatalf("expected SKILL.md to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(geminiDir, "skills", "demo-skill", "scripts", "run.sh")); err != nil {
		t.Fatalf("expected script to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(geminiDir, "skills", "demo-skill", "references", "guide.md")); err != nil {
		t.Fatalf("expected reference to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(geminiDir, "skills", "demo-skill", "assets", "templates", "default.yaml")); err != nil {
		t.Fatalf("expected asset to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(geminiDir, "instructions.md")); err != nil {
		t.Fatalf("expected instructions.md to exist: %v", err)
	}

	skillMD, err := os.ReadFile(skillMDPath)
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	skillMDText := string(skillMD)
	if !strings.Contains(skillMDText, "name: demo-skill") {
		t.Fatalf("expected skill frontmatter name, got:\n%s", skillMDText)
	}
	if !strings.Contains(skillMDText, "$HOME/.gemini/skills/demo-skill/scripts/run.sh") {
		t.Fatalf("expected stable gemini skill path substitution, got:\n%s", skillMDText)
	}

	manifest, err := ReadManifest(geminiDir)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest == nil {
		t.Fatal("expected manifest to be written")
	}

	wantPaths := []string{
		filepath.Join("skills", "demo-skill", "SKILL.md"),
		filepath.Join("skills", "demo-skill", "scripts", "run.sh"),
		filepath.Join("skills", "demo-skill", "references", "guide.md"),
		filepath.Join("skills", "demo-skill", "assets", "templates", "default.yaml"),
		"instructions.md",
	}
	for _, want := range wantPaths {
		if !containsString(manifest.Generated, want) {
			t.Fatalf("expected manifest to include %q, got %#v", want, manifest.Generated)
		}
	}
}

func TestGenerateForTarget_GeminiNonInstructionCreatesSkillWithoutCompositeInstructions(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "mcp", "skills")
	sourceSkillDir := filepath.Join(sourceDir, "ops-helper")
	if err := os.MkdirAll(sourceSkillDir, 0755); err != nil {
		t.Fatalf("mkdir source skill dir: %v", err)
	}

	enabled := true
	skill := &Skill{
		Name: "ops-helper",
		Common: &SkillSpec{
			Description:  "Ops helper skill",
			Instructions: "# Ops Helper\n\nDo ops work.",
		},
		Targets: map[string]*TargetSpec{
			"gemini": {Enabled: &enabled, Type: "command"},
		},
	}

	g := &Generator{
		Registry:  &Registry{Skills: []*Skill{skill}},
		SourceDir: sourceDir,
		Target:    "gemini",
		RepoRoot:  tmpDir,
		CodexHome: "/tmp/codex",
	}

	if err := g.generateForTarget("gemini"); err != nil {
		t.Fatalf("generateForTarget(gemini): %v", err)
	}

	geminiDir := filepath.Join(tmpDir, ".gemini")
	if _, err := os.Stat(filepath.Join(geminiDir, "skills", "ops-helper", "SKILL.md")); err != nil {
		t.Fatalf("expected non-instruction Gemini skill to be generated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(geminiDir, "instructions.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no instructions.md for non-instruction-only generation, err=%v", err)
	}

	manifest, err := ReadManifest(geminiDir)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest == nil {
		t.Fatal("expected manifest to be written")
	}
	if containsString(manifest.Generated, "instructions.md") {
		t.Fatalf("did not expect instructions.md in manifest, got %#v", manifest.Generated)
	}
	if !containsString(manifest.Generated, filepath.Join("skills", "ops-helper", "SKILL.md")) {
		t.Fatalf("expected skill path in manifest, got %#v", manifest.Generated)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
