package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// Path patterns - resolved during config generation
	repoRegex = regexp.MustCompile(`\$\{repo\}`)
	homeRegex = regexp.MustCompile(`\$\{HOME\}`)

	// Secret patterns for validation (NOT resolved during generation)
	// These are used by ValidateNoPlaintextSecrets to check generated configs
	secretPatternRegex = regexp.MustCompile(`\$\{(env|keychain|secret):([^}]+)\}`)
)

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func resolvePathLike(value string, workspaceRoot string, registryRoot string, context string) string {
	resolved := ResolveTokens(value, workspaceRoot, context)
	if context != "local" {
		return resolved
	}
	if filepath.IsAbs(resolved) {
		return resolved
	}

	// Only rewrite clearly path-like values; leave things like "python3" or "npx" intact.
	if strings.HasPrefix(resolved, "scripts/") || strings.HasPrefix(resolved, "mcp/") || strings.HasPrefix(resolved, "./") {
		roots := []string{
			registryRoot,
			workspaceRoot,
			filepath.Join(workspaceRoot, "platform", "gitops"),
			filepath.Join(workspaceRoot, "services", "loom-core"),
		}
		for _, root := range roots {
			if root == "" {
				continue
			}
			candidate := filepath.Join(root, resolved)
			if fileExists(candidate) {
				return candidate
			}
		}

		// Last resort: still make it absolute to avoid cwd-dependence.
		if registryRoot != "" {
			return filepath.Join(registryRoot, resolved)
		}
		if workspaceRoot != "" {
			return filepath.Join(workspaceRoot, resolved)
		}
	}

	return resolved
}

// ResolveTokens replaces path placeholders in strings with actual values.
// SECURITY: Only resolves ${repo} and ${HOME} - NEVER resolves secret patterns.
// Secret patterns (${env:VAR}, ${keychain:VAR}, ${secret:VAR}) are preserved
// as-is for runtime resolution by the daemon or client tools.
//
// repoPath: The root of the repository (e.g. workspace root).
// context: "local" (client-side) or "cluster" (k8s hub).
func ResolveTokens(value string, repoPath string, context string) string {
	// Replace ${repo} - safe path expansion
	if context == "cluster" {
		value = repoRegex.ReplaceAllString(value, "/app")
	} else {
		value = repoRegex.ReplaceAllString(value, repoPath)
	}

	// Replace ${HOME} - safe path expansion
	if context == "cluster" {
		value = homeRegex.ReplaceAllString(value, "/home/mcp")
	} else {
		home, _ := os.UserHomeDir()
		value = homeRegex.ReplaceAllString(value, home)
	}

	// SECURITY: Do NOT resolve ${env:VAR}, ${keychain:VAR}, or ${secret:VAR}
	// These are secret patterns that must be resolved at runtime, not during
	// config generation. Resolving them here would bake secrets into config files.

	return value
}

// ResolveCommand resolves the command path.
func ResolveCommand(cmd string, workspaceRoot string, registryRoot string, context string) string {
	if cmd == "" {
		return ""
	}
	return resolvePathLike(cmd, workspaceRoot, registryRoot, context)
}

// ResolveArgs resolves a list of arguments.
func ResolveArgs(args []any, workspaceRoot string, registryRoot string, context string) []string {
	var resolved []string
	for _, arg := range args {
		s := fmt.Sprintf("%v", arg)
		resolved = append(resolved, resolvePathLike(s, workspaceRoot, registryRoot, context))
	}
	return resolved
}

// Known secret patterns that indicate plaintext secrets in configs
var knownSecretPatterns = []struct {
	pattern *regexp.Regexp
	name    string
}{
	{regexp.MustCompile(`ghp_[a-zA-Z0-9]{36}`), "GitHub Personal Access Token"},
	{regexp.MustCompile(`gho_[a-zA-Z0-9]{36}`), "GitHub OAuth Token"},
	{regexp.MustCompile(`ghu_[a-zA-Z0-9]{36}`), "GitHub User Token"},
	{regexp.MustCompile(`ghs_[a-zA-Z0-9]{36}`), "GitHub Server Token"},
	{regexp.MustCompile(`ghr_[a-zA-Z0-9]{36}`), "GitHub Refresh Token"},
	{regexp.MustCompile(`glpat-[a-zA-Z0-9_-]{20,}`), "GitLab Personal Access Token"},
	{regexp.MustCompile(`sk-[a-zA-Z0-9]{32,}`), "API Secret Key (OpenAI/Anthropic style)"},
	{regexp.MustCompile(`sk-E-[a-zA-Z0-9]{40,}`), "Morph API Key"},
	{regexp.MustCompile(`tvly-[a-zA-Z0-9-]{30,}`), "Tavily API Key"},
	{regexp.MustCompile(`glsa_[a-zA-Z0-9]{32,}`), "Grafana Service Account Token"},
	{regexp.MustCompile(`z_[a-zA-Z0-9._-]{50,}`), "Zep API Key"},
	{regexp.MustCompile(`hf_[a-zA-Z0-9]{30,}`), "Hugging Face Token"},
	{regexp.MustCompile(`AIzaSy[a-zA-Z0-9_-]{33}`), "Google API Key"},
	{regexp.MustCompile(`GOCSPX-[a-zA-Z0-9_-]{28}`), "Google OAuth Client Secret"},
}

// SecretLeak represents a detected plaintext secret in a config file
type SecretLeak struct {
	File    string
	Line    int
	Type    string
	Snippet string // Redacted snippet showing context
}

// ValidateNoPlaintextSecrets checks a config file for plaintext secrets.
// Returns a list of detected leaks.
func ValidateNoPlaintextSecrets(filepath string, content string) []SecretLeak {
	var leaks []SecretLeak

	lines := strings.Split(content, "\n")
	for lineNum, line := range lines {
		for _, pattern := range knownSecretPatterns {
			if pattern.pattern.MatchString(line) {
				// Create redacted snippet
				snippet := pattern.pattern.ReplaceAllString(line, "[REDACTED]")
				if len(snippet) > 80 {
					snippet = snippet[:80] + "..."
				}

				leaks = append(leaks, SecretLeak{
					File:    filepath,
					Line:    lineNum + 1,
					Type:    pattern.name,
					Snippet: snippet,
				})
			}
		}
	}

	return leaks
}

// IsValidSecretReference checks if a value is a valid secret reference pattern
// (not a plaintext secret).
func IsValidSecretReference(value string) bool {
	// Valid patterns: ${env:VAR}, ${keychain:VAR}, ${secret:VAR}
	return secretPatternRegex.MatchString(value)
}
