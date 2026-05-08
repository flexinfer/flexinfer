package agentcontext

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// Engram metadata key constants. Stored under MemoryItem.Metadata.
const (
	mdEngramURI           = "engram_uri"
	mdEngramFamily        = "engram_family"
	mdEngramSlug          = "engram_slug"
	mdEngramTier          = "engram_tier"
	mdEngramPrerequisites = "engram_prerequisites"
	mdEngramProofStatus   = "engram_proof_status"
	mdEngramUnlockedIn    = "engram_unlocked_in"
	mdEngramLastVerified  = "engram_last_verified"
	mdEngramLanguage      = "engram_language"
	mdEngramScope         = "engram_scope"

	// Legacy recipe metadata keys (still written for back-compat consumers).
	mdRecipeProblem  = "recipe_problem"
	mdRecipeSolution = "recipe_solution"
	mdRecipeProof    = "recipe_proof"
	mdRecipeLanguage = "recipe_language"
	mdRecipeScope    = "recipe_scope"

	// Proof status values.
	ProofStatusUnverified = "unverified"
	ProofStatusVerified   = "verified"
	ProofStatusStale      = "stale"
	ProofStatusFailing    = "failing"

	// Default tier when omitted.
	DefaultEngramTier = 1
)

// EngramCategory is the MemoryItem.Category value for engrams.
const EngramCategory = "engram"

// HandleEngramAdd persists a new engram. Validates the URI, tier/proof
// contract, and runs a cycle check before storing.
func (s *Service) HandleEngramAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	title := v.Required("title")
	problem := v.Required("problem")
	solution := v.Required("solution")
	proof := v.Required("proof")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	tier := v.Int("tier", DefaultEngramTier)
	if tier < 1 || tier > 3 {
		return mcp.ErrorResult(fmt.Errorf("tier must be 1, 2, or 3 (got %d)", tier)), nil
	}

	if err := validateProofForTier(proof, tier); err != nil {
		return mcp.ErrorResult(err), nil
	}

	language := v.String("language", "")
	scope := v.String("scope", "project")
	if scope != "project" && scope != "workspace" && scope != "universal" {
		return mcp.ErrorResult(fmt.Errorf("scope must be project, workspace, or universal (got %q)", scope)), nil
	}

	family := v.String("family", "")
	slug := v.String("slug", "")

	// Derive defaults: family from title, slug from language or title.
	if family == "" {
		family = slugify(title)
	}
	if slug == "" {
		if language != "" {
			slug = language
		} else {
			slug = "default"
		}
	}
	if !SlugValid(family) {
		return mcp.ErrorResult(fmt.Errorf("invalid family slug %q", family)), nil
	}
	if !SlugValid(slug) {
		return mcp.ErrorResult(fmt.Errorf("invalid slug %q", slug)), nil
	}
	uri := EngramURI{Family: family, Slug: slug}.String()

	// Validate prerequisites are well-formed URIs.
	prereqs := v.StringSlice("prerequisites")
	for _, p := range prereqs {
		if _, err := ParseEngramURI(p); err != nil {
			return mcp.ErrorResult(fmt.Errorf("prerequisite: %w", err)), nil
		}
	}

	// Cycle check: walk each prereq's transitive prereqs; reject if our URI appears.
	resolver := s.makePrereqResolver()
	if path, err := DetectCycle(EngramNode{URI: uri, Prerequisites: prereqs}, resolver); err != nil {
		return mcp.ErrorResult(fmt.Errorf("cycle detected: path=%s", strings.Join(path, " -> "))), nil
	}

	// Reject duplicate URI within the same scope.
	if existing, _ := s.lookupEngramByURI(uri); existing != nil {
		return mcp.ErrorResult(fmt.Errorf("engram %s already exists", uri)), nil
	}

	tags := buildEngramTags(uri, family, slug, tier, language, scope, v.StringSlice("tags"))

	metadata := map[string]any{
		mdEngramURI:           uri,
		mdEngramFamily:        family,
		mdEngramSlug:          slug,
		mdEngramTier:          tier,
		mdEngramPrerequisites: stringSliceToAny(prereqs),
		mdEngramProofStatus:   ProofStatusUnverified,
		mdEngramUnlockedIn:    []any{},
		mdEngramLastVerified:  "",
		mdEngramLanguage:      language,
		mdEngramScope:         scope,

		// Legacy recipe keys (still consumed by older readers).
		mdRecipeProblem:  problem,
		mdRecipeSolution: solution,
		mdRecipeProof:    proof,
		mdRecipeLanguage: language,
		mdRecipeScope:    scope,

		"created_at": time.Now().UTC().Format(time.RFC3339),
	}

	content := fmt.Sprintf(
		"## Problem\n\n%s\n\n## Solution\n\n%s\n\n## Proof\n\n%s\n\n## Engram\n\nURI: %s\nTier: %d\nPrerequisites: %s\n",
		problem, solution, proof, uri, tier, formatPrereqList(prereqs),
	)

	memoryArgs := map[string]any{
		"items": []any{
			map[string]any{
				"title":      title,
				"content":    content,
				"tier":       "long_term",
				"importance": "high",
				"category":   EngramCategory,
				"tags":       toAnySlice(tags),
				"metadata":   metadata,
			},
		},
	}
	if sid := v.String("session_id", ""); sid != "" {
		memoryArgs["session_id"] = sid
	}
	if aid := v.String("agent_id", ""); aid != "" {
		memoryArgs["agent_id"] = aid
	}
	if ns := v.String("namespace", ""); ns != "" {
		memoryArgs["namespace"] = ns
	}

	result, err := s.HandleMemoryAdd(ctx, memoryArgs)
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return result, nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":            true,
		"uri":           uri,
		"family":        family,
		"slug":          slug,
		"tier":          tier,
		"prerequisites": prereqs,
		"proof_status":  ProofStatusUnverified,
		"language":      language,
		"scope":         scope,
		"tags":          tags,
	})
}

