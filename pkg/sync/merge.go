package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// ExtractHooksFromSettings parses a settings.json and returns the "hooks"
// value as raw JSON. Returns nil if the key is absent.
func ExtractHooksFromSettings(data []byte) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse settings: %w", err)
	}
	hooks, ok := m["hooks"]
	if !ok {
		return nil, nil
	}
	return hooks, nil
}

// MergeHooksIntoSettings reads an existing settings.json at existingPath,
// replaces the "hooks" key with canonicalHooks, and preserves all other
// top-level keys (permissions, mcpServers, etc.).
//
// Returns:
//   - merged JSON bytes (indented)
//   - changed: true if the output differs from the existing content
//   - error on parse/write failure
//
// If the file is missing or empty, returns {"hooks": <canonicalHooks>}.
// If canonicalHooks is nil, the hooks key is removed from the output.
func MergeHooksIntoSettings(existingData []byte, canonicalHooks json.RawMessage) ([]byte, bool, error) {
	var m map[string]json.RawMessage

	if len(existingData) > 0 {
		if err := json.Unmarshal(existingData, &m); err != nil {
			// Existing file is invalid JSON -- start fresh
			m = make(map[string]json.RawMessage)
		}
	} else {
		m = make(map[string]json.RawMessage)
	}

	// Replace hooks
	if canonicalHooks != nil {
		m["hooks"] = canonicalHooks
	} else {
		delete(m, "hooks")
	}

	// Serialize with deterministic key ordering:
	// "hooks" first, "permissions" second, then alphabetical remainder.
	out, err := marshalOrderedSettings(m)
	if err != nil {
		return nil, false, fmt.Errorf("marshal settings: %w", err)
	}

	changed := !bytes.Equal(normalizeJSON(existingData), normalizeJSON(out))
	return out, changed, nil
}

// StripSettingsKeys removes selected top-level keys from a settings.json blob.
// Missing or invalid JSON is treated as an empty object.
func StripSettingsKeys(existingData []byte, keys ...string) ([]byte, bool, error) {
	var m map[string]json.RawMessage

	if len(existingData) > 0 {
		if err := json.Unmarshal(existingData, &m); err != nil {
			m = make(map[string]json.RawMessage)
		}
	} else {
		m = make(map[string]json.RawMessage)
	}

	for _, key := range keys {
		delete(m, key)
	}

	out, err := marshalOrderedSettings(m)
	if err != nil {
		return nil, false, fmt.Errorf("marshal settings: %w", err)
	}

	changed := !bytes.Equal(normalizeJSON(existingData), normalizeJSON(out))
	return out, changed, nil
}

// marshalOrderedSettings produces indented JSON with deterministic key order:
// hooks, permissions, experimental, general, tools, security, then remaining
// keys alphabetically.
func marshalOrderedSettings(m map[string]json.RawMessage) ([]byte, error) {
	// Collect keys in priority order
	priority := []string{"hooks", "permissions", "experimental", "general", "tools", "security"}
	var remaining []string
	seen := map[string]bool{}
	for _, k := range priority {
		if _, ok := m[k]; ok {
			seen[k] = true
		}
	}
	for k := range m {
		if !seen[k] {
			remaining = append(remaining, k)
		}
	}
	sort.Strings(remaining)

	var ordered []string
	for _, k := range priority {
		if seen[k] {
			ordered = append(ordered, k)
		}
	}
	ordered = append(ordered, remaining...)

	// Build JSON manually for key ordering
	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, key := range ordered {
		// Re-indent the value
		var indented bytes.Buffer
		if err := json.Indent(&indented, m[key], "  ", "  "); err != nil {
			// Fallback: use raw value
			indented.Reset()
			indented.Write(m[key])
		}
		fmt.Fprintf(&buf, "  %s: %s", jsonQuote(key), indented.String())
		if i < len(ordered)-1 {
			buf.WriteByte(',')
		}
		buf.WriteByte('\n')
	}
	buf.WriteString("}\n")
	return buf.Bytes(), nil
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// MergeSettingsForHome merges repo settings into existing home settings.
// Non-hook keys (permissions, etc.) are taken from the repo copy.
// Hooks are taken from the repo copy if present, otherwise preserved from home.
// This prevents losing hooks when syncing without --regen (repo copy has no hooks
// because they were stripped to avoid duplicate execution at multiple hierarchy levels).
func MergeSettingsForHome(homeData, repoData []byte) ([]byte, bool, error) {
	var repoMap map[string]json.RawMessage
	if err := json.Unmarshal(repoData, &repoMap); err != nil {
		return nil, false, fmt.Errorf("parse repo settings: %w", err)
	}

	// Start with repo settings (latest permissions, etc.)
	result := repoMap

	// If repo has no hooks but home does, preserve home hooks.
	if _, hasRepoHooks := repoMap["hooks"]; !hasRepoHooks && len(homeData) > 0 {
		var homeMap map[string]json.RawMessage
		if err := json.Unmarshal(homeData, &homeMap); err == nil {
			if homeHooks, ok := homeMap["hooks"]; ok {
				result["hooks"] = homeHooks
			}
		}
	}

	out, err := marshalOrderedSettings(result)
	if err != nil {
		return nil, false, fmt.Errorf("marshal settings: %w", err)
	}

	changed := !bytes.Equal(normalizeJSON(homeData), normalizeJSON(out))
	return out, changed, nil
}

// StripHooksFromFile reads a settings.json file, removes the hooks key,
// and writes it back. Returns true if the file was modified.
func StripHooksFromFile(path string) (bool, error) {
	return StripSettingsKeysFromFile(path, "hooks")
}

// StripSettingsKeysFromFile reads a settings.json file, removes the provided
// top-level keys, and writes it back. Returns true if the file was modified.
func StripSettingsKeysFromFile(path string, keys ...string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	stripped, changed, err := StripSettingsKeys(data, keys...)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	return true, writeFileAtomic(path, stripped, 0o644)
}

// normalizeJSON compacts JSON for comparison. Returns nil on error.
func normalizeJSON(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return data // return as-is if not valid JSON
	}
	return buf.Bytes()
}
