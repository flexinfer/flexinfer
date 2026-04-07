// generator_codex_gemini.go contains Codex and Gemini skill generation logic.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
	// Atomic write to avoid codex file watcher observing partial/empty files,
	// which triggers spurious "missing YAML frontmatter" errors (openai/codex#11495).
	if err := writeFileAtomic(skillMDPath, []byte(skillMD), 0o644); err != nil {
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

	// Description: normalize whitespace and escape for YAML
	desc := strings.TrimSpace(skill.Common.Description)
	desc = strings.ReplaceAll(desc, "\n", " ")
	sb.WriteString(fmt.Sprintf("description: \"%s\"\n", escapeYAMLString(desc)))
	shortDesc := shortSkillDescription(desc)
	if shortDesc != "" {
		sb.WriteString("metadata:\n")
		sb.WriteString(fmt.Sprintf("  short-description: \"%s\"\n", escapeYAMLString(shortDesc)))
	}
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
	// Atomic write: see generator_codex_gemini.go:generateCodexSkill for rationale.
	if err := writeFileAtomic(skillMDPath, []byte(skillMD), 0o644); err != nil {
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

	// Use the final Gemini/Antigravity skills home path to keep references
	// stable after sync.
	skillsHome := strings.TrimSpace(g.GeminiSkillsHome)
	if skillsHome == "" {
		skillsHome = "$HOME/.gemini/skills"
	}
	geminiSkillPath := fmt.Sprintf("%s/%s", strings.TrimRight(skillsHome, "/"), skill.Name)
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