// HandleEngramRecall recalls engrams matching a query and optionally resolves
// transitive prerequisites.
func (s *Service) HandleEngramRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	depth := v.Int("depth", 1)
	tierMax := v.Int("tier_max", 0) // 0 = unbounded
	limit := v.Int("limit", 10)
	tokenBudget := v.Int("token_budget", 4000)

	req := MemoryRecallRequest{
		Query:       query,
		Categories:  []string{EngramCategory, "recipe"},
		Tiers:       []MemoryTier{MemoryTierLongTerm},
		Tags:        buildRecipeTagFilters(v),
		TokenBudget: tokenBudget,
		Limit:       limit,
	}

	matched, err := s.memoryHierarchy.Recall(req)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("engram recall: %w", err)), nil
	}

	matches := filterByTier(matched.Items, tierMax)

	// Transitively pull in prerequisites if requested.
	resolver := s.makePrereqResolver()
	seen := map[string]bool{}
	var prereqItems []MemoryItem
	if depth > 0 {
		for _, m := range matches {
			uri := metadataString(m.Metadata, mdEngramURI)
			if uri == "" {
				continue
			}
			seen[uri] = true
		}
		for _, m := range matches {
			uri := metadataString(m.Metadata, mdEngramURI)
			if uri == "" {
				continue
			}
			prereqs, _ := TransitivePrereqs(uri, depth, resolver)
			for _, p := range prereqs {
				if seen[p] {
					continue
				}
				seen[p] = true
				if item, _ := s.lookupEngramByURI(p); item != nil {
					prereqItems = append(prereqItems, *item)
				}
			}
		}
	}

	all := append([]MemoryItem{}, matches...)
	all = append(all, prereqItems...)
	all = sortByTierThenURI(all)

	out := make([]map[string]any, 0, len(all))
	totalTokens := 0
	for _, item := range all {
		summary := engramItemToMap(item)
		out = append(out, summary)
		totalTokens += item.OriginalTokens
		if tokenBudget > 0 && totalTokens >= tokenBudget {
			break
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":              true,
		"count":           len(out),
		"items":           out,
		"matched_count":   len(matches),
		"prereq_count":    len(prereqItems),
		"depth":           depth,
		"truncated":       totalTokens >= tokenBudget && tokenBudget > 0 && len(out) < len(all),
		"total_tokens":    totalTokens,
		"requested_query": query,
	})
}

