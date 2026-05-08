package agentcontext

import (
	"fmt"
	"regexp"
	"strings"
)

// EngramURIScheme is the URI prefix for engram identifiers.
const EngramURIScheme = "engram://"

// engramSlugPattern restricts family and slug segments to lowercase alnum +
// interior hyphens. Must start and end with an alphanumeric; max 64 chars.
var engramSlugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// EngramURI represents a parsed engram identifier of the form
// engram://<family>/<slug>.
type EngramURI struct {
	Family string
	Slug   string
}

// String returns the canonical URI form.
func (u EngramURI) String() string {
	return EngramURIScheme + u.Family + "/" + u.Slug
}

// ParseEngramURI parses a string into an EngramURI. Accepts both the full
// engram://family/slug form and bare family/slug.
func ParseEngramURI(s string) (EngramURI, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return EngramURI{}, fmt.Errorf("engram uri: empty")
	}
	raw = strings.TrimPrefix(raw, EngramURIScheme)

	parts := strings.Split(raw, "/")
	if len(parts) != 2 {
		return EngramURI{}, fmt.Errorf("engram uri %q: expected <family>/<slug>", s)
	}

	family, slug := parts[0], parts[1]
	if !engramSlugPattern.MatchString(family) {
		return EngramURI{}, fmt.Errorf("engram uri %q: invalid family %q", s, family)
	}
	if !engramSlugPattern.MatchString(slug) {
		return EngramURI{}, fmt.Errorf("engram uri %q: invalid slug %q", s, slug)
	}

	return EngramURI{Family: family, Slug: slug}, nil
}

// MustParseEngramURI is the panicking variant for tests and known-good inputs.
func MustParseEngramURI(s string) EngramURI {
	u, err := ParseEngramURI(s)
	if err != nil {
		panic(err)
	}
	return u
}

// SlugValid reports whether s matches the engram slug grammar.
func SlugValid(s string) bool {
	return engramSlugPattern.MatchString(s)
}

// EngramNode is the minimal info needed for graph operations: an URI and the
// list of its direct prerequisite URIs.
type EngramNode struct {
	URI           string
	Prerequisites []string
}

// PrereqResolver looks up the prerequisites of an engram by URI. Returns
// (nil, nil) if the URI is unknown — callers may treat that as a dangling
// prerequisite rather than a hard error.
type PrereqResolver func(uri string) ([]string, error)

// DetectCycle reports whether adding `node` to a graph (where existing
// engrams' prerequisites are looked up via `resolve`) would introduce a
// cycle. Returns the offending path on detection.
//
// The check walks each declared prerequisite transitively; if any path
// re-encounters node.URI, that is a cycle.
func DetectCycle(node EngramNode, resolve PrereqResolver) ([]string, error) {
	if node.URI == "" {
		return nil, fmt.Errorf("cycle check: node uri required")
	}
	visited := make(map[string]bool)
	for _, prereq := range node.Prerequisites {
		path, err := walkPrereqs(prereq, node.URI, resolve, visited, []string{node.URI})
		if err != nil {
			return path, err
		}
	}
	return nil, nil
}

func walkPrereqs(current, target string, resolve PrereqResolver, visited map[string]bool, path []string) ([]string, error) {
	if current == target {
		return append(path, current), fmt.Errorf("cycle: %s -> %s", strings.Join(path, " -> "), current)
	}
	if visited[current] {
		return nil, nil
	}
	visited[current] = true

	prereqs, err := resolve(current)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", current, err)
	}
	for _, p := range prereqs {
		nextPath := append(path, current) //nolint:gocritic // intentional fresh slice each iter via re-slicing rules
		if cyclePath, err := walkPrereqs(p, target, resolve, visited, nextPath); err != nil {
			return cyclePath, err
		}
	}
	return nil, nil
}

// TransitivePrereqs returns the set of all transitive prerequisites of
// `start`, up to `maxDepth` (0 = direct only is *not* meaningful here; pass
// a positive value or pass -1 for unlimited). The result is deduplicated and
// preserves discovery order (BFS).
func TransitivePrereqs(start string, maxDepth int, resolve PrereqResolver) ([]string, error) {
	if start == "" {
		return nil, nil
	}

	type queueItem struct {
		uri   string
		depth int
	}

	visited := map[string]bool{start: true}
	out := make([]string, 0, 8)
	queue := []queueItem{{uri: start, depth: 0}}

	for len(queue) > 0 {
		head := queue[0]
		queue = queue[1:]

		if maxDepth >= 0 && head.depth >= maxDepth {
			continue
		}

		prereqs, err := resolve(head.uri)
		if err != nil {
			return nil, fmt.Errorf("transitive prereqs: %w", err)
		}
		for _, p := range prereqs {
			if visited[p] {
				continue
			}
			visited[p] = true
			out = append(out, p)
			queue = append(queue, queueItem{uri: p, depth: head.depth + 1})
		}
	}

	return out, nil
}

// TopoSortByTier orders nodes lowest-tier-first, breaking ties alphabetically
// by URI for determinism.
func TopoSortByTier(nodes []EngramNode, tierOf func(uri string) int) []EngramNode {
	sorted := make([]EngramNode, len(nodes))
	copy(sorted, nodes)

	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			ti, tj := tierOf(sorted[i].URI), tierOf(sorted[j].URI)
			swap := false
			switch {
			case ti > tj:
				swap = true
			case ti == tj && sorted[i].URI > sorted[j].URI:
				swap = true
			}
			if swap {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}
