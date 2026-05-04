package gates

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// Scope fails when the implement stage modified files outside the slice's
// declared scope. The council emits each backlog item with a per-slice
// `files` + `tests` allowlist; the implement worker is supposed to stay
// inside that envelope. A scope violation usually means either:
//
//   - the worker took an unrelated detour (escalate; the plan needs to be
//     re-decomposed by the next council run), or
//   - the slice list was incomplete (escalate; humans should re-author it).
//
// Either way the right move is to stop the pipeline rather than auto-merge.
type Scope struct {
	// AllowTests opts out of the gate for files matching common test
	// extensions/paths even when they're not in the slice's `tests` list.
	// Keeps the gate from over-firing on incidental fixture renames; defaults
	// to true because the cost of a false-positive escalation is high.
	AllowTests bool
}

// Name returns the gate identifier.
func (g *Scope) Name() string { return "scope" }

// Evaluate compares in.FilesChanged to the union of every slice's files +
// tests on the backlog item.
func (g *Scope) Evaluate(_ context.Context, in StageInput) (Outcome, error) {
	if in.Item == nil {
		// Without an item we have no scope to compare against; treat as a
		// pass so the gate doesn't block stages that legitimately run
		// before the item is materialised (e.g. a dryrun smoke).
		return pass(), nil
	}
	if len(in.FilesChanged) == 0 {
		return pass(), nil
	}
	allowed := buildAllowedSet(in.Item.Slices, g.AllowTests || true)
	if allowed.empty() {
		// No slices => no scope can be enforced. Don't pass silently —
		// the council should always emit at least one slice; flag the
		// drift so the operator can re-decompose.
		return fail("backlog item has no slices; no scope to enforce"), nil
	}

	var violations []string
	for _, f := range in.FilesChanged {
		if isAllowed(f, allowed, g.AllowTests || true) {
			continue
		}
		violations = append(violations, f)
	}
	if len(violations) == 0 {
		return pass(), nil
	}
	sort.Strings(violations)
	// Cap the rendered list to keep persisted reasons[] readable; the full
	// list is in stage_results.artifacts_json for forensics.
	const maxRendered = 8
	rendered := violations
	suffix := ""
	if len(rendered) > maxRendered {
		suffix = fmt.Sprintf(" (and %d more)", len(rendered)-maxRendered)
		rendered = rendered[:maxRendered]
	}
	return fail(fmt.Sprintf(
		"%d file(s) outside slice scope: %s%s",
		len(violations), strings.Join(rendered, ", "), suffix,
	)), nil
}

// allowedSet keeps the literal allowlisted paths plus any directories
// implied by glob patterns. Lookups are O(1) for literal hits; glob
// fallbacks (filepath.Match) are quadratic but n is small.
type allowedSet struct {
	literals map[string]struct{}
	globs    []string
}

func buildAllowedSet(slices []store.Slice, includeTests bool) allowedSet {
	set := allowedSet{literals: make(map[string]struct{})}
	for _, s := range slices {
		for _, f := range s.Files {
			set.add(f)
		}
		if includeTests {
			for _, t := range s.Tests {
				set.add(t)
			}
		}
	}
	return set
}

func (s *allowedSet) empty() bool {
	return len(s.literals) == 0 && len(s.globs) == 0
}

func (s *allowedSet) add(path string) {
	if path == "" {
		return
	}
	if strings.ContainsAny(path, "*?[") {
		s.globs = append(s.globs, path)
		return
	}
	s.literals[filepath.Clean(path)] = struct{}{}
}

func isAllowed(path string, allowed allowedSet, allowTests bool) bool {
	cleaned := filepath.Clean(path)
	if _, ok := allowed.literals[cleaned]; ok {
		return true
	}
	for _, pat := range allowed.globs {
		if matched, _ := filepath.Match(pat, cleaned); matched {
			return true
		}
	}
	if allowTests && looksLikeTestFile(cleaned) {
		return true
	}
	return false
}

// looksLikeTestFile recognises the common test-file conventions across the
// languages this workspace uses so a slice that adds a fixture under
// `_test.go` / `*.test.ts` / `tests/...` doesn't trip the gate.
func looksLikeTestFile(path string) bool {
	switch {
	case strings.HasSuffix(path, "_test.go"):
		return true
	case strings.HasSuffix(path, ".test.ts"), strings.HasSuffix(path, ".test.tsx"),
		strings.HasSuffix(path, ".spec.ts"), strings.HasSuffix(path, ".spec.tsx"):
		return true
	case strings.HasSuffix(path, ".test.js"), strings.HasSuffix(path, ".spec.js"):
		return true
	case strings.HasPrefix(path, "tests/"), strings.Contains(path, "/tests/"),
		strings.HasPrefix(path, "test/"), strings.Contains(path, "/test/"):
		return true
	case strings.HasSuffix(path, "_test.py"), strings.HasPrefix(filepath.Base(path), "test_"):
		return true
	}
	return false
}
