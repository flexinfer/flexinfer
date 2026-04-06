package generator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/validator"
)

// generateJSONConfig generates mcp.json format configs for vscode and claude targets
// Uses "mcpServers" as root key per Claude Code CLI specification
// generateJSONConfig generates a JSON MCP configuration file.
// Uses profile data to determine the root key, config filename, timeout support,
// command format, and environment key — eliminating per-platform special cases.
func generateJSONConfig(p *GenerateParams) error {
	targets, err := p.buildTargets()
	if err != nil {
		return err
	}

	profile := p.Profile

	// OpenCode uses array-style commands, "environment" key, and millisecond timeouts.
	if profile.Features.CommandFormat == "array" {
		return generateOpenCodeJSONConfig(p, targets)
	}

	// Standard JSON: separate command/args, configurable root key and timeout.
	rootKey := profile.ConfigRoot
	if rootKey == "" {
		rootKey = "mcpServers"
	}

	servers := make(map[string]any)
	for name, spec := range targets {
		args := make([]string, 0, len(spec.Args))
		for _, a := range spec.Args {
			args = append(args, fmt.Sprintf("%v", a))
		}

		server := map[string]any{
			"command": spec.Command,
			"args":    args,
		}
		if len(spec.Env) > 0 {
			server[profile.Features.EnvKey] = spec.Env
		}
		if spec.Timeout > 0 && profile.Features.SupportsTimeout {
			field := profile.Features.TimeoutField
			if field == "" {
				field = "timeout"
			}
			server[field] = spec.Timeout
		}
		servers[name] = server
	}

	config := map[string]any{rootKey: servers}

	// Annotate proxy-enforced policies in JSON configs via a metadata field.
	if summaries := PlatformPolicySummaries(profile.Hooks); len(summaries) > 0 {
		policyMeta := make(map[string]string, len(summaries))
		for _, s := range summaries {
			policyMeta[s.PolicyRef] = s.Description
		}
		config["_loom_policy"] = policyMeta
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	destDir := p.destDir()
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	configFile := profile.ConfigFile
	if configFile == "" {
		configFile = "mcp.json"
	}
	return os.WriteFile(filepath.Join(destDir, configFile), append(data, '\n'), p.filePerm())
}

// generateOpenCodeJSONConfig handles OpenCode's unique JSON format:
// array commands, "environment" key, millisecond timeouts, and "type": "local" field.
func generateOpenCodeJSONConfig(p *GenerateParams, targets map[string]*registry.TargetSpec) error {
	type OpenCodeServer struct {
		Type        string            `json:"type"`
		Command     []string          `json:"command"`
		Environment map[string]string `json:"environment,omitempty"`
		Enabled     bool              `json:"enabled"`
		Timeout     int               `json:"timeout,omitempty"`
	}

	mcpServers := make(map[string]OpenCodeServer)
	for name, spec := range targets {
		cmd := []string{spec.Command}
		for _, a := range spec.Args {
			cmd = append(cmd, fmt.Sprintf("%v", a))
		}

		server := OpenCodeServer{
			Type:    "local",
			Command: cmd,
			Enabled: true,
		}
		if len(spec.Env) > 0 {
			server.Environment = spec.Env
		}
		if spec.Timeout > 0 {
			server.Timeout = spec.Timeout * 1000 // OpenCode uses milliseconds
		}
		mcpServers[name] = server
	}

	rootKey := p.Profile.ConfigRoot
	if rootKey == "" {
		rootKey = "mcp"
	}

	config := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		rootKey:   mcpServers,
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	destDir := p.destDir()
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	configFile := p.Profile.ConfigFile
	if configFile == "" {
		configFile = "opencode.json"
	}
	return os.WriteFile(filepath.Join(destDir, configFile), append(data, '\n'), p.filePerm())
}

// generateTomlConfig generates a TOML MCP configuration file.
// Uses profile features to determine timeout field name, description support,
// and preamble requirements — eliminating per-platform target name checks.
func generateTomlConfig(p *GenerateParams) error {
	targets, err := p.buildTargets()
	if err != nil {
		return err
	}

	profile := p.Profile
	target := p.Target

	var sb strings.Builder
	if p.ResolveSecrets {
		sb.WriteString("# WARNING: Secret templates resolved to literal values.\n")
		sb.WriteString(fmt.Sprintf("# %s does not support template syntax.\n", target))
		sb.WriteString(fmt.Sprintf("# To regenerate: loom sync %s --regen\n", target))
	}
	sb.WriteString(fmt.Sprintf("# Generated MCP configuration for %s\n", target))
	sb.WriteString("# Source: mcp/context/registry.yaml\n")

	// Platforms with requires_preamble get a config preamble (e.g., Codex's
	// approval_policy, features, sandbox_mode, and notify hook) which includes
	// its own policy comment — skip the duplicate here.
	if profile.Features.RequiresPreamble {
		sb.WriteString("\n")
		emitCodexPreamble(&sb, p.Reg, p.WorkspaceRoot, p.LoomBinary)
	} else {
		// Emit policy enforcement annotations for platforms with policy refs.
		if comment := FormatPolicyComment(profile.Hooks, "# "); comment != "" {
			sb.WriteString(comment)
		}
		sb.WriteString("\n")
	}

	// Sort keys for deterministic output
	var names []string
	for name := range targets {
		names = append(names, name)
	}
	sortStrings(names)

	for _, name := range names {
		spec := targets[name]
		// TOML platforms that lack fine-grained permissions default to allow-all.
		if len(spec.AlwaysAllow) == 0 && !profile.Capabilities.Permissions {
			spec.AlwaysAllow = []string{"*"}
		}
		sb.WriteString(fmt.Sprintf("[mcp_servers.%s]\n", name))
		sb.WriteString(fmt.Sprintf("command = %q\n", spec.Command))

		argsJSON, _ := json.Marshal(spec.Args)
		sb.WriteString(fmt.Sprintf("args = %s\n", string(argsJSON)))

		// Emit optional fields based on profile feature support.
		if profile.Features.SupportsDescription {
			if spec.Description != "" {
				sb.WriteString(fmt.Sprintf("description = %q\n", spec.Description))
			}
			if spec.Hint != "" {
				sb.WriteString(fmt.Sprintf("hint = %q\n", spec.Hint))
			}
		}

		if spec.Timeout > 0 && profile.Features.SupportsTimeout {
			field := profile.Features.TimeoutField
			if field == "" {
				field = "timeout"
			}
			sb.WriteString(fmt.Sprintf("%s = %d\n", field, spec.Timeout))
		}

		// Emit always_allow for platforms that support it (e.g. Kilocode).
		// Codex does NOT support always_allow on MCP server entries — tool
		// approval is controlled by approval_policy (granular.mcp_elicitations).
		if profile.Features.SupportsDescription && !profile.Features.RequiresPreamble && len(spec.AlwaysAllow) > 0 {
			allowJSON, _ := json.Marshal(spec.AlwaysAllow)
			sb.WriteString(fmt.Sprintf("always_allow = %s\n", string(allowJSON)))
		}

		if len(spec.Env) > 0 {
			sb.WriteString(fmt.Sprintf("[mcp_servers.%s.env]\n", name))

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

	destDir := p.destDir()
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	configFile := profile.ConfigFile
	if configFile == "" {
		configFile = "config.toml"
	}
	configPath := filepath.Join(destDir, configFile)
	content := []byte(sb.String())

	if err := os.WriteFile(configPath, content, p.filePerm()); err != nil {
		return err
	}

	// Validate Codex configs against upstream schema.
	if profile.Features.RequiresPreamble {
		result := validator.ValidateCodexConfig(configPath, content)
		if result != nil {
			if !result.HasErrors() && !result.HasWarnings() {
				fmt.Fprintf(os.Stderr, "  [%s] config.toml validated against upstream schema\n", target)
			}
			for _, verr := range result.Errors {
				fmt.Fprintf(os.Stderr, "WARN  [%s] upstream schema: %s - %s\n", target, verr.Field, verr.Message)
			}
		}

		// Warn about unresolved template patterns that the target cannot resolve at runtime.
		if n := WarnUnresolvedTemplates(string(content), target); n > 0 {
			fmt.Fprintf(os.Stderr, "WARN  [%s] %d unresolved template(s) in generated config — %s does not resolve ${env:...} syntax\n", target, n, target)
		}
	}

	return nil
}

// --- Agent lifecycle hook generation ---

// generateHooksConfig writes a settings.json with lifecycle hooks for platforms
// that support them. The hooks call `loom agent` subcommands to ensure
// consistent session tracking and presence management.
func generateHooksConfig(reg *registry.Registry, outputDir, target string, profile *PlatformProfile, loomBinary string) error {
	if profile == nil || !profile.Hooks.Enabled {
		return nil // Platform doesn't support hooks.
	}

	// TypeScript plugin-based hooks (e.g. OpenCode).
	if profile.Hooks.Type == "typescript" {
		return generateOpenCodeHooksPlugin(outputDir)
	}

	// Skip platforms whose hooks are embedded in their main config file
	// rather than a separate settings.json (e.g. Codex notify in config.toml).
	if profile.Hooks.File != "settings.json" && profile.Hooks.File != "" {
		return nil
	}

	var config map[string]any
	switch target {
	case "claude":
		config = claudeHooksConfig(reg, profile, loomBinary)
	case "gemini":
		config = geminiHooksConfigFromRegistry(reg, profile, loomBinary)
	default:
		// Generic JSON hooks stub for platforms with hooks.enabled but no
		// platform-specific wrapper (future platforms).
		config = hooksConfigFromProfile(reg, profile, loomBinary)
	}

	destDir := filepath.Join(outputDir, target)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	// Use Encoder with SetEscapeHTML(false) to avoid \u003e for > and \u0026 for &
	// in shell commands. The escaped form is valid JSON but makes hook commands unreadable.
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(config); err != nil {
		return err
	}

	settingsPath := filepath.Join(destDir, "settings.json")
	content := []byte(buf.String())

	if err := os.WriteFile(settingsPath, content, 0644); err != nil {
		return err
	}

	// Validate generated settings against upstream schema.
	validateSettingsAgainstUpstream(target, settingsPath, content)

	return nil
}

// validateSettingsAgainstUpstream validates a generated settings file against
// the vendored upstream JSON schema. Validation failures are reported as
// non-blocking warnings to stderr.
func validateSettingsAgainstUpstream(target, filePath string, content []byte) {
	var result *validator.ValidationResult

	switch target {
	case "claude":
		result = validator.ValidateClaudeSettings(filePath, content)
	case "gemini":
		result = validator.ValidateGeminiSettings(filePath, content)
	default:
		return
	}

	if result == nil {
		return
	}

	if !result.HasErrors() && !result.HasWarnings() {
		fmt.Fprintf(os.Stderr, "  [%s] settings.json validated against upstream schema\n", target)
		return
	}

	for _, verr := range result.Errors {
		fmt.Fprintf(os.Stderr, "WARN  [%s] upstream schema: %s - %s\n", target, verr.Field, verr.Message)
	}
}
