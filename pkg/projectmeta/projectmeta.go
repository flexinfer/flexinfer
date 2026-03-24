package projectmeta

import "strings"

var workspaceNamespaceRoots = map[string]struct{}{
	"apps":     {},
	"libs":     {},
	"platform": {},
	"services": {},
}

// Normalize trims an explicit project identifier.
func Normalize(project string) string {
	return strings.TrimSpace(project)
}

// FromNamespace derives a project identifier from a namespace like
// "loom-core/feature-x". It returns an empty string when the namespace is
// blank or does not contain a usable project segment.
func FromNamespace(namespace string) string {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		return ""
	}
	parts := strings.Split(ns, "/")
	if len(parts) >= 2 {
		if _, ok := workspaceNamespaceRoots[parts[0]]; ok && strings.TrimSpace(parts[1]) != "" {
			return parts[0] + "/" + parts[1]
		}
		if strings.TrimSpace(parts[0]) != "" {
			return parts[0]
		}
	}
	if strings.HasPrefix(ns, "/") {
		return ""
	}
	return ns
}

// Canonical returns the preferred project identifier from the available link
// metadata. Explicit values win; namespace-derived values are the fallback.
func Canonical(explicitProject, namespace string) string {
	if project := Normalize(explicitProject); project != "" {
		return project
	}
	return FromNamespace(namespace)
}
