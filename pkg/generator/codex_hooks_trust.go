package generator

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// Codex `[hooks]` block trust gate
//
// Codex v0.129+ silently skips any hook in `~/.codex/hooks.json` whose
// `trusted_hash` in `~/.codex/config.toml [hooks.state]` does not equal a
// hash computed over a canonical, normalized identity of the hook. Every
// time we regenerate hooks.json the content changes (workspace paths,
// dynamic agent IDs in shell snippets), so the stale trust hashes drop and
// the hooks vanish until the user re-trusts via the `/hooks` TUI command.
//
// To make our generated hooks self-trusting, we compute the same canonical
// trust hash codex would and write matching `[hooks.state]` entries into
// config.toml.
//
// Reference sources (verified 2026-05-12 against codex main):
//   - codex-rs/hooks/src/engine/discovery.rs::command_hook_hash
//   - codex-rs/hooks/src/lib.rs::hook_event_key_label, hook_key
//   - codex-rs/config/src/hook_config.rs (HookHandlerConfig, MatcherGroup)
//   - codex-rs/config/src/fingerprint.rs::version_for_toml, canonical_json
//
// Algorithm:
//  1. Build NormalizedHookIdentity { event_name (snake_case label), matcher,
//     hooks: [normalized_handler] } where normalized_handler always sets
//     command_windows=None and timeout = unwrap_or(600).max(1).
//  2. Serialize struct -> TomlValue -> JsonValue (drops None fields because
//     TOML has no null).
//  3. Canonicalize: recursively sort object keys, preserve array order.
//  4. SHA256 of compact JSON bytes.
//  5. Format as "sha256:<hex>".

// codexHookEventLabels maps Codex CamelCase event names (the keys in
// ~/.codex/hooks.json) to the snake_case labels used in [hooks.state] keys
// and inside the hashed identity. Mirrors hook_event_key_label in codex-rs.
var codexHookEventLabels = map[string]string{
	"PreToolUse":        "pre_tool_use",
	"PermissionRequest": "permission_request",
	"PostToolUse":       "post_tool_use",
	"PreCompact":        "pre_compact",
	"PostCompact":       "post_compact",
	"SessionStart":      "session_start",
	"UserPromptSubmit":  "user_prompt_submit",
	"Stop":              "stop",
}

// codexHookEventLabel returns the snake_case label for a Codex hook event
// name. Returns ("", false) for unknown events so callers can choose to
// skip them rather than panic.
func codexHookEventLabel(eventName string) (string, bool) {
	label, ok := codexHookEventLabels[eventName]
	return label, ok
}

// CodexHookTrustEntry is one (key, trusted_hash) pair to emit in the
// [hooks.state] table of ~/.codex/config.toml.
type CodexHookTrustEntry struct {
	Key          string // e.g. "/Users/x/.codex/hooks.json:post_tool_use:0:0"
	TrustedHash  string // "sha256:<64 hex>"
	EventName    string // CamelCase, for log/debug
	BlockIndex   int
	HandlerIndex int
}

// ComputeCodexHookTrust takes the path to a hooks.json plus its parsed
// contents and returns the [hooks.state] entries codex needs to consider
// the hooks Trusted. Entries are returned sorted by Key for stable output.
//
// hooksJSON shape (matches the Claude-style JSON Codex reads):
//
//	{"hooks": {"<EventName>": [{"matcher":"...","hooks":[{"type":"command","command":"...",...}]}]}}
func ComputeCodexHookTrust(hooksPath string, hooksJSON map[string]any) ([]CodexHookTrustEntry, error) {
	hooksBlock, _ := hooksJSON["hooks"].(map[string]any)
	if len(hooksBlock) == 0 {
		return nil, nil
	}

	// Sort event names for stable iteration order so the entries are
	// deterministic across runs.
	eventNames := make([]string, 0, len(hooksBlock))
	for ev := range hooksBlock {
		eventNames = append(eventNames, ev)
	}
	sort.Strings(eventNames)

	var entries []CodexHookTrustEntry
	for _, eventName := range eventNames {
		label, ok := codexHookEventLabel(eventName)
		if !ok {
			// Unknown event — skip; codex would ignore it too.
			continue
		}

		blocks := coerceMapList(hooksBlock[eventName])
		for bi, block := range blocks {
			matcher := matcherFromBlock(block)
			handlers := coerceMapList(block["hooks"])
			for hi, handler := range handlers {
				hash, err := codexHookTrustHash(label, matcher, handler)
				if err != nil {
					return nil, fmt.Errorf("hash %s[%d][%d]: %w", eventName, bi, hi, err)
				}
				entries = append(entries, CodexHookTrustEntry{
					Key:          fmt.Sprintf("%s:%s:%d:%d", hooksPath, label, bi, hi),
					TrustedHash:  hash,
					EventName:    eventName,
					BlockIndex:   bi,
					HandlerIndex: hi,
				})
			}
		}
	}
	return entries, nil
}

