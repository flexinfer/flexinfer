package agentcontext

import (
	"context"
	"testing"
)

// bridgeFixture builds a small graph across two namespaces for the bridge
// walker tests. Shape:
//
//	[projA/foo] seed-A
//	    │ derived_from
//	    ▼
//	[projA/foo] hop-1-A  ── references ──▶ [projA/foo] hop-2-A
//	    │ followup_of
//	    ▼
//	[projA/foo] hop-1-B
//
//	[projA/foo] seed-A ── derived_from ──▶ [projB/foo] cross-ns
//
//	[projA/foo] seed-B ── calls ──▶ [projA/foo] noise (non-bridge edge)
//
// Namespace deny must filter "cross-ns". The `calls` edge type must be
// ignored because it is outside the whitelist.
func bridgeFixture(t *testing.T) (*KnowledgeGraph, map[string]string) {
	t.Helper()
	g := NewKnowledgeGraph()

	add := func(id, ns, name, sess string) *Entity {
		e := &Entity{
			ID:          id,
			Type:        EntityTypeConcept,
			Name:        name,
			Description: "desc-" + name,
			Namespace:   ns,
			SessionID:   sess,
		}
		if err := g.AddEntity(e); err != nil {
			t.Fatalf("AddEntity %s: %v", id, err)
		}
		return e
	}

	add("seedA", "projA/foo", "seedA", "sess-seedA")
	add("hop1A", "projA/foo", "hop1A", "sess-hop1A")
	add("hop2A", "projA/foo", "hop2A", "sess-hop2A")
	add("hop1B", "projA/foo", "hop1B", "sess-hop1B")
	add("crossNS", "projB/foo", "crossNS", "sess-crossNS")
	add("seedB", "projA/foo", "seedB", "sess-seedB")
	add("noise", "projA/foo", "noise", "sess-noise")

	addRel := func(src, tgt string, rt RelationType) {
		if err := g.AddRelation(&Relation{Type: rt, SourceID: src, TargetID: tgt}); err != nil {
			t.Fatalf("AddRelation %s->%s (%s): %v", src, tgt, rt, err)
		}
	}
	addRel("seedA", "hop1A", RelationType("derived_from"))
	addRel("hop1A", "hop2A", RelationReferences)
	addRel("seedA", "hop1B", RelationType("followup_of"))
	addRel("seedA", "crossNS", RelationType("derived_from"))
	addRel("seedB", "noise", RelationCalls)

	return g, map[string]string{
		"seedA":   "seedA",
		"seedB":   "seedB",
		"hop1A":   "hop1A",
		"hop2A":   "hop2A",
		"hop1B":   "hop1B",
		"crossNS": "crossNS",
		"noise":   "noise",
	}
}

func idSet(entries []ContextEntry) map[string]ContextEntry {
	out := make(map[string]ContextEntry, len(entries))
	for _, e := range entries {
		out[e.ID] = e
	}
	return out
}

// TestBridgeWalk_HappyPath_Depth2 asserts that a depth=2 walk from seedA
// reaches both the 1-hop neighbors (hop1A, hop1B) and the 2-hop neighbor
// (hop2A) along whitelisted edges, while skipping non-bridge edges.
func TestBridgeWalk_HappyPath_Depth2(t *testing.T) {
	t.Parallel()
	g, ids := bridgeFixture(t)

	got, err := g.BridgeWalk(context.Background(), []string{ids["seedA"]}, 2, "projA/", 0)
	if err != nil {
		t.Fatalf("BridgeWalk: %v", err)
	}

	found := idSet(got)
	for _, want := range []string{"hop1A", "hop1B", "hop2A"} {
		if _, ok := found[want]; !ok {
			t.Errorf("expected %s in bridge results, got IDs=%v", want, keysOf(found))
		}
	}
	// Seed must NOT be returned.
	if _, ok := found["seedA"]; ok {
		t.Error("seed entity must not be emitted as a bridged entry")
	}
	// Non-bridge edge target must NOT be returned.
	if _, ok := found["noise"]; ok {
		t.Error("entity reached via non-whitelisted edge must not appear")
	}
	// Every emitted entry must carry bridged_from metadata.
	for _, e := range got {
		if e.Metadata == nil {
			t.Errorf("entry %s: missing metadata", e.ID)
			continue
		}
		if _, ok := e.Metadata["bridged_from"]; !ok {
			t.Errorf("entry %s: missing bridged_from metadata", e.ID)
		}
	}
}

