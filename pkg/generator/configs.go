package generator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/templatevars"
	"github.com/crb2nu/loom/pkg/validator"
)

func normalizeLoomBinary(loomBinary string) string {
	if strings.TrimSpace(loomBinary) == "" {
		return "loom"
	}
	return strings.TrimSpace(loomBinary)
}

func isExecutableFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.Mode().IsRegular() {
		return false
	}
	return info.Mode()&0o111 != 0
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

const (
	defaultHubWrapperCommand      = "mcp-hub-wrapper"
	hubWrapperOverrideEnv         = "LOOM_MCP_HUB_WRAPPER"
	hubWrapperHealthCheckTimeout  = 3 * time.Second
	hubWrapperLegacyRelativePath  = "scripts/mcp/hub_wrapper.sh"
	hubWrapperWorkspaceBinaryPath = "services/loom-core/bin/mcp-hub-wrapper"
)

var (
	hubWrapperLookPath      = exec.LookPath
	hubWrapperCommandRunner = exec.CommandContext
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

// InferWorkspaceRoot walks up from candidate looking for the workspace root
// (identified by a services/loom-core subdirectory). Returns candidate as
// fallback if no match is found.
func InferWorkspaceRoot(candidate string) string {
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
// GenerateParams bundles the parameters that all config generators share.
// Reduces 10-parameter function signatures to a single struct.
type GenerateParams struct {
	Reg            *registry.Registry
	OutputDir      string
	Target         string
	Profile        *PlatformProfile
	HubMode        bool
	HubURL         string
	LoomMode       bool
	LoomBinary     string
	WorkspaceRoot  string
	RegistryRoot   string
	ResolveSecrets bool
}

// buildTargets resolves the MCP server specs for this target.
func (p *GenerateParams) buildTargets() (map[string]*registry.TargetSpec, error) {
	return buildTargetMap(p.Reg, p.Target, p.Profile, p.HubMode, p.HubURL, p.LoomMode, p.LoomBinary, p.WorkspaceRoot, p.RegistryRoot, p.ResolveSecrets)
}

// destDir returns the platform-specific output directory.
func (p *GenerateParams) destDir() string {
	return filepath.Join(p.OutputDir, p.Target)
}

// filePerm returns the output file permission (restrictive when secrets are resolved).
func (p *GenerateParams) filePerm() os.FileMode {
	if p.ResolveSecrets {
		fmt.Fprintf(os.Stderr, "Note: resolved secret templates for %s (file contains sensitive values)\n", p.Target)
		return 0600
	}
	return 0644
}

func GenerateConfigsWithPath(reg *registry.Registry, registryPath string, outputDir string, targets []string, hubMode bool, hubURL string, loomMode bool, loomBinary string, resolveSecrets bool) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	if len(targets) == 0 || targets[0] == "all" {
		targets = AllPlatformNames()
	}

	// Resolve repo root from registry path
	workspaceRoot := InferWorkspaceRoot(registry.GetRepoRoot(registryPath))
	registryRoot := inferRegistryRoot(registryPath)

	for _, target := range targets {
		profile, profileErr := GetPlatformProfile(target)
		if profileErr != nil {
			return fmt.Errorf("unknown target %q: %w", target, profileErr)
		}

		params := &GenerateParams{
			Reg:            reg,
			OutputDir:      outputDir,
			Target:         target,
			Profile:        profile,
			HubMode:        hubMode,
			HubURL:         hubURL,
			LoomMode:       loomMode,
			LoomBinary:     loomBinary,
			WorkspaceRoot:  workspaceRoot,
			RegistryRoot:   registryRoot,
			ResolveSecrets: resolveSecrets,
		}

		var err error
		switch profile.ConfigFormat {
		case "json":
			err = generateJSONConfig(params)
		case "toml":
			err = generateTomlConfig(params)
		default:
			err = fmt.Errorf("unsupported config format %q for %s", profile.ConfigFormat, target)
		}
		if err != nil {
			return fmt.Errorf("generate %s: %w", target, err)
		}

		// Generate lifecycle hook configs for platforms that support them.
		if err := generateHooksConfig(reg, outputDir, target, profile, loomBinary); err != nil {
			return fmt.Errorf("generate hooks for %s: %w", target, err)
		}
	}

	// Emit sandbox policy advisory file if defined in registry.
	if reg.SandboxPolicy != nil {
		if err := emitSandboxPolicy(reg.SandboxPolicy, outputDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: sandbox policy emission failed: %v\n", err)
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

func buildTargetMap(reg *registry.Registry, target string, profile *PlatformProfile, hubMode bool, hubURL string, loomMode bool, loomBinary string, workspaceRoot string, registryRoot string, resolveSecrets bool) (map[string]*registry.TargetSpec, error) {
	if loomMode {
		cmd := normalizeLoomBinary(loomBinary)
		args := []any{"proxy"}
		// Apply proxy args from profile (agent-hint, tool-profile, max-tools).
		if profile != nil {
			lp := profile.LoomProxy
			if lp.AgentHint != "" {
				args = append(args, "--agent-hint", lp.AgentHint)
			}
			if lp.ToolProfile != "" {
				args = append(args, "--tool-profile", lp.ToolProfile)
			}
			if lp.MaxTools > 0 {
				args = append(args, "--max-tools", fmt.Sprintf("%d", lp.MaxTools))
			}
		}
		return map[string]*registry.TargetSpec{
			"loom": {
				Description: "Loom MCP proxy - unified access to all servers",
				Command:     cmd,
				Args:        args,
				Hint:        "network",
				Timeout:     600,
				AlwaysAllow: []string{"*"},
				Type:        "stdio",
			},
		}, nil
	}

	resolved := make(map[string]*registry.TargetSpec)
	repoPath := workspaceRoot // Use provided workspace root instead of cwd
	hubWrapper := ""

	if hubMode {
		var err error
		hubWrapper, err = resolveHubWrapper(workspaceRoot, registryRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve hub wrapper: %w", err)
		}
	}

	// Create expander lazily if secrets resolution is requested
	var expander *templatevars.Expander
	if resolveSecrets {
		expander = templatevars.New(
			templatevars.WithRegistry(reg),
			templatevars.WithLazySecrets(),
		)
	}

	// Load catalog state to filter disabled servers
	catalogState, _ := registry.LoadCatalogState()

	for _, server := range reg.Servers {
		if catalogState != nil && catalogState.IsDisabled(server.Name) {
			continue
		}

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
			spec = convertToHubMode(spec, server.Name, hubURL, target, hubWrapper)
		}

		if spec.Command != "" {
			resolved[server.Name] = spec
		}
	}
	return resolved, nil
}

func convertToHubMode(spec *registry.TargetSpec, serverName, hubURL, profile string, wrapper string) *registry.TargetSpec {
	return &registry.TargetSpec{
		Description: spec.Description,
		Command:     strings.TrimSpace(wrapper),
		Args:        []any{serverName, "--profile", profile, "--hub-url", hubURL},
		Env:         spec.Env,
		Hint:        spec.Hint,
		Timeout:     spec.Timeout,
		AlwaysAllow: spec.AlwaysAllow,
		Type:        spec.Type,
	}
}

func resolveHubWrapper(workspaceRoot string, registryRoot string) (string, error) {
	candidates := hubWrapperCandidates(workspaceRoot, registryRoot)
	seen := make(map[string]struct{}, len(candidates))
	failures := make([]string, 0, len(candidates))

	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}

		resolved, err := resolveWrapperExecutable(candidate)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s (%v)", candidate, err))
			continue
		}

		if err := probeHubWrapper(resolved); err != nil {
			failures = append(failures, fmt.Sprintf("%s (%v)", resolved, err))
			continue
		}
		return resolved, nil
	}

	return "", fmt.Errorf("no healthy hub wrapper found (candidates tried: %s)", strings.Join(failures, "; "))
}

