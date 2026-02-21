package skills

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Generator handles skill generation for different platforms.
type Generator struct {
	Registry  *Registry
	SourceDir string // Where skill source files live (mcp/skills/)
	Target    string // "codex" | "claude" | "kilocode" | "gemini" | "all"
	OutputDir string // Where to generate skills (overrides platform defaults)
	// CodexSkillsDir overrides the default Codex skills directory (normally ~/.codex/skills).
	// This is intentionally separate from OutputDir so callers (like `loom sync`) can
	// generate Codex skills into the repo while keeping manifest/instructions rooted
	// at the repo's .codex/ directory.
	CodexSkillsDir string
	RepoRoot       string // Base directory containing .claude/, .codex/, .kilocode/, .gemini/
	CodexHome      string // ~/.codex
	WorkspaceRoot  string // For Claude: workspace root for .agents/skills/
	DryRun         bool
	Verbose        bool
}

// GeneratorOptions configures the generator.
type GeneratorOptions struct {
	RegistryPath   string
	Target         string
	OutputDir      string
	RepoRoot       string
	CodexHome      string
	CodexSkillsDir string
	WorkspaceRoot  string
	DryRun         bool
	Verbose        bool
}

// AllTargets lists all supported skill generation targets.
var AllTargets = []string{"codex", "claude", "kilocode", "gemini"}

// NewGenerator creates a new skill generator.
func NewGenerator(opts GeneratorOptions) (*Generator, error) {
	reg, err := Load(opts.RegistryPath)
	if err != nil {
		return nil, err
	}

	sourceDir := FindSkillsSourceDir(opts.RegistryPath)

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}
	codexHome := opts.CodexHome
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}

	workspaceRoot := opts.WorkspaceRoot
	if workspaceRoot == "" {
		workspaceRoot, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
	}

	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = workspaceRoot
	}

	return &Generator{
		Registry:       reg,
		SourceDir:      sourceDir,
		Target:         opts.Target,
		OutputDir:      opts.OutputDir,
		CodexSkillsDir: opts.CodexSkillsDir,
		RepoRoot:       repoRoot,
		CodexHome:      codexHome,
		WorkspaceRoot:  workspaceRoot,
		DryRun:         opts.DryRun,
		Verbose:        opts.Verbose,
	}, nil
}

// Generate generates skills for the configured target(s).
func (g *Generator) Generate() error {
	targets := []string{g.Target}
	if g.Target == "all" {
		targets = AllTargets
	}

	for _, target := range targets {
		if err := g.generateForTarget(target); err != nil {
			return fmt.Errorf("generate %s: %w", target, err)
		}
	}

	return nil
}

func (g *Generator) generateForTarget(target string) error {
	var generatedFiles []string
	var instructionSkills []*Skill

	for _, skill := range g.Registry.Skills {
		if !skill.IsEnabled(target) {
			if g.Verbose {
				fmt.Printf("Skipping %s for %s (disabled)\n", skill.Name, target)
			}
			continue
		}

		skillType := skill.GetType(target)

		// Collect instruction-type skills for composite instructions.md / GEMINI.md
		if skillType == "instruction" {
			instructionSkills = append(instructionSkills, skill)
			continue
		}

		var files []string
		var err error

		switch target {
		case "codex":
			err = g.generateCodexSkill(skill)
			if err == nil {
				files = append(files, g.codexManifestFiles(skill)...)
			}
		case "claude":
			files, err = g.generateClaudeSkillByType(skill)
		case "kilocode":
			files, err = g.generateKilocodeSkill(skill)
		case "gemini":
			err = g.generateGeminiSkill(skill)
			if err == nil {
				files = append(files, g.geminiManifestFiles(skill)...)
			}
		default:
			return fmt.Errorf("unknown target: %s", target)
		}

		if err != nil {
			return fmt.Errorf("generate %s skill %s: %w", target, skill.Name, err)
		}
		generatedFiles = append(generatedFiles, files...)
	}

	// Generate composite instructions.md for platforms that support it
	if len(instructionSkills) > 0 {
		filename := "instructions.md"
		if target == "gemini" {
			filename = "GEMINI.md"
		}
		files, err := g.generateInstructionsFile(target, instructionSkills)
		if err != nil {
			return fmt.Errorf("generate %s %s: %w", target, filename, err)
		}
		generatedFiles = append(generatedFiles, files...)
	}

	// Write manifest
	if !g.DryRun && len(generatedFiles) > 0 {
		manifestDir := g.resolveTargetDir(target)
		if manifestDir != "" {
			if err := WriteManifest(manifestDir, target, generatedFiles); err != nil {
				if g.Verbose {
					fmt.Printf("Warning: could not write manifest for %s: %v\n", target, err)
				}
			}
		}
	}

	return nil
}