// HandleEngramList lists engrams with optional filters.
func (s *Service) HandleEngramList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	limit := v.Int("limit", 50)
	tierMax := v.Int("tier", 0)

	req := MemoryRecallRequest{
		// Empty query => no substring filter; we still scope by category+tags.
		Categories: []string{EngramCategory, "recipe"},
		Tiers:      []MemoryTier{MemoryTierLongTerm},
		Tags:       buildRecipeTagFilters(v),
		Limit:      limit,
	}

	if family := v.String("family", ""); family != "" {
		req.Tags = append(req.Tags, "engram-family:"+family)
	}
	if status := v.String("proof_status", ""); status != "" {
		req.Tags = append(req.Tags, "engram-status:"+status)
	}

	res, err := s.memoryHierarchy.Recall(req)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("engram list: %w", err)), nil
	}

	items := filterByTier(res.Items, tierMax)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, engramItemToMap(item))
	}

	return mcp.JSONResult(map[string]any{
		"ok":    true,
		"count": len(out),
		"items": out,
	})
}

// HandleEngramGraph returns the adjacency list for the prerequisite graph
// rooted at a given URI or family.
func (s *Service) HandleEngramGraph(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	root := v.Required("root")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	maxDepth := v.Int("max_depth", 3)
	direction := v.String("direction", "down")

	resolver := s.makePrereqResolver()

	type edge struct{ From, To string }
	nodes := map[string]bool{root: true}
	var edges []edge

	switch direction {
	case "down":
		// Walk root's prereqs.
		visited := map[string]bool{root: true}
		queue := []struct {
			uri   string
			depth int
		}{{uri: root, depth: 0}}
		for len(queue) > 0 {
			head := queue[0]
			queue = queue[1:]
			if maxDepth > 0 && head.depth >= maxDepth {
				continue
			}
			prereqs, _ := resolver(head.uri)
			for _, p := range prereqs {
				edges = append(edges, edge{From: head.uri, To: p})
				nodes[p] = true
				if !visited[p] {
					visited[p] = true
					queue = append(queue, struct {
						uri   string
						depth int
					}{uri: p, depth: head.depth + 1})
				}
			}
		}
	case "up":
		// Walk dependents (engrams that list root in prerequisites).
		dependents, err := s.findDependents(root, maxDepth)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		for _, e := range dependents {
			nodes[e.From] = true
			nodes[e.To] = true
			edges = append(edges, edge{From: e.From, To: e.To})
		}
	default:
		return mcp.ErrorResult(fmt.Errorf("direction must be down or up (got %q)", direction)), nil
	}

	nodeList := make([]string, 0, len(nodes))
	for n := range nodes {
		nodeList = append(nodeList, n)
	}
	sort.Strings(nodeList)

	edgeList := make([]map[string]string, 0, len(edges))
	for _, e := range edges {
		edgeList = append(edgeList, map[string]string{"from": e.From, "to": e.To})
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"root":      root,
		"direction": direction,
		"nodes":     nodeList,
		"edges":     edgeList,
	})
}

// makePrereqResolver returns a closure that looks up an engram's
// prerequisites by URI from the memory hierarchy. Unknown URIs return
// (nil, nil) so dangling refs do not crash callers.
func (s *Service) makePrereqResolver() PrereqResolver {
	return func(uri string) ([]string, error) {
		item, err := s.lookupEngramByURI(uri)
		if err != nil {
			return nil, err
		}
		if item == nil {
			return nil, nil
		}
		return metadataStringSlice(item.Metadata, mdEngramPrerequisites), nil
	}
}