func hubWrapperCandidates(workspaceRoot string, registryRoot string) []string {
	candidates := []string{}

	if override := strings.TrimSpace(os.Getenv(hubWrapperOverrideEnv)); override != "" {
		candidates = append(candidates, override)
	}

	if workspaceRoot != "" {
		candidates = append(candidates, filepath.Join(workspaceRoot, hubWrapperWorkspaceBinaryPath))
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", defaultHubWrapperCommand))
	}

	candidates = append(candidates, defaultHubWrapperCommand)

	legacy := resolvePathLike(hubWrapperLegacyRelativePath, workspaceRoot, registryRoot, "local")
	if legacy != "" {
		candidates = append(candidates, legacy)
	}

	return candidates
}

func resolveWrapperExecutable(candidate string) (string, error) {
	if strings.Contains(candidate, string(filepath.Separator)) || filepath.IsAbs(candidate) {
		if !isExecutableFile(candidate) {
			return "", fmt.Errorf("not executable")
		}
		return candidate, nil
	}
	resolved, err := hubWrapperLookPath(candidate)
	if err != nil {
		return "", err
	}
	if !isExecutableFile(resolved) {
		return "", fmt.Errorf("resolved path is not executable: %s", resolved)
	}
	return resolved, nil
}

func probeHubWrapper(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), hubWrapperHealthCheckTimeout)
	defer cancel()

	cmd := hubWrapperCommandRunner(ctx, path, "--help")
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}

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
	sb.WriteString("# Source: mcp/context/registry.yaml\n\n")

	// Platforms with requires_preamble get a config preamble (e.g., Codex's
	// approval_policy, features, sandbox_mode, and notify hook).
	if profile.Features.RequiresPreamble {
		emitCodexPreamble(&sb, p.Reg, p.WorkspaceRoot, p.LoomBinary)
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

		// Always-allow only emitted when platform supports description (proxy for
		// "has extended TOML schema"). Platforms with strict schemas skip this.
		if profile.Features.SupportsDescription && len(spec.AlwaysAllow) > 0 {
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
	}

	return nil
}

