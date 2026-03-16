// Package skills provides loading and generation of cross-agent skill configurations.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Registry holds the parsed skills registry configuration.
type Registry struct {
	Version int      `yaml:"version"`
	Updated string   `yaml:"updated,omitempty"`
	Skills  []*Skill `yaml:"skills"`
}

// Skill defines a skill in the registry.
type Skill struct {
	Name       string                 `yaml:"name"`
	Categories []string               `yaml:"categories,omitempty"`
	Priority   *int                   `yaml:"priority,omitempty"` // Lower = first in composite output. Nil = after explicit priorities.
	Common     *SkillSpec             `yaml:"common,omitempty"`
	Targets    map[string]*TargetSpec `yaml:"targets,omitempty"`
}

// SkillSpec defines the common configuration for a skill.
type SkillSpec struct {
	Description  string    `yaml:"description"`
	Instructions string    `yaml:"instructions"`
	Scripts      []*Script `yaml:"scripts,omitempty"`
	References   []string  `yaml:"references,omitempty"`
	Assets       []string  `yaml:"assets,omitempty"`
	AlwaysAllow  []string  `yaml:"always_allow,omitempty"`
}

// Script defines a bundled script in a skill.
type Script struct {
	Name        string `yaml:"name"`
	Path        string `yaml:"path"`
	Description string `yaml:"description,omitempty"`
}

// TargetSpec defines platform-specific overrides for a skill.
type TargetSpec struct {
	Enabled            *bool  `yaml:"enabled,omitempty"`
	OutputFormat       string `yaml:"output_format,omitempty"`       // "markdown" for Claude
	Type               string `yaml:"type,omitempty"`                // "command", "skill", "rule", "instruction", "workflow"
	InstructionsAppend string `yaml:"instructions_append,omitempty"` // platform-specific additive instruction block
}

// Load reads and parses a skills registry YAML file.
func Load(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read skills registry: %w", err)
	}

	var reg Registry
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse skills registry: %w", err)
	}

	return &reg, nil
}

// FindRegistry locates the skills registry file.
// It searches in the following order:
// 1. Current directory: mcp/context/skills-registry.yaml
// 2. Workspace root: services/loom-core/mcp/context/skills-registry.yaml
// 3. Legacy GitOps path: platform/gitops/mcp/context/skills-registry.yaml
// 4. Home workspace paths, then ~/.config/loom/skills-registry.yaml
func FindRegistry() (string, bool) {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()

	paths := []string{
		filepath.Join(cwd, "mcp", "context", "skills-registry.yaml"),
		filepath.Join(cwd, "services", "loom-core", "mcp", "context", "skills-registry.yaml"),
		filepath.Join(cwd, "platform", "gitops", "mcp", "context", "skills-registry.yaml"),
		filepath.Join(home, "workspace", "services", "loom-core", "mcp", "context", "skills-registry.yaml"),
		filepath.Join(home, "workspace", "platform", "gitops", "mcp", "context", "skills-registry.yaml"),
		filepath.Join(home, ".config", "loom", "skills-registry.yaml"),
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}

	return "", false
}

// FindSkillsSourceDir locates the skills source directory relative to the registry.
// Skills are stored in mcp/skills/<skill-name>/ next to the registry.
func FindSkillsSourceDir(registryPath string) string {
	// Registry is at mcp/context/skills-registry.yaml
	// Skills are at mcp/skills/
	contextDir := filepath.Dir(registryPath) // mcp/context
	mcpDir := filepath.Dir(contextDir)       // mcp
	return filepath.Join(mcpDir, "skills")
}

// IsEnabled checks if a skill is enabled for a specific target.
func (s *Skill) IsEnabled(target string) bool {
	if s.Targets == nil {
		return true // Default to enabled if no targets specified
	}

	spec, ok := s.Targets[target]
	if !ok {
		return true // Default to enabled if target not specified
	}

	if spec.Enabled == nil {
		return true // Default to enabled if enabled field not specified
	}

	return *spec.Enabled
}

// GetOutputFormat returns the output format for a target, defaulting to empty string.
func (s *Skill) GetOutputFormat(target string) string {
	if s.Targets == nil {
		return ""
	}

	spec, ok := s.Targets[target]
	if !ok {
		return ""
	}

	return spec.OutputFormat
}

// GetType returns the output type for a target, using platform-specific defaults.
// Default types: claude → "command", codex → "skill", kilocode → "rule", gemini → "skill".
func (s *Skill) GetType(target string) string {
	if s.Targets != nil {
		if spec, ok := s.Targets[target]; ok && spec.Type != "" {
			return spec.Type
		}
	}

	// Platform-specific defaults
	switch target {
	case "claude":
		return "command"
	case "codex":
		return "skill"
	case "kilocode":
		return "rule"
	case "gemini":
		return "skill"
	default:
		return "skill"
	}
}

// escapeSentinel is a placeholder used during template resolution to protect
// escaped variable references (\${...}) from substitution. It is replaced
// back to a literal "${" after all variables have been resolved.
const escapeSentinel = "\x00LOOM_ESC_DOLLAR_BRACE\x00"

// ResolveInstructions replaces template variables in instructions.
// Supported variables:
//   - ${SKILL_PATH}: Path to skill directory (Codex: $CODEX_HOME/skills/<name>, Claude: direct paths)
//   - ${CODEX_HOME}: Codex home directory (~/.codex)
//   - ${HOME}: User home directory
//
// To emit a literal ${VARIABLE} in the generated output, escape it as
// \${VARIABLE} in the registry YAML.
func (s *Skill) ResolveInstructions(target, codexHome, skillSourceDir string) string {
	if s.Common == nil {
		return ""
	}

	instructions := s.Common.Instructions
	if s.Targets != nil {
		if spec, ok := s.Targets[target]; ok && strings.TrimSpace(spec.InstructionsAppend) != "" {
			instructions = strings.TrimRight(instructions, "\n") + "\n\n" + strings.TrimLeft(spec.InstructionsAppend, "\n")
		}
	}

	// Protect escaped references (\${...}) from substitution.
	instructions = strings.ReplaceAll(instructions, "\\${", escapeSentinel)

	switch target {
	case "codex":
		// For Codex, use $CODEX_HOME/skills/<name>
		skillPath := fmt.Sprintf("$CODEX_HOME/skills/%s", s.Name)
		instructions = strings.ReplaceAll(instructions, "${SKILL_PATH}", skillPath)
		instructions = strings.ReplaceAll(instructions, "${CODEX_HOME}", "$CODEX_HOME")
	case "claude":
		// For Claude, use actual paths since it doesn't have a skill home concept
		instructions = strings.ReplaceAll(instructions, "${SKILL_PATH}", skillSourceDir)
	case "kilocode":
		// For Kilocode, use actual paths (similar to Claude)
		instructions = strings.ReplaceAll(instructions, "${SKILL_PATH}", skillSourceDir)
	case "gemini":
		// For Gemini, use actual paths (similar to Claude)
		instructions = strings.ReplaceAll(instructions, "${SKILL_PATH}", skillSourceDir)
	}

	// Restore escaped references to literal ${...}.
	instructions = strings.ReplaceAll(instructions, escapeSentinel, "${")

	return instructions
}
