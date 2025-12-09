package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	envVarRegex      = regexp.MustCompile(`\$\{env:([^}]+)\}`)
	keychainVarRegex = regexp.MustCompile(`\$\{keychain:([^}]+)\}`)
	repoRegex        = regexp.MustCompile(`\$\{repo\}`)
	homeRegex        = regexp.MustCompile(`\$\{HOME\}`)
)

// ResolveTokens replaces placeholders in strings with actual values.
// repoPath: The root of the repository (e.g. workspace root).
// context: "local" (client-side) or "cluster" (k8s hub).
func ResolveTokens(value string, repoPath string, context string) string {
	// Replace ${repo}
	if context == "cluster" {
		value = repoRegex.ReplaceAllString(value, "/app")
	} else {
		value = repoRegex.ReplaceAllString(value, repoPath)
	}

	// Replace ${HOME}
	if context == "cluster" {
		value = homeRegex.ReplaceAllString(value, "/home/mcp")
	} else {
		home, _ := os.UserHomeDir()
		value = homeRegex.ReplaceAllString(value, home)
	}

	// Replace ${env:VAR} with actual environment variable value
	// If env var is not set, keep the placeholder for runtime resolution
	value = envVarRegex.ReplaceAllStringFunc(value, func(match string) string {
		varName := envVarRegex.FindStringSubmatch(match)[1]
		if envVal := os.Getenv(varName); envVal != "" {
			return envVal
		}
		// Keep placeholder for clients that support runtime env resolution
		return match
	})

	// Keep ${keychain:VAR} as-is for VSCode extension to resolve via macOS keychain
	// The extension has keychain resolution logic that will handle these

	return value
}

// ResolveCommand resolves the command path.
func ResolveCommand(cmd string, repoPath string, context string) string {
	if cmd == "" {
		return ""
	}
	resolved := ResolveTokens(cmd, repoPath, context)

	// If local context, ensure absolute path for scripts
	if context == "local" {
		if strings.HasPrefix(resolved, "scripts/") || strings.HasPrefix(resolved, "mcp/") || strings.HasPrefix(resolved, "./") {
			return filepath.Join(repoPath, resolved)
		}
	}
	return resolved
}

// ResolveArgs resolves a list of arguments.
func ResolveArgs(args []any, repoPath string, context string) []string {
	var resolved []string
	for _, arg := range args {
		s := fmt.Sprintf("%v", arg)
		resolved = append(resolved, ResolveTokens(s, repoPath, context))
	}
	return resolved
}
