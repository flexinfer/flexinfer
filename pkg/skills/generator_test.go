package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if !strings.Contains(got, "metadata:\n  short-description: \"A simple skill\"") {
		t.Errorf("output should contain metadata.short-description, got:\n%s", got)
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

func TestGenerateForTarget_GeminiInstructionCreatesGEMINIMD(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "mcp", "skills")
	sourceSkillDir := filepath.Join(sourceDir, "demo-skill")

	if err := os.MkdirAll(filepath.Join(sourceSkillDir, "scripts"), 0755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sourceSkillDir, "references"), 0755); err != nil {
		t.Fatalf("mkdir references: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceSkillDir, "scripts", "run.sh"), []byte("#!/usr/bin/env bash\necho ok\n"), 0755); err != nil {
		t.Fatalf("write script: %v", err)
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
	if _, err := os.Stat(filepath.Join(geminiDir, "GEMINI.md")); err != nil {
		t.Fatalf("expected GEMINI.md to exist: %v", err)
	}

	// Instructions-only generation should NOT create a bundle
	skillMDPath := filepath.Join(geminiDir, "skills", "demo-skill", "SKILL.md")
	if _, err := os.Stat(skillMDPath); !os.IsNotExist(err) {
		t.Fatalf("did not expect SKILL.md to exist for instruction-type Gemini skill: %v", err)
	}

	manifest, err := ReadManifest(geminiDir)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !containsString(manifest.Generated, "GEMINI.md") {
		t.Fatalf("expected manifest to include GEMINI.md, got %#v", manifest.Generated)
	}
}

func TestGenerateForTarget_GeminiSkillCreatesBundle(t *testing.T) {
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
			"gemini": {Enabled: &enabled, Type: "skill"},
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
		t.Fatalf("expected Gemini skill bundle to be generated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(geminiDir, "GEMINI.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no GEMINI.md for skill-type generation, err=%v", err)
	}

	manifest, err := ReadManifest(geminiDir)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest == nil {
		t.Fatal("expected manifest to be written")
	}
	if containsString(manifest.Generated, "GEMINI.md") {
		t.Fatalf("did not expect GEMINI.md in manifest, got %#v", manifest.Generated)
	}
	if !containsString(manifest.Generated, filepath.Join("skills", "ops-helper", "SKILL.md")) {
		t.Fatalf("expected skill path in manifest, got %#v", manifest.Generated)
	}
}

func TestGenerateForTarget_CodexDirectToHomeWritesRootFilesToCodexHome(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "mcp", "skills")
	sourceSkillDir := filepath.Join(sourceDir, "ops-helper")
	if err := os.MkdirAll(sourceSkillDir, 0o755); err != nil {
		t.Fatalf("mkdir source skill dir: %v", err)
	}

	enabled := true
	instructionSkill := &Skill{
		Name: "ops-helper",
		Common: &SkillSpec{
			Description:  "Ops helper skill",
			Instructions: "# Ops Helper\n\nDo ops work.",
		},
		Targets: map[string]*TargetSpec{
			"codex": {Enabled: &enabled, Type: "instruction"},
		},
	}
	bundleSkill := &Skill{
		Name: "bundle-helper",
		Common: &SkillSpec{
			Description:  "Bundle helper skill",
			Instructions: "# Bundle Helper\n\nUse bundled workflows.",
		},
		Targets: map[string]*TargetSpec{
			"codex": {Enabled: &enabled, Type: "skill"},
		},
	}

	codexRoot := filepath.Join(tmpDir, "home", ".codex")
	g := &Generator{
		Registry:       &Registry{Skills: []*Skill{instructionSkill, bundleSkill}},
		SourceDir:      sourceDir,
		Target:         "codex",
		RepoRoot:       tmpDir,
		CodexHome:      codexRoot,
		CodexRootDir:   codexRoot,
		CodexSkillsDir: filepath.Join(codexRoot, "skills"),
	}

	if err := g.generateForTarget("codex"); err != nil {
		t.Fatalf("generateForTarget(codex): %v", err)
	}

	if _, err := os.Stat(filepath.Join(codexRoot, "instructions.md")); err != nil {
		t.Fatalf("expected instructions.md in codex home root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, ".codex", "instructions.md")); !os.IsNotExist(err) {
		t.Fatalf("did not expect instructions.md in repo .codex, err=%v", err)
	}

	manifest, err := ReadManifest(codexRoot)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest == nil {
		t.Fatal("expected manifest to be written in codex home root")
	}
	if !containsString(manifest.Generated, "instructions.md") {
		t.Fatalf("expected manifest to include instructions.md, got %#v", manifest.Generated)
	}

	data, err := os.ReadFile(filepath.Join(codexRoot, "instructions.md"))
	if err != nil {
		t.Fatalf("read instructions.md: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "### Available skills") {
		t.Fatalf("expected available skills section in instructions.md:\n%s", text)
	}
	if !strings.Contains(text, filepath.Join(codexRoot, "skills", "bundle-helper", "SKILL.md")) {
		t.Fatalf("expected generated skill path in instructions.md:\n%s", text)
	}
}

func TestGenerateForTarget_CodexAvailableSkillsIncludesExistingHomeSkills(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "mcp", "skills")
	sourceSkillDir := filepath.Join(sourceDir, "ops-helper")
	if err := os.MkdirAll(sourceSkillDir, 0o755); err != nil {
		t.Fatalf("mkdir source skill dir: %v", err)
	}

	enabled := true
	instructionSkill := &Skill{
		Name: "ops-helper",
		Common: &SkillSpec{
			Description:  "Ops helper skill",
			Instructions: "# Ops Helper\n\nDo ops work.",
		},
		Targets: map[string]*TargetSpec{
			"codex": {Enabled: &enabled, Type: "instruction"},
		},
	}
	bundleSkill := &Skill{
		Name: "bundle-helper",
		Common: &SkillSpec{
			Description:  "Bundle helper skill",
			Instructions: "# Bundle Helper\n\nUse bundled workflows.",
		},
		Targets: map[string]*TargetSpec{
			"codex": {Enabled: &enabled, Type: "skill"},
		},
	}

	codexRoot := filepath.Join(tmpDir, "home", ".codex")
	existingSkills := map[string]string{
		filepath.Join(codexRoot, "skills", ".system", "openai-docs", "SKILL.md"): `---
name: "openai-docs"
description: "Use current OpenAI docs with citations."
---

# OpenAI Docs
`,
		filepath.Join(codexRoot, "skills", "speech", "SKILL.md"): `---
name: "speech"
description: "Generate text-to-speech output."
---

# Speech
`,
	}
	for path, content := range existingSkills {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir existing skill dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write existing skill: %v", err)
		}
	}

	g := &Generator{
		Registry:       &Registry{Skills: []*Skill{instructionSkill, bundleSkill}},
		SourceDir:      sourceDir,
		Target:         "codex",
		RepoRoot:       tmpDir,
		CodexHome:      codexRoot,
		CodexRootDir:   codexRoot,
		CodexSkillsDir: filepath.Join(codexRoot, "skills"),
	}

	if err := g.generateForTarget("codex"); err != nil {
		t.Fatalf("generateForTarget(codex): %v", err)
	}

	data, err := os.ReadFile(filepath.Join(codexRoot, "instructions.md"))
	if err != nil {
		t.Fatalf("read instructions.md: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"- openai-docs: Use current OpenAI docs with citations.",
		"- speech: Generate text-to-speech output.",
		filepath.Join(codexRoot, "skills", ".system", "openai-docs", "SKILL.md"),
		filepath.Join(codexRoot, "skills", "speech", "SKILL.md"),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected instructions.md to contain %q:\n%s", want, text)
		}
	}
}

