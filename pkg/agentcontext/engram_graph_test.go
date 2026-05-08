package agentcontext

import (
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// ParseEngramURI / SlugValid
// ---------------------------------------------------------------------------

func TestParseEngramURI_Valid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in           string
		family, slug string
	}{
		{"engram://retry-jitter/go", "retry-jitter", "go"},
		{"retry-jitter/go", "retry-jitter", "go"},
		{"engram://atomic-file-write/polyglot", "atomic-file-write", "polyglot"},
		{"engram://a/b", "a", "b"},
	}
	for _, tc := range cases {
		u, err := ParseEngramURI(tc.in)
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", tc.in, err)
		}
		if u.Family != tc.family {
			t.Errorf("%q: family %q want %q", tc.in, u.Family, tc.family)
		}
		if u.Slug != tc.slug {
			t.Errorf("%q: slug %q want %q", tc.in, u.Slug, tc.slug)
		}
	}
}

func TestParseEngramURI_Invalid(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"engram://",
		"engram://only-family",
		"engram://bad/SLUG",  // uppercase
		"engram://-bad/slug", // leading hyphen
		"engram://family/slug/extra",
		"engram://Family/slug",
	}
	for _, in := range cases {
		if _, err := ParseEngramURI(in); err == nil {
			t.Errorf("%q: expected error, got none", in)
		}
	}
}

