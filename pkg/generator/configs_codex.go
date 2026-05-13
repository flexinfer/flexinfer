package generator

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/crb2nu/loom/pkg/registry"
)

// codexTemplateContext is the dot value for templates/hooks/codex.toml.tmpl.
// EPIC 3 / CONFIG-4 (.loom/108): all conditional resolution (registry
// overrides, agent-safety policy, profile policy comment, notify command
// shape) happens in Go via buildCodexContext; the template is a thin
// stitcher that lays out TOML keys.
type codexTemplateContext struct {
	// approval_policy renders as one of:
	//   approval_policy = "<str>"
	// or
	//   approval_policy = { granular = { <kv pairs> } }
	// ApprovalPolicyGranular is non-empty when the granular form is in use;
	// otherwise ApprovalPolicyStr is set.
	ApprovalPolicyStr      string
	ApprovalPolicyGranular string

	SuppressWarning bool
	Features        string // pre-rendered "k1 = v1, k2 = v2"

	SandboxMode   string
	WorkspaceRoot string
	WebSearchMode string

	Model                string
	ModelReasoningEffort string

	PolicyComment string // multi-line, ends in trailing newline if non-empty

	DirtyWorktreeMode     string
	DirtyWorktreeIsScoped bool

	NotifyCommand string // shell command, raw (template applies %q)

	Agents *codexAgentsBlock // nil when [agents] section is absent

	// HookTrustEntries are [hooks.state."<key>"] trusted_hash entries that
	// make our emitted hooks.json self-trusting. Without them, Codex v0.129+
	// silently skips every hook on the first run because the trusted_hash
	// in [hooks.state] does not match the canonical hash of the hook content.
	// Each regen invalidates pre-existing trust hashes, so the generator must
	// always recompute these alongside hooks.json. See
	// pkg/generator/codex_hooks_trust.go for the hash algorithm. Empty when
	// hooks.json is absent or has no hooks.
	HookTrustEntries []CodexHookTrustEntry
}

// codexAgentsBlock holds the resolved [agents] section data. nil when the
// registry has no agents settings.
type codexAgentsBlock struct {
	TopLevelLines []string             // pre-formatted "k = v" lines
	Definitions   []codexAgentDefBlock // sorted by Name
}

type codexAgentDefBlock struct {
	Name        string
	Description string
}

// emitCodexPreamble writes Codex-specific top-level TOML settings to sb.
// Settings are pulled from the registry's platform_permissions.codex section
// with sensible defaults. EPIC 3 / CONFIG-4 (.loom/108): the output shape
// lives in templates/hooks/codex.toml.tmpl; this function builds the
// template context and renders.
func emitCodexPreamble(sb *strings.Builder, reg *registry.Registry, workspaceRoot string, loomBinary string) {
	ctx := buildCodexContext(reg, workspaceRoot, loomBinary)
	rendered, err := renderCodexTemplate(ctx)
	if err != nil {
		// The legacy emitCodexPreamble had no error path (everything was
		// Fprintf into a strings.Builder, which can't fail). With the
		// template render path we now have a possible failure mode, so
		// surface it inline as a TOML comment rather than silently
		// producing nothing. This both keeps the output file syntactically
		// valid TOML and makes the failure visible to operators running
		// `loom sync`.
		fmt.Fprintf(sb, "# codex preamble template error: %v\n", err)
		return
	}
	sb.WriteString(rendered)
}

// renderCodexTemplate executes templates/hooks/codex.toml.tmpl with the
// given context and returns the rendered string. Output is the raw TOML
// preamble text; no JSON round-trip is involved (Codex output is
// text-only, not parsed back into a map).
func renderCodexTemplate(ctx *codexTemplateContext) (string, error) {
	const relPath = "hooks/codex.toml.tmpl"
	path := filepath.Join("templates", relPath)
	data, err := templatesFS.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read embedded codex template %s: %w", path, err)
	}
	tmpl, err := template.New(filepath.Base(relPath)).Parse(string(data))
	if err != nil {
		return "", fmt.Errorf("parse codex template %s: %w", path, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("execute codex template %s: %w", path, err)
	}
	return buf.String(), nil
}