func TestGenerateGeminiSkillMD_UsesConfiguredSkillsHomePath(t *testing.T) {
	g := &Generator{
		CodexHome:        "/tmp/codex",
		GeminiSkillsHome: "$HOME/.gemini/antigravity/skills",
	}

	skill := &Skill{
		Name: "ops-helper",
		Common: &SkillSpec{
			Description:  "Ops helper skill",
			Instructions: "Run ${SKILL_PATH}/scripts/run.sh",
		},
	}

	got := g.generateGeminiSkillMD(skill)
	if !strings.Contains(got, "$HOME/.gemini/antigravity/skills/ops-helper/scripts/run.sh") {
		t.Fatalf("expected antigravity skill path in generated output:\n%s", got)
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

// =========================================================================
// Validate tests
// =========================================================================

func TestValidate_AllResourcesExist(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "mcp", "skills")
	skillDir := filepath.Join(sourceDir, "my-skill")

	// Create all referenced files.
	for _, p := range []string{
		filepath.Join(skillDir, "scripts", "run.sh"),
		filepath.Join(skillDir, "references", "guide.md"),
		filepath.Join(skillDir, "assets", "templates", "default.yaml"),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("ok"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	skill := newTestSkill("my-skill", "A skill")
	skill.Common.Scripts = []*Script{{Name: "run", Path: "scripts/run.sh"}}
	skill.Common.References = []string{"guide.md"}
	skill.Common.Assets = []string{"templates/default.yaml"}

	g := &Generator{
		Registry:  &Registry{Skills: []*Skill{skill}},
		SourceDir: sourceDir,
		Target:    "all",
		CodexHome: "/tmp/codex",
	}

	errs := g.Validate()
	if len(errs) != 0 {
		t.Fatalf("expected 0 validation errors, got %d: %v", len(errs), errs)
	}
}

func TestValidate_MissingScript(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "mcp", "skills")
	skillDir := filepath.Join(sourceDir, "broken-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}

	skill := newTestSkill("broken-skill", "Missing script")
	skill.Common.Scripts = []*Script{{Name: "run", Path: "scripts/run.sh"}}

	g := &Generator{
		Registry:  &Registry{Skills: []*Skill{skill}},
		SourceDir: sourceDir,
		Target:    "all",
		CodexHome: "/tmp/codex",
	}

	errs := g.Validate()
	if len(errs) != 1 {
		t.Fatalf("expected 1 validation error, got %d: %v", len(errs), errs)
	}
	if errs[0].ResourceType != "script" {
		t.Errorf("expected resource type 'script', got %q", errs[0].ResourceType)
	}
	if errs[0].Skill != "broken-skill" {
		t.Errorf("expected skill 'broken-skill', got %q", errs[0].Skill)
	}
}

func TestValidate_MissingReference(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "mcp", "skills")
	skillDir := filepath.Join(sourceDir, "ref-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}

	skill := newTestSkill("ref-skill", "Missing ref")
	skill.Common.References = []string{"missing.md"}

	g := &Generator{
		Registry:  &Registry{Skills: []*Skill{skill}},
		SourceDir: sourceDir,
		Target:    "all",
		CodexHome: "/tmp/codex",
	}

	errs := g.Validate()
	if len(errs) != 1 {
		t.Fatalf("expected 1 validation error, got %d: %v", len(errs), errs)
	}
	if errs[0].ResourceType != "reference" {
		t.Errorf("expected resource type 'reference', got %q", errs[0].ResourceType)
	}
}

func TestValidate_MissingAsset(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "mcp", "skills")
	skillDir := filepath.Join(sourceDir, "asset-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}

	skill := newTestSkill("asset-skill", "Missing asset")
	skill.Common.Assets = []string{"templates/missing.yaml"}

	g := &Generator{
		Registry:  &Registry{Skills: []*Skill{skill}},
		SourceDir: sourceDir,
		Target:    "all",
		CodexHome: "/tmp/codex",
	}

	errs := g.Validate()
	if len(errs) != 1 {
		t.Fatalf("expected 1 validation error, got %d: %v", len(errs), errs)
	}
	if errs[0].ResourceType != "asset" {
		t.Errorf("expected resource type 'asset', got %q", errs[0].ResourceType)
	}
}

func TestValidate_MultipleErrors(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "mcp", "skills")
	if err := os.MkdirAll(filepath.Join(sourceDir, "multi-err"), 0755); err != nil {
		t.Fatal(err)
	}

	skill := newTestSkill("multi-err", "Multiple missing resources")
	skill.Common.Scripts = []*Script{
		{Name: "a", Path: "scripts/a.sh"},
		{Name: "b", Path: "scripts/b.sh"},
	}
	skill.Common.References = []string{"guide.md"}

	g := &Generator{
		Registry:  &Registry{Skills: []*Skill{skill}},
		SourceDir: sourceDir,
		Target:    "all",
		CodexHome: "/tmp/codex",
	}

	errs := g.Validate()
	if len(errs) != 3 {
		t.Fatalf("expected 3 validation errors, got %d: %v", len(errs), errs)
	}
}

func TestValidate_DisabledSkillSkipped(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "mcp", "skills")
	if err := os.MkdirAll(filepath.Join(sourceDir, "disabled"), 0755); err != nil {
		t.Fatal(err)
	}

	disabled := false
	skill := newTestSkill("disabled", "Disabled skill")
	skill.Common.Scripts = []*Script{{Name: "run", Path: "scripts/run.sh"}}
	skill.Targets = map[string]*TargetSpec{
		"codex":    {Enabled: &disabled},
		"claude":   {Enabled: &disabled},
		"kilocode": {Enabled: &disabled},
		"gemini":   {Enabled: &disabled},
	}

	g := &Generator{
		Registry:  &Registry{Skills: []*Skill{skill}},
		SourceDir: sourceDir,
		Target:    "all",
		CodexHome: "/tmp/codex",
	}

	errs := g.Validate()
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors for disabled skill, got %d: %v", len(errs), errs)
	}
}

