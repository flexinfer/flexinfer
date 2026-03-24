package projectmeta

import "strings"

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
	if i := strings.IndexRune(ns, '/'); i > 0 {
		return ns[:i]
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
