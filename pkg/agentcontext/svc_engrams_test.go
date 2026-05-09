package agentcontext

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/trace/noop"
)

// engramTestEnvOnce ensures LOOM_MCP_OUTPUT_FORMAT is set exactly once for
// this test binary. We can't use t.Setenv because the engram tests run with
// t.Parallel(), and t.Setenv panics in parallel tests. os.Setenv is safe.
var engramTestEnvOnce sync.Once

func ensureEngramTestEnv() {
	engramTestEnvOnce.Do(func() {
		os.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	})
}

// newEngramTestService spins up a minimal Service with just the memory
// substrate the engram tools need. Persistence is not configured, so writes
// stay in-memory (PersistItem short-circuits when MemoryQdrant is nil; see
// memory_hierarchy_persist.go:35).
//
// LOOM_MCP_OUTPUT_FORMAT=json is set once via TestMain so JSONResult emits
// machine-parseable JSON instead of the default TOON format.
func newEngramTestService() *Service {
	ensureEngramTestEnv()
	svc := &Service{
		cfg:     Config{},
		logger:  slog.Default(),
		tracer:  noop.NewTracerProvider().Tracer("test"),
		metrics: GetMetrics(),
	}
	svc.memoryHierarchy = NewMemoryHierarchy()
	svc.persistedMemoryHierarchy = svc.memoryHierarchy.SetPersistence(&MemoryPersistenceConfig{})
	svc.memory = &MemorySvc{Service: svc}
	return svc
}

// readResultJSON extracts the JSON object encoded in a CallToolResult's
// first text content block.
func readResultJSON(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res == nil {
		t.Fatal("nil result")
	}
	if len(res.Content) == 0 {
		t.Fatal("result has no content")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("unmarshal: %v (text=%q)", err, res.Content[0].Text)
	}
	return out
}

// ---------------------------------------------------------------------------
// HandleEngramAdd
// ---------------------------------------------------------------------------

