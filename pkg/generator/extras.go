package generator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"text/template"
)

// extraDescriptor declares how a profile-defined hook extra (an entry in
// HookProfile.Extras like "postToolUse_formatters") translates into actual
// hook blocks. EPIC 3 / CONFIG-2 (.loom/108).
//
// Adding a new descriptor here + a template file under
// pkg/generator/templates/extras/ is sufficient to wire a new extra. No
// Go switch case needed.
type extraDescriptor struct {
	// targetEvents is the list of hook event slot names to receive the
	// rendered blocks. The dispatcher appends to each one whose slot is
	// already non-empty (matches legacy behavior — extras only enrich
	// already-built event blocks; they don't bootstrap empty slots).
	targetEvents []string

	// templatePath is the path under pkg/generator/templates/ used to
	// render the hook-block JSON. The template must produce a top-level
	// JSON array of hook blocks ([{ "matcher": ..., "hooks": [...] }]).
	templatePath string
}

// extraDescriptors maps each known extra name to its dispatch metadata.
// The order of evaluation does not matter — each descriptor operates on
// independent event slots. Unknown extra names fall through to the
// special-case branches at the bottom of appendHookExtras (currently
// just telemetry_eventEmit, whose multi-event walk doesn't fit the
// descriptor model).
var extraDescriptors = map[string]extraDescriptor{
	"postToolUse_formatters": {
		targetEvents: []string{"PostToolUse"},
		templatePath: "extras/post_tool_use_formatters.json.tmpl",
	},
	"postToolUse_taskSync": {
		targetEvents: []string{"PostToolUse"},
		templatePath: "extras/post_tool_use_task_sync.json.tmpl",
	},
	"postSessionEnd_retrospective": {
		// Both Stop (Claude) and SessionEnd (Gemini) appear in target
		// events; the dispatcher appends only to the slot that's already
		// non-empty for the platform, matching the legacy retro behavior.
		targetEvents: []string{"Stop", "SessionEnd"},
		templatePath: "extras/post_session_end_retrospective.json.tmpl",
	},
	"sessionStart_testHealth": {
		targetEvents: []string{"SessionStart"},
		templatePath: "extras/session_start_test_health.json.tmpl",
	},
	// EPIC 3 / CONFIG-2 extensibility example: a new hook extra that can
	// be wired into any platform via HookProfile.Extras with no Go code
	// change. Demonstrates the data-driven contract — new extras require
	// only an entry here + a template file. Not currently referenced
	// from any platform_profiles.yaml entry.
	"postToolUse_lint": {
		targetEvents: []string{"PostToolUse"},
		templatePath: "extras/post_tool_use_lint.json.tmpl",
	},
}

// extraContext is passed to extras templates as the dot value. Mirrors
// the legacy Go helpers' signature (loomBinary + sometimes a
// hookAgentIDBootstrap snippet).
type extraContext struct {
	LoomCmd       string // shell-quoted, normalized loom binary path
	LoomBinaryRaw string // unquoted form (rarely needed)
	AgentID       string // platform's agent id, e.g. "claude-code"
}

// renderExtraTemplate executes a template under
// pkg/generator/templates/extras/ with the supplied context and parses
// the output as a JSON array of hook blocks. Callers append the returned
// slice to the relevant event slot per the descriptor's targetEvents.
//
// Returns nil with no error when the descriptor is empty (template
// rendered to nothing) so the caller can skip cleanly.
func renderExtraTemplate(relPath string, ctx *extraContext) ([]map[string]any, error) {
	if relPath == "" {
		return nil, nil
	}

	path := filepath.Join("templates", relPath)
	data, err := templatesFS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read embedded extra template %s: %w", path, err)
	}
	tmpl, err := template.New(filepath.Base(relPath)).Funcs(extraTemplateFuncs()).Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse extra template %s: %w", path, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return nil, fmt.Errorf("execute extra template %s: %w", path, err)
	}

	var blocks []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &blocks); err != nil {
		return nil, fmt.Errorf("extra template %s produced invalid JSON: %w\nrendered:\n%s", path, err, buf.String())
	}
	canonicalizeHookBlocks(blocks)
	return blocks, nil
}

// canonicalizeHookBlocks walks the unmarshalled hook blocks and re-types
// nested "hooks" arrays from []any back to []map[string]any to match the
// shape that legacy Go-built hook blocks have had since
// pkg/generator/configs_hooks.go was first written. Without this, callers
// (and tests) that type-assert block["hooks"].([]map[string]any) would
// break on extras-rendered blocks even though the on-wire JSON is
// byte-identical.
func canonicalizeHookBlocks(blocks []map[string]any) {
	for _, b := range blocks {
		raw, ok := b["hooks"].([]any)
		if !ok {
			continue
		}
		converted := make([]map[string]any, 0, len(raw))
		for _, item := range raw {
			if m, ok := item.(map[string]any); ok {
				converted = append(converted, m)
			}
		}
		b["hooks"] = converted
	}
}

// extraTemplateFuncs returns the FuncMap available inside extras
// templates. Keeps the surface small — extras are short shell snippets,
// not full hook configs, so the heavy infrastructure helpers
// (buildHooks, registrySettings, etc.) from hook templates aren't
// exposed here.
func extraTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"shellQuote":           shellQuote,
		"hookAgentIDBootstrap": hookAgentIDBootstrap,
		// jsonString JSON-encodes a Go string and returns it WITH surrounding
		// double quotes — suitable for direct inlining as a JSON value. Used
		// to splice runtime-constructed shell commands (which contain double
		// quotes, $ signs, backslashes, etc.) into JSON-shaped templates
		// without manual escaping.
		"jsonString": func(s string) (string, error) {
			b, err := json.Marshal(s)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	}
}

// newExtraContext builds the dot value for an extras template render.
func newExtraContext(loomBinary, agentID string) *extraContext {
	return &extraContext{
		LoomCmd:       shellQuote(normalizeLoomBinary(loomBinary)),
		LoomBinaryRaw: loomBinary,
		AgentID:       agentID,
	}
}