// TestBridgeWalk_DepthZero_ReturnsNothing confirms the depth=0 contract:
// no hops, no results — not even the seeds.
func TestBridgeWalk_DepthZero_ReturnsNothing(t *testing.T) {
	t.Parallel()
	g, ids := bridgeFixture(t)

	got, err := g.BridgeWalk(context.Background(), []string{ids["seedA"]}, 0, "projA/", 0)
	if err != nil {
		t.Fatalf("BridgeWalk: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("depth=0 must return 0 entries, got %d: %v", len(got), keysOf(idSet(got)))
	}
}

// TestBridgeWalk_DepthOne_LimitsHops verifies depth=1 stops before reaching
// hop2A (which sits 2 edges from seedA).
func TestBridgeWalk_DepthOne_LimitsHops(t *testing.T) {
	t.Parallel()
	g, ids := bridgeFixture(t)

	got, err := g.BridgeWalk(context.Background(), []string{ids["seedA"]}, 1, "projA/", 0)
	if err != nil {
		t.Fatalf("BridgeWalk: %v", err)
	}
	found := idSet(got)
	if _, ok := found["hop2A"]; ok {
		t.Errorf("depth=1 must NOT reach 2-hop entity hop2A, got IDs=%v", keysOf(found))
	}
	if _, ok := found["hop1A"]; !ok {
		t.Errorf("depth=1 must reach hop1A, got IDs=%v", keysOf(found))
	}
}

// TestBridgeWalk_NamespaceDeny is the load-bearing privacy test. seedA has a
// derived_from edge to crossNS in namespace projB/foo; with a projA/ prefix
// that edge MUST be filtered. This test is designed to FAIL if the prefix
// filter is removed from BridgeWalk.
func TestBridgeWalk_NamespaceDeny(t *testing.T) {
	t.Parallel()
	g, ids := bridgeFixture(t)

	got, err := g.BridgeWalk(context.Background(), []string{ids["seedA"]}, 2, "projA/", 0)
	if err != nil {
		t.Fatalf("BridgeWalk: %v", err)
	}

	for _, e := range got {
		if e.ID == "crossNS" {
			t.Fatalf("namespace deny violated: crossNS entity leaked across namespaces (ns=%s)", e.Namespace)
		}
		if e.Namespace != "" && !startsWith(e.Namespace, "projA/") {
			t.Errorf("entry %s has namespace %q outside prefix projA/", e.ID, e.Namespace)
		}
	}
}

// TestBridgeWalk_NamespaceDeny_ExactPrefixNotSubstring ensures the prefix is
// matched at the start, not anywhere in the string. "projA-mirror/foo" must
// NOT be accepted under a "projA/" prefix even though it contains "projA".
func TestBridgeWalk_NamespaceDeny_ExactPrefixNotSubstring(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()

	_ = g.AddEntity(&Entity{ID: "seed", Type: EntityTypeConcept, Name: "seed", Namespace: "projA/foo"})
	_ = g.AddEntity(&Entity{ID: "mirror", Type: EntityTypeConcept, Name: "mirror", Namespace: "projA-mirror/foo"})
	_ = g.AddRelation(&Relation{Type: RelationType("derived_from"), SourceID: "seed", TargetID: "mirror"})

	got, err := g.BridgeWalk(context.Background(), []string{"seed"}, 2, "projA/", 0)
	if err != nil {
		t.Fatalf("BridgeWalk: %v", err)
	}
	for _, e := range got {
		if e.ID == "mirror" {
			t.Fatalf("prefix match leaked through substring: mirror (ns=%s) under prefix projA/", e.Namespace)
		}
	}
}

// TestBridgeWalk_BudgetCap ensures the returned slice is capped by `budget`.
func TestBridgeWalk_BudgetCap(t *testing.T) {
	t.Parallel()
	g, ids := bridgeFixture(t)

	got, err := g.BridgeWalk(context.Background(), []string{ids["seedA"]}, 2, "projA/", 1)
	if err != nil {
		t.Fatalf("BridgeWalk: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("budget=1 must cap results at 1, got %d", len(got))
	}
}

// TestBridgeWalk_NonBridgeEdgesIgnored confirms `calls` and other edge types
// outside the whitelist are never followed.
func TestBridgeWalk_NonBridgeEdgesIgnored(t *testing.T) {
	t.Parallel()
	g, ids := bridgeFixture(t)

	got, err := g.BridgeWalk(context.Background(), []string{ids["seedB"]}, 2, "projA/", 0)
	if err != nil {
		t.Fatalf("BridgeWalk: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("seedB only has a `calls` edge; expected 0 bridged entries, got %d: %v",
			len(got), keysOf(idSet(got)))
	}
}

// TestBridgeWalk_EmptyPrefixRejected asserts the required-namespace contract.
func TestBridgeWalk_EmptyPrefixRejected(t *testing.T) {
	t.Parallel()
	g, ids := bridgeFixture(t)

	_, err := g.BridgeWalk(context.Background(), []string{ids["seedA"]}, 2, "", 0)
	if err == nil {
		t.Error("expected error when namespacePrefix is empty")
	}
}

// TestBridgeWalk_BridgedFromMetadata verifies every returned entry carries a
// bridged_from key pointing at the originating session.
func TestBridgeWalk_BridgedFromMetadata(t *testing.T) {
	t.Parallel()
	g, ids := bridgeFixture(t)

	got, err := g.BridgeWalk(context.Background(), []string{ids["seedA"]}, 2, "projA/", 0)
	if err != nil {
		t.Fatalf("BridgeWalk: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected non-zero bridged entries")
	}
	for _, e := range got {
		v, ok := e.Metadata["bridged_from"]
		if !ok {
			t.Errorf("entry %s missing bridged_from", e.ID)
			continue
		}
		if s, ok := v.(string); !ok || s == "" {
			t.Errorf("entry %s bridged_from must be a non-empty string, got %T=%v", e.ID, v, v)
		}
	}
}

// -- helpers --

func keysOf(m map[string]ContextEntry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