// resolveTargetDir returns the platform repo directory for writing generated files.
func (g *Generator) resolveTargetDir(target string) string {
	if g.OutputDir != "" {
		return g.OutputDir
	}
	if g.RepoRoot != "" {
		switch target {
		case "claude":
			return filepath.Join(g.RepoRoot, ".claude")
		case "codex":
			return filepath.Join(g.RepoRoot, ".codex")
		case "kilocode":
			return filepath.Join(g.RepoRoot, ".kilocode")
		case "gemini":
			return filepath.Join(g.RepoRoot, ".gemini")
		}
	}
	return ""
}

func (g *Generator) codexManifestFiles(skill *Skill) []string {
	files := []string{filepath.Join("skills", skill.Name, "SKILL.md")}
	seen := map[string]struct{}{files[0]: {}}

	add := func(path string) {
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		files = append(files, path)
	}

	if skill.Common != nil {
		for _, script := range skill.Common.Scripts {
			if script == nil || script.Path == "" {
				continue
			}
			add(filepath.Join("skills", skill.Name, script.Path))
		}
		for _, ref := range skill.Common.References {
			add(filepath.Join("skills", skill.Name, "references", ref))
		}
		for _, asset := range skill.Common.Assets {
			add(filepath.Join("skills", skill.Name, "assets", asset))
		}
	}

	return files
}

func (g *Generator) geminiManifestFiles(skill *Skill) []string {
	return g.codexManifestFiles(skill)
}

// =========================================================================
// Codex Generator
// =========================================================================

// generateCodexSkill generates a Codex skill in SKILL.md + scripts/ + references/ + assets/ format.
func (g *Generator) generateCodexSkill(skill *Skill) error {
	// For Codex, OutputDir refers to the skills root directory (not the platform root).
	outputDir := g.OutputDir
	if outputDir == "" {
		outputDir = g.CodexSkillsDir
	}
	if outputDir == "" {
		outputDir = filepath.Join(g.CodexHome, "skills")
	}

	skillDir := filepath.Join(outputDir, skill.Name)

	if g.Verbose {
		fmt.Printf("Generating Codex skill: %s -> %s\n", skill.Name, skillDir)
	}

	if g.DryRun {
		fmt.Printf("[dry-run] Would create Codex skill: %s\n", skillDir)
		return nil
	}

	// Create skill directory structure
	for _, subdir := range []string{"scripts", "references", "assets/templates"} {
		if err := os.MkdirAll(filepath.Join(skillDir, subdir), 0755); err != nil {
			return fmt.Errorf("create %s: %w", subdir, err)
		}
	}

	// Generate SKILL.md
	skillMD := g.generateCodexSkillMD(skill)
	skillMDPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillMDPath, []byte(skillMD), 0644); err != nil {
		return fmt.Errorf("write SKILL.md: %w", err)
	}

	// Copy scripts
	sourceSkillDir := filepath.Join(g.SourceDir, skill.Name)
	if skill.Common.Scripts != nil {
		for _, script := range skill.Common.Scripts {
			srcPath := filepath.Join(sourceSkillDir, script.Path)
			dstPath := filepath.Join(skillDir, script.Path)

			if err := copyFile(srcPath, dstPath); err != nil {
				if g.Verbose {
					fmt.Printf("Warning: could not copy script %s: %v\n", script.Path, err)
				}
			}
		}
	}

	// Copy references
	if skill.Common.References != nil {
		for _, ref := range skill.Common.References {
			srcPath := filepath.Join(sourceSkillDir, "references", ref)
			dstPath := filepath.Join(skillDir, "references", ref)

			if err := copyFile(srcPath, dstPath); err != nil {
				if g.Verbose {
					fmt.Printf("Warning: could not copy reference %s: %v\n", ref, err)
				}
			}
		}
	}

	// Copy assets
	if skill.Common.Assets != nil {
		for _, asset := range skill.Common.Assets {
			srcPath := filepath.Join(sourceSkillDir, "assets", asset)
			dstPath := filepath.Join(skillDir, "assets", asset)

			// Ensure parent directory exists
			if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
				return err
			}

			if err := copyFile(srcPath, dstPath); err != nil {
				if g.Verbose {
					fmt.Printf("Warning: could not copy asset %s: %v\n", asset, err)
				}
			}
		}
	}

	return nil
}