// coerceMapList accepts the two list-of-map shapes that turn up in our
// hooks config: `[]any` (when the JSON came from json.Unmarshal into
// map[string]any) and `[]map[string]any` (when buildPlatformHooks
// constructed the data directly). Returns a uniform []map[string]any.
func coerceMapList(v any) []map[string]any {
	switch t := v.(type) {
	case []map[string]any:
		return t
	case []any:
		out := make([]map[string]any, 0, len(t))
		for _, item := range t {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// matcherFromBlock returns the matcher string of a hook block, or "" if
// absent. The empty case is preserved as absent in the hashed identity (per
// the TOML→JSON conversion in codex which drops Option::None fields).
func matcherFromBlock(block map[string]any) string {
	if m, ok := block["matcher"].(string); ok {
		return m
	}
	return ""
}

// codexHookTrustHash computes "sha256:<hex>" for one hook handler. The
// label argument is the snake_case event name (e.g. "post_tool_use").
func codexHookTrustHash(label, matcher string, handler map[string]any) (string, error) {
	normalized := normalizedCodexHandler(handler)
	identity := map[string]any{
		"event_name": label,
		"hooks":      []any{normalized},
	}
	if matcher != "" {
		identity["matcher"] = matcher
	}
	canonical := canonicalJSONForCodexTrust(identity)
	// Codex (via serde_json::to_vec) does NOT HTML-escape `<`, `>`, `&` —
	// it emits them raw. Go's json.Marshal escapes them by default. Use
	// json.Encoder with SetEscapeHTML(false) to match codex's bytes
	// exactly. Encoder appends a trailing newline; strip it before hashing.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(canonical); err != nil {
		return "", err
	}
	body := bytes.TrimRight(buf.Bytes(), "\n")
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// normalizedCodexHandler returns the hook handler in the same shape
// codex's discovery.rs::command_hook_hash hashes. Specifically:
//   - command unchanged
//   - commandWindows always omitted (None in normalization)
//   - timeout = unwrap_or(600).max(1)
//   - async preserved (default false)
//   - statusMessage preserved if present
func normalizedCodexHandler(h map[string]any) map[string]any {
	timeout := 600
	if v, ok := h["timeout"]; ok && v != nil {
		switch n := v.(type) {
		case int:
			timeout = n
		case int64:
			timeout = int(n)
		case float64:
			timeout = int(n)
		}
	}
	if timeout < 1 {
		timeout = 1
	}
	out := map[string]any{
		"type":    "command",
		"command": h["command"],
		"timeout": timeout,
		"async":   asBool(h["async"]),
	}
	if sm, ok := h["statusMessage"].(string); ok && sm != "" {
		out["statusMessage"] = sm
	}
	return out
}

func asBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// canonicalJSONForCodexTrust returns a value that, when marshalled by
// encoding/json, produces the same bytes codex computes as input to its
// SHA256. The canonicalization (per codex fingerprint.rs::canonical_json)
// recursively sorts object keys and preserves array order. Go's json
// package emits object keys in insertion order; we use json.RawMessage to
// pre-sort.
//
// We rebuild every map as an ordered slice of marshalled key/value pairs
// to bypass Go's default unsorted map serialization.
func canonicalJSONForCodexTrust(v any) any {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return canonicalOrderedMap{keys: keys, values: func() []any {
			vals := make([]any, len(keys))
			for i, k := range keys {
				vals[i] = canonicalJSONForCodexTrust(t[k])
			}
			return vals
		}()}
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = canonicalJSONForCodexTrust(item)
		}
		return out
	default:
		return v
	}
}

// canonicalOrderedMap marshals to JSON with keys in sorted order. The
// canonicalJSONForCodexTrust helper produces values of this type for any
// nested map so the final json.Marshal output is byte-stable.
type canonicalOrderedMap struct {
	keys   []string
	values []any
}

func (c canonicalOrderedMap) MarshalJSON() ([]byte, error) {
	buf := []byte{'{'}
	for i, k := range c.keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		kBytes, err := encodeNoHTML(k)
		if err != nil {
			return nil, err
		}
		buf = append(buf, kBytes...)
		buf = append(buf, ':')
		vBytes, err := encodeNoHTML(c.values[i])
		if err != nil {
			return nil, err
		}
		buf = append(buf, vBytes...)
	}
	buf = append(buf, '}')
	return buf, nil
}

// encodeNoHTML marshals v with HTML escaping disabled, matching codex's
// serde_json::to_vec output. Returns the JSON bytes with the trailing
// newline stripped (json.Encoder always appends one).
func encodeNoHTML(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}
