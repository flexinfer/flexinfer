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

func preferWorkspaceLoomBinary(loomBinary string, workspaceRoot string) string {
	if workspaceRoot == "" {
		return loomBinary
	}

	// In this monorepo, prefer the workspace-built loom binary so GUI clients
	// don't depend on PATH and we avoid known issues with ~/.local/bin/loom.
	candidate := filepath.Join(workspaceRoot, "services", "loom-core", "bin", "loom")
	if !isExecutableFile(candidate) {
		return loomBinary
	}

	// If unset, or explicitly pointing at ~/.local/bin/loom, force the workspace binary.
	if loomBinary == "" {
		return candidate
	}
	home, _ := os.UserHomeDir()
	local := filepath.Join(home, ".local", "bin", "loom")
	if filepath.Clean(loomBinary) == filepath.Clean(local) {
		return candidate
	}

	return loomBinary
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
func GenerateConfigsWithPath(reg *registry.Registry, registryPath string, outputDir string, targets []string, hubMode bool, hubURL string, loomMode bool, loomBinary string, resolveSecrets bool) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	if len(targets) == 0 || targets[0] == "all" {
		targets = []string{"codex", "kilocode", "vscode", "claude", "claude_desktop", "gemini", "antigravity", "zed", "opencode"}
	}

	// Resolve repo root from registry path
	workspaceRoot := InferWorkspaceRoot(registry.GetRepoRoot(registryPath))
	registryRoot := inferRegistryRoot(registryPath)

	for _, target := range targets {
		profile, profileErr := GetPlatformProfile(target)
		if profileErr != nil {
			return fmt.Errorf("unknown target %q: %w", target, profileErr)
		}

		var err error
		switch {
		case target == "opencode":
			// OpenCode has unique serialization (array commands, "mcp" root, etc.)
			err = generateOpenCodeConfig(reg, outputDir, hubMode, hubURL, loomMode, loomBinary, workspaceRoot, registryRoot, resolveSecrets)
		case target == "claude_desktop":
			// Claude Desktop omits timeout field.
			err = generateClaudeDesktopConfig(reg, outputDir, hubMode, hubURL, loomMode, loomBinary, workspaceRoot, registryRoot, resolveSecrets)
		case profile.ConfigFormat == "json":
			err = generateJSONConfig(reg, outputDir, target, hubMode, hubURL, loomMode, loomBinary, workspaceRoot, registryRoot, resolveSecrets)
		case profile.ConfigFormat == "toml":
			err = generateTomlConfig(reg, outputDir, target, hubMode, hubURL, loomMode, loomBinary, workspaceRoot, registryRoot, resolveSecrets)
		default:
			err = fmt.Errorf("unsupported config format %q for %s", profile.ConfigFormat, target)
		}
		if err != nil {
			return fmt.Errorf("generate %s: %w", target, err)
		}

		// Generate lifecycle hook configs for platforms that support them.
		if err := generateHooksConfig(reg, outputDir, target); err != nil {
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

func buildTargetMap(reg *registry.Registry, target string, hubMode bool, hubURL string, profile string, loomMode bool, loomBinary string, workspaceRoot string, registryRoot string, resolveSecrets bool) (map[string]*registry.TargetSpec, error) {
	if loomMode {
		cmd := preferWorkspaceLoomBinary(loomBinary, workspaceRoot)
		if cmd == "" {
			cmd = "loom"
		}
		args := []any{"proxy"}
		// For platforms with no native hook support, add --agent-hint so the
		// proxy fires heartbeats on each tool call, providing universal presence.
		switch target {
		case "kilocode", "zed":
			args = append(args, "--agent-hint", target)
		case "antigravity":
			// Antigravity currently hard-limits MCP registrations to ~100 tools.
			// Apply a proxy-local core profile so Antigravity gets a stable
			// high-value subset without affecting other clients sharing loomd.
			args = append(args,
				"--agent-hint", "antigravity",
				"--tool-profile", "antigravity-core",
				"--max-tools", "100",
			)
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
			spec = convertToHubMode(spec, server.Name, hubURL, profile, hubWrapper)
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
		Timeout int               `json:"timeout,omitempty"`
	}

	// Claude Code CLI expects "mcpServers" as the root key
	config := map[string]map[string]JSONServer{"mcpServers": {}}
	for name, spec := range targets {
		args := []string{}
		for _, a := range spec.Args {
			args = append(args, fmt.Sprintf("%v", a))
		}

		server := JSONServer{
			Command: spec.Command,
			Args:    args,
			Env:     spec.Env,
		}
		if spec.Timeout > 0 {
			server.Timeout = spec.Timeout
		}
		config["mcpServers"][name] = server
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

	// Codex supports a notify hook that fires on agent-turn-complete.
	// This is the only lifecycle hook Codex provides. Combined with --infer-namespace
	// and --ensure-session, the first heartbeat auto-bootstraps a session with
	// git-derived namespace context. The session reaper handles cleanup on idle.
	if target == "codex" {
		emitCodexPreamble(&sb, reg, workspaceRoot)
	}

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

		// NOTE: Codex has a strict upstream schema for mcp_servers entries.
		// Keep Codex server blocks schema-compliant to avoid breaking config load.
		if target != "codex" {
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
		} else {
			// Best-effort mapping of our registry timeout to Codex's per-tool timeout.
			if spec.Timeout > 0 {
				sb.WriteString(fmt.Sprintf("tool_timeout_sec = %d\n", spec.Timeout))
			}
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

	configPath := filepath.Join(destDir, "config.toml")
	content := []byte(sb.String())

	if err := os.WriteFile(configPath, content, perm); err != nil {
		return err
	}

	// Validate Codex configs against upstream schema.
	if target == "codex" {
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
// that support them (Claude Code and Gemini CLI). The hooks call `loom agent`
// subcommands to ensure consistent session tracking and presence management.
func generateHooksConfig(reg *registry.Registry, outputDir, target string) error {
	var config map[string]any

	switch target {
	case "claude":
		config = claudeHooksConfig(reg)
	case "gemini":
		config = geminiHooksConfigFromRegistry(reg)
	case "antigravity":
		// Antigravity has no native hook support, but benefits from a
		// settings.json stub for consistency with the sync architecture.
		// The --agent-hint flag in mcp.json handles session tracking.
		config = map[string]any{
			"hooks": map[string]any{},
		}
	case "opencode":
		// OpenCode uses JS/TS plugins for hooks, not a JSON settings file.
		return generateOpenCodeHooksPlugin(outputDir)
	default:
		return nil // Platform doesn't support hooks.
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
// guardrail hooks, and default-allow permissions. Permissions are read from the
// registry's platform_permissions.claude section; hooks remain in Go because
// they are structural (event names, matcher groups) rather than data.
func claudeHooksConfig(reg *registry.Registry) map[string]any {
	return map[string]any{
		"permissions": claudePermissions(reg),
		"hooks":       claudeHooks(reg),
	}
}

// hookPlatformConfig defines the platform-specific parameters needed to
// generate lifecycle hooks (SessionStart, session-end, heartbeat).
// Claude and Gemini share the same hook structure but differ in event names
// and tool matchers.
type hookPlatformConfig struct {
	AgentID          string // "claude-code" or "gemini-cli"
	AgentType        string // same as AgentID for now
	Description      string // "Claude Code session" or "Gemini CLI session"
	SessionEndEvent  string // "Stop" (Claude) or "SessionEnd" (Gemini)
	HeartbeatEvent   string // "PostToolUse" (Claude) or "AfterTool" (Gemini)
	HeartbeatMatcher string // "Bash|Task" (Claude) or "run_shell_command" (Gemini)
}

// buildPlatformHooks generates the shared SessionStart / session-end / heartbeat
// hooks for any platform that supports lifecycle hooks. Platform-specific extras
// (e.g. Claude's PreToolUse guardrails) are appended by the caller.
func buildPlatformHooks(reg *registry.Registry, cfg hookPlatformConfig) map[string]any {
	log := `2>>"${TMPDIR:-/tmp}/loom-agent-hooks.log"`
	bootstrap := hookAgentIDBootstrap(cfg.AgentID)
	staleCleanup := hookStaleCleanup()
	policy := agentSafetyPolicyFromRegistry(reg)

	sessionStartHooks := []map[string]any{
		{
			"type": "command",
			"command": fmt.Sprintf(
				`%s; %s; loom agent session-start --namespace "$(basename $(git rev-parse --show-toplevel 2>/dev/null || echo ${PWD##*/}))/$(git branch --show-current 2>/dev/null || echo main)" --agent-id "$AGENT_ID" --agent-type %s --description %q --auto-recall --auto-recall-strategy fast --quiet %s || true`,
				bootstrap, staleCleanup, cfg.AgentType, cfg.Description, log),
		},
		{
			"type": "command",
			"command": fmt.Sprintf(
				`%s; PID_FILE="${TMPDIR:-/tmp}/loom-keepalive-${AGENT_ID}.pid"; [ -f "$PID_FILE" ] && kill "$(cat "$PID_FILE")" 2>/dev/null; loom agent keepalive --agent-id "$AGENT_ID" --agent-type %s --quiet %s & printf '%%s' "$!" > "${PID_FILE}.tmp" && mv "${PID_FILE}.tmp" "$PID_FILE"`,
				bootstrap, cfg.AgentType, log),
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
		cfg.SessionEndEvent: []map[string]any{
			{
				"hooks": []map[string]any{
					{
						"type": "command",
						"command": fmt.Sprintf(
							`%s; PID_FILE="${TMPDIR:-/tmp}/loom-keepalive-${AGENT_ID}.pid"; [ -f "$PID_FILE" ] && kill "$(cat "$PID_FILE")" 2>/dev/null; rm -f "$PID_FILE"; rm -f "$AGENT_ID_FILE"; loom agent session-end --agent-id "$AGENT_ID" --summarize --summary-async --quiet %s || true`,
							bootstrap, log),
					},
				},
			},
		},
		cfg.HeartbeatEvent: []map[string]any{
			{
				"matcher": cfg.HeartbeatMatcher,
				"hooks": []map[string]any{
					{
						"type": "command",
						"command": fmt.Sprintf(
							`%s; loom agent heartbeat --agent-id "$AGENT_ID" --status active --ensure-session --agent-type %s --quiet %s || true`,
							bootstrap, cfg.AgentType, log),
					},
				},
			},
		},
	}

	return hooks
}

// claudePreToolUseHooks returns the PreToolUse guardrail hooks specific to
// Claude Code (kubectl edit/set env policy enforcement).
func claudePreToolUseHooks() []map[string]any {
	return []map[string]any{
		{
			"matcher": "Bash",
			"hooks": []map[string]any{
				{
					"type":    "command",
					"command": `INPUT=$(cat); CMD=$(echo "$INPUT" | jq -r '.tool_input.command // ""'); if echo "$CMD" | grep -qE 'kubectl\s+(edit|set\s+env)'; then echo "GitOps policy: kubectl edit/set env bypasses git history. Edit manifests and use flux reconcile." >&2; exit 2; fi; if echo "$CMD" | grep -qE '^\s*git\s+commit'; then echo '{"systemMessage":"Pre-commit quality reminder: consider running quality_check (or quality_lint / quality_test) before committing to catch issues early."}'; fi; exit 0`,
				},
			},
		},
	}
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

// claudeHooks returns the hooks block for Claude Code settings.json.
func claudeHooks(reg *registry.Registry) map[string]any {
	hooks := buildPlatformHooks(reg, hookPlatformConfig{
		AgentID:          "claude-code",
		AgentType:        "claude-code",
		Description:      "Claude Code session",
		SessionEndEvent:  "Stop",
		HeartbeatEvent:   "PostToolUse",
		HeartbeatMatcher: "Bash|Task",
	})

	// Append Claude-specific PreToolUse guardrails.
	hooks["PreToolUse"] = claudePreToolUseHooks()

	// Append Claude-specific Write/Edit formatters to PostToolUse.
	postToolUse := hooks["PostToolUse"].([]map[string]any)
	postToolUse = append(postToolUse, claudePostToolUseExtras()...)
	hooks["PostToolUse"] = postToolUse

	return hooks
}

// claudePermissions builds the permissions block for Claude Code settings.json.
// It reads from the registry's platform_permissions.claude section so the allow/deny
// lists are maintained in YAML rather than Go code. Falls back to a minimal default
// if the registry has no claude entry.
func claudePermissions(reg *registry.Registry) map[string]any {
	perms := map[string]any{}

	pp := registryPlatformPerms(reg, "claude")
	if pp == nil {
		// Minimal fallback: allow loom proxy tools only.
		return map[string]any{
			"allow": []string{"mcp__loom"},
		}
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
func emitCodexPreamble(sb *strings.Builder, reg *registry.Registry, workspaceRoot string) {
	pp := registryPlatformPerms(reg, "codex")
	policy := agentSafetyPolicyFromRegistry(reg)

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

	// Codex notify uses a TOML string array with no shell expansion. Use sh -c
	// to get $$ expansion for a per-process agent ID. The workspace hash from
	// cksum matches the scheme used by hookAgentIDBootstrap for Claude/Gemini,
	// avoiding cross-workspace agent ID collisions.
	sb.WriteString("# Agent lifecycle: heartbeat on turn completion (self-bootstraps session/presence)\n")
	sb.WriteString(`notify = ["sh", "-c", "WS_HASH=\"$(printf '%s' \"$(git rev-parse --show-toplevel 2>/dev/null || printf '%s' \"$PWD\")\" | cksum | cut -d' ' -f1)\"; exec loom agent heartbeat --agent-id \"codex-${WS_HASH}-$$\" --status active --ensure-session --infer-namespace --agent-type codex --quiet 2>>\"${TMPDIR:-/tmp}/loom-agent-hooks.log\" || true"]`)
	sb.WriteString("\n\n")
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
	return geminiHooksConfigFromRegistry(nil)
}

// geminiHooksConfigFromRegistry builds Gemini CLI settings.json, merging
// lifecycle hooks with auto-approve settings from the registry's
// platform_permissions.gemini section.
func geminiHooksConfigFromRegistry(reg *registry.Registry) map[string]any {
	config := map[string]any{
		"hooks": geminiHooks(reg),
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
		if allowed := coerceStringSlice(pp.Settings["tools_allowed"]); len(allowed) > 0 {
			config["tools"] = map[string]any{
				"allowed": allowed,
			}
		}
	}

	return config
}

// geminiHooks returns the hooks block for Gemini CLI settings.json.
func geminiHooks(reg *registry.Registry) map[string]any {
	return buildPlatformHooks(reg, hookPlatformConfig{
		AgentID:          "gemini-cli",
		AgentType:        "gemini-cli",
		Description:      "Gemini CLI session",
		SessionEndEvent:  "SessionEnd",
		HeartbeatEvent:   "AfterTool",
		HeartbeatMatcher: "run_shell_command",
	})
}

func hookAgentIDBootstrap(agentID string) string {
	return fmt.Sprintf(
		`WS_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || printf '%%s' "$PWD")"; `+
			`WS_HASH="$(printf '%%s' "$WS_ROOT" | cksum | cut -d' ' -f1)"; `+
			`AGENT_ID_FILE="${TMPDIR:-/tmp}/loom-agent-id-%s-${WS_HASH}"; `+
			`if [ -s "$AGENT_ID_FILE" ]; then `+
			`AGENT_ID="$(cat "$AGENT_ID_FILE")"; `+
			`else `+
			`AGENT_ID="%s-${WS_HASH}-$PPID"; `+
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