// =========================================================================
// Gemini Generator
// =========================================================================

// generateGeminiSkill generates a Gemini CLI skill bundle in .gemini/skills/<name>/.
func (g *Generator) generateGeminiSkill(skill *Skill) error {
	baseDir := g.resolveTargetDir("gemini")
	if baseDir == "" {
		baseDir = filepath.Join(g.RepoRoot, ".gemini")
	}

	skillDir := filepath.Join(baseDir, "skills", skill.Name)

	if g.Verbose {
		fmt.Printf("Generating Gemini skill: %s -> %s\n", skill.Name, skillDir)
	}

	if g.DryRun {
		fmt.Printf("[dry-run] Would create Gemini skill: %s\n", skillDir)
		return nil
	}

	for _, subdir := range []string{"scripts", "references", "assets/templates"} {
		if err := os.MkdirAll(filepath.Join(skillDir, subdir), 0755); err != nil {
			return fmt.Errorf("create %s: %w", subdir, err)
		}
	}

	skillMD := g.generateGeminiSkillMD(skill)
	skillMDPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillMDPath, []byte(skillMD), 0644); err != nil {
		return fmt.Errorf("write SKILL.md: %w", err)
	}

	sourceSkillDir := filepath.Join(g.SourceDir, skill.Name)
	if skill.Common.Scripts != nil {
		for _, script := range skill.Common.Scripts {
			srcPath := filepath.Join(sourceSkillDir, script.Path)
			dstPath := filepath.Join(skillDir, script.Path)
			if err := copyFile(srcPath, dstPath); err != nil && g.Verbose {
				fmt.Printf("Warning: could not copy script %s: %v\n", script.Path, err)
			}
		}
	}

	if skill.Common.References != nil {
		for _, ref := range skill.Common.References {
			srcPath := filepath.Join(sourceSkillDir, "references", ref)
			dstPath := filepath.Join(skillDir, "references", ref)
			if err := copyFile(srcPath, dstPath); err != nil && g.Verbose {
				fmt.Printf("Warning: could not copy reference %s: %v\n", ref, err)
			}
		}
	}

	if skill.Common.Assets != nil {
		for _, asset := range skill.Common.Assets {
			srcPath := filepath.Join(sourceSkillDir, "assets", asset)
			dstPath := filepath.Join(skillDir, "assets", asset)
			if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
				return err
			}
			if err := copyFile(srcPath, dstPath); err != nil && g.Verbose {
				fmt.Printf("Warning: could not copy asset %s: %v\n", asset, err)
			}
		}
	}

	return nil
}

