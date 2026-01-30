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
	Registry      *Registry
	SourceDir     string // Where skill source files live (mcp/skills/)
	Target        string // "codex" | "claude" | "all"
	OutputDir     string // Where to generate skills
	CodexHome     string // ~/.codex
	WorkspaceRoot string // For Claude: workspace root for .agents/skills/
	DryRun        bool
	Verbose       bool
}

// GeneratorOptions configures the generator.
type GeneratorOptions struct {
	RegistryPath  string
	Target        string
	OutputDir     string
	CodexHome     string
	WorkspaceRoot string
	DryRun        bool
	Verbose       bool
}

// NewGenerator creates a new skill generator.
func NewGenerator(opts GeneratorOptions) (*Generator, error) {
	reg, err := Load(opts.RegistryPath)
	if err != nil {
		return nil, err
	}

	sourceDir := FindSkillsSourceDir(opts.RegistryPath)

	home, _ := os.UserHomeDir()
	codexHome := opts.CodexHome
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}

	workspaceRoot := opts.WorkspaceRoot
	if workspaceRoot == "" {
		workspaceRoot, _ = os.Getwd()
	}

	return &Generator{
		Registry:      reg,
		SourceDir:     sourceDir,
		Target:        opts.Target,
		OutputDir:     opts.OutputDir,
		CodexHome:     codexHome,
		WorkspaceRoot: workspaceRoot,
		DryRun:        opts.DryRun,
		Verbose:       opts.Verbose,
	}, nil
}

// Generate generates skills for the configured target(s).
func (g *Generator) Generate() error {
	targets := []string{g.Target}
	if g.Target == "all" {
		targets = []string{"codex", "claude"}
	}

	for _, target := range targets {
		if err := g.generateForTarget(target); err != nil {
			return fmt.Errorf("generate %s: %w", target, err)
		}
	}

	return nil
}

func (g *Generator) generateForTarget(target string) error {
	for _, skill := range g.Registry.Skills {
		if !skill.IsEnabled(target) {
			if g.Verbose {
				fmt.Printf("Skipping %s for %s (disabled)\n", skill.Name, target)
			}
			continue
		}

		switch target {
		case "codex":
			if err := g.generateCodexSkill(skill); err != nil {
				return fmt.Errorf("generate codex skill %s: %w", skill.Name, err)
			}
		case "claude":
			if err := g.generateClaudeSkill(skill); err != nil {
				return fmt.Errorf("generate claude skill %s: %w", skill.Name, err)
			}
		default:
			return fmt.Errorf("unknown target: %s", target)
		}
	}

	return nil
}

// generateCodexSkill generates a Codex skill in SKILL.md + scripts/ + references/ + assets/ format.
func (g *Generator) generateCodexSkill(skill *Skill) error {
	outputDir := g.OutputDir
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

// generateCodexSkillMD generates the SKILL.md content for a Codex skill.
func (g *Generator) generateCodexSkillMD(skill *Skill) string {
	var sb strings.Builder

	// YAML frontmatter
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("name: %s\n", skill.Name))

	// Description: escape quotes and normalize whitespace
	desc := strings.TrimSpace(skill.Common.Description)
	desc = strings.ReplaceAll(desc, "\n", " ")
	desc = strings.ReplaceAll(desc, "\"", "\\\"")
	sb.WriteString(fmt.Sprintf("description: %q\n", desc))
	sb.WriteString("---\n\n")

	// Instructions body with resolved paths
	sourceSkillDir := filepath.Join(g.SourceDir, skill.Name)
	instructions := skill.ResolveInstructions("codex", g.CodexHome, sourceSkillDir)
	sb.WriteString(instructions)

	// Bundled Resources section
	sb.WriteString("\n## Bundled Resources\n\n")

	if skill.Common.Scripts != nil {
		for _, script := range skill.Common.Scripts {
			sb.WriteString(fmt.Sprintf("- `%s`\n", script.Path))
		}
	}

	if skill.Common.References != nil {
		for _, ref := range skill.Common.References {
			sb.WriteString(fmt.Sprintf("- `references/%s`\n", ref))
		}
	}

	if skill.Common.Assets != nil {
		for _, asset := range skill.Common.Assets {
			sb.WriteString(fmt.Sprintf("- `assets/%s`\n", asset))
		}
	}

	return sb.String()
}

// generateClaudeSkill generates a Claude Code skill as a simple markdown file.
func (g *Generator) generateClaudeSkill(skill *Skill) error {
	outputDir := g.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join(g.WorkspaceRoot, ".agents", "skills")
	}

	if g.Verbose {
		fmt.Printf("Generating Claude skill: %s -> %s\n", skill.Name, outputDir)
	}

	if g.DryRun {
		fmt.Printf("[dry-run] Would create Claude skill: %s/%s.md\n", outputDir, skill.Name)
		return nil
	}

	// Create output directory
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// Generate markdown file
	content := g.generateClaudeSkillMD(skill)
	outputPath := filepath.Join(outputDir, skill.Name+".md")
	if err := os.WriteFile(outputPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write skill markdown: %w", err)
	}

	return nil
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
	lines := strings.Split(instructions, "\n")
	startIdx := 0
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "# ") {
			startIdx = i + 1
			break
		}
	}

	// Find first non-empty line after header
	for startIdx < len(lines) && strings.TrimSpace(lines[startIdx]) == "" {
		startIdx++
	}

	if startIdx > 0 && startIdx < len(lines) {
		instructions = strings.Join(lines[startIdx:], "\n")
	}

	sb.WriteString(instructions)

	// Add script references section if there are scripts
	if skill.Common.Scripts != nil && len(skill.Common.Scripts) > 0 {
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