func TestEngramURI_String(t *testing.T) {
	t.Parallel()
	u := EngramURI{Family: "x", Slug: "y"}
	if got, want := u.String(), "engram://x/y"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestSlugValid(t *testing.T) {
	t.Parallel()
	good := []string{"a", "abc", "abc-def", "go", "tier-1", "v2"}
	bad := []string{"", "Abc", "-leading", "bad_underscore", "spaced word", "trailing-"}
	for _, s := range good {
		if !SlugValid(s) {
			t.Errorf("%q: expected valid", s)
		}
	}
	for _, s := range bad {
		if SlugValid(s) {
			t.Errorf("%q: expected invalid", s)
		}
	}
}

// ---------------------------------------------------------------------------
// DetectCycle
// ---------------------------------------------------------------------------

// staticResolver returns prereqs from a fixed map. Useful for graph tests.
func staticResolver(graph map[string][]string) PrereqResolver {
	return func(uri string) ([]string, error) {
		return graph[uri], nil
	}
}

func TestDetectCycle_NoCycle(t *testing.T) {
	t.Parallel()
	graph := map[string][]string{
		"engram://a/x": {"engram://b/x"},
		"engram://b/x": nil,
	}
	node := EngramNode{
		URI:           "engram://c/x",
		Prerequisites: []string{"engram://a/x"},
	}
	if path, err := DetectCycle(node, staticResolver(graph)); err != nil {
		t.Fatalf("expected no cycle, got err=%v path=%v", err, path)
	}
}

func TestDetectCycle_DirectSelfLoop(t *testing.T) {
	t.Parallel()
	node := EngramNode{
		URI:           "engram://a/x",
		Prerequisites: []string{"engram://a/x"},
	}
	if _, err := DetectCycle(node, staticResolver(nil)); err == nil {
		t.Fatal("expected cycle error for self-prereq")
	}
}

func TestDetectCycle_IndirectCycle(t *testing.T) {
	t.Parallel()
	// We are about to add engram://c/x with prereq b/x.
	// Existing graph: a/x -> c/x already (so adding c/x with prereq b/x where b/x -> a/x creates c -> b -> a -> c).
	graph := map[string][]string{
		"engram://b/x": {"engram://a/x"},
		"engram://a/x": {"engram://c/x"},
	}
	node := EngramNode{
		URI:           "engram://c/x",
		Prerequisites: []string{"engram://b/x"},
	}
	if _, err := DetectCycle(node, staticResolver(graph)); err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestDetectCycle_MissingNodeURI(t *testing.T) {
	t.Parallel()
	if _, err := DetectCycle(EngramNode{}, staticResolver(nil)); err == nil {
		t.Fatal("expected error for empty URI")
	}
}

func TestDetectCycle_ResolverError(t *testing.T) {
	t.Parallel()
	resolver := func(uri string) ([]string, error) {
		return nil, errors.New("boom")
	}
	node := EngramNode{
		URI:           "engram://a/x",
		Prerequisites: []string{"engram://b/x"},
	}
	if _, err := DetectCycle(node, resolver); err == nil {
		t.Fatal("expected resolver error to propagate")
	}
}

// ---------------------------------------------------------------------------
// TransitivePrereqs
// ---------------------------------------------------------------------------

func TestTransitivePrereqs_LinearChain(t *testing.T) {
	t.Parallel()
	graph := map[string][]string{
		"engram://a/x": {"engram://b/x"},
		"engram://b/x": {"engram://c/x"},
		"engram://c/x": {"engram://d/x"},
		"engram://d/x": nil,
	}
	got, err := TransitivePrereqs("engram://a/x", -1, staticResolver(graph))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	want := []string{"engram://b/x", "engram://c/x", "engram://d/x"}
	if !sliceEq(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestTransitivePrereqs_DepthLimit(t *testing.T) {
	t.Parallel()
	graph := map[string][]string{
		"engram://a/x": {"engram://b/x"},
		"engram://b/x": {"engram://c/x"},
		"engram://c/x": {"engram://d/x"},
	}
	got, err := TransitivePrereqs("engram://a/x", 2, staticResolver(graph))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	want := []string{"engram://b/x", "engram://c/x"}
	if !sliceEq(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestTransitivePrereqs_DiamondDeduped(t *testing.T) {
	t.Parallel()
	// a -> b, a -> c; b -> d; c -> d. d should appear once.
	graph := map[string][]string{
		"engram://a/x": {"engram://b/x", "engram://c/x"},
		"engram://b/x": {"engram://d/x"},
		"engram://c/x": {"engram://d/x"},
		"engram://d/x": nil,
	}
	got, err := TransitivePrereqs("engram://a/x", -1, staticResolver(graph))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	count := 0
	for _, u := range got {
		if u == "engram://d/x" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("d should appear once, appeared %d times in %v", count, got)
	}
}

func TestTransitivePrereqs_EmptyStart(t *testing.T) {
	t.Parallel()
	got, err := TransitivePrereqs("", -1, staticResolver(nil))
	if err != nil || len(got) != 0 {
		t.Errorf("expected empty result, got %v err=%v", got, err)
	}
}

// ---------------------------------------------------------------------------
// TopoSortByTier
// ---------------------------------------------------------------------------

func TestTopoSortByTier_OrdersAscendingThenAlpha(t *testing.T) {
	t.Parallel()
	nodes := []EngramNode{
		{URI: "engram://z/x"},
		{URI: "engram://a/x"},
		{URI: "engram://m/x"},
	}
	tiers := map[string]int{
		"engram://z/x": 1,
		"engram://a/x": 2,
		"engram://m/x": 1,
	}
	out := TopoSortByTier(nodes, func(uri string) int { return tiers[uri] })
	want := []string{"engram://m/x", "engram://z/x", "engram://a/x"}
	got := make([]string, len(out))
	for i, n := range out {
		got[i] = n.URI
	}
	if !sliceEq(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// validateProofForTier
// ---------------------------------------------------------------------------

func TestValidateProofForTier_Tier1AnyProof(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"file.go:42", "see https://example.com", "command: go test"} {
		if err := validateProofForTier(p, 1); err != nil {
			t.Errorf("%q: tier 1 should accept any proof, got %v", p, err)
		}
	}
}

func TestValidateProofForTier_Tier2RequiresCommand(t *testing.T) {
	t.Parallel()
	if err := validateProofForTier("file.go:1", 2); err == nil {
		t.Error("tier 2 should reject file-only proof")
	}
	if err := validateProofForTier("command: go test ./...", 2); err != nil {
		t.Errorf("tier 2 should accept command proof, got %v", err)
	}
}

func TestValidateProofForTier_Tier3RequiresCommandAndArtifact(t *testing.T) {
	t.Parallel()
	cases := []struct {
		proof  string
		valid  bool
		reason string
	}{
		{"command: go test", false, "no benchmark/dashboard"},
		{"command: go test\nbenchmark: go test -bench=.", true, "command + benchmark"},
		{"command: go test\ndashboard: https://grafana", true, "command + dashboard"},
		{"benchmark: go test -bench=.", false, "no command"},
		{"file.go:1", false, "no command"},
	}
	for _, tc := range cases {
		err := validateProofForTier(tc.proof, 3)
		if tc.valid && err != nil {
			t.Errorf("%s: expected valid, got %v", tc.reason, err)
		}
		if !tc.valid && err == nil {
			t.Errorf("%s: expected invalid", tc.reason)
		}
	}
}

func TestValidateProofForTier_UnsupportedTier(t *testing.T) {
	t.Parallel()
	if err := validateProofForTier("any", 4); err == nil {
		t.Error("expected error for tier 4")
	}
}

// ---------------------------------------------------------------------------
// slugify
// ---------------------------------------------------------------------------

func TestSlugify(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"Atomic File Write", "atomic-file-write"},
		{"Retry With Jitter!", "retry-with-jitter"},
		{"  trim me  ", "trim-me"},
		{"---", "engram"},
		{"", "engram"},
		{"go/python", "go-python"},
	}
	for _, tc := range cases {
		if got := slugify(tc.in); got != tc.want {
			t.Errorf("slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
