package validator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// RuntimeValidator performs runtime checks on config values.
type RuntimeValidator struct {
	RepoRoot string
	HomeDir  string
}

// NewRuntimeValidator creates a runtime validator with the given paths.
func NewRuntimeValidator(repoRoot, homeDir string) *RuntimeValidator {
	return &RuntimeValidator{
		RepoRoot: repoRoot,
		HomeDir:  homeDir,
	}
}

// ValidateJSONRuntime validates runtime aspects of a JSON config.
func (v *RuntimeValidator) ValidateJSONRuntime(filePath string, content []byte, result *ValidationResult) {
	var data map[string]interface{}
	if err := json.Unmarshal(content, &data); err != nil {
		return // Schema validation already caught this
	}

	servers, ok := data["mcpServers"].(map[string]interface{})
	if !ok {
		return
	}

	for name, serverData := range servers {
		server, ok := serverData.(map[string]interface{})
		if !ok {
			continue
		}

		field := fmt.Sprintf("mcpServers.%s", name)
		v.validateServerSpec(field, server, result)
	}
}

// ValidateTOMLRuntime validates runtime aspects of a TOML config.
func (v *RuntimeValidator) ValidateTOMLRuntime(filePath string, content []byte, result *ValidationResult) {
	var cfg TOMLConfig
	if err := toml.Unmarshal(content, &cfg); err != nil {
		return // Schema validation already caught this
	}

	for name, server := range cfg.MCPServers {
		field := fmt.Sprintf("mcp_servers.%s", name)

		// Convert to generic map for unified validation
		serverMap := map[string]interface{}{
			"command": server.Command,
			"env":     server.Env,
		}
		if len(server.Args) > 0 {
			args := make([]interface{}, len(server.Args))
			for i, a := range server.Args {
				args[i] = a
			}
			serverMap["args"] = args
		}

		v.validateServerSpec(field, serverMap, result)
	}
}

// validateServerSpec validates a single server's runtime aspects.
func (v *RuntimeValidator) validateServerSpec(field string, server map[string]interface{}, result *ValidationResult) {
	// Validate command
	if cmd, ok := server["command"].(string); ok && cmd != "" {
		v.validateCommand(field+".command", cmd, result)
	}

	// Validate environment variable names and values
	if env, ok := server["env"].(map[string]interface{}); ok {
		v.validateEnv(field+".env", env, result)
	} else if env, ok := server["env"].(map[string]string); ok {
		// Convert to interface map
		envMap := make(map[string]interface{})
		for k, val := range env {
			envMap[k] = val
		}
		v.validateEnv(field+".env", envMap, result)
	}

	// Validate args for unresolved tokens
	if args, ok := server["args"].([]interface{}); ok {
		v.validateArgs(field+".args", args, result)
	}
}

// validateCommand checks if a command path exists and is executable.
func (v *RuntimeValidator) validateCommand(field, cmd string, result *ValidationResult) {
	// Skip commands with runtime-resolved tokens
	if strings.Contains(cmd, "${env:") || strings.Contains(cmd, "${keychain:") || strings.Contains(cmd, "${secret:") {
		return
	}

	// Warn about unresolved ${repo} or ${HOME} tokens
	if strings.Contains(cmd, "${repo}") || strings.Contains(cmd, "${HOME}") {
		result.AddWarning(CodeUnresolvedToken, field,
			fmt.Sprintf("unresolved token in command: %s", cmd))
		return
	}

	// Resolve the path
	resolved := v.resolvePath(cmd)

	// Check if it's an absolute path
	if !filepath.IsAbs(resolved) {
		// Could be a command in PATH - just warn
		result.AddWarning(CodeCommandNotFound, field,
			fmt.Sprintf("command is relative or in PATH: %s", cmd))
		return
	}

	// Check if file exists
	info, err := os.Stat(resolved)
	if os.IsNotExist(err) {
		result.AddWarning(CodeCommandNotFound, field,
			fmt.Sprintf("command not found: %s", resolved))
		return
	}

	if err != nil {
		result.AddWarning(CodeCommandNotFound, field,
			fmt.Sprintf("cannot stat command: %s", err))
		return
	}

	// Check if executable
	if info.Mode()&0111 == 0 {
		result.AddError(CodeCommandNotExecutable, field,
			fmt.Sprintf("command is not executable: %s", resolved))
	}
}

