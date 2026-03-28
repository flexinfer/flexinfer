// generator.go contains the core Generator struct, constructor, and top-level dispatch logic.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
)

// Generator handles skill generation for different platforms.
type Generator struct {
	Registry     *Registry
	RegistryPath string // Path to skills-registry.yaml on disk
	SourceDir    string // Where skill source files live (mcp/skills/)
	Target       string // "codex" | "claude" | "kilocode" | "gemini" | "all"
	OutputDir    string // Where to generate skills (overrides platform defaults)
	// CodexRootDir overrides the Codex platform root (normally ~/.codex) for
	// instructions.md and manifest generation.
	CodexRootDir string
	// CodexSkillsDir overrides the default Codex skills directory (normally ~/.codex/skills).
	// This is intentionally separate from OutputDir so callers (like `loom sync`) can
	// generate Codex skills into the repo while keeping manifest/instructions rooted
	// at the repo's .codex/ directory.
	CodexSkillsDir string
	RepoRoot       string // Base directory containing .claude/, .codex/, .kilocode/, .gemini/
	CodexHome      string // ~/.codex
	// GeminiSkillsHome controls the ${SKILL_PATH} base for Gemini-formatted
	// skill bundles. Defaults to $HOME/.gemini/skills when empty.
	GeminiSkillsHome string
	WorkspaceRoot    string // For Claude: workspace root for .agents/skills/
	DryRun           bool
	Verbose          bool
}

// GeneratorOptions configures the generator.
type GeneratorOptions struct {
	RegistryPath     string
	Target           string
	OutputDir        string
	RepoRoot         string
	CodexHome        string
	CodexRootDir     string
	GeminiSkillsHome string
	CodexSkillsDir   string
	WorkspaceRoot    string
	DryRun           bool
	Verbose          bool
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
		Registry:         reg,
		RegistryPath:     opts.RegistryPath,
		SourceDir:        sourceDir,
		Target:           opts.Target,
		OutputDir:        opts.OutputDir,
		CodexRootDir:     opts.CodexRootDir,
		CodexSkillsDir:   opts.CodexSkillsDir,
		RepoRoot:         repoRoot,
		CodexHome:        codexHome,
		GeminiSkillsHome: opts.GeminiSkillsHome,
		WorkspaceRoot:    workspaceRoot,
		DryRun:           opts.DryRun,
		Verbose:          opts.Verbose,
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

	// Update the registry's `updated:` date after successful generation.
	if !g.DryRun {
		if err := g.UpdateRegistryDate(); err != nil {
			if g.Verbose {
				fmt.Printf("Warning: could not update registry date: %v\n", err)
			}
		}
	}

	return nil
}

func (g *Generator) generateForTarget(target string) error {
	var generatedFiles []string
	var instructionSkills []*Skill
	var bundleSkills []*Skill

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
		bundleSkills = append(bundleSkills, skill)

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
		sortSkillsByPriority(instructionSkills)

		filename := "instructions.md"
		if target == "gemini" {
			filename = "GEMINI.md"
		}
		files, err := g.generateInstructionsFile(target, instructionSkills, bundleSkills)
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
	if target == "codex" {
		if g.CodexRootDir != "" {
			return g.CodexRootDir
		}
		if g.RepoRoot != "" {
			return filepath.Join(g.RepoRoot, ".codex")
		}
		if g.CodexHome != "" {
			return g.CodexHome
		}
	}
	if g.OutputDir != "" {
		return g.OutputDir
	}
	if g.RepoRoot != "" {
		switch target {
		case "claude":
			return filepath.Join(g.RepoRoot, ".claude")
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
