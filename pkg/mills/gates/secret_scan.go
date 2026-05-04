package gates

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// SecretScan fails when the diff body contains anything that looks like a
// credential. It runs a small regex set covering the high-signal patterns
// (provider API keys, private-key armor, JWT-shaped tokens) — the workspace
// will eventually wire mcp-secret-scanner here for the long tail, but the
// regex set is enough to catch the nine-out-of-ten foot-guns and ship.
//
// The gate scans only added lines (`^+`) so an old key being deleted from
// the diff doesn't trip the gate; that's already a fix, not a leak.
type SecretScan struct {
	// Extra patterns are appended to the built-in set; useful for tests
	// or workspace-specific tokens.
	Extra []*regexp.Regexp
}

// Name returns the gate identifier.
func (g *SecretScan) Name() string { return "secret_scan" }

var defaultSecretPatterns = []namedPattern{
	{name: "AWS access key", re: regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{name: "GitHub token", re: regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`)},
	{name: "GitLab PAT", re: regexp.MustCompile(`glpat-[A-Za-z0-9_\-]{20,}`)},
	{name: "Slack token", re: regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{10,}`)},
	{name: "Anthropic key", re: regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{20,}`)},
	{name: "OpenAI key", re: regexp.MustCompile(`sk-(?:proj-)?[A-Za-z0-9_\-]{20,}`)},
	{name: "Google API key", re: regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`)},
	{name: "JWT", re: regexp.MustCompile(`eyJ[A-Za-z0-9_\-]{8,}\.eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`)},
	{name: "Private key armor", re: regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----`)},
	// Generic high-entropy base64 chunks are too noisy; rely on context-
	// specific patterns above. Add an Extra pattern if a workspace token
	// shape gets missed.
}

type namedPattern struct {
	name string
	re   *regexp.Regexp
}

// Evaluate scans only added lines from in.DiffPatch against the pattern
// set. An empty diff passes; absent diffs (gates that ran before any
// implement step) also pass.
func (g *SecretScan) Evaluate(_ context.Context, in StageInput) (Outcome, error) {
	if len(in.DiffPatch) == 0 {
		return pass(), nil
	}
	added := addedLines(in.DiffPatch)
	if len(added) == 0 {
		return pass(), nil
	}
	hits := make(map[string]struct{})
	patterns := append([]namedPattern(nil), defaultSecretPatterns...)
	for _, p := range g.Extra {
		patterns = append(patterns, namedPattern{name: "custom", re: p})
	}
	for _, line := range added {
		for _, p := range patterns {
			if p.re.MatchString(line) {
				hits[p.name] = struct{}{}
			}
		}
	}
	if len(hits) == 0 {
		return pass(), nil
	}
	names := make([]string, 0, len(hits))
	for n := range hits {
		names = append(names, n)
	}
	sort.Strings(names)
	return fail(fmt.Sprintf(
		"diff contains %d secret pattern hit(s): %s",
		len(names), strings.Join(names, ", "),
	)), nil
}

// addedLines extracts the body of every `+` line in a unified diff,
// excluding the `+++` header lines so file paths don't trigger the
// scanners.
func addedLines(diff []byte) []string {
	var out []string
	for _, raw := range strings.Split(string(diff), "\n") {
		if !strings.HasPrefix(raw, "+") {
			continue
		}
		if strings.HasPrefix(raw, "+++") {
			continue
		}
		out = append(out, raw[1:])
	}
	return out
}
