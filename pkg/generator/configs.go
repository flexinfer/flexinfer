package generator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/templatevars"
	"github.com/crb2nu/loom/pkg/validator"
)

// GenerateConfigs generates MCP client configurations.
// registryPath is used to determine the repo root for resolving ${repo} tokens.
func GenerateConfigs(reg *registry.Registry, outputDir string, targets []string, hubMode bool, hubURL string, loomMode bool, loomBinary string, resolveSecrets bool) error {
	return GenerateConfigsWithPath(reg, "", outputDir, targets, hubMode, hubURL, loomMode, loomBinary, resolveSecrets)
}

func inferRegistryRoot(registryPath string) string {
	if registryPath == "" {
		return ""
	}
	// Typical layout: <root>/mcp/context/registry.yaml
	// We want <root> as the base for resolving relative paths like scripts/...
	contextDir := filepath.Dir(registryPath) // .../mcp/context
	mcpDir := filepath.Dir(contextDir)       // .../mcp
	if filepath.Base(contextDir) == "context" && filepath.Base(mcpDir) == "mcp" {
		return filepath.Dir(mcpDir) // .../<root>
	}
	return filepath.Dir(registryPath)
}

func inferWorkspaceRoot(candidate string) string {
	if candidate == "" {
		return ""
	}
	try := func(dir string) bool {
		if dir == "" {
			return false
		}
		if _, err := os.Stat(filepath.Join(dir, "services", "loom-core")); err != nil {
			return false
		}
		return true
	}
	if try(candidate) {
		return candidate
	}

	// Walk upwards a few levels to handle cases where the registry lives under
	// platform/gitops but ${repo} should point at the monorepo root.
	dir := candidate
	for range 6 {
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
		if try(dir) {
			return dir
		}
	}
	return candidate
}

// GenerateConfigsWithPath generates MCP client configurations with an explicit registry path.
func GenerateConfigsWithPath(reg *registry.Registry, registryPath string, outputDir string, targets []string, hubMode bool, hubURL string, loomMode bool, loomBinary string, resolveSecrets bool) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	if len(targets) == 0 || targets[0] == "all" {
		targets = []string{"codex", "kilocode", "vscode", "claude", "claude_desktop", "gemini", "antigravity"}
	}

	// Resolve repo root from registry path
	workspaceRoot := inferWorkspaceRoot(registry.GetRepoRoot(registryPath))
	registryRoot := inferRegistryRoot(registryPath)

	for _, target := range targets {
		var err error
		switch target {
		case "vscode", "antigravity":
			// VSCode and Antigravity (VSCode fork) use mcp.json format
			err = generateJSONConfig(reg, outputDir, target, hubMode, hubURL, loomMode, loomBinary, workspaceRoot, registryRoot, resolveSecrets)
		case "claude":
			err = generateClaudeConfig(reg, outputDir, hubMode, hubURL, loomMode, loomBinary, workspaceRoot, registryRoot, resolveSecrets)
		case "claude_desktop":
			err = generateClaudeDesktopConfig(reg, outputDir, hubMode, hubURL, loomMode, loomBinary, workspaceRoot, registryRoot, resolveSecrets)
		default:
			// Codex, Kilocode, Gemini use TOML format
			err = generateTomlConfig(reg, outputDir, target, hubMode, hubURL, loomMode, loomBinary, workspaceRoot, registryRoot, resolveSecrets)
		}
		if err != nil {
			return fmt.Errorf("generate %s: %w", target, err)
		}
	}

	// Validate generated configs
	homeDir, _ := os.UserHomeDir()
	v := validator.New(workspaceRoot, homeDir)
	results, err := v.ValidateGenerated(outputDir, targets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: validation check failed: %v\n", err)
	} else {
		for _, result := range results {
			if result.HasErrors() || result.HasWarnings() {
				for _, verr := range result.Errors {
					// Skip plaintext secret warnings when secrets were intentionally resolved
					if resolveSecrets && strings.Contains(verr.Message, "plaintext secret") {
						continue
					}
					if verr.Severity == validator.SeverityError {
						fmt.Fprintf(os.Stderr, "ERROR [%s] %s: %s\n", result.Target, verr.Field, verr.Message)
					} else {
						fmt.Fprintf(os.Stderr, "WARN  [%s] %s: %s\n", result.Target, verr.Field, verr.Message)
					}
				}
			}
		}
	}

	return nil
}