// generateGeminiSkillMD generates the SKILL.md content for a Gemini skill.
func (g *Generator) generateGeminiSkillMD(skill *Skill) string {
	var sb strings.Builder

	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("name: %s\n", skill.Name))
	desc := strings.TrimSpace(skill.Common.Description)
	desc = strings.ReplaceAll(desc, "\n", " ")
	sb.WriteString(fmt.Sprintf("description: \"%s\"\n", escapeYAMLString(desc)))
	sb.WriteString("---\n\n")

	// Use the final Gemini skills home path to keep references stable after sync.
	geminiSkillPath := fmt.Sprintf("$HOME/.gemini/skills/%s", skill.Name)
	instructions := skill.ResolveInstructions("gemini", g.CodexHome, geminiSkillPath)
	sb.WriteString(instructions)

	hasResources := len(skill.Common.Scripts) > 0 || len(skill.Common.References) > 0 || len(skill.Common.Assets) > 0
	if hasResources {
		sb.WriteString("\n## Bundled Resources\n\n")
		for _, script := range skill.Common.Scripts {
			sb.WriteString(fmt.Sprintf("- `%s`\n", script.Path))
		}
		for _, ref := range skill.Common.References {
			sb.WriteString(fmt.Sprintf("- `references/%s`\n", ref))
		}
		for _, asset := range skill.Common.Assets {
			sb.WriteString(fmt.Sprintf("- `assets/%s`\n", asset))
		}
	}

	return sb.String()
}

// generateCodexSkillMD generates the SKILL.md content for a Codex skill.
func (g *Generator) generateCodexSkillMD(skill *Skill) string {
	var sb strings.Builder

	// YAML frontmatter
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("name: %s\n", skill.Name))

	// Description: normalize whitespace and escape for YAML
	desc := strings.TrimSpace(skill.Common.Description)
	desc = strings.ReplaceAll(desc, "\n", " ")
	sb.WriteString(fmt.Sprintf("description: \"%s\"\n", escapeYAMLString(desc)))
	sb.WriteString("---\n\n")

	// Instructions body with resolved paths
	sourceSkillDir := filepath.Join(g.SourceDir, skill.Name)
	instructions := skill.ResolveInstructions("codex", g.CodexHome, sourceSkillDir)
	sb.WriteString(instructions)

	// Bundled Resources section (only if resources exist)
	hasResources := len(skill.Common.Scripts) > 0 || len(skill.Common.References) > 0 || len(skill.Common.Assets) > 0
	if hasResources {
		sb.WriteString("\n## Bundled Resources\n\n")

		for _, script := range skill.Common.Scripts {
			sb.WriteString(fmt.Sprintf("- `%s`\n", script.Path))
		}

		for _, ref := range skill.Common.References {
			sb.WriteString(fmt.Sprintf("- `references/%s`\n", ref))
		}

		for _, asset := range skill.Common.Assets {
			sb.WriteString(fmt.Sprintf("- `assets/%s`\n", asset))
		}
	}

	return sb.String()
}

// =========================================================================
// Claude Generator
// =========================================================================

// generateClaudeSkillByType routes to the appropriate Claude generator based on skill type.
func (g *Generator) generateClaudeSkillByType(skill *Skill) ([]string, error) {
	skillType := skill.GetType("claude")

	switch skillType {
	case "command":
		return g.generateClaudeCommand(skill)
	case "rule":
		return g.generateClaudeRule(skill)
	default:
		// Fall back to legacy .agents/skills/ format
		return g.generateClaudeSkill(skill)
	}
}

// generateClaudeCommand writes a Claude slash command to .claude/commands/<name>.md.
func (g *Generator) generateClaudeCommand(skill *Skill) ([]string, error) {
	baseDir := g.resolveTargetDir("claude")
	if baseDir == "" {
		baseDir = filepath.Join(g.WorkspaceRoot, ".claude")
	}
	outputDir := filepath.Join(baseDir, "commands")

	if g.Verbose {
		fmt.Printf("Generating Claude command: %s -> %s\n", skill.Name, outputDir)
	}

	if g.DryRun {
		fmt.Printf("[dry-run] Would create Claude command: %s/%s.md\n", outputDir, skill.Name)
		return []string{filepath.Join("commands", skill.Name+".md")}, nil
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create commands dir: %w", err)
	}

	var sb strings.Builder

	// YAML frontmatter with description
	desc := strings.TrimSpace(skill.Common.Description)
	desc = strings.ReplaceAll(desc, "\n", " ")
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("description: %s\n", desc))
	sb.WriteString("---\n\n")

	// Title and instructions
	title := toTitleCase(skill.Name)
	sb.WriteString(fmt.Sprintf("# %s\n\n", title))

	sourceSkillDir := filepath.Join(g.SourceDir, skill.Name)
	instructions := skill.ResolveInstructions("claude", g.CodexHome, sourceSkillDir)

	// Strip the first header since we already have the title
	instructions = stripFirstHeader(instructions)
	sb.WriteString(instructions)

	outputPath := filepath.Join(outputDir, skill.Name+".md")
	if err := os.WriteFile(outputPath, []byte(sb.String()), 0644); err != nil {
		return nil, fmt.Errorf("write command markdown: %w", err)
	}

	return []string{filepath.Join("commands", skill.Name+".md")}, nil
}