func sortStrings(s []string) {
	sort.Strings(s)
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

// claudeHooksConfig returns a Claude Code settings.json with lifecycle hooks,
// policy-driven guardrails, and default-allow permissions. Permissions are read
// from the registry's platform_permissions.claude section; hook shape remains in
// Go while the guarded command data comes from shared registry policy.
func claudeHooksConfig(reg *registry.Registry, profile *PlatformProfile, loomBinary string) map[string]any {
	return map[string]any{
		"$schema":     "https://json.schemastore.org/claude-code-settings.json",
		"permissions": claudePermissions(reg),
		"hooks":       claudeHooks(reg, profile, loomBinary),
	}
}

// buildPlatformHooks generates the shared SessionStart / session-end / heartbeat
// hooks for any platform that supports lifecycle hooks. Platform-specific extras
// (e.g. policy-driven PreToolUse guardrails) are appended by the caller.
// Hook parameters are read from the platform profile's HookProfile.
func buildPlatformHooks(reg *registry.Registry, hp HookProfile, loomBinary string) map[string]any {
	log := `2>>"${TMPDIR:-/tmp}/loom-agent-hooks.log"`
	bootstrap := hookAgentIDBootstrap(hp.AgentID)
	staleCleanup := hookStaleCleanup()
	loomCmd := shellQuote(normalizeLoomBinary(loomBinary))
	policy := agentSafetyPolicyFromRegistry(reg)

	sessionStartHooks := []map[string]any{
		{
			"type": "command",
			"command": fmt.Sprintf(
				`INPUT=$(cat); %s; %s; PARENT_FLAG=""; [ -n "${LOOM_PARENT_SESSION_ID:-}" ] && PARENT_FLAG="--parent-session-id $LOOM_PARENT_SESSION_ID"; %s agent session-start --namespace "$(basename $(git rev-parse --show-toplevel 2>/dev/null || echo ${PWD##*/}))/$(git branch --show-current 2>/dev/null || echo main)" --agent-id "$AGENT_ID" --agent-type %s --description %q --auto-recall --auto-recall-strategy fast $PARENT_FLAG --quiet %s || true`,
				bootstrap, staleCleanup, loomCmd, hp.AgentType, hp.Description, log),
		},
		{
			"type": "command",
			"command": fmt.Sprintf(
				// Let keepalive own its PID file lifecycle so repeated SessionStart hooks
				// (for example after compact/relaunch) do not race old/new helpers.
				`INPUT=$(cat); %s; %s agent keepalive --agent-id "$AGENT_ID" --agent-type %s --quiet </dev/null >/dev/null %s &`,
				bootstrap, loomCmd, hp.AgentType, log),
		},
	}
	if policy.DirtyWorktreeNudgeOnSessionStart {
		sessionStartHooks = append(sessionStartHooks, map[string]any{
			"type":    "command",
			"command": dirtyWorktreeSessionStartNudgeCommand(policy),
		})
	}
	// Suggest worktree allocation when on main/master to avoid dirty-worktree issues.
	sessionStartHooks = append(sessionStartHooks, map[string]any{
		"type":    "command",
		"command": mainBranchWorktreeNudgeCommand(),
	})

	hooks := map[string]any{
		"SessionStart": []map[string]any{
			{
				"hooks": sessionStartHooks,
			},
		},
		hp.SessionEndEvent: []map[string]any{
			{
				"hooks": []map[string]any{
					{
						"type": "command",
						"command": fmt.Sprintf(
							`INPUT=$(cat); %s; PID_FILE="${TMPDIR:-/tmp}/loom-keepalive-${AGENT_ID}.pid"; [ -f "$PID_FILE" ] && kill "$(cat "$PID_FILE")" 2>/dev/null; rm -f "$PID_FILE"; rm -f "$AGENT_ID_FILE"; %s agent session-end --agent-id "$AGENT_ID" --summarize --summary-async --quiet %s || true`,
							bootstrap, loomCmd, log),
					},
				},
			},
		},
		hp.HeartbeatEvent: []map[string]any{
			{
				"matcher": hp.HeartbeatMatcher,
				"hooks": []map[string]any{
					{
						"type": "command",
						"command": fmt.Sprintf(
							`INPUT=$(cat); %s; %s agent heartbeat --agent-id "$AGENT_ID" --status active --ensure-session --infer-namespace --agent-type %s --description %q --quiet %s || true`,
							bootstrap, loomCmd, hp.AgentType, hp.Description, log),
					},
				},
			},
		},
		// Capture parent session ID for subagent session grouping.
		"SubagentStart": []map[string]any{
			{
				"hooks": []map[string]any{
					{
						"type": "command",
						"command": fmt.Sprintf(
							`INPUT=$(cat); %s; PARENT_SID=$(%s agent session --agent-id "$AGENT_ID" --quiet 2>/dev/null | jq -r '.session.id // empty' 2>/dev/null || true); [ -n "$PARENT_SID" ] && export LOOM_PARENT_SESSION_ID="$PARENT_SID"; exit 0`,
							bootstrap, loomCmd),
					},
				},
			},
		},
	}

	return hooks
}

// claudePostToolUseExtras returns the Write/Edit formatter hooks specific to
// Claude Code, appended to the shared heartbeat PostToolUse hooks.
func claudePostToolUseExtras() []map[string]any {
	return []map[string]any{
		{
			"matcher": "Write|Edit",
			"hooks": []map[string]any{
				{
					"type":    "command",
					"command": `jq -r '.tool_input.file_path // ""' | { read f; [[ "$f" == *.py ]] && black "$f" 2>/dev/null; exit 0; }`,
				},
				{
					"type":    "command",
					"command": `jq -r '.tool_input.file_path // ""' | { read f; [[ "$f" == *.go ]] && gofmt -w "$f" 2>/dev/null && goimports -w "$f" 2>/dev/null; exit 0; }`,
				},
				{
					"type":    "command",
					"command": `jq -r '.tool_input.new_string // .tool_input.content // ""' | { read content; if echo "$content" | grep -qE 'image:.*:latest'; then echo '{"systemMessage":"Noticed :latest tag - consider pinning to a specific version for reproducibility."}'; fi; exit 0; }`,
				},
			},
		},
	}
}

// claudePostToolUseTaskSyncHook returns the PostToolUse hook that syncs native
// Claude Code task tools (TaskCreate, TaskUpdate, TodoWrite) to the loom
// agent-context task system via `loom agent task-sync`.
func claudePostToolUseTaskSyncHook(loomBinary string) []map[string]any {
	loomCmd := shellQuote(normalizeLoomBinary(loomBinary))
	return []map[string]any{
		{
			"matcher": "TaskCreate|TaskUpdate|TodoWrite",
			"hooks": []map[string]any{
				{
					"type": "command",
					"command": fmt.Sprintf(
						`INPUT=$(cat); %s; echo "$INPUT" | %s agent task-sync --agent-id "$AGENT_ID" --quiet 2>>"${TMPDIR:-/tmp}/loom-agent-hooks.log" || true`,
						hookAgentIDBootstrap("claude-code"), loomCmd),
				},
			},
		},
	}
}

// claudeHooks returns the hooks block for Claude Code settings.json.
func claudeHooks(reg *registry.Registry, profile *PlatformProfile, loomBinary string) map[string]any {
	hooks := buildPlatformHooks(reg, profile.Hooks, loomBinary)

	// Append shared policy hooks before the remaining profile-specific extras.
	appendHookPolicies(hooks, reg, profile.Hooks.PolicyRefs)

	// Append extras defined in the profile (e.g. postToolUse_formatters, postToolUse_taskSync).
	appendHookExtras(hooks, profile.Hooks.Extras, loomBinary)

	return hooks
}

// appendHookPolicies dispatches shared policy refs to their hook implementations.
func appendHookPolicies(hooks map[string]any, reg *registry.Registry, policyRefs []string) {
	for _, ref := range policyRefs {
		switch ref {
		case "gitops_flux":
			if policyHooks := claudeGitopsFluxGuardrailHooks(reg); len(policyHooks) > 0 {
				hooks["PreToolUse"] = appendHookBlocks(hooks["PreToolUse"], policyHooks...)
			}
		}
	}
}

// appendHookExtras dispatches profile-defined extras to their hook implementations.
func appendHookExtras(hooks map[string]any, extras []string, loomBinary string) {
	for _, extra := range extras {
		switch extra {
		case "postToolUse_formatters":
			event := "PostToolUse"
			if existing, ok := hooks[event].([]map[string]any); ok {
				hooks[event] = append(existing, claudePostToolUseExtras()...)
			}
		case "postToolUse_taskSync":
			event := "PostToolUse"
			if existing, ok := hooks[event].([]map[string]any); ok {
				hooks[event] = append(existing, claudePostToolUseTaskSyncHook(loomBinary)...)
			}
		}
	}
}

func appendHookBlocks(existing any, hooks ...map[string]any) []map[string]any {
	current, ok := existing.([]map[string]any)
	if !ok {
		current = []map[string]any{}
	}
	return append(current, hooks...)
}

type gitopsFluxGuardrailPolicy struct {
	BlockedCommands []string
	Message         string
}

func gitopsFluxGuardrailPolicyFromRegistry(reg *registry.Registry) *gitopsFluxGuardrailPolicy {
	pp := registryPlatformPerms(reg, "agents")
	if pp == nil || pp.Settings == nil {
		return nil
	}

	guardrails, ok := pp.Settings["guardrails"].(map[string]any)
	if !ok || len(guardrails) == 0 {
		return nil
	}
	raw, ok := guardrails["gitops_flux"].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}

	policy := &gitopsFluxGuardrailPolicy{}
	if cmds := coerceStringSlice(raw["blocked_commands"]); len(cmds) > 0 {
		policy.BlockedCommands = cmds
	} else if cmds := coerceStringSlice(raw["deny"]); len(cmds) > 0 {
		policy.BlockedCommands = cmds
	}
	if msg, ok := raw["message"].(string); ok && strings.TrimSpace(msg) != "" {
		policy.Message = strings.TrimSpace(msg)
	}
	return policy
}