// lookupEngramByURI returns the memory item whose engram URI tag matches.
// Returns (nil, nil) if no match is found.
func (s *Service) lookupEngramByURI(uri string) (*MemoryItem, error) {
	if s.memoryHierarchy == nil {
		return nil, nil
	}
	res, err := s.memoryHierarchy.Recall(MemoryRecallRequest{
		// Query intentionally empty: substring-matching against title/content
		// would miss URI-tagged items unless their text happens to contain
		// the URI literal.
		Categories: []string{EngramCategory},
		Tiers:      []MemoryTier{MemoryTierLongTerm},
		Tags:       []string{"uri:" + uri},
		Limit:      1,
	})
	if err != nil {
		return nil, err
	}
	for i := range res.Items {
		// Confirm exact URI (Recall tag-match is any-of; the URI tag is
		// globally unique by construction so this is paranoia).
		if metadataString(res.Items[i].Metadata, mdEngramURI) == uri {
			return &res.Items[i], nil
		}
	}
	return nil, nil
}

// findDependents returns edges {from -> to} where `to` is `target` and
// `from` is any engram whose prerequisites include `target`. Walks up
// transitively to maxDepth.
func (s *Service) findDependents(target string, maxDepth int) ([]struct{ From, To string }, error) {
	res, err := s.memoryHierarchy.Recall(MemoryRecallRequest{
		Categories: []string{EngramCategory},
		Tiers:      []MemoryTier{MemoryTierLongTerm},
		Limit:      1000,
	})
	if err != nil {
		return nil, err
	}

	// Index prereq -> dependents for one BFS pass.
	dependents := make(map[string][]string)
	for _, item := range res.Items {
		uri := metadataString(item.Metadata, mdEngramURI)
		if uri == "" {
			continue
		}
		for _, p := range metadataStringSlice(item.Metadata, mdEngramPrerequisites) {
			dependents[p] = append(dependents[p], uri)
		}
	}

	var edges []struct{ From, To string }
	visited := map[string]bool{target: true}
	type qItem struct {
		uri   string
		depth int
	}
	queue := []qItem{{uri: target, depth: 0}}
	for len(queue) > 0 {
		head := queue[0]
		queue = queue[1:]
		if maxDepth > 0 && head.depth >= maxDepth {
			continue
		}
		for _, d := range dependents[head.uri] {
			edges = append(edges, struct{ From, To string }{From: d, To: head.uri})
			if !visited[d] {
				visited[d] = true
				queue = append(queue, qItem{uri: d, depth: head.depth + 1})
			}
		}
	}
	return edges, nil
}

// validateProofForTier enforces the tier/proof contract described in
// .loom/114-product-spec-engram-tech-tree §3.2.
//
//	tier 1: any non-empty proof (file ref, command, or URL).
//	tier 2: must include "command:" line.
//	tier 3: must include "command:" AND ("benchmark:" OR "dashboard:").
func validateProofForTier(proof string, tier int) error {
	p := strings.ToLower(proof)
	switch tier {
	case 1:
		return nil
	case 2:
		if !strings.Contains(p, "command:") {
			return fmt.Errorf("tier 2 proof must include a 'command:' line (runnable test). got: %q", proof)
		}
		return nil
	case 3:
		if !strings.Contains(p, "command:") {
			return fmt.Errorf("tier 3 proof must include 'command:'")
		}
		if !strings.Contains(p, "benchmark:") && !strings.Contains(p, "dashboard:") {
			return fmt.Errorf("tier 3 proof must include 'benchmark:' or 'dashboard:'")
		}
		return nil
	default:
		return fmt.Errorf("unsupported tier %d", tier)
	}
}