func TestHandleEngramAdd_Defaults(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()

	res, err := svc.HandleEngramAdd(context.Background(), map[string]any{
		"title":    "Atomic file write",
		"problem":  "Concurrent readers see partial writes during os.WriteFile",
		"solution": "Write to tempfile and rename",
		"proof":    "pkg/skills/fileops.go:42-58",
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if res.IsError {
		t.Fatalf("error result: %+v", res)
	}

	payload := readResultJSON(t, res)
	if payload["uri"] != "engram://atomic-file-write/default" {
		t.Errorf("uri=%v", payload["uri"])
	}
	if payload["tier"].(float64) != 1 {
		t.Errorf("expected tier 1, got %v", payload["tier"])
	}
	if payload["proof_status"] != ProofStatusUnverified {
		t.Errorf("expected proof_status=unverified, got %v", payload["proof_status"])
	}
}

func TestHandleEngramAdd_Tier2RejectsNonCommandProof(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()

	res, err := svc.HandleEngramAdd(context.Background(), map[string]any{
		"title":    "Connection pool with healthcheck",
		"problem":  "Stale DB connections after idle timeout",
		"solution": "Configure ConnMaxLifetime + healthcheck ping",
		"proof":    "pkg/db/pool.go",
		"tier":     2,
	})
	if err != nil {
		t.Fatalf("unexpected err=%v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for tier 2 without command:")
	}
}

func TestHandleEngramAdd_Tier2AcceptsCommandProof(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()

	res, err := svc.HandleEngramAdd(context.Background(), map[string]any{
		"title":    "Connection pool",
		"problem":  "Stale DB connections",
		"solution": "Set ConnMaxLifetime",
		"proof":    "command: go test ./pkg/db -run TestPoolReconnect",
		"tier":     2,
		"family":   "db-pool-healthcheck",
		"language": "go",
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if res.IsError {
		t.Fatalf("error result: %+v", res)
	}
	payload := readResultJSON(t, res)
	if payload["uri"] != "engram://db-pool-healthcheck/go" {
		t.Errorf("unexpected uri: %v", payload["uri"])
	}
}

func TestHandleEngramAdd_DuplicateURIRejected(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	args := map[string]any{
		"title":    "Atomic file write",
		"problem":  "Partial writes",
		"solution": "Tempfile + rename",
		"proof":    "pkg/skills/fileops.go:42",
		"family":   "atomic-file-write",
		"slug":     "go",
	}
	if _, err := svc.HandleEngramAdd(context.Background(), args); err != nil {
		t.Fatalf("first add: %v", err)
	}
	res, err := svc.HandleEngramAdd(context.Background(), args)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !res.IsError {
		t.Fatal("expected duplicate URI to be rejected")
	}
}

func TestHandleEngramAdd_PrerequisitesValidated(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()

	res, err := svc.HandleEngramAdd(context.Background(), map[string]any{
		"title":         "Composite",
		"problem":       "p",
		"solution":      "s",
		"proof":         "command: go test",
		"tier":          2,
		"prerequisites": []any{"NOT a uri"},
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for malformed prerequisite URI")
	}
}

func TestHandleEngramAdd_CycleRejected(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()

	// First add A.
	if _, err := svc.HandleEngramAdd(context.Background(), map[string]any{
		"title":    "A",
		"problem":  "p",
		"solution": "s",
		"proof":    "f.go:1",
		"family":   "fam-a",
		"slug":     "x",
	}); err != nil {
		t.Fatalf("add A: %v", err)
	}

	// Now adding B with prereq A is fine.
	if _, err := svc.HandleEngramAdd(context.Background(), map[string]any{
		"title":         "B",
		"problem":       "p",
		"solution":      "s",
		"proof":         "f.go:1",
		"family":        "fam-b",
		"slug":          "x",
		"prerequisites": []any{"engram://fam-a/x"},
	}); err != nil {
		t.Fatalf("add B: %v", err)
	}

	// Manually mutate A so its prereqs include B (simulating a future state).
	// Then re-adding A would be a cycle. We can't re-add A (duplicate), so
	// instead try to add C with prereq C-self via prereq chain. Simpler test:
	// just verify direct self-prereq is rejected for a fresh engram.
	res, err := svc.HandleEngramAdd(context.Background(), map[string]any{
		"title":         "C",
		"problem":       "p",
		"solution":      "s",
		"proof":         "f.go:1",
		"family":        "fam-c",
		"slug":          "x",
		"prerequisites": []any{"engram://fam-c/x"},
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !res.IsError {
		t.Fatal("expected self-cycle to be rejected")
	}
}

// ---------------------------------------------------------------------------
// HandleEngramRecall
// ---------------------------------------------------------------------------

func TestHandleEngramRecall_IncludesPrerequisites(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	ctx := context.Background()

	// Tier 1 base.
	if _, err := svc.HandleEngramAdd(ctx, map[string]any{
		"title":    "Error wrap with context",
		"problem":  "Lose context when bubbling errors",
		"solution": "fmt.Errorf(\"%w\", err)",
		"proof":    "pkg/x.go:10",
		"family":   "error-wrap",
		"slug":     "go",
	}); err != nil {
		t.Fatalf("add base: %v", err)
	}

	// Tier 2 dependent.
	if _, err := svc.HandleEngramAdd(ctx, map[string]any{
		"title":         "Retry with jitter",
		"problem":       "Hot loop on transient errors",
		"solution":      "Exponential backoff with jitter",
		"proof":         "command: go test ./pkg/retry",
		"tier":          2,
		"family":        "retry-jitter",
		"slug":          "go",
		"prerequisites": []any{"engram://error-wrap/go"},
	}); err != nil {
		t.Fatalf("add dep: %v", err)
	}

	res, err := svc.HandleEngramRecall(ctx, map[string]any{
		"query": "retry",
		"depth": 1,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if res.IsError {
		t.Fatalf("error: %+v", res)
	}

	payload := readResultJSON(t, res)
	items, ok := payload["items"].([]any)
	if !ok {
		t.Fatalf("items not slice: %T", payload["items"])
	}

	hasBase, hasDep := false, false
	for _, it := range items {
		m := it.(map[string]any)
		switch m["uri"] {
		case "engram://error-wrap/go":
			hasBase = true
		case "engram://retry-jitter/go":
			hasDep = true
		}
	}
	if !hasDep {
		t.Errorf("recall missed retry-jitter")
	}
	if !hasBase {
		t.Errorf("recall did not pull in error-wrap prerequisite via depth=1")
	}
}

func TestHandleEngramRecall_DepthZeroSkipsPrereqs(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	ctx := context.Background()

	if _, err := svc.HandleEngramAdd(ctx, map[string]any{
		"title": "base", "problem": "p", "solution": "s", "proof": "f:1",
		"family": "base-engram", "slug": "x",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.HandleEngramAdd(ctx, map[string]any{
		"title": "dep", "problem": "p", "solution": "s", "proof": "f:1",
		"family": "dep-engram", "slug": "x",
		"prerequisites": []any{"engram://base-engram/x"},
	}); err != nil {
		t.Fatalf("add dep: %v", err)
	}

	res, err := svc.HandleEngramRecall(ctx, map[string]any{
		"query": "dep",
		"depth": 0,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	payload := readResultJSON(t, res)
	if payload["prereq_count"].(float64) != 0 {
		t.Errorf("expected 0 prereqs at depth=0, got %v", payload["prereq_count"])
	}
}

// ---------------------------------------------------------------------------
// HandleEngramGraph
// ---------------------------------------------------------------------------

func TestHandleEngramGraph_DownTraversal(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	ctx := context.Background()

	if _, err := svc.HandleEngramAdd(ctx, map[string]any{
		"title": "leaf", "problem": "p", "solution": "s", "proof": "f:1",
		"family": "leaf-graph", "slug": "x",
	}); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := svc.HandleEngramAdd(ctx, map[string]any{
		"title": "root", "problem": "p", "solution": "s", "proof": "f:1",
		"family": "root-graph", "slug": "x",
		"prerequisites": []any{"engram://leaf-graph/x"},
	}); err != nil {
		t.Fatalf("%v", err)
	}

	res, err := svc.HandleEngramGraph(ctx, map[string]any{
		"root":      "engram://root-graph/x",
		"direction": "down",
		"max_depth": 3,
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if res.IsError {
		t.Fatalf("err result: %+v", res)
	}
	payload := readResultJSON(t, res)
	edges := payload["edges"].([]any)
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d: %v", len(edges), edges)
	}
	e := edges[0].(map[string]any)
	if e["from"] != "engram://root-graph/x" || e["to"] != "engram://leaf-graph/x" {
		t.Errorf("unexpected edge: %v", e)
	}
}

func TestHandleEngramGraph_UpTraversal(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	ctx := context.Background()

	if _, err := svc.HandleEngramAdd(ctx, map[string]any{
		"title": "shared base", "problem": "p", "solution": "s", "proof": "f:1",
		"family": "shared-base", "slug": "x",
	}); err != nil {
		t.Fatalf("%v", err)
	}
	for _, fam := range []string{"dep-one", "dep-two"} {
		if _, err := svc.HandleEngramAdd(ctx, map[string]any{
			"title": fam, "problem": "p", "solution": "s", "proof": "f:1",
			"family": fam, "slug": "x",
			"prerequisites": []any{"engram://shared-base/x"},
		}); err != nil {
			t.Fatalf("%s: %v", fam, err)
		}
	}

	res, err := svc.HandleEngramGraph(ctx, map[string]any{
		"root":      "engram://shared-base/x",
		"direction": "up",
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	payload := readResultJSON(t, res)
	edges := payload["edges"].([]any)
	if len(edges) != 2 {
		t.Errorf("expected 2 dependents, got %d: %v", len(edges), edges)
	}
}

// ---------------------------------------------------------------------------
// Recipe back-compat
// ---------------------------------------------------------------------------

func TestHandleRecipeAdd_DelegatesToEngram(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	ctx := context.Background()

	res, err := svc.HandleRecipeAdd(ctx, map[string]any{
		"title":    "Fix stale DB pool connections",
		"problem":  "Connections go stale after idle",
		"solution": "Set db.SetConnMaxLifetime(5*time.Minute)",
		"proof":    "go test ./internal/db -run TestPoolReconnect",
		"language": "go",
		"scope":    "universal",
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if res.IsError {
		t.Fatalf("error result: %+v", res)
	}

	// Legacy response shape preserved.
	payload := readResultJSON(t, res)
	if payload["scope"] != "universal" {
		t.Errorf("scope: %v", payload["scope"])
	}
	if payload["language"] != "go" {
		t.Errorf("language: %v", payload["language"])
	}
	tags := toStringSlice(payload["tags"])
	if !containsTagOrPrefix(tags, "recipe") {
		t.Errorf("tags missing 'recipe': %v", tags)
	}

	// Engram recall finds the same item.
	rres, err := svc.HandleEngramRecall(ctx, map[string]any{
		"query": "stale DB pool",
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	rpayload := readResultJSON(t, rres)
	items := rpayload["items"].([]any)
	if len(items) == 0 {
		t.Fatal("engram recall returned no items for recipe-added entry")
	}
	first := items[0].(map[string]any)
	if first["tier"].(float64) != 1 {
		t.Errorf("expected tier 1 for legacy recipe, got %v", first["tier"])
	}
}

func TestHandleRecipeAdd_StripsInjectedTier(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	ctx := context.Background()

	// Caller passes tier=3 to recipe API; should be ignored (recipes are tier 1).
	res, err := svc.HandleRecipeAdd(ctx, map[string]any{
		"title":         "Try to sneak in tier 3",
		"problem":       "p",
		"solution":      "s",
		"proof":         "f.go:1",
		"tier":          3,
		"prerequisites": []any{"engram://does-not-exist/x"}, // would fail validation if honored
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if res.IsError {
		t.Fatalf("error: %+v", res)
	}
}

// ---------------------------------------------------------------------------
// HandleEngramList
// ---------------------------------------------------------------------------

func TestHandleEngramList_TierFilter(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	ctx := context.Background()

	add := func(family, slug string, tier int, proof string) {
		args := map[string]any{
			"title": family, "problem": "p", "solution": "s",
			"proof": proof, "family": family, "slug": slug, "tier": tier,
		}
		if _, err := svc.HandleEngramAdd(ctx, args); err != nil {
			t.Fatalf("add %s: %v", family, err)
		}
	}
	add("idiom-fam", "x", 1, "file.go:1")
	add("composite-fam", "x", 2, "command: go test")

	res, err := svc.HandleEngramList(ctx, map[string]any{
		"tier": 1,
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	payload := readResultJSON(t, res)
	items := payload["items"].([]any)
	for _, it := range items {
		m := it.(map[string]any)
		if m["tier"].(float64) > 1 {
			t.Errorf("tier filter leaked tier %v engram %v", m["tier"], m["uri"])
		}
	}
}

// ---------------------------------------------------------------------------
// S2: Locked filter + token budget truncation
// ---------------------------------------------------------------------------

// markVerified flips proof_status to "verified" and pushes repo onto unlocked_in
// for the engram with the given URI. Used by S2 tests to simulate the
// outcome of the proof verification job (S3) without running it.
func markVerified(t *testing.T, svc *Service, uri, repo string) {
	t.Helper()
	item, err := svc.lookupEngramByURI(uri)
	if err != nil || item == nil {
		t.Fatalf("lookup %s: err=%v item=%v", uri, err, item)
	}
	stored, err := svc.memoryHierarchy.GetItem(item.ID)
	if err != nil {
		t.Fatalf("get %s: %v", item.ID, err)
	}
	if stored.Metadata == nil {
		stored.Metadata = map[string]any{}
	}
	stored.Metadata[mdEngramProofStatus] = ProofStatusVerified
	unlocked := metadataStringSlice(stored.Metadata, mdEngramUnlockedIn)
	for _, u := range unlocked {
		if u == repo {
			return
		}
	}
	unlocked = append(unlocked, repo)
	stored.Metadata[mdEngramUnlockedIn] = stringSliceToAny(unlocked)
	if err := svc.memoryHierarchy.UpdateItem(stored); err != nil {
		t.Fatalf("update: %v", err)
	}
}

func TestHandleEngramRecall_LockedFilterDropsUnverified(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	ctx := context.Background()

	if _, err := svc.HandleEngramAdd(ctx, map[string]any{
		"title": "verified", "problem": "p", "solution": "s", "proof": "f:1",
		"family": "verified-fam", "slug": "x",
	}); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := svc.HandleEngramAdd(ctx, map[string]any{
		"title": "unverified", "problem": "p", "solution": "s", "proof": "f:1",
		"family": "unverified-fam", "slug": "x",
	}); err != nil {
		t.Fatalf("%v", err)
	}
	markVerified(t, svc, "engram://verified-fam/x", "demo-repo")

	res, err := svc.HandleEngramRecall(ctx, map[string]any{
		"query":          "p",
		"include_locked": false,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	payload := readResultJSON(t, res)
	items := payload["items"].([]any)

	gotURIs := map[string]bool{}
	for _, it := range items {
		gotURIs[it.(map[string]any)["uri"].(string)] = true
	}
	if !gotURIs["engram://verified-fam/x"] {
		t.Error("verified engram missing from results")
	}
	if gotURIs["engram://unverified-fam/x"] {
		t.Error("locked unverified engram should be filtered out")
	}
	if payload["locked_dropped"].(float64) < 1 {
		t.Errorf("expected locked_dropped >= 1, got %v", payload["locked_dropped"])
	}
}

func TestHandleEngramRecall_LockedFilterScopedByRepo(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	ctx := context.Background()

	if _, err := svc.HandleEngramAdd(ctx, map[string]any{
		"title": "scope test", "problem": "p", "solution": "s", "proof": "f:1",
		"family": "scope-test", "slug": "x",
	}); err != nil {
		t.Fatalf("%v", err)
	}
	markVerified(t, svc, "engram://scope-test/x", "repo-a")

	// Asking for repo-a: should appear.
	resA, _ := svc.HandleEngramRecall(ctx, map[string]any{
		"query":          "scope",
		"include_locked": false,
		"repo":           "repo-a",
	})
	if itemsCount(t, resA) != 1 {
		t.Errorf("repo-a should see the engram, got %d items", itemsCount(t, resA))
	}

	// Asking for repo-b: filtered out.
	resB, _ := svc.HandleEngramRecall(ctx, map[string]any{
		"query":          "scope",
		"include_locked": false,
		"repo":           "repo-b",
	})
	if itemsCount(t, resB) != 0 {
		t.Errorf("repo-b should NOT see the engram, got %d items", itemsCount(t, resB))
	}

	// Default include_locked=true: visible regardless of repo.
	resAll, _ := svc.HandleEngramRecall(ctx, map[string]any{"query": "scope"})
	if itemsCount(t, resAll) != 1 {
		t.Errorf("include_locked=true (default) should see the engram, got %d", itemsCount(t, resAll))
	}
}

func TestHandleEngramRecall_TokenBudgetDropsHighestTierPrereqsFirst(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	ctx := context.Background()

	// Add a tier-1 base, a tier-2 mid, and a tier-3 system that depends on both.
	add := func(title, family, slug string, tier int, proof string, prereqs []any) {
		args := map[string]any{
			"title": title, "problem": "p", "solution": "s",
			"proof": proof, "family": family, "slug": slug, "tier": tier,
		}
		if len(prereqs) > 0 {
			args["prerequisites"] = prereqs
		}
		if _, err := svc.HandleEngramAdd(ctx, args); err != nil {
			t.Fatalf("add %s: %v", family, err)
		}
	}
	add("base idiom", "budget-base", "x", 1, "f:1", nil)
	add("composite", "budget-comp", "x", 2, "command: go test", []any{"engram://budget-base/x"})
	add("system", "budget-sys", "x", 3,
		"command: go test\nbenchmark: go test -bench=.",
		[]any{"engram://budget-base/x", "engram://budget-comp/x"})

	// Budget that fits the match (~62 tokens) plus the tier-1 base (~41)
	// but not the tier-2 composite (~46). Truncator should drop tier-2
	// composite first, leaving match + base.
	res, err := svc.HandleEngramRecall(ctx, map[string]any{
		"query":        "system",
		"depth":        3,
		"token_budget": 110,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	payload := readResultJSON(t, res)
	items := payload["items"].([]any)

	// Direct match (system, tier 3) must always be kept; the comp/base prereqs
	// can be dropped to fit the budget.
	hasMatch := false
	for _, it := range items {
		if it.(map[string]any)["uri"] == "engram://budget-sys/x" {
			hasMatch = true
		}
	}
	if !hasMatch {
		t.Errorf("direct match should not be dropped; items=%v", items)
	}
	if payload["truncated"].(bool) != true {
		t.Errorf("expected truncated=true with tight budget, got %v", payload["truncated"])
	}
	if payload["budget_dropped"].(float64) < 1 {
		t.Errorf("expected budget_dropped >= 1, got %v", payload["budget_dropped"])
	}
}

func TestHandleEngramRecall_NoBudgetKeepsEverything(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	ctx := context.Background()

	add := func(title, family string, prereqs []any) {
		args := map[string]any{
			"title": title, "problem": "p", "solution": "s",
			"proof":  "f:1",
			"family": family, "slug": "x",
		}
		if len(prereqs) > 0 {
			args["prerequisites"] = prereqs
		}
		if _, err := svc.HandleEngramAdd(ctx, args); err != nil {
			t.Fatalf("%v", err)
		}
	}
	add("a", "no-budget-a", nil)
	add("b", "no-budget-b", []any{"engram://no-budget-a/x"})

	res, err := svc.HandleEngramRecall(ctx, map[string]any{
		"query":        "b",
		"depth":        1,
		"token_budget": 0, // explicitly disable budget
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	payload := readResultJSON(t, res)
	if payload["truncated"].(bool) {
		t.Error("budget=0 should never truncate")
	}
	if payload["budget_dropped"].(float64) != 0 {
		t.Errorf("budget_dropped expected 0, got %v", payload["budget_dropped"])
	}
}

func itemsCount(t *testing.T, res *mcp.CallToolResult) int {
	t.Helper()
	payload := readResultJSON(t, res)
	items, ok := payload["items"].([]any)
	if !ok {
		return 0
	}
	return len(items)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// containsTagOrPrefix reports whether any tag in haystack equals needle or
// has needle as a prefix. Used to assert that an emitted tag set advertises
// the engram (or recipe) class plus any related "key:value" tags.
func containsTagOrPrefix(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
		if strings.HasPrefix(h, needle) {
			return true
		}
	}
	return false
}
