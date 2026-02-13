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

		// Generate lifecycle hook configs for platforms that support them.
		if err := generateHooksConfig(outputDir, target); err != nil {
			return fmt.Errorf("generate hooks for %s: %w", target, err)
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
		args := []any{"proxy"}
		// For platforms with no native hook support, add --agent-hint so the
		// proxy fires heartbeats on each tool call, providing universal presence.
		switch target {
		case "kilocode", "antigravity":
			args = append(args, "--agent-hint", target)
		}
		return map[string]*registry.TargetSpec{
			"loom": {
				Description: "Loom MCP proxy - unified access to all servers",
				Command:     cmd,
				Args:        args,
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

	// Codex supports a notify hook that fires on agent-turn-complete.
	// This is the only lifecycle hook Codex provides. Combined with --infer-namespace
	// and --ensure-session, the first heartbeat auto-bootstraps a session with
	// git-derived namespace context. The session reaper handles cleanup on idle.
	if target == "codex" {
		sb.WriteString("# Agent lifecycle: heartbeat on turn completion (self-bootstraps session/presence)\n")
		sb.WriteString("notify = [\"loom\", \"agent\", \"heartbeat\", \"--agent-id\", \"codex\", \"--status\", \"active\", \"--ensure-session\", \"--infer-namespace\", \"--agent-type\", \"codex\", \"--quiet\"]\n\n")
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
func generateHooksConfig(outputDir, target string) error {
	var config map[string]any

	switch target {
	case "claude":
		config = claudeHooksConfig()
	case "gemini":
		config = geminiHooksConfig()
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
// guardrail hooks, and default-allow permissions using the correct three-level
// nesting:
//
//	event → matcher group (with "hooks" array) → handler objects
//
// Lifecycle hooks:
//   - SessionStart: create session + register presence + recall context
//   - Stop: end session + summarize + deregister presence
//   - PostToolUse (Bash|Task): heartbeat to keep presence alive
//
// Guardrail hooks:
//   - PreToolUse (Bash): deny kubectl edit/set env (GitOps violation)
//   - PostToolUse (Write|Edit): auto-format Python files with black
//   - PostToolUse (Write|Edit): warn on image:latest tags
//
// Permissions:
//   - Default-allow for loom MCP proxy tools, common dev CLIs, and file ops
//   - Deny kubectl edit/set env (enforced by hook, belt-and-suspenders)
func claudeHooksConfig() map[string]any {
	return map[string]any{
		"permissions": claudePermissions(),
		"hooks": map[string]any{
			// SessionStart has no matcher — fires on every session start.
			"SessionStart": []map[string]any{
				{
					"hooks": []map[string]any{
						{
							"type":    "command",
							"command": `loom agent session-start --namespace "$(basename $(git rev-parse --show-toplevel 2>/dev/null || echo ${PWD##*/}))/$(git branch --show-current 2>/dev/null || echo main)" --agent-id claude-code --agent-type claude-code --description "Claude Code session" --auto-recall --quiet 2>/dev/null || true`,
						},
					},
				},
			},
			// Stop has no matcher — fires on every stop.
			"Stop": []map[string]any{
				{
					"hooks": []map[string]any{
						{
							"type":    "command",
							"command": "loom agent session-end --agent-id claude-code --summarize --quiet 2>/dev/null || true",
						},
					},
				},
			},
			// PreToolUse matcher group: Bash commands only.
			"PreToolUse": []map[string]any{
				{
					"matcher": "Bash",
					"hooks": []map[string]any{
						{
							"type":    "command",
							"command": `INPUT=$(cat); CMD=$(echo "$INPUT" | jq -r '.tool_input.command // ""'); if echo "$CMD" | grep -qE 'kubectl\s+(edit|set\s+env)'; then echo "GitOps policy: kubectl edit/set env bypasses git history. Edit manifests and use flux reconcile." >&2; exit 2; fi; exit 0`,
						},
					},
				},
			},
			// PostToolUse has two matcher groups:
			//   1. Bash|Task → heartbeat
			//   2. Write|Edit → black + image tag warning
			"PostToolUse": []map[string]any{
				{
					"matcher": "Bash|Task",
					"hooks": []map[string]any{
						{
							"type":    "command",
							"command": "loom agent heartbeat --agent-id claude-code --status active --ensure-session --agent-type claude-code --quiet 2>/dev/null || true",
						},
					},
				},
				{
					"matcher": "Write|Edit",
					"hooks": []map[string]any{
						{
							"type":    "command",
							"command": `jq -r '.tool_input.file_path // ""' | { read f; [[ "$f" == *.py ]] && black "$f" 2>/dev/null; exit 0; }`,
						},
						{
							"type":    "command",
							"command": `jq -r '.tool_input.new_string // .tool_input.content // ""' | { read content; if echo "$content" | grep -qE 'image:.*:latest'; then echo '{"systemMessage":"Noticed :latest tag - consider pinning to a specific version for reproducibility."}'; fi; exit 0; }`,
						},
					},
				},
			},
		},
	}
}

// claudePermissions returns the permissions block for Claude Code settings.json.
// This configures default-allow for the loom MCP proxy, common development CLIs,
// and file operations — suitable for a personal dev machine where approval prompts
// for routine operations are unwanted friction.
func claudePermissions() map[string]any {
	return map[string]any{
		"allow": []string{
			// ── MCP: all tools via loom proxy ──
			"mcp__loom",

			// ── Build & language toolchains ──
			"Bash(go *)",
			"Bash(make *)",
			"Bash(cargo *)",
			"Bash(npm *)",
			"Bash(npx *)",
			"Bash(pnpm *)",
			"Bash(yarn *)",
			"Bash(node *)",
			"Bash(python *)",
			"Bash(python3 *)",
			"Bash(pip *)",
			"Bash(uv *)",
			"Bash(poetry *)",
			"Bash(pytest *)",
			"Bash(black *)",
			"Bash(ruff *)",
			"Bash(golangci-lint *)",

			// ── Git & SCM ──
			"Bash(git *)",
			"Bash(gh *)",

			// ── Kubernetes & infrastructure ──
			"Bash(kubectl *)",
			"Bash(helm *)",
			"Bash(flux *)",
			"Bash(kustomize *)",
			"Bash(docker *)",
			"Bash(docker-compose *)",

			// ── Loom CLI ──
			"Bash(loom *)",
			"Bash(loomd *)",

			// ── Common unix utilities ──
			"Bash(ls *)",
			"Bash(cat *)",
			"Bash(head *)",
			"Bash(tail *)",
			"Bash(wc *)",
			"Bash(grep *)",
			"Bash(rg *)",
			"Bash(find *)",
			"Bash(sort *)",
			"Bash(diff *)",
			"Bash(which *)",
			"Bash(echo *)",
			"Bash(yes *)",
			"Bash(mkdir *)",
			"Bash(cp *)",
			"Bash(mv *)",
			"Bash(rm *)",
			"Bash(touch *)",
			"Bash(chmod *)",
			"Bash(pwd)",
			"Bash(env *)",
			"Bash(export *)",
			"Bash(sed *)",
			"Bash(awk *)",
			"Bash(tr *)",
			"Bash(cut *)",
			"Bash(xargs *)",
			"Bash(tee *)",
			"Bash(date *)",
			"Bash(hostname)",
			"Bash(whoami)",
			"Bash(readlink *)",
			"Bash(realpath *)",
			"Bash(basename *)",
			"Bash(dirname *)",
			"Bash(stat *)",
			"Bash(file *)",
			"Bash(du *)",
			"Bash(df *)",

			// ── Data processing & network ──
			"Bash(jq *)",
			"Bash(yq *)",
			"Bash(curl *)",
			"Bash(wget *)",
			"Bash(ssh *)",
			"Bash(scp *)",
			"Bash(rsync *)",

			// ── Testing & quality ──
			"Bash(gofmt *)",
			"Bash(goimports *)",
			"Bash(eslint *)",
			"Bash(prettier *)",
			"Bash(shellcheck *)",

			// ── File operations ──
			"Read",
			"Edit",
			"Write",

			// ── Web access ──
			"WebFetch",
			"WebSearch",
		},
		"deny": []string{
			// GitOps policy: never allow direct cluster mutations that bypass git.
			// Also enforced by PreToolUse hook as belt-and-suspenders.
			"Bash(kubectl edit *)",
			"Bash(kubectl set env *)",
		},
	}
}

// geminiHooksConfig returns a Gemini CLI settings.json with lifecycle hooks.
// Uses the same three-level nesting as Claude Code but with Gemini event names:
//   - SessionStart → SessionStart (same)
//   - SessionEnd → SessionEnd (Gemini uses SessionEnd, not Stop)
//   - AfterTool → AfterTool (Gemini uses AfterTool, not PostToolUse)
//
// Gemini tool names also differ (run_shell_command vs Bash).
func geminiHooksConfig() map[string]any {
	return map[string]any{
		"hooks": map[string]any{
			"SessionStart": []map[string]any{
				{
					"hooks": []map[string]any{
						{
							"type":    "command",
							"command": `loom agent session-start --namespace "$(basename $(git rev-parse --show-toplevel 2>/dev/null || echo ${PWD##*/}))/$(git branch --show-current 2>/dev/null || echo main)" --agent-id gemini-cli --agent-type gemini-cli --description "Gemini CLI session" --auto-recall --quiet 2>/dev/null || true`,
						},
					},
				},
			},
			"SessionEnd": []map[string]any{
				{
					"hooks": []map[string]any{
						{
							"type":    "command",
							"command": "loom agent session-end --agent-id gemini-cli --summarize --quiet 2>/dev/null || true",
						},
					},
				},
			},
			"AfterTool": []map[string]any{
				{
					"matcher": "run_shell_command",
					"hooks": []map[string]any{
						{
							"type":    "command",
							"command": "loom agent heartbeat --agent-id gemini-cli --status active --ensure-session --agent-type gemini-cli --quiet 2>/dev/null || true",
						},
					},
				},
			},
		},
	}
}
