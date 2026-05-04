package gates

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// CommitFormat fails when any commit message in the implement stage
// doesn't match the workspace's Conventional Commits style. The mills's
// auto-merged changes flow into release tooling that derives changelogs
// from commit messages, so the format isn't optional.
//
// Subject regex matches:
//
//	<type>(<scope>)?(!)?: <description>
//
// where type ∈ feat|fix|refactor|docs|test|chore|build|ci|perf|style|debt
// and scope is any non-empty token. Bodies are not parsed.
type CommitFormat struct {
	// MaxSubjectLen overrides the default 80-char ceiling for tests.
	MaxSubjectLen int
	// AllowedTypes overrides the default type set; useful for tests.
	AllowedTypes []string
}

// Name returns the gate identifier.
func (g *CommitFormat) Name() string { return "commit_format" }

const defaultMaxSubjectLen = 80

var defaultAllowedTypes = []string{
	"feat", "fix", "refactor", "docs", "test", "chore",
	"build", "ci", "perf", "style", "debt",
}

// commitSubjectRE matches an optional scope, optional ! breaking marker,
// then ": " separator. We anchor on the type via a capture so the gate
// can give a specific reason for unknown types.
var commitSubjectRE = regexp.MustCompile(`^([a-z]+)(?:\(([^)]+)\))?(!)?:\s+(.+)$`)

// Evaluate checks every CommitMessage's subject (first line). A commit
// with a body is fine; the gate only enforces subject shape.
func (g *CommitFormat) Evaluate(_ context.Context, in StageInput) (Outcome, error) {
	if len(in.CommitMessages) == 0 {
		return pass(), nil
	}
	allowed := g.AllowedTypes
	if len(allowed) == 0 {
		allowed = defaultAllowedTypes
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, t := range allowed {
		allowedSet[t] = struct{}{}
	}
	maxLen := g.MaxSubjectLen
	if maxLen <= 0 {
		maxLen = defaultMaxSubjectLen
	}

	var reasons []string
	for i, msg := range in.CommitMessages {
		subject := strings.SplitN(strings.TrimSpace(msg), "\n", 2)[0]
		if subject == "" {
			reasons = append(reasons, fmt.Sprintf("commit %d: empty subject", i))
			continue
		}
		if len(subject) > maxLen {
			reasons = append(reasons, fmt.Sprintf(
				"commit %d: subject %d chars exceeds %d-char cap (%q)",
				i, len(subject), maxLen, truncate(subject, 40),
			))
		}
		m := commitSubjectRE.FindStringSubmatch(subject)
		if m == nil {
			reasons = append(reasons, fmt.Sprintf(
				"commit %d: %q does not match conventional format <type>(scope)?: <desc>",
				i, truncate(subject, 60),
			))
			continue
		}
		typ := m[1]
		if _, ok := allowedSet[typ]; !ok {
			reasons = append(reasons, fmt.Sprintf(
				"commit %d: type %q not in allowed set", i, typ,
			))
		}
	}
	if len(reasons) == 0 {
		return pass(), nil
	}
	return fail(reasons...), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
