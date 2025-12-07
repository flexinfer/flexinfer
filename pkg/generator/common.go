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

	// Replace ${env:VAR}
	// In cluster mode, we strip the wrapper to let K8s handle env vars,
	// or we might want to keep it if we are generating the env var value itself.
	// For now, let's strip the wrapper so "FOO" becomes "FOO" (or value of FOO).
	// The Python script did: sanitized = re.sub(r"\$\{env:([^}]+)\}", r"\1", sanitized)
	value = envVarRegex.ReplaceAllString(value, "$1")

	// Replace ${keychain:VAR} -> VAR
	value = keychainVarRegex.ReplaceAllString(value, "$1")

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