func TestValidate_NilScriptSkipped(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "mcp", "skills")
	if err := os.MkdirAll(filepath.Join(sourceDir, "nil-script"), 0755); err != nil {
		t.Fatal(err)
	}

	skill := newTestSkill("nil-script", "Skill with nil script entry")
	skill.Common.Scripts = []*Script{nil, {Name: "empty", Path: ""}}

	g := &Generator{
		Registry:  &Registry{Skills: []*Skill{skill}},
		SourceDir: sourceDir,
		Target:    "all",
		CodexHome: "/tmp/codex",
	}

	errs := g.Validate()
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors for nil/empty script entries, got %d: %v", len(errs), errs)
	}
}

func TestValidate_AlwaysAllowWriteScriptWithoutDryRunFails(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "mcp", "skills")
	skillDir := filepath.Join(sourceDir, "unsafe-allow")

	scriptPath := filepath.Join(skillDir, "scripts", "mutate.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env bash\nmkdir -p .loom\n"), 0755); err != nil {
		t.Fatal(err)
	}

	skill := newTestSkill("unsafe-allow", "Unsafe always_allow")
	skill.Common.Scripts = []*Script{{Name: "mutate", Path: "scripts/mutate.sh"}}
	skill.Common.AlwaysAllow = []string{"mutate"}

	g := &Generator{
		Registry:  &Registry{Skills: []*Skill{skill}},
		SourceDir: sourceDir,
		Target:    "all",
		CodexHome: "/tmp/codex",
	}

	errs := g.Validate()
	if len(errs) != 1 {
		t.Fatalf("expected 1 validation error, got %d: %v", len(errs), errs)
	}
	if errs[0].ResourceType != "always_allow" {
		t.Fatalf("expected always_allow validation error, got %q", errs[0].ResourceType)
	}
}

