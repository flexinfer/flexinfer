package generator

import (
	"os"

	"github.com/crb2nu/loom/pkg/registry"
)

// activeHost returns the host profile selector for sync-time overrides.
// Set via the LOOM_HOST environment variable (or the --host flag, which sets
// LOOM_HOST before the generator runs). Empty means "no host override —
// emit the base config." Used to make the same registry.yaml produce
// different configs on macOS, code-server pods, or other environments where
// path layout / sandbox capabilities differ.
func activeHost() string {
	return os.Getenv("LOOM_HOST")
}

// hostOverride returns the override map for the currently active host, or
// nil when no override applies. Lookup is pp.Settings["host_overrides"][<host>].
// Keys inside the override map shadow the corresponding base fields when
// consumed by a generator (e.g. additional_directories, sandbox_mode).
//
// host_overrides lives inside Settings (which is map[string]any) instead of
// as a top-level PlatformPermission field so we don't need to extend the
// fi-mcp-kit schema. fi-mcp-kit's yaml.Unmarshal silently accepts unknown
// nested keys.
func hostOverride(pp *registry.PlatformPermission) map[string]any {
	if pp == nil || pp.Settings == nil {
		return nil
	}
	host := activeHost()
	if host == "" {
		return nil
	}
	overrides, ok := pp.Settings["host_overrides"].(map[string]any)
	if !ok {
		return nil
	}
	override, ok := overrides[host].(map[string]any)
	if !ok {
		return nil
	}
	return override
}

// hostOverrideStringSlice reads a string slice from the host override map.
// Returns nil when the key is absent or the value isn't a string slice.
func hostOverrideStringSlice(override map[string]any, key string) []string {
	if override == nil {
		return nil
	}
	raw, ok := override[key]
	if !ok {
		return nil
	}
	return coerceStringSlice(raw)
}

// hostOverrideString reads a string from the host override map. Returns ""
// when absent or not a string.
func hostOverrideString(override map[string]any, key string) string {
	if override == nil {
		return ""
	}
	if v, ok := override[key].(string); ok {
		return v
	}
	return ""
}