// validateEnv checks environment variable names and values.
func (v *RuntimeValidator) validateEnv(field string, env map[string]interface{}, result *ValidationResult) {
	// Valid env var name pattern: letters, numbers, underscore, starting with letter or underscore
	envNameRegex := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

	for name, value := range env {
		// Check name format
		if !envNameRegex.MatchString(name) {
			result.AddWarning(CodeInvalidEnvName, fmt.Sprintf("%s.%s", field, name),
				fmt.Sprintf("environment variable name '%s' may not be portable", name))
		}

		// Check for plaintext secrets in values
		if strVal, ok := value.(string); ok {
			v.checkPlaintextSecrets(fmt.Sprintf("%s.%s", field, name), strVal, result)
		}
	}
}

// validateArgs checks arguments for unresolved tokens.
func (v *RuntimeValidator) validateArgs(field string, args []interface{}, result *ValidationResult) {
	for i, arg := range args {
		if strArg, ok := arg.(string); ok {
			// Warn about unresolved build-time tokens
			if strings.Contains(strArg, "${repo}") || strings.Contains(strArg, "${HOME}") {
				result.AddWarning(CodeUnresolvedToken, fmt.Sprintf("%s[%d]", field, i),
					fmt.Sprintf("unresolved token in argument: %s", strArg))
			}
		}
	}
}

// checkPlaintextSecrets detects potential hardcoded secrets.
func (v *RuntimeValidator) checkPlaintextSecrets(field, value string, result *ValidationResult) {
	// Secret patterns to detect
	patterns := []struct {
		pattern *regexp.Regexp
		name    string
	}{
		{regexp.MustCompile(`^ghp_[a-zA-Z0-9]{36}$`), "GitHub personal access token"},
		{regexp.MustCompile(`^gho_[a-zA-Z0-9]{36}$`), "GitHub OAuth token"},
		{regexp.MustCompile(`^ghu_[a-zA-Z0-9]{36}$`), "GitHub user-to-server token"},
		{regexp.MustCompile(`^ghs_[a-zA-Z0-9]{36}$`), "GitHub server-to-server token"},
		{regexp.MustCompile(`^ghr_[a-zA-Z0-9]{36}$`), "GitHub refresh token"},
		{regexp.MustCompile(`^glpat-[a-zA-Z0-9_-]{20,}$`), "GitLab personal access token"},
		{regexp.MustCompile(`^sk-[a-zA-Z0-9]{48}$`), "OpenAI API key"},
		{regexp.MustCompile(`^sk-E[a-zA-Z0-9-]{48}$`), "OpenAI API key"},
		{regexp.MustCompile(`^tvly-[a-zA-Z0-9]{32}$`), "Tavily API key"},
		{regexp.MustCompile(`^AIzaSy[a-zA-Z0-9_-]{33}$`), "Google API key"},
		{regexp.MustCompile(`^GOCSPX-[a-zA-Z0-9_-]+$`), "Google OAuth client secret"},
		{regexp.MustCompile(`^hf_[a-zA-Z0-9]{34}$`), "HuggingFace token"},
	}

	for _, p := range patterns {
		if p.pattern.MatchString(value) {
			result.AddError(CodePlaintextSecret, field,
				fmt.Sprintf("plaintext %s detected", p.name))
			return
		}
	}
}

// resolvePath resolves a path, expanding environment variables.
func (v *RuntimeValidator) resolvePath(path string) string {
	// Expand $HOME or ~
	if strings.HasPrefix(path, "~") {
		path = filepath.Join(v.HomeDir, path[1:])
	} else if strings.HasPrefix(path, "$HOME") {
		path = filepath.Join(v.HomeDir, path[5:])
	}

	// Expand environment variables
	path = os.ExpandEnv(path)

	return path
}