// buildCodexContext resolves all registry overrides and policy comments
// into a flat struct suitable for the codex.toml.tmpl template.
func buildCodexContext(reg *registry.Registry, workspaceRoot string, loomBinary string) *codexTemplateContext {
	pp := registryPlatformPerms(reg, "codex")
	policy := agentSafetyPolicyFromRegistry(reg)
	loomCmd := shellQuote(normalizeLoomBinary(loomBinary))

	ctx := &codexTemplateContext{
		SuppressWarning:   true,
		SandboxMode:       "workspace-write",
		WorkspaceRoot:     workspaceRoot,
		WebSearchMode:     "live",
		ApprovalPolicyStr: "never",
		Features: codexFeaturesString(map[string]any{
			"apply_patch_freeform": true,
			"unified_exec":         true,
			"collaboration_modes":  true,
			// Opt in to Codex's [hooks] block surface (GA in v0.129.0,
			// 2026-05-07). Pairs with the hooks.json generated alongside
			// config.toml via generateHooksConfig. The `notify = [...]`
			// fallback below is intentionally kept active during transition
			// because [hooks] is unreliable for SessionStart/Stop with
			// repo-local config (openai/codex#17532).
			"hooks": true,
		}),
		DirtyWorktreeMode:     policy.DirtyWorktreeMode,
		DirtyWorktreeIsScoped: policy.DirtyWorktreeMode == "continue_scoped_commits",
	}

	// Override defaults from registry settings if present.
	if pp != nil && pp.Settings != nil {
		switch v := pp.Settings["approval_policy"].(type) {
		case string:
			if v != "" {
				ctx.ApprovalPolicyStr = v
			}
		case map[string]any:
			if granular, ok := v["granular"].(map[string]any); ok {
				ctx.ApprovalPolicyGranular = codexGranularApprovalString(granular)
				ctx.ApprovalPolicyStr = "" // granular form wins
			}
		}
		if v, ok := pp.Settings["suppress_unstable_features_warning"].(bool); ok {
			ctx.SuppressWarning = v
		}
		if v, ok := pp.Settings["sandbox_mode"].(string); ok {
			ctx.SandboxMode = v
		}
		if v, ok := pp.Settings["web_search"].(string); ok {
			// Empty string here disables the line entirely (matches legacy behavior).
			ctx.WebSearchMode = v
		}
		if v, ok := pp.Settings["features"].(map[string]any); ok {
			ctx.Features = codexFeaturesString(v)
		}
		if v, ok := pp.Settings["model"].(string); ok && v != "" {
			ctx.Model = v
		}
		if v, ok := pp.Settings["model_reasoning_effort"].(string); ok && v != "" {
			ctx.ModelReasoningEffort = v
		}
	}

	// Policy enforcement annotations from the platform profile.
	if codexProfile, _ := GetPlatformProfile("codex"); codexProfile != nil {
		ctx.PolicyComment = FormatPolicyComment(codexProfile.Hooks, "# ")
	}

	// Notify command. telemetry_eventEmit opts in to the post-tool-use
	// event-emit pipe; codex has no per-tool granularity so this is the
	// best-effort signal (Phase 2.2c of spectator plan).
	telemetryEmit := false
	if codexProfile, _ := GetPlatformProfile("codex"); codexProfile != nil {
		telemetryEmit = containsString(codexProfile.Hooks.Extras, "telemetry_eventEmit")
	}
	ctx.NotifyCommand = codexNotifyCommand(loomCmd, telemetryEmit)

	// [agents] section if registry declares one.
	ctx.Agents = buildCodexAgentsBlock(pp)

	// Self-trust [hooks.state] entries. Compute the canonical hash for
	// every hook in the hooks.json we're about to emit, so codex doesn't
	// silently drop them on the first run (or after a content change on
	// regen). The hook content here is built by the same hooksConfigFromProfile
	// that generateHooksConfig writes out, so the hashes match by construction.
	ctx.HookTrustEntries = buildCodexHookTrustEntries(reg, loomBinary)

	return ctx
}

// buildCodexHookTrustEntries renders the codex hooks structure the same
// way generateHooksConfig does, then computes the [hooks.state] trust
// entries for it. The resulting entries are keyed on the absolute path
// where the file will land at sync time (~/.codex/hooks.json), so the
// trust state matches what codex sees when it reads the file.
//
// Returns nil if the codex profile is missing, hooks are disabled, or the
// user's home directory can't be resolved (the latter only happens in
// pathological environments — generation continues without self-trust).
func buildCodexHookTrustEntries(reg *registry.Registry, loomBinary string) []CodexHookTrustEntry {
	profile, err := GetPlatformProfile("codex")
	if err != nil || profile == nil || !profile.Hooks.Enabled {
		return nil
	}
	hooksConfig := hooksConfigFromProfile(reg, profile, loomBinary)
	if hooksConfig == nil {
		return nil
	}
	hooksPath, ok := codexHooksJSONHomePath()
	if !ok {
		return nil
	}
	entries, err := ComputeCodexHookTrust(hooksPath, hooksConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN  [codex] hook trust hash failed: %v\n", err)
		return nil
	}
	return entries
}

// codexHooksJSONHomePath returns the absolute path codex will look at
// when checking [hooks.state] keys against the loaded hooks.json. Codex
// reads ~/.codex/hooks.json and stores absolute paths in trust keys.
func codexHooksJSONHomePath() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return home + "/.codex/hooks.json", true
}