func gitopsFluxGuardrailDenyRules(policy *gitopsFluxGuardrailPolicy) []string {
	if policy == nil {
		return nil
	}
	rules := make([]string, 0, len(policy.BlockedCommands))
	for _, cmd := range policy.BlockedCommands {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}
		rules = append(rules, fmt.Sprintf("Bash(%s *)", cmd))
	}
	return rules
}

func gitopsFluxGuardrailRegex(policy *gitopsFluxGuardrailPolicy) string {
	if policy == nil || len(policy.BlockedCommands) == 0 {
		return ""
	}

	parts := make([]string, 0, len(policy.BlockedCommands))
	for _, cmd := range policy.BlockedCommands {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}
		quoted := regexp.QuoteMeta(cmd)
		quoted = strings.ReplaceAll(quoted, `\ `, " ")
		parts = append(parts, quoted)
	}
	if len(parts) == 0 {
		return ""
	}
	return `^[[:space:]]*(` + strings.Join(parts, "|") + `)([[:space:]]|$)`
}

// claudeGitopsFluxGuardrailHooks returns the PreToolUse hooks backed by shared
// GitOps/Flux policy data from platform_permissions.agents.
func claudeGitopsFluxGuardrailHooks(reg *registry.Registry) []map[string]any {
	policy := gitopsFluxGuardrailPolicyFromRegistry(reg)
	if policy == nil {
		return nil
	}

	message := policy.Message
	if message == "" {
		message = "GitOps policy: kubectl edit/set env bypasses git history. Edit manifests and use flux reconcile."
	}
	pattern := gitopsFluxGuardrailRegex(policy)
	if pattern == "" {
		return nil
	}

	return []map[string]any{
		{
			"matcher": "Bash",
			"hooks": []map[string]any{
				{
					"type": "command",
					"command": fmt.Sprintf(
						`INPUT=$(cat); CMD=$(echo "$INPUT" | jq -r '.tool_input.command // ""'); if echo "$CMD" | grep -qE %q; then printf '%%s\n' %q >&2; exit 2; fi; exit 0`,
						pattern, message,
					),
				},
			},
		},
		{
			"matcher": "Bash",
			"hooks": []map[string]any{
				{
					"type":    "command",
					"command": `INPUT=$(cat); CMD=$(echo "$INPUT" | jq -r '.tool_input.command // ""'); if echo "$CMD" | grep -qE '^[[:space:]]*git[[:space:]]+commit([[:space:]]|$)'; then echo '{"systemMessage":"Pre-commit quality reminder: consider running quality_check (or quality_lint / quality_test) before committing to catch issues early."}'; fi; exit 0`,
				},
			},
		},
	}
}