// generateClaudeRule writes a Claude rule to .claude/rules/<name>.md.
func (g *Generator) generateClaudeRule(skill *Skill) ([]string, error) {
	baseDir := g.resolveTargetDir("claude")
	if baseDir == "" {
		baseDir = filepath.Join(g.WorkspaceRoot, ".claude")
	}
	outputDir := filepath.Join(baseDir, "rules")

	if g.Verbose {
		fmt.Printf("Generating Claude rule: %s -> %s\n", skill.Name, outputDir)
	}

	if g.DryRun {
		fmt.Printf("[dry-run] Would create Claude rule: %s/%s.md\n", outputDir, skill.Name)
		return []string{filepath.Join("rules", skill.Name+".md")}, nil
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create rules dir: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("<!-- Generated by loom from skills-registry.yaml -->\n")

	title := toTitleCase(skill.Name)
	sb.WriteString(fmt.Sprintf("# %s\n\n", title))

	sourceSkillDir := filepath.Join(g.SourceDir, skill.Name)
	instructions := skill.ResolveInstructions("claude", g.CodexHome, sourceSkillDir)
	instructions = stripFirstHeader(instructions)
	sb.WriteString(instructions)

	outputPath := filepath.Join(outputDir, skill.Name+".md")
	if err := os.WriteFile(outputPath, []byte(sb.String()), 0644); err != nil {
		return nil, fmt.Errorf("write rule markdown: %w", err)
	}

	return []string{filepath.Join("rules", skill.Name+".md")}, nil
}

// generateClaudeSkill generates a Claude Code skill as a simple markdown file (legacy .agents/skills/ format).
func (g *Generator) generateClaudeSkill(skill *Skill) ([]string, error) {
	outputDir := g.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join(g.WorkspaceRoot, ".agents", "skills")
	}

	if g.Verbose {
		fmt.Printf("Generating Claude skill: %s -> %s\n", skill.Name, outputDir)
	}

	if g.DryRun {
		fmt.Printf("[dry-run] Would create Claude skill: %s/%s.md\n", outputDir, skill.Name)
		return nil, nil
	}

	// Create output directory
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	// Generate markdown file
	content := g.generateClaudeSkillMD(skill)
	outputPath := filepath.Join(outputDir, skill.Name+".md")
	if err := os.WriteFile(outputPath, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("write skill markdown: %w", err)
	}

	return nil, nil
}

// generateClaudeSkillMD generates the markdown content for a Claude skill.
func (g *Generator) generateClaudeSkillMD(skill *Skill) string {
	var sb strings.Builder

	// Title from skill name (convert kebab-case to Title Case)
	title := toTitleCase(skill.Name)
	sb.WriteString(fmt.Sprintf("# %s\n\n", title))

	// Description
	desc := strings.TrimSpace(skill.Common.Description)
	sb.WriteString(desc)
	sb.WriteString("\n\n")

	// Instructions with resolved paths
	sourceSkillDir := filepath.Join(g.SourceDir, skill.Name)
	instructions := skill.ResolveInstructions("claude", g.CodexHome, sourceSkillDir)

	// For Claude, we strip the first header since we already have the title
	instructions = stripFirstHeader(instructions)
	sb.WriteString(instructions)

	// Add script references section if there are scripts
	if len(skill.Common.Scripts) > 0 {
		sb.WriteString("\n## Scripts\n\n")
		for _, script := range skill.Common.Scripts {
			scriptPath := filepath.Join(sourceSkillDir, script.Path)
			desc := script.Description
			if desc == "" {
				desc = "Script"
			}
			sb.WriteString(fmt.Sprintf("- `%s` - %s\n", scriptPath, desc))
		}
	}

	// Add reference to source location
	sb.WriteString("\n## Source\n\n")
	sb.WriteString(fmt.Sprintf("Skill source: `%s`\n", sourceSkillDir))

	return sb.String()
}