// buildEngramTags assembles the tag set written to MemoryItem.Tags. The URI
// tag is the unique lookup key. tier:N and engram-family:F enable filtered
// listing.
func buildEngramTags(uri, family, slug string, tier int, language, scope string, extra []string) []string {
	tags := []string{
		"engram",
		"recipe", // back-compat: legacy recall queries filter by this tag
		"uri:" + uri,
		"engram-family:" + family,
		"engram-slug:" + slug,
		fmt.Sprintf("tier:%d", tier),
		"engram-status:" + ProofStatusUnverified,
		"scope:" + scope,
	}
	if language != "" {
		tags = append(tags, "lang:"+language)
	}
	tags = append(tags, extra...)
	return tags
}

// engramItemToMap projects a MemoryItem into the JSON shape returned by recall/list.
func engramItemToMap(item MemoryItem) map[string]any {
	prereqs := metadataStringSlice(item.Metadata, mdEngramPrerequisites)
	unlocked := metadataStringSlice(item.Metadata, mdEngramUnlockedIn)
	return map[string]any{
		"id":            item.ID,
		"title":         item.Title,
		"content":       item.Content,
		"uri":           metadataString(item.Metadata, mdEngramURI),
		"family":        metadataString(item.Metadata, mdEngramFamily),
		"slug":          metadataString(item.Metadata, mdEngramSlug),
		"tier":          metadataInt(item.Metadata, mdEngramTier, DefaultEngramTier),
		"language":      metadataString(item.Metadata, mdEngramLanguage),
		"scope":         metadataString(item.Metadata, mdEngramScope),
		"prerequisites": prereqs,
		"proof_status":  metadataStringDefault(item.Metadata, mdEngramProofStatus, ProofStatusUnverified),
		"unlocked_in":   unlocked,
		"last_verified": metadataString(item.Metadata, mdEngramLastVerified),
		"problem":       metadataString(item.Metadata, mdRecipeProblem),
		"solution":      metadataString(item.Metadata, mdRecipeSolution),
		"proof":         metadataString(item.Metadata, mdRecipeProof),
		"tags":          item.Tags,
	}
}

// filterByTier keeps items whose engram_tier is <= tierMax. tierMax==0
// disables filtering. Items missing the tier metadata are kept (likely
// legacy recipes; treat as tier 1).
func filterByTier(items []MemoryItem, tierMax int) []MemoryItem {
	if tierMax <= 0 {
		return items
	}
	out := items[:0]
	for _, item := range items {
		t := metadataInt(item.Metadata, mdEngramTier, DefaultEngramTier)
		if t <= tierMax {
			out = append(out, item)
		}
	}
	return out
}

// sortByTierThenURI orders items lowest-tier-first, breaking ties by URI.
func sortByTierThenURI(items []MemoryItem) []MemoryItem {
	sort.SliceStable(items, func(i, j int) bool {
		ti := metadataInt(items[i].Metadata, mdEngramTier, DefaultEngramTier)
		tj := metadataInt(items[j].Metadata, mdEngramTier, DefaultEngramTier)
		if ti != tj {
			return ti < tj
		}
		ui := metadataString(items[i].Metadata, mdEngramURI)
		uj := metadataString(items[j].Metadata, mdEngramURI)
		return ui < uj
	})
	return items
}

// --- metadata helpers ---

func metadataString(md map[string]any, key string) string {
	if md == nil {
		return ""
	}
	if v, ok := md[key].(string); ok {
		return v
	}
	return ""
}

func metadataStringDefault(md map[string]any, key, def string) string {
	v := metadataString(md, key)
	if v == "" {
		return def
	}
	return v
}

func metadataInt(md map[string]any, key string, def int) int {
	if md == nil {
		return def
	}
	switch v := md[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return def
}

func metadataStringSlice(md map[string]any, key string) []string {
	if md == nil {
		return nil
	}
	switch v := md[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func stringSliceToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// slugify is a minimal, deterministic kebab-case helper for default slugs.
// Caller-supplied slugs are validated separately; this is only used to
// derive a sensible default from a title.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		out = "engram"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func formatPrereqList(prereqs []string) string {
	if len(prereqs) == 0 {
		return "(none)"
	}
	return strings.Join(prereqs, ", ")
}