// claudePermissions builds the permissions block for Claude Code settings.json.
// It reads from the registry's platform_permissions.claude section so the allow/deny
// lists are maintained in YAML rather than Go code. Falls back to a minimal default
// if the registry has no claude entry.
func claudePermissions(reg *registry.Registry) map[string]any {
	perms := map[string]any{}

	pp := registryPlatformPerms(reg, "claude")
	sharedPolicy := gitopsFluxGuardrailPolicyFromRegistry(reg)
	if pp == nil {
		// Minimal fallback: allow loom proxy tools only.
		fallback := map[string]any{
			"allow": []string{"mcp__loom"},
		}
		if deny := gitopsFluxGuardrailDenyRules(sharedPolicy); len(deny) > 0 {
			fallback["deny"] = deny
		}
		return fallback
	}

	if len(pp.AdditionalDirectories) > 0 {
		perms["additionalDirectories"] = pp.AdditionalDirectories
	}
	if pp.Settings != nil {
		if mode, ok := pp.Settings["default_mode"].(string); ok && mode != "" {
			perms["defaultMode"] = mode
		}
		// Optional keys stored under platform_permissions.claude.settings until the
		// registry schema grows first-class fields.
		if ask := coerceStringSlice(pp.Settings["ask"]); len(ask) > 0 {
			perms["ask"] = ask
		}
		if v, ok := pp.Settings["disable_bypass_permissions_mode"].(string); ok && v != "" {
			perms["disableBypassPermissionsMode"] = v
		}
	}

	// Claude Code rejects settings.json when permission rules don't match its
	// upstream schema regex. Filter invalid entries so we always emit a schema-valid
	// settings.json, and warn so the registry can be corrected.
	if len(pp.Allow) > 0 {
		allow, dropped := filterClaudePermissionRules(pp.Allow)
		if len(dropped) > 0 {
			fmt.Fprintf(os.Stderr, "WARN  [claude] dropping %d invalid permissions.allow entries: %s\n", len(dropped), strings.Join(dropped, ", "))
		}
		if len(allow) > 0 {
			perms["allow"] = allow
		}
	}
	if len(pp.Deny) > 0 {
		deny, dropped := filterClaudePermissionRules(pp.Deny)
		if len(dropped) > 0 {
			fmt.Fprintf(os.Stderr, "WARN  [claude] dropping %d invalid permissions.deny entries: %s\n", len(dropped), strings.Join(dropped, ", "))
		}
		if len(deny) > 0 {
			perms["deny"] = deny
		}
	}
	if deny := gitopsFluxGuardrailDenyRules(sharedPolicy); len(deny) > 0 {
		if existing, ok := perms["deny"].([]string); ok {
			perms["deny"] = append(existing, deny...)
		} else {
			perms["deny"] = deny
		}
	}
	if askAny, ok := perms["ask"].([]string); ok && len(askAny) > 0 {
		ask, dropped := filterClaudePermissionRules(askAny)
		if len(dropped) > 0 {
			fmt.Fprintf(os.Stderr, "WARN  [claude] dropping %d invalid permissions.ask entries: %s\n", len(dropped), strings.Join(dropped, ", "))
		}
		if len(ask) > 0 {
			perms["ask"] = ask
		} else {
			delete(perms, "ask")
		}
	}
	return perms
}