// =========================================================================
// Kilocode Generator
// =========================================================================

// generateKilocodeSkill generates a Kilocode skill based on its type.
func (g *Generator) generateKilocodeSkill(skill *Skill) ([]string, error) {
	skillType := skill.GetType("kilocode")

	switch skillType {
	case "workflow":
		return g.generateKilocodeWorkflow(skill)
	case "rule":
		return g.generateKilocodeRule(skill)
	default:
		// Default to rule format
		return g.generateKilocodeRule(skill)
	}
}

// generateKilocodeRule writes a Kilocode rule to .kilocode/rules/<name>.md.
func (g *Generator) generateKilocodeRule(skill *Skill) ([]string, error) {
	baseDir := g.resolveTargetDir("kilocode")
	if baseDir == "" {
		baseDir = filepath.Join(g.RepoRoot, ".kilocode")
	}
	outputDir := filepath.Join(baseDir, "rules")

	if g.Verbose {
		fmt.Printf("Generating Kilocode rule: %s -> %s\n", skill.Name, outputDir)
	}

	if g.DryRun {
		fmt.Printf("[dry-run] Would create Kilocode rule: %s/%s.md\n", outputDir, skill.Name)
		return []string{filepath.Join("rules", skill.Name+".md")}, nil
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create rules dir: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("<!-- Generated by loom from skills-registry.yaml -->\n")

	title := toTitleCase(skill.Name)
	sb.WriteString(fmt.Sprintf("# %s\n\n", title))

	sourceSkillDir := filepath.Join(g.SourceDir, skill.Name)
	instructions := skill.ResolveInstructions("kilocode", g.CodexHome, sourceSkillDir)
	instructions = stripFirstHeader(instructions)
	sb.WriteString(instructions)

	outputPath := filepath.Join(outputDir, skill.Name+".md")
	if err := os.WriteFile(outputPath, []byte(sb.String()), 0644); err != nil {
		return nil, fmt.Errorf("write rule markdown: %w", err)
	}

	return []string{filepath.Join("rules", skill.Name+".md")}, nil
}

// generateKilocodeWorkflow writes a Kilocode workflow to .kilocode/workflows/<name>.yaml.
func (g *Generator) generateKilocodeWorkflow(skill *Skill) ([]string, error) {
	baseDir := g.resolveTargetDir("kilocode")
	if baseDir == "" {
		baseDir = filepath.Join(g.RepoRoot, ".kilocode")
	}
	outputDir := filepath.Join(baseDir, "workflows")

	if g.Verbose {
		fmt.Printf("Generating Kilocode workflow: %s -> %s\n", skill.Name, outputDir)
	}

	if g.DryRun {
		fmt.Printf("[dry-run] Would create Kilocode workflow: %s/%s.yaml\n", outputDir, skill.Name)
		return []string{filepath.Join("workflows", skill.Name+".yaml")}, nil
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create workflows dir: %w", err)
	}

	sourceSkillDir := filepath.Join(g.SourceDir, skill.Name)
	instructions := skill.ResolveInstructions("kilocode", g.CodexHome, sourceSkillDir)

	desc := strings.TrimSpace(skill.Common.Description)
	desc = strings.ReplaceAll(desc, "\n", " ")

	var sb strings.Builder
	sb.WriteString("# Generated by loom from skills-registry.yaml\n")
	sb.WriteString("version: 1\n")
	sb.WriteString(fmt.Sprintf("name: \"%s\"\n", toTitleCase(skill.Name)))
	sb.WriteString(fmt.Sprintf("description: \"%s\"\n", escapeYAMLString(desc)))
	sb.WriteString("on:\n")
	sb.WriteString(fmt.Sprintf("  slash_command: \"/%s\"\n", skill.Name))
	sb.WriteString("steps:\n")
	sb.WriteString("  - id: execute\n")
	sb.WriteString("    prompt:\n")
	sb.WriteString("      content: |\n")

	// Indent instructions for YAML block scalar
	for _, line := range strings.Split(instructions, "\n") {
		if line == "" {
			sb.WriteString("\n")
		} else {
			sb.WriteString("        " + line + "\n")
		}
	}

	outputPath := filepath.Join(outputDir, skill.Name+".yaml")
	if err := os.WriteFile(outputPath, []byte(sb.String()), 0644); err != nil {
		return nil, fmt.Errorf("write workflow yaml: %w", err)
	}

	return []string{filepath.Join("workflows", skill.Name+".yaml")}, nil
}

// Composite instructions.md / GEMINI.md Generation

// generateInstructionsFile assembles instructions.md (or GEMINI.md for Gemini) from all instruction-type skills for a platform.
func (g *Generator) generateInstructionsFile(target string, skills []*Skill) ([]string, error) {
	baseDir := g.resolveTargetDir(target)
	if baseDir == "" {
		return nil, nil
	}

	filename := "instructions.md"
	if target == "gemini" {
		filename = "GEMINI.md"
	}

	if g.Verbose {
		fmt.Printf("Generating %s %s from %d skills\n", target, filename, len(skills))
	}

	if g.DryRun {
		fmt.Printf("[dry-run] Would create %s/%s from %d skills\n", baseDir, filename, len(skills))
		return []string{filename}, nil
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create dir: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("<!-- Generated by loom from skills-registry.yaml. Do not edit manually. -->\n")
	sb.WriteString("<!-- To customize, add content to _custom_instructions.md -->\n")
	sb.WriteString("# Agent Instructions\n\n")

	for _, skill := range skills {
		title := toTitleCase(skill.Name)
		sb.WriteString(fmt.Sprintf("## %s\n\n", title))

		sourceSkillDir := filepath.Join(g.SourceDir, skill.Name)
		instructions := skill.ResolveInstructions(target, g.CodexHome, sourceSkillDir)
		instructions = stripFirstHeader(instructions)
		sb.WriteString(strings.TrimSpace(instructions))
		sb.WriteString("\n\n")
	}

	// Append custom instructions sidecar if it exists
	customPath := filepath.Join(baseDir, "_custom_instructions.md")
	if data, err := os.ReadFile(customPath); err == nil {
		sb.WriteString("<!-- Custom instructions appended below -->\n")
		sb.WriteString(string(data))
		sb.WriteString("\n")
	}

	outputPath := filepath.Join(baseDir, filename)
	if err := os.WriteFile(outputPath, []byte(sb.String()), 0644); err != nil {
		return nil, fmt.Errorf("write %s: %w", filename, err)
	}

	return []string{filename}, nil
}

// =========================================================================
// Helpers
// =========================================================================

// stripFirstHeader removes the first # header and any immediately following blank lines.
func stripFirstHeader(instructions string) string {
	lines := strings.Split(instructions, "\n")
	startIdx := 0
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "# ") {
			startIdx = i + 1
			break
		}
	}

	// Skip blank lines after header
	for startIdx < len(lines) && strings.TrimSpace(lines[startIdx]) == "" {
		startIdx++
	}

	if startIdx > 0 && startIdx < len(lines) {
		return strings.Join(lines[startIdx:], "\n")
	}
	return instructions
}

// escapeYAMLString escapes a string for use as a YAML double-quoted scalar value.
func escapeYAMLString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	// Preserve executable bit
	info, err := sourceFile.Stat()
	if err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode())
}

// toTitleCase converts kebab-case to Title Case.
func toTitleCase(s string) string {
	words := strings.Split(s, "-")
	for i, word := range words {
		if len(word) > 0 {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}
