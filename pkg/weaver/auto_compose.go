package weaver

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// AutoComposeRouter is the minimal interface AutoCompose needs from a router.
// Allows unit tests to stub the dispatch path without instantiating a full
// Router + FlexInfer stack.
type AutoComposeRouter interface {
	// Registry returns the domain registry used for read-only selection.
	Registry() *DomainRegistry
	// Query dispatches a query against the provided domains and synthesizes
	// the per-domain results. Auto-compose populates Domains explicitly to
	// bypass classification.
	Query(ctx context.Context, req QueryRequest) (QueryResult, error)
}

// domainScore is an intermediate ranking entry.
type domainScore struct {
	name  string
	score int
}

// AutoCompose selects up to maxDomains from the router registry using a
// deterministic lowercased substring match against each domain's description,
// refuses any domain with Write=true, then dispatches the selected domains
// through router.Query. Returns an empty QueryResult when no domains score.
//
// This function performs no LLM calls of its own; the downstream Query path
// handles per-domain orchestration and synthesis.
func AutoCompose(ctx context.Context, router AutoComposeRouter, query string, maxDomains int) (QueryResult, error) {
	if router == nil {
		return QueryResult{}, fmt.Errorf("auto-compose: router is nil")
	}
	if maxDomains <= 0 {
		maxDomains = DefaultAutoComposeMaxDomains
	}

	reg := router.Registry()
	if reg == nil {
		return QueryResult{}, fmt.Errorf("auto-compose: registry is nil")
	}

	picked := selectDomains(reg.List(), query, maxDomains)
	if len(picked) == 0 {
		return QueryResult{}, nil
	}

	return router.Query(ctx, QueryRequest{
		Query:   query,
		Domains: picked,
	})
}

// selectDomains ranks domains deterministically and returns the top-N names.
// Selection rules:
//   - Skip any SubAgent with Write == true.
//   - Tokenize the (lowercased) query; substring-match each token against the
//     domain's description (lowercased). Each match increments the score by 1.
//   - Break ties by domain name (ascending) for stable output.
//   - Return at most max names.
//   - A zero score excludes the domain entirely.
func selectDomains(domains []SubAgent, query string, max int) []string {
	if max <= 0 || len(domains) == 0 || strings.TrimSpace(query) == "" {
		return nil
	}

	tokens := tokenizeQuery(query)
	if len(tokens) == 0 {
		return nil
	}

	scored := make([]domainScore, 0, len(domains))
	for _, d := range domains {
		if d.Write {
			continue
		}
		desc := strings.ToLower(d.Description)
		if desc == "" {
			continue
		}
		s := 0
		for _, tok := range tokens {
			if strings.Contains(desc, tok) {
				s++
			}
		}
		if s > 0 {
			scored = append(scored, domainScore{name: d.Name, score: s})
		}
	}

	// Deterministic ordering: higher score first, then name ascending.
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].name < scored[j].name
	})

	if len(scored) > max {
		scored = scored[:max]
	}
	out := make([]string, 0, len(scored))
	for _, s := range scored {
		out = append(out, s.name)
	}
	return out
}

// tokenizeQuery lowercases and splits on non-alphanumeric runes. It drops
// stopword-sized (<=2 char) tokens which are too noisy for substring matching.
func tokenizeQuery(query string) []string {
	lower := strings.ToLower(query)
	raw := strings.FieldsFunc(lower, func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z':
			return false
		case r >= '0' && r <= '9':
			return false
		default:
			return true
		}
	})
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, t := range raw {
		if len(t) <= 2 {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}