// codexFeaturesString formats a features map as the inline TOML body
// `k1 = v1, k2 = v2` with sorted keys for deterministic output.
func codexFeaturesString(features map[string]any) string {
	parts := make([]string, 0, len(features))
	for k, v := range features {
		switch val := v.(type) {
		case bool:
			parts = append(parts, fmt.Sprintf("%s = %t", k, val))
		case string:
			parts = append(parts, fmt.Sprintf("%s = %q", k, val))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// codexGranularApprovalString renders the granular form as
// `key1 = bool1, key2 = bool2` with the legacy key order:
// sandbox_approval, rules, mcp_elicitations, request_permissions,
// skill_approval. Keys not present are omitted; unknown keys are dropped.
func codexGranularApprovalString(granular map[string]any) string {
	var parts []string
	for _, key := range []string{"sandbox_approval", "rules", "mcp_elicitations", "request_permissions", "skill_approval"} {
		if v, ok := granular[key]; ok {
			if b, ok := v.(bool); ok {
				parts = append(parts, fmt.Sprintf("%s = %t", key, b))
			}
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// buildCodexAgentsBlock resolves the [agents] section from the registry's
// codex platform settings. Returns nil when no agents section is declared
// so the template can skip the entire [agents] block.
func buildCodexAgentsBlock(pp *registry.PlatformPermission) *codexAgentsBlock {
	if pp == nil || pp.Settings == nil {
		return nil
	}
	agents, ok := pp.Settings["agents"].(map[string]any)
	if !ok || len(agents) == 0 {
		return nil
	}
	block := &codexAgentsBlock{}

	for _, key := range []string{"max_threads", "max_depth", "job_max_runtime_seconds"} {
		if v, exists := agents[key]; exists {
			switch val := v.(type) {
			case int:
				block.TopLevelLines = append(block.TopLevelLines, fmt.Sprintf("%s = %d", key, val))
			case float64:
				block.TopLevelLines = append(block.TopLevelLines, fmt.Sprintf("%s = %d", key, int(val)))
			}
		}
	}

	if defs, ok := agents["definitions"].(map[string]any); ok {
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
			d := codexAgentDefBlock{Name: name}
			if desc, ok := def["description"].(string); ok {
				d.Description = desc
			}
			block.Definitions = append(block.Definitions, d)
		}
	}

	return block
}

// codexNotifyCommand renders the shell snippet codex spawns on every
// turn-end via its `notify` config.toml key. When telemetryEmit is true,
// the snippet also pipes the notify payload into
// `loom agent event-emit --platform codex` so the daemon EventBus sees
// a coarse `tool.call.end` per turn (Phase 2.2c of the spectator plan;
// codex has no per-tool granularity, so this is the best-effort surface).
//
// Codex passes the notify JSON as positional arg `$1` (per the codex
// notify contract — `notify = [shell, args..., "--"]` means $0="--",
// $1=payload). We pipe `${1:-}` into stdin for both the existing
// keepalive-wrap session extraction and the new event-emit publish.
func codexNotifyCommand(loomCmd string, telemetryEmit bool) string {
	base := fmt.Sprintf(`WS_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || printf '%%s' "$PWD")"; WS_HASH="$(printf '%%s' "$WS_ROOT" | cksum | cut -d' ' -f1)"; CACHE_DIR="${HOME}/.cache/loom"; AGENT_ID_FILE="${CACHE_DIR}/agent-id-codex-${WS_HASH}"; KEEPALIVE_STAMP_FILE="${CACHE_DIR}/keepalive-wrap-codex-${WS_HASH}.stamp"; mkdir -p "$CACHE_DIR"; if [ -s "$AGENT_ID_FILE" ]; then AGENT_ID="$(cat "$AGENT_ID_FILE")"; else AGENT_ID="codex-${WS_HASH}"; printf '%%s' "$AGENT_ID" > "$AGENT_ID_FILE"; fi; NOTIFY_PAYLOAD="${INPUT:-${1:-}}"; NOW="$(date +%%s)"; LAST="$(cat "$KEEPALIVE_STAMP_FILE" 2>/dev/null || true)"; case "$LAST" in ''|*[!0-9]*) ;; *) if [ $((NOW - LAST)) -lt 15 ]; then exit 0; fi ;; esac; printf '%%s' "$NOW" > "$KEEPALIVE_STAMP_FILE"; HOOK_SESSION_ID="$(printf '%%s' "$NOTIFY_PAYLOAD" | jq -r '.session_id // empty' 2>/dev/null || true)"; nohup %s agent keepalive-wrap --agent-id "$AGENT_ID" --session-id "$HOOK_SESSION_ID" --status active --ensure-session --infer-namespace --agent-type codex --description "Codex keepalive wrapper session" --quiet </dev/null >/dev/null 2>>"${TMPDIR:-/tmp}/loom-agent-hooks.log" &`, loomCmd)
	if !telemetryEmit {
		return base
	}
	return base + fmt.Sprintf(` printf '%%s' "$NOTIFY_PAYLOAD" | %s agent event-emit --hook post-tool-use --platform codex --agent-id "$AGENT_ID" --quiet 2>>"${TMPDIR:-/tmp}/loom-agent-hooks.log" || true`, loomCmd)
}

// containsString reports whether haystack contains needle. Inlined here
// to avoid pulling in a generic slices helper for a single call site.
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