func TestValidate_AlwaysAllowWriteScriptWithDryRunAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "mcp", "skills")
	skillDir := filepath.Join(sourceDir, "safe-allow")

	scriptPath := filepath.Join(skillDir, "scripts", "generate.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
		t.Fatal(err)
	}
	content := "#!/usr/bin/env bash\n# defaults to --dry-run unless --apply is passed\nif [[ \"$1\" == \"--apply\" ]]; then echo apply; else echo --dry-run; fi\n"
	if err := os.WriteFile(scriptPath, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	skill := newTestSkill("safe-allow", "Safe always_allow")
	skill.Common.Scripts = []*Script{{Name: "generate", Path: "scripts/generate.sh"}}
	skill.Common.AlwaysAllow = []string{"generate"}

	g := &Generator{
		Registry:  &Registry{Skills: []*Skill{skill}},
		SourceDir: sourceDir,
		Target:    "all",
		CodexHome: "/tmp/codex",
	}

	errs := g.Validate()
	if len(errs) != 0 {
		t.Fatalf("expected no validation errors, got %d: %v", len(errs), errs)
	}
}

func TestValidate_AlwaysAllowScriptMustExistInScripts(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "mcp", "skills")
	skillDir := filepath.Join(sourceDir, "bad-allow")
	scriptPath := filepath.Join(skillDir, "scripts", "read.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env bash\necho read-only\n"), 0755); err != nil {
		t.Fatal(err)
	}

	skill := newTestSkill("bad-allow", "Bad always_allow reference")
	skill.Common.Scripts = []*Script{{Name: "read", Path: "scripts/read.sh"}}
	skill.Common.AlwaysAllow = []string{"missing-script-name"}

	g := &Generator{
		Registry:  &Registry{Skills: []*Skill{skill}},
		SourceDir: sourceDir,
		Target:    "all",
		CodexHome: "/tmp/codex",
	}

	errs := g.Validate()
	if len(errs) != 1 {
		t.Fatalf("expected 1 validation error, got %d: %v", len(errs), errs)
	}
	if errs[0].ResourceType != "always_allow" {
		t.Fatalf("expected always_allow validation error, got %q", errs[0].ResourceType)
	}
}