func buildTargetMap(reg *registry.Registry, target string, hubMode bool, hubURL string, profile string, loomMode bool, loomBinary string, workspaceRoot string, registryRoot string, resolveSecrets bool) (map[string]*registry.TargetSpec, error) {
	if loomMode {
		cmd := loomBinary
		if cmd == "" {
			cmd = "loom"
		}
		return map[string]*registry.TargetSpec{
			"loom": {
				Description: "Loom MCP proxy - unified access to all servers",
				Command:     cmd,
				Args:        []any{"proxy"},
				Hint:        "network",
				Timeout:     300,
				AlwaysAllow: []string{"*"},
				Type:        "stdio",
			},
		}, nil
	}

	resolved := make(map[string]*registry.TargetSpec)
	repoPath := workspaceRoot // Use provided workspace root instead of cwd

	// Create expander lazily if secrets resolution is requested
	var expander *templatevars.Expander
	if resolveSecrets {
		expander = templatevars.New(
			templatevars.WithRegistry(reg),
			templatevars.WithLazySecrets(),
		)
	}

	for _, server := range reg.Servers {
		spec, err := reg.GetServerSpec(server.Name, target)
		if err != nil {
			continue // Skip if not found (shouldn't happen with GetServerSpec logic)
		}

		// Resolve path tokens (${repo}, ${HOME})
		spec.Command = ResolveCommand(spec.Command, repoPath, registryRoot, "local")
		resolvedArgs := ResolveArgs(spec.Args, repoPath, registryRoot, "local")
		spec.Args = make([]any, len(resolvedArgs))
		for i, v := range resolvedArgs {
			spec.Args[i] = v
		}
		newEnv := make(map[string]string)
		for k, v := range spec.Env {
			newEnv[k] = ResolveTokens(v, repoPath, "local")
		}
		spec.Env = newEnv

		// Resolve secret templates if requested (for platforms that can't resolve at runtime)
		if resolveSecrets && expander != nil {
			spec.Env = expander.ExpandMap(spec.Env)
			for i, arg := range spec.Args {
				if s, ok := arg.(string); ok {
					spec.Args[i] = expander.Expand(s)
				}
			}
		}

		if hubMode && !server.IsLocalOnly() {
			// Convert to hub mode
			spec = convertToHubMode(spec, server.Name, hubURL, profile, workspaceRoot, registryRoot)
		}

		if spec.Command != "" {
			resolved[server.Name] = spec
		}
	}
	return resolved, nil
}

func convertToHubMode(spec *registry.TargetSpec, serverName, hubURL, profile string, workspaceRoot string, registryRoot string) *registry.TargetSpec {
	// Use mcp-hub-wrapper
	// We assume it's in the PATH or we resolve it. For now, just use "mcp-hub-wrapper"
	// The Python script had complex resolution logic. We can simplify or assume it's installed.

	wrapper := "mcp-hub-wrapper"
	localWrapper := resolvePathLike("scripts/mcp/hub_wrapper.sh", workspaceRoot, registryRoot, "local")
	if _, err := os.Stat(localWrapper); err == nil {
		wrapper = localWrapper
	}

	return &registry.TargetSpec{
		Description: spec.Description,
		Command:     wrapper,
		Args:        []any{serverName, "--profile", profile, "--hub-url", hubURL},
		Env:         spec.Env,
		Hint:        spec.Hint,
		Timeout:     spec.Timeout,
		AlwaysAllow: spec.AlwaysAllow,
		Type:        spec.Type,
	}
}

func generateClaudeConfig(reg *registry.Registry, outputDir string, hubMode bool, hubURL string, loomMode bool, loomBinary string, workspaceRoot string, registryRoot string, resolveSecrets bool) error {
	return generateJSONConfig(reg, outputDir, "claude", hubMode, hubURL, loomMode, loomBinary, workspaceRoot, registryRoot, resolveSecrets)
}

// generateJSONConfig generates mcp.json format configs for vscode and claude targets
// Uses "mcpServers" as root key per Claude Code CLI specification
func generateJSONConfig(reg *registry.Registry, outputDir string, target string, hubMode bool, hubURL string, loomMode bool, loomBinary string, workspaceRoot string, registryRoot string, resolveSecrets bool) error {
	// Use the actual target from registry (claude, vscode, etc.)
	// The registry.GetServerSpec() will fall back to common config if target not found
	targets, err := buildTargetMap(reg, target, hubMode, hubURL, target, loomMode, loomBinary, workspaceRoot, registryRoot, resolveSecrets)
	if err != nil {
		return err
	}

	type JSONServer struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env,omitempty"`
	}

	// Claude Code CLI expects "mcpServers" as the root key
	config := map[string]map[string]JSONServer{"mcpServers": {}}
	for name, spec := range targets {
		args := []string{}
		for _, a := range spec.Args {
			args = append(args, fmt.Sprintf("%v", a))
		}

		config["mcpServers"][name] = JSONServer{
			Command: spec.Command,
			Args:    args,
			Env:     spec.Env,
		}
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	destDir := filepath.Join(outputDir, target)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	perm := os.FileMode(0644)
	if resolveSecrets {
		perm = 0600
		fmt.Fprintf(os.Stderr, "Note: resolved secret templates for %s (file contains sensitive values)\n", target)
	}
	return os.WriteFile(filepath.Join(destDir, "mcp.json"), data, perm)
}

