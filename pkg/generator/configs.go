package generator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/crb2nu/loom/pkg/registry"
)

// GenerateConfigs generates MCP client configurations.
func GenerateConfigs(reg *registry.Registry, outputDir string, targets []string, hubMode bool, hubURL string, loomMode bool, loomBinary string) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	if len(targets) == 0 || targets[0] == "all" {
		targets = []string{"codex", "kilocode", "vscode", "claude", "claude_desktop", "gemini", "antigravity"}
	}

	for _, target := range targets {
		var err error
		switch target {
		case "vscode":
			err = generateVSCodeConfig(reg, outputDir, hubMode, hubURL, loomMode, loomBinary)
		case "claude":
			err = generateClaudeConfig(reg, outputDir, hubMode, hubURL, loomMode, loomBinary)
		case "claude_desktop":
			err = generateClaudeDesktopConfig(reg, outputDir, hubMode, hubURL, loomMode, loomBinary)
		default:
			err = generateTomlConfig(reg, outputDir, target, hubMode, hubURL, loomMode, loomBinary)
		}
		if err != nil {
			return fmt.Errorf("generate %s: %w", target, err)
		}
	}

	return nil
}

func buildTargetMap(reg *registry.Registry, target string, hubMode bool, hubURL string, profile string, loomMode bool, loomBinary string) (map[string]*registry.TargetSpec, error) {
	if loomMode {
		return map[string]*registry.TargetSpec{
			"loom": {
				Description: "Loom MCP proxy - unified access to all servers",
				Command:     loomBinary,
				Args:        []any{"proxy"},
				Hint:        "network",
				Timeout:     300,
				Type:        "stdio",
			},
		}, nil
	}

	resolved := make(map[string]*registry.TargetSpec)
	repoPath, _ := os.Getwd() // Assuming running from repo root

	for _, server := range reg.Servers {
		spec, err := reg.GetServerSpec(server.Name, target)
		if err != nil {
			continue // Skip if not found (shouldn't happen with GetServerSpec logic)
		}

		// Resolve tokens
		spec.Command = ResolveCommand(spec.Command, repoPath, "local")
		resolvedArgs := ResolveArgs(spec.Args, repoPath, "local")
		spec.Args = make([]any, len(resolvedArgs))
		for i, v := range resolvedArgs {
			spec.Args[i] = v
		}
		newEnv := make(map[string]string)
		for k, v := range spec.Env {
			newEnv[k] = ResolveTokens(v, repoPath, "local")
		}
		spec.Env = newEnv

		if hubMode && !server.IsLocalOnly() {
			// Convert to hub mode
			spec = convertToHubMode(spec, server.Name, hubURL, profile)
		}

		if spec.Command != "" {
			resolved[server.Name] = spec
		}
	}
	return resolved, nil
}

func convertToHubMode(spec *registry.TargetSpec, serverName, hubURL, profile string) *registry.TargetSpec {
	// Use mcp-hub-wrapper
	// We assume it's in the PATH or we resolve it. For now, just use "mcp-hub-wrapper"
	// The Python script had complex resolution logic. We can simplify or assume it's installed.

	wrapper := "mcp-hub-wrapper"
	// Check if wrapper exists in scripts/mcp/hub_wrapper.sh
	cwd, _ := os.Getwd()
	localWrapper := filepath.Join(cwd, "scripts", "mcp", "hub_wrapper.sh")
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

func generateVSCodeConfig(reg *registry.Registry, outputDir string, hubMode bool, hubURL string, loomMode bool, loomBinary string) error {
	return generateJSONConfig(reg, outputDir, "vscode", hubMode, hubURL, loomMode, loomBinary)
}

func generateClaudeConfig(reg *registry.Registry, outputDir string, hubMode bool, hubURL string, loomMode bool, loomBinary string) error {
	return generateJSONConfig(reg, outputDir, "claude", hubMode, hubURL, loomMode, loomBinary)
}

// generateJSONConfig generates mcp.json format configs for vscode and claude targets
func generateJSONConfig(reg *registry.Registry, outputDir string, target string, hubMode bool, hubURL string, loomMode bool, loomBinary string) error {
	// Use the actual target from registry (claude, vscode, etc.)
	// The registry.GetServerSpec() will fall back to common config if target not found
	targets, err := buildTargetMap(reg, target, hubMode, hubURL, target, loomMode, loomBinary)
	if err != nil {
		return err
	}

	type JSONServer struct {
		ID      string            `json:"id"`
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env,omitempty"`
	}

	config := map[string]map[string]JSONServer{"servers": {}}
	for name, spec := range targets {
		args := []string{}
		for _, a := range spec.Args {
			args = append(args, fmt.Sprintf("%v", a))
		}

		srvType := spec.Type
		if srvType == "" {
			srvType = "stdio"
		}

		config["servers"][name] = JSONServer{
			ID:      name,
			Type:    srvType,
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
	return os.WriteFile(filepath.Join(destDir, "mcp.json"), data, 0644)
}

func generateClaudeDesktopConfig(reg *registry.Registry, outputDir string, hubMode bool, hubURL string, loomMode bool, loomBinary string) error {
	// Claude Desktop uses claude_desktop target, falling back to common config
	targets, err := buildTargetMap(reg, "claude_desktop", hubMode, hubURL, "claude_desktop", loomMode, loomBinary)
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
	return os.WriteFile(filepath.Join(destDir, "claude_desktop_config.json"), data, 0644)
}

func generateTomlConfig(reg *registry.Registry, outputDir, target string, hubMode bool, hubURL string, loomMode bool, loomBinary string) error {
	targets, err := buildTargetMap(reg, target, hubMode, hubURL, target, loomMode, loomBinary)
	if err != nil {
		return err
	}

	var sb strings.Builder
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
	return os.WriteFile(filepath.Join(destDir, "config.toml"), []byte(sb.String()), 0644)
}

func sortStrings(s []string) {
	sort.Strings(s)
}
