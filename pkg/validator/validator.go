package validator

import (
	"os"
	"path/filepath"
)

// Validator orchestrates all validation checks.
type Validator struct {
	RepoRoot string
	HomeDir  string
	runtime  *RuntimeValidator
}

// New creates a new validator with the given paths.
func New(repoRoot, homeDir string) *Validator {
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}
	return &Validator{
		RepoRoot: repoRoot,
		HomeDir:  homeDir,
		runtime:  NewRuntimeValidator(repoRoot, homeDir),
	}
}

// ValidateFile validates a single config file.
func (v *Validator) ValidateFile(target, filePath string) (*ValidationResult, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	return v.ValidateContent(target, filePath, content), nil
}

// ValidateContent validates config content directly.
func (v *Validator) ValidateContent(target, filePath string, content []byte) *ValidationResult {
	var result *ValidationResult

	// Perform schema validation based on format
	if IsJSONTarget(target) {
		result = ValidateJSONSchema(target, filePath, content)
		// Add runtime checks
		v.runtime.ValidateJSONRuntime(filePath, content, result)
	} else if IsTOMLTarget(target) {
		result = ValidateTOMLStructure(target, filePath, content)
		// Add runtime checks
		v.runtime.ValidateTOMLRuntime(filePath, content, result)
	} else {
		// Unknown target format
		result = &ValidationResult{
			Target: target,
			File:   filePath,
			Valid:  true,
		}
		result.AddWarning(CodeInvalidSchema, "", "unknown target format, skipping validation")
	}

	// Update valid flag
	result.Valid = !result.HasErrors()
	return result
}

// ValidateGenerated validates configs after generation.
func (v *Validator) ValidateGenerated(outputDir string, targets []string) ([]*ValidationResult, error) {
	var results []*ValidationResult

	for _, target := range targets {
		configFile := v.getConfigPath(outputDir, target)
		if configFile == "" {
			continue
		}

		// Skip if file doesn't exist (not all targets may be generated)
		if _, err := os.Stat(configFile); os.IsNotExist(err) {
			continue
		}

		result, err := v.ValidateFile(target, configFile)
		if err != nil {
			// Create an error result
			result = &ValidationResult{
				Target: target,
				File:   configFile,
				Valid:  false,
			}
			result.AddError(CodeInvalidSchema, "", err.Error())
		}

		results = append(results, result)
	}

	return results, nil
}

// ValidateDirectory validates all configs in a directory.
func (v *Validator) ValidateDirectory(dir string) ([]*ValidationResult, error) {
	var results []*ValidationResult

	// Check for known config patterns
	patterns := map[string]string{
		"mcp.json":                   "", // Will detect target from parent dir
		"config.toml":                "",
		"claude_desktop_config.json": "claude_desktop",
	}

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		filename := info.Name()
		target, known := patterns[filename]
		if !known {
			return nil
		}

		// Infer target from parent directory if not specified
		if target == "" {
			parentDir := filepath.Base(filepath.Dir(path))
			target = inferTarget(parentDir, filename)
		}

		result, err := v.ValidateFile(target, path)
		if err != nil {
			result = &ValidationResult{
				Target: target,
				File:   path,
				Valid:  false,
			}
			result.AddError(CodeInvalidSchema, "", err.Error())
		}

		results = append(results, result)
		return nil
	})

	return results, err
}

// getConfigPath returns the config file path for a target.
func (v *Validator) getConfigPath(outputDir, target string) string {
	switch target {
	case "claude", "vscode", "antigravity":
		return filepath.Join(outputDir, target, "mcp.json")
	case "claude_desktop":
		return filepath.Join(outputDir, target, "claude_desktop_config.json")
	case "codex", "kilocode", "gemini":
		return filepath.Join(outputDir, target, "config.toml")
	default:
		return ""
	}
}

// inferTarget infers the target from directory/filename context.
func inferTarget(parentDir, filename string) string {
	// Try to match parent directory to known targets
	switch parentDir {
	case ".claude", "claude":
		return "claude"
	case ".codex", "codex":
		return "codex"
	case ".kilocode", "kilocode":
		return "kilocode"
	case ".gemini", "gemini":
		return "gemini"
	case ".vscode", "vscode", ".vscode-mcp":
		return "vscode"
	case ".antigravity", "antigravity":
		return "antigravity"
	case "claude_desktop", "claude_desktop_config":
		return "claude_desktop"
	}

	// Fall back to filename-based detection
	if filename == "config.toml" {
		return "codex" // Default TOML target
	}
	if filename == "mcp.json" {
		return "claude" // Default JSON target
	}

	return "unknown"
}

// HasErrors returns true if any result has errors.
func HasErrors(results []*ValidationResult) bool {
	for _, r := range results {
		if r.HasErrors() {
			return true
		}
	}
	return false
}

// SummaryString returns a summary of validation results.
func SummaryString(results []*ValidationResult) string {
	var totalErrors, totalWarnings int
	for _, r := range results {
		totalErrors += r.ErrorCount()
		totalWarnings += r.WarningCount()
	}

	if totalErrors == 0 && totalWarnings == 0 {
		return "All configs valid"
	}

	if totalErrors == 0 {
		return "No errors, " + pluralize(totalWarnings, "warning", "warnings")
	}

	return pluralize(totalErrors, "error", "errors") + ", " + pluralize(totalWarnings, "warning", "warnings")
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10)) + " " + plural
}