func generateClaudeDesktopConfig(reg *registry.Registry, outputDir string, hubMode bool, hubURL string, loomMode bool, loomBinary string, workspaceRoot string, registryRoot string, resolveSecrets bool) error {
	// Claude Desktop uses claude_desktop target, falling back to common config
	targets, err := buildTargetMap(reg, "claude_desktop", hubMode, hubURL, "claude_desktop", loomMode, loomBinary, workspaceRoot, registryRoot, resolveSecrets)
	if err != nil {
		return err
	}

	type ClaudeServer struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env,omitempty"`
	}

	config := map[string]map[string]ClaudeServer{"mcpServers": {}}
	for name, spec := range targets {
		args := []string{}
		for _, a := range spec.Args {
			args = append(args, fmt.Sprintf("%v", a))
		}

		config["mcpServers"][name] = ClaudeServer{
			Command: spec.Command,
			Args:    args,
			Env:     spec.Env,
		}
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	destDir := filepath.Join(outputDir, "claude_desktop")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	perm := os.FileMode(0644)
	if resolveSecrets {
		perm = 0600
		fmt.Fprintf(os.Stderr, "Note: resolved secret templates for claude_desktop (file contains sensitive values)\n")
	}
	return os.WriteFile(filepath.Join(destDir, "claude_desktop_config.json"), data, perm)
}

func generateTomlConfig(reg *registry.Registry, outputDir, target string, hubMode bool, hubURL string, loomMode bool, loomBinary string, workspaceRoot string, registryRoot string, resolveSecrets bool) error {
	targets, err := buildTargetMap(reg, target, hubMode, hubURL, target, loomMode, loomBinary, workspaceRoot, registryRoot, resolveSecrets)
	if err != nil {
		return err
	}

	var sb strings.Builder
	if resolveSecrets {
		sb.WriteString("# WARNING: Secret templates resolved to literal values.\n")
		sb.WriteString(fmt.Sprintf("# %s does not support template syntax.\n", target))
		sb.WriteString(fmt.Sprintf("# To regenerate: loom sync %s --regen\n", target))
	}
	sb.WriteString(fmt.Sprintf("# Generated MCP configuration for %s\n", target))
	sb.WriteString("# Source: mcp/context/registry.yaml\n\n")

	// Sort keys for deterministic output
	var names []string
	for name := range targets {
		names = append(names, name)
	}
	sortStrings(names)

	for _, name := range names {
		spec := targets[name]
		if len(spec.AlwaysAllow) == 0 && (target == "codex" || target == "gemini" || target == "kilocode") {
			spec.AlwaysAllow = []string{"*"}
		}
		sb.WriteString(fmt.Sprintf("[mcp_servers.%s]\n", name))
		sb.WriteString(fmt.Sprintf("command = %q\n", spec.Command))

		argsJSON, _ := json.Marshal(spec.Args)
		sb.WriteString(fmt.Sprintf("args = %s\n", string(argsJSON)))

		if spec.Description != "" {
			sb.WriteString(fmt.Sprintf("description = %q\n", spec.Description))
		}
		if spec.Hint != "" {
			sb.WriteString(fmt.Sprintf("hint = %q\n", spec.Hint))
		}
		if spec.Timeout > 0 {
			sb.WriteString(fmt.Sprintf("timeout = %d\n", spec.Timeout))
		}
		if len(spec.AlwaysAllow) > 0 {
			allowJSON, _ := json.Marshal(spec.AlwaysAllow)
			sb.WriteString(fmt.Sprintf("always_allow = %s\n", string(allowJSON)))
		}

		if len(spec.Env) > 0 {
			sb.WriteString(fmt.Sprintf("[mcp_servers.%s.env]\n", name))

			// Sort env keys
			var envKeys []string
			for k := range spec.Env {
				envKeys = append(envKeys, k)
			}
			sortStrings(envKeys)

			for _, k := range envKeys {
				sb.WriteString(fmt.Sprintf("%s = %q\n", k, spec.Env[k]))
			}
		}
		sb.WriteString("\n")
	}

	destDir := filepath.Join(outputDir, target)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	// Use restrictive permissions when secrets are resolved
	perm := os.FileMode(0644)
	if resolveSecrets {
		perm = 0600
		fmt.Fprintf(os.Stderr, "Note: resolved secret templates for %s (file contains sensitive values)\n", target)
	}
	return os.WriteFile(filepath.Join(destDir, "config.toml"), []byte(sb.String()), perm)
}

func sortStrings(s []string) {
	sort.Strings(s)
}