func coerceStringSlice(v any) []string {
	switch vv := v.(type) {
	case nil:
		return nil
	case []string:
		return vv
	case []any:
		out := make([]string, 0, len(vv))
		for _, x := range vv {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

var (
	claudePermRuleOnce sync.Once
	claudePermRuleRE   *regexp.Regexp
)

func claudePermissionRuleRegexp() *regexp.Regexp {
	claudePermRuleOnce.Do(func() {
		// Default to a conservative RE2-compatible regex. Claude's upstream schema
		// uses lookaheads that Go's regexp doesn't support, so we cannot compile it
		// verbatim.
		pattern := `^((Bash|Edit|ExitPlanMode|Glob|Grep|KillShell|LS|LSP|MultiEdit|NotebookEdit|NotebookRead|Read|Skill|Task|TaskCreate|TaskGet|TaskList|TaskOutput|TaskStop|TaskUpdate|TodoWrite|ToolSearch|WebFetch|WebSearch|Write)(\([^)]*\))?|mcp__.*)$`

		if schemaBytes, ok := validator.GetEmbeddedSchema("claude_settings.json"); ok && len(schemaBytes) > 0 {
			var raw map[string]any
			if err := json.Unmarshal(schemaBytes, &raw); err == nil {
				if defs, ok := raw["$defs"].(map[string]any); ok {
					if pr, ok := defs["permissionRule"].(map[string]any); ok {
						if p, ok := pr["pattern"].(string); ok && p != "" {
							// Skip patterns with unsupported tokens (lookaheads/lookbehinds).
							if !strings.Contains(p, "(?") {
								pattern = p
							}
						}
					}
				}
			}
		}

		re, err := regexp.Compile(pattern)
		if err != nil {
			// Fall back to a minimal safe regex rather than failing generation.
			re = regexp.MustCompile(`^(mcp__.*|Bash(\([^)]*\))?|Read(\([^)]*\))?|Write(\([^)]*\))?|Edit(\([^)]*\))?|MultiEdit(\([^)]*\))?|Task(\([^)]*\))?|Glob(\([^)]*\))?|Grep(\([^)]*\))?|ToolSearch(\([^)]*\))?|LS(\([^)]*\))?|WebFetch(\([^)]*\))?|WebSearch(\([^)]*\))?)$`)
		}
		claudePermRuleRE = re
	})
	return claudePermRuleRE
}

func filterClaudePermissionRules(rules []string) (kept []string, dropped []string) {
	re := claudePermissionRuleRegexp()
	for _, r := range rules {
		if r == "" {
			continue
		}
		if re.MatchString(r) {
			kept = append(kept, r)
		} else {
			dropped = append(dropped, r)
		}
	}
	return kept, dropped
}

// emitCodexPreamble writes Codex-specific top-level TOML settings.
// Settings are read from the registry's platform_permissions.codex section.
func emitCodexPreamble(sb *strings.Builder, reg *registry.Registry, workspaceRoot string, loomBinary string) {
	pp := registryPlatformPerms(reg, "codex")
	policy := agentSafetyPolicyFromRegistry(reg)
	loomCmd := shellQuote(normalizeLoomBinary(loomBinary))

	// Defaults when registry has no codex entry.
	approvalPolicy := "never"
	suppressWarning := true
	sandboxMode := "workspace-write"
	webSearchMode := "live"
	features := map[string]any{
		"apply_patch_freeform": true,
		"unified_exec":         true,
		"collaboration_modes":  true,
	}

	// Override from registry settings if present.
	if pp != nil && pp.Settings != nil {
		if v, ok := pp.Settings["approval_policy"].(string); ok {
			approvalPolicy = v
		}
		if v, ok := pp.Settings["suppress_unstable_features_warning"].(bool); ok {
			suppressWarning = v
		}
		if v, ok := pp.Settings["sandbox_mode"].(string); ok {
			sandboxMode = v
		}
		if v, ok := pp.Settings["web_search"].(string); ok && v != "" {
			webSearchMode = v
		}
		if v, ok := pp.Settings["features"].(map[string]any); ok {
			features = v
		}
	}

	fmt.Fprintf(sb, "approval_policy = %q\n\n", approvalPolicy)

	if suppressWarning {
		sb.WriteString("suppress_unstable_features_warning = true\n")
	}

	// Emit features as inline TOML table.
	var featureParts []string
	for k, v := range features {
		switch val := v.(type) {
		case bool:
			featureParts = append(featureParts, fmt.Sprintf("%s = %t", k, val))
		case string:
			featureParts = append(featureParts, fmt.Sprintf("%s = %q", k, val))
		}
	}
	sort.Strings(featureParts)
	fmt.Fprintf(sb, "features = { %s }\n\n", strings.Join(featureParts, ", "))

	fmt.Fprintf(sb, "sandbox_mode = %q\n", sandboxMode)
	fmt.Fprintf(sb, "sandbox_workspace_write = { network_access = true, writable_roots = [%q] }\n\n", workspaceRoot)

	// Enable Codex builtin web search tool (controls internet access for web.run/web_search).
	// Values: disabled, cached, live.
	if webSearchMode != "" {
		fmt.Fprintf(sb, "web_search = %q\n\n", webSearchMode)
	}

	sb.WriteString("# Git safety policy: treat pre-existing dirty worktrees as baseline context.\n")
	if policy.DirtyWorktreeMode == "continue_scoped_commits" {
		sb.WriteString("# Continue on current branch/worktree; stage+commit only files changed for the active task.\n")
		sb.WriteString("# Escalate only when new unexpected changes appear in files you are editing.\n\n")
	} else {
		fmt.Fprintf(sb, "# Dirty-worktree mode: %s\n\n", policy.DirtyWorktreeMode)
	}

	// Codex notify runs on every turn, so use a workspace-scoped persistent
	// AGENT_ID_FILE to avoid per-hook process-ID churn that fragments identity.
	// The workspace hash from cksum matches the scheme used by hookAgentIDBootstrap
	// for Claude/Gemini, avoiding cross-workspace agent ID collisions.
	// Emit notify before any [agents.*] tables so TOML keeps it at top level.
	sb.WriteString("# Agent lifecycle: heartbeat on turn completion (rate-limited to avoid notify storms)\n")
	fmt.Fprintf(sb, `notify = ["sh", "-c", %q, "--"]`, codexNotifyCommand(loomCmd))
	sb.WriteString("\n\n")

	// Emit [agents] section for multi-agent support if configured in registry.
	emitCodexAgents(sb, pp)
}

func codexNotifyCommand(loomCmd string) string {
	return fmt.Sprintf(`WS_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || printf '%%s' "$PWD")"; WS_HASH="$(printf '%%s' "$WS_ROOT" | cksum | cut -d' ' -f1)"; CACHE_DIR="${HOME}/.cache/loom"; AGENT_ID_FILE="${CACHE_DIR}/agent-id-codex-${WS_HASH}"; HEARTBEAT_STAMP_FILE="${CACHE_DIR}/notify-heartbeat-codex-${WS_HASH}.stamp"; mkdir -p "$CACHE_DIR"; if [ -s "$AGENT_ID_FILE" ]; then AGENT_ID="$(cat "$AGENT_ID_FILE")"; else AGENT_ID="codex-${WS_HASH}"; printf '%%s' "$AGENT_ID" > "$AGENT_ID_FILE"; fi; NOW="$(date +%%s)"; LAST="$(cat "$HEARTBEAT_STAMP_FILE" 2>/dev/null || true)"; case "$LAST" in ''|*[!0-9]*) ;; *) if [ $((NOW - LAST)) -lt 15 ]; then exit 0; fi ;; esac; printf '%%s' "$NOW" > "$HEARTBEAT_STAMP_FILE"; exec %s agent heartbeat --agent-id "$AGENT_ID" --status active --ensure-session --infer-namespace --agent-type codex --description "Codex notify session" --quiet 2>>"${TMPDIR:-/tmp}/loom-agent-hooks.log" || true`, loomCmd)
}

// emitCodexAgents writes the [agents] TOML section for Codex multi-agent support.
func emitCodexAgents(sb *strings.Builder, pp *registry.PlatformPermission) {
	if pp == nil || pp.Settings == nil {
		return
	}
	agents, ok := pp.Settings["agents"].(map[string]any)
	if !ok || len(agents) == 0 {
		return
	}

	sb.WriteString("[agents]\n")

	// Emit top-level agent settings.
	for _, key := range []string{"max_threads", "max_depth", "job_max_runtime_seconds"} {
		if v, exists := agents[key]; exists {
			switch val := v.(type) {
			case int:
				fmt.Fprintf(sb, "%s = %d\n", key, val)
			case float64:
				fmt.Fprintf(sb, "%s = %d\n", key, int(val))
			}
		}
	}
	sb.WriteString("\n")

	// Emit named agent definitions as [agents.<name>] sections.
	if defs, ok := agents["definitions"].(map[string]any); ok {
		// Sort keys for deterministic output.
		names := make([]string, 0, len(defs))
		for name := range defs {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			def, ok := defs[name].(map[string]any)
			if !ok {
				continue
			}
			fmt.Fprintf(sb, "[agents.%s]\n", name)
			if desc, ok := def["description"].(string); ok {
				fmt.Fprintf(sb, "description = %q\n", desc)
			}
			sb.WriteString("\n")
		}
	}
}

// registryPlatformPerms returns the PlatformPermission for a given platform,
// or nil if the registry has no entry.
func registryPlatformPerms(reg *registry.Registry, platform string) *registry.PlatformPermission {
	if reg == nil || reg.PlatformPermissions == nil {
		return nil
	}
	return reg.PlatformPermissions[platform]
}

type agentSafetyPolicy struct {
	DirtyWorktreeMode                string
	DirtyWorktreeNudgeOnSessionStart bool
	DirtyWorktreeNudgeMessage        string
}

func defaultAgentSafetyPolicy() agentSafetyPolicy {
	return agentSafetyPolicy{
		DirtyWorktreeMode:                "continue_scoped_commits",
		DirtyWorktreeNudgeOnSessionStart: true,
		DirtyWorktreeNudgeMessage:        "Dirty worktree detected. Treat pre-existing changes as baseline context, continue work, and stage/commit only files for the active task. Escalate only if new unexpected changes appear in files you are editing.",
	}
}

func agentSafetyPolicyFromRegistry(reg *registry.Registry) agentSafetyPolicy {
	policy := defaultAgentSafetyPolicy()
	pp := registryPlatformPerms(reg, "agents")
	if pp == nil || pp.Settings == nil {
		return policy
	}

	if v, ok := pp.Settings["dirty_worktree_mode"].(string); ok && strings.TrimSpace(v) != "" {
		policy.DirtyWorktreeMode = strings.TrimSpace(v)
	}
	if v, ok := pp.Settings["dirty_worktree_nudge_on_session_start"].(bool); ok {
		policy.DirtyWorktreeNudgeOnSessionStart = v
	}
	if v, ok := pp.Settings["dirty_worktree_nudge_message"].(string); ok && strings.TrimSpace(v) != "" {
		policy.DirtyWorktreeNudgeMessage = strings.TrimSpace(v)
	}
	return policy
}

func dirtyWorktreeSessionStartNudgeCommand(policy agentSafetyPolicy) string {
	payload, err := json.Marshal(map[string]string{
		"systemMessage": policy.DirtyWorktreeNudgeMessage,
	})
	if err != nil {
		payload = []byte(`{"systemMessage":"Dirty worktree detected. Continue on this branch and stage only task-scoped files."}`)
	}

	// Keep this check fast at session start by avoiding untracked-file scans,
	// which can be expensive in large monorepos.
	return fmt.Sprintf(`if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then if ! git diff --quiet --no-ext-diff || ! git diff --cached --quiet --no-ext-diff; then printf '%%s\n' %q; fi; fi; exit 0`, string(payload))
}

// mainBranchWorktreeNudgeCommand returns a shell command that emits a systemMessage
// suggesting worktree allocation when the agent is on main or master. This is a
// non-blocking suggestion — quick single-file fixes on main are still fine.
func mainBranchWorktreeNudgeCommand() string {
	payload := `{"systemMessage":"You are on main. For feature work or multi-file changes, consider using agent_worktree_allocate() to create an isolated branch and worktree before making changes."}`
	return fmt.Sprintf(`if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then BRANCH="$(git branch --show-current 2>/dev/null)"; if [ "$BRANCH" = "main" ] || [ "$BRANCH" = "master" ]; then printf '%%s\n' %q; fi; fi; exit 0`, payload)
}

// geminiHooksConfig returns a Gemini CLI settings.json with lifecycle hooks
// and auto-approve settings. Uses the same three-level nesting as Claude Code
// but with Gemini event names:
//   - SessionStart → SessionStart (same)
//   - SessionEnd → SessionEnd (Gemini uses SessionEnd, not Stop)
//   - AfterTool → AfterTool (Gemini uses AfterTool, not PostToolUse)
//
// Gemini tool names also differ (run_shell_command vs Bash).
func geminiHooksConfig() map[string]any {
	profile, _ := GetPlatformProfile("gemini")
	return geminiHooksConfigFromRegistry(nil, profile, "")
}

// geminiHooksConfigFromRegistry builds Gemini CLI settings.json, merging
// lifecycle hooks with auto-approve settings from the registry's
// platform_permissions.gemini section.
func geminiHooksConfigFromRegistry(reg *registry.Registry, profile *PlatformProfile, loomBinary string) map[string]any {
	config := map[string]any{
		"hooks":        geminiHooks(reg, profile, loomBinary),
		"experimental": map[string]any{"enableAgents": true},
	}

	// Merge auto-approve and tool settings from registry.
	pp := registryPlatformPerms(reg, "gemini")
	if pp != nil && pp.Settings != nil {
		general := map[string]any{}
		if v, ok := pp.Settings["approval_mode"].(string); ok && v != "" {
			general["defaultApprovalMode"] = v
		}
		if v, ok := pp.Settings["checkpointing"].(bool); ok && v {
			general["checkpointing"] = map[string]any{"enabled": true}
		}
		if len(general) > 0 {
			config["general"] = general
		}
		tools := map[string]any{}
		if allowed := coerceStringSlice(pp.Settings["tools_allowed"]); len(allowed) > 0 {
			tools["allowed"] = allowed
		}
		if core := coerceStringSlice(pp.Settings["tools_core"]); len(core) > 0 {
			tools["core"] = core
		}
		if exclude := coerceStringSlice(pp.Settings["tools_exclude"]); len(exclude) > 0 {
			tools["exclude"] = exclude
		}
		if len(tools) > 0 {
			config["tools"] = tools
		}

		security := map[string]any{}
		if v, ok := pp.Settings["enable_permanent_tool_approval"].(bool); ok {
			security["enablePermanentToolApproval"] = v
		}
		if v, ok := pp.Settings["folder_trust_enabled"].(bool); ok {
			security["folderTrust"] = map[string]any{
				"enabled": v,
			}
		}
		if len(security) > 0 {
			config["security"] = security
		}
	}

	return config
}

// geminiHooks returns the hooks block for Gemini CLI settings.json.
func geminiHooks(reg *registry.Registry, profile *PlatformProfile, loomBinary string) map[string]any {
	hooks := buildPlatformHooks(reg, profile.Hooks, loomBinary)
	appendHookExtras(hooks, profile.Hooks.Extras, loomBinary)
	return hooks
}

// hooksConfigFromProfile builds a generic hooks config from the platform profile.
// Used for platforms that have hooks.enabled but no platform-specific wrapper.
func hooksConfigFromProfile(reg *registry.Registry, profile *PlatformProfile, loomBinary string) map[string]any {
	hooks := buildPlatformHooks(reg, profile.Hooks, loomBinary)
	appendHookExtras(hooks, profile.Hooks.Extras, loomBinary)
	return map[string]any{"hooks": hooks}
}

// hookAgentIDBootstrap returns shell that derives a stable AGENT_ID for the
// current Claude/Gemini hook input.
//
// When hook JSON includes a session_id, the identity is scoped to that Claude
// session so subprocesses from the same CLI instance stay grouped together.
// If hook input is unavailable, we fall back to a workspace-scoped key.
func hookAgentIDBootstrap(agentID string) string {
	return fmt.Sprintf(
		`HOOK_INPUT="${INPUT:-}"; `+
			`HOOK_SESSION_ID=""; `+
			`if [ -n "$HOOK_INPUT" ]; then `+
			`HOOK_SESSION_ID="$(printf '%%s' "$HOOK_INPUT" | jq -r '.session_id // empty' 2>/dev/null || true)"; `+
			`fi; `+
			`SESSION_SCOPE=""; `+
			`if [ -n "$HOOK_SESSION_ID" ]; then `+
			`SESSION_SCOPE="$(printf '%%s' "$HOOK_SESSION_ID" | cksum | cut -d' ' -f1)"; `+
			`fi; `+
			`WS_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || printf '%%s' "$PWD")"; `+
			`WS_HASH="$(printf '%%s' "$WS_ROOT" | cksum | cut -d' ' -f1)"; `+
			`AGENT_CACHE_DIR="${HOME:-${TMPDIR:-/tmp}}/.cache/loom"; `+
			`mkdir -p "$AGENT_CACHE_DIR"; `+
			`AGENT_ID_FILE="${AGENT_CACHE_DIR}/agent-id-%s-${WS_HASH}${SESSION_SCOPE:+-${SESSION_SCOPE}}"; `+
			`if [ -s "$AGENT_ID_FILE" ]; then `+
			`AGENT_ID="$(cat "$AGENT_ID_FILE")"; `+
			`else `+
			`AGENT_ID="%s-${WS_HASH}${SESSION_SCOPE:+-${SESSION_SCOPE}}"; `+
			`printf '%%s' "$AGENT_ID" > "$AGENT_ID_FILE"; `+
			`fi`,
		agentID, agentID,
	)
}

// hookStaleCleanup returns a shell snippet that removes stale PID and agent ID
// files left behind by a previous session that crashed (Stop hook never fired).
// It checks whether the PID recorded in the keepalive PID file is still alive;
// if the process is dead, it removes both files so a fresh session can start.
func hookStaleCleanup() string {
	return `PID_FILE="${TMPDIR:-/tmp}/loom-keepalive-${AGENT_ID}.pid"; ` +
		`if [ -f "$PID_FILE" ]; then ` +
		`OLD_PID="$(cat "$PID_FILE" 2>/dev/null)"; ` +
		`if [ -n "$OLD_PID" ] && ! kill -0 "$OLD_PID" 2>/dev/null; then ` +
		`rm -f "$PID_FILE" "$AGENT_ID_FILE"; ` +
		`fi; fi`
}

// emitSandboxPolicy writes a .sandbox-policy.json file for the HUD and agents.
func emitSandboxPolicy(policy *registry.SandboxPolicy, outputDir string) error {
	data := map[string]any{
		"require_sandbox":   policy.RequireSandbox,
		"recommend_sandbox": policy.RecommendSandbox,
		"auto_provision":    policy.AutoProvision,
		"default_backend":   policy.DefaultBackend,
	}
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sandbox policy: %w", err)
	}
	path := filepath.Join(outputDir, ".sandbox-policy.json")
	return os.WriteFile(path, append(out, '\n'), 0644)
}
