// Package baseimage provides a registry of pre-built base images for common
// language stacks. These images are pre-pushed to Harbor so Dockerfiles can
// use them as FROM targets, skipping the runtime/tool install layers.
package baseimage

import "strings"

// entry maps a language+version to a pre-built Harbor image tag.
type entry struct {
	Language string
	Version  string
	Image    string
}

// registry holds known pre-built base images.
// Entries are populated by scripts/build-base-images.sh pushing to Harbor.
var registry = []entry{
	{"go", "1.24", "registry.harbor.lan/mcp/devbox-base/go:1.24"},
	{"go", "1.25", "registry.harbor.lan/mcp/devbox-base/go:1.25"},
	{"python", "3.12", "registry.harbor.lan/mcp/devbox-base/python:3.12"},
	{"python", "3.13", "registry.harbor.lan/mcp/devbox-base/python:3.13"},
	{"node", "20", "registry.harbor.lan/mcp/devbox-base/node:20"},
	{"node", "22", "registry.harbor.lan/mcp/devbox-base/node:22"},
}

// Lookup returns the pre-built base image tag for a language and version.
// Returns empty string if no pre-built base exists.
func Lookup(language, version string) string {
	language = strings.ToLower(language)
	for _, candidate := range versionCandidates(version) {
		for _, e := range registry {
			if e.Language == language && e.Version == candidate {
				return e.Image
			}
		}
	}
	return ""
}

func versionCandidates(version string) []string {
	version = strings.TrimSpace(version)
	if version == "" {
		return nil
	}
	candidates := []string{version}
	parts := strings.Split(version, ".")
	if len(parts) >= 2 {
		candidates = append(candidates, parts[0]+"."+parts[1])
	}
	if len(parts) >= 1 {
		candidates = append(candidates, parts[0])
	}
	return candidates
}

// Languages returns all registered language+version pairs.
func Languages() []struct{ Language, Version, Image string } {
	result := make([]struct{ Language, Version, Image string }, len(registry))
	for i, e := range registry {
		result[i] = struct{ Language, Version, Image string }{e.Language, e.Version, e.Image}
	}
	return result
}