// =========================================================================
// UpdateRegistryDate tests
// =========================================================================

func TestUpdateRegistryDate_UpdatesStaleDate(t *testing.T) {
	tmpDir := t.TempDir()
	registryPath := filepath.Join(tmpDir, "skills-registry.yaml")

	content := "version: 1\nupdated: 2025-01-01\nskills: []\n"
	if err := os.WriteFile(registryPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	g := &Generator{
		Registry:     &Registry{},
		RegistryPath: registryPath,
	}

	if err := g.UpdateRegistryDate(); err != nil {
		t.Fatalf("UpdateRegistryDate: %v", err)
	}

	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}

	today := time.Now().Format("2006-01-02")
	expected := "version: 1\nupdated: " + today + "\nskills: []\n"
	if string(data) != expected {
		t.Errorf("unexpected content:\ngot:  %q\nwant: %q", string(data), expected)
	}
}

func TestUpdateRegistryDate_IdempotentWhenCurrent(t *testing.T) {
	tmpDir := t.TempDir()
	registryPath := filepath.Join(tmpDir, "skills-registry.yaml")

	today := time.Now().Format("2006-01-02")
	content := "version: 1\nupdated: " + today + "\nskills: []\n"
	if err := os.WriteFile(registryPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	info, _ := os.Stat(registryPath)
	origModTime := info.ModTime()

	g := &Generator{
		Registry:     &Registry{},
		RegistryPath: registryPath,
	}

	if err := g.UpdateRegistryDate(); err != nil {
		t.Fatalf("UpdateRegistryDate: %v", err)
	}

	// File should not have been rewritten (same date).
	info2, _ := os.Stat(registryPath)
	if !info2.ModTime().Equal(origModTime) {
		t.Error("file was rewritten despite date being current")
	}
}

func TestUpdateRegistryDate_PreservesComments(t *testing.T) {
	tmpDir := t.TempDir()
	registryPath := filepath.Join(tmpDir, "skills-registry.yaml")

	content := "# Header comment\nversion: 1\nupdated: 2025-06-15\n# Skills below\nskills:\n  - name: test\n"
	if err := os.WriteFile(registryPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	g := &Generator{
		Registry:     &Registry{},
		RegistryPath: registryPath,
	}

	if err := g.UpdateRegistryDate(); err != nil {
		t.Fatalf("UpdateRegistryDate: %v", err)
	}

	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}

	got := string(data)
	if !strings.Contains(got, "# Header comment") {
		t.Error("header comment was lost")
	}
	if !strings.Contains(got, "# Skills below") {
		t.Error("inline comment was lost")
	}
	if !strings.Contains(got, "- name: test") {
		t.Error("skill entry was lost")
	}

	today := time.Now().Format("2006-01-02")
	if !strings.Contains(got, "updated: "+today) {
		t.Errorf("date not updated, got:\n%s", got)
	}
}

func TestUpdateRegistryDate_EmptyRegistryPath(t *testing.T) {
	g := &Generator{
		Registry:     &Registry{},
		RegistryPath: "",
	}

	if err := g.UpdateRegistryDate(); err != nil {
		t.Fatalf("expected nil error for empty path, got: %v", err)
	}
}

// =========================================================================
// sortSkillsByPriority tests
// =========================================================================

func intPtr(n int) *int { return &n }

func TestSortSkillsByPriority_ExplicitOrder(t *testing.T) {
	skills := []*Skill{
		{Name: "c", Priority: intPtr(30)},
		{Name: "a", Priority: intPtr(10)},
		{Name: "b", Priority: intPtr(20)},
	}

	sortSkillsByPriority(skills)

	want := []string{"a", "b", "c"}
	for i, s := range skills {
		if s.Name != want[i] {
			t.Errorf("position %d: got %q, want %q", i, s.Name, want[i])
		}
	}
}

func TestSortSkillsByPriority_NilAfterExplicit(t *testing.T) {
	skills := []*Skill{
		{Name: "no-prio-1"},
		{Name: "explicit", Priority: intPtr(10)},
		{Name: "no-prio-2"},
	}

	sortSkillsByPriority(skills)

	if skills[0].Name != "explicit" {
		t.Errorf("explicit priority should come first, got %q", skills[0].Name)
	}
	// Nil-priority skills preserve registry order among themselves.
	if skills[1].Name != "no-prio-1" || skills[2].Name != "no-prio-2" {
		t.Errorf("nil-priority skills should preserve order, got [%s, %s]", skills[1].Name, skills[2].Name)
	}
}

func TestSortSkillsByPriority_AllNilPreservesOrder(t *testing.T) {
	skills := []*Skill{
		{Name: "z"},
		{Name: "m"},
		{Name: "a"},
	}

	sortSkillsByPriority(skills)

	// With no priorities, registry order is preserved.
	want := []string{"z", "m", "a"}
	for i, s := range skills {
		if s.Name != want[i] {
			t.Errorf("position %d: got %q, want %q", i, s.Name, want[i])
		}
	}
}

func TestSortSkillsByPriority_EqualPriorityPreservesOrder(t *testing.T) {
	skills := []*Skill{
		{Name: "second", Priority: intPtr(10)},
		{Name: "first", Priority: intPtr(10)},
		{Name: "third", Priority: intPtr(10)},
	}

	sortSkillsByPriority(skills)

	// Same priority → stable sort preserves registry order.
	want := []string{"second", "first", "third"}
	for i, s := range skills {
		if s.Name != want[i] {
			t.Errorf("position %d: got %q, want %q", i, s.Name, want[i])
		}
	}
}

func TestCompositeInstructions_OrderRespectsPriority(t *testing.T) {
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "mcp", "skills")

	// Create minimal source dirs so generation doesn't fail.
	for _, name := range []string{"guardrails", "mcp-usage", "memory"} {
		if err := os.MkdirAll(filepath.Join(sourceDir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}

	enabled := true
	skills := []*Skill{
		{
			Name:     "guardrails",
			Priority: intPtr(30),
			Common:   &SkillSpec{Description: "Safety guardrails", Instructions: "# Guardrails\n\nBe safe."},
			Targets:  map[string]*TargetSpec{"gemini": {Enabled: &enabled, Type: "instruction"}},
		},
		{
			Name:   "mcp-usage",
			Common: &SkillSpec{Description: "MCP usage core", Instructions: "# MCP Usage\n\nUse MCP tools."},
			// No priority → comes after explicit priorities.
			Targets: map[string]*TargetSpec{"gemini": {Enabled: &enabled, Type: "instruction"}},
		},
		{
			Name:     "memory",
			Priority: intPtr(10),
			Common:   &SkillSpec{Description: "Memory practices", Instructions: "# Memory\n\nRemember things."},
			Targets:  map[string]*TargetSpec{"gemini": {Enabled: &enabled, Type: "instruction"}},
		},
	}

	g := &Generator{
		Registry:  &Registry{Skills: skills},
		SourceDir: sourceDir,
		Target:    "gemini",
		RepoRoot:  tmpDir,
		CodexHome: "/tmp/codex",
	}

	if err := g.generateForTarget("gemini"); err != nil {
		t.Fatalf("generateForTarget: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, ".gemini", "GEMINI.md"))
	if err != nil {
		t.Fatalf("read GEMINI.md: %v", err)
	}

	content := string(data)

	// memory (priority 10) should come before guardrails (priority 30),
	// which should come before mcp-usage (no priority).
	memIdx := strings.Index(content, "## Memory")
	guardIdx := strings.Index(content, "## Guardrails")
	mcpIdx := strings.Index(content, "## Mcp Usage")

	if memIdx < 0 || guardIdx < 0 || mcpIdx < 0 {
		t.Fatalf("missing expected sections in GEMINI.md:\n%s", content)
	}

	if memIdx > guardIdx {
		t.Errorf("memory (priority 10) should appear before guardrails (priority 30)")
	}
	if guardIdx > mcpIdx {
		t.Errorf("guardrails (priority 30) should appear before mcp-usage (no priority)")
	}
}

func TestUpdateRegistryDate_NoUpdatedField(t *testing.T) {
	tmpDir := t.TempDir()
	registryPath := filepath.Join(tmpDir, "skills-registry.yaml")

	content := "version: 1\nskills: []\n"
	if err := os.WriteFile(registryPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	g := &Generator{
		Registry:     &Registry{},
		RegistryPath: registryPath,
	}

	if err := g.UpdateRegistryDate(); err != nil {
		t.Fatalf("expected nil error when no updated field, got: %v", err)
	}

	// File should remain unchanged.
	data, _ := os.ReadFile(registryPath)
	if string(data) != content {
		t.Errorf("file was modified despite no updated field:\n%s", string(data))
	}
}
