// knowledge_graph_bridge.go — cross-session knowledge-graph bridge walker (Slice A3 / F4).
//
// BridgeWalk expands a set of seed entity IDs by traversing a whitelisted set of
// knowledge-graph edges (derived_from, references, followup_of) up to `depth`
// hops. Results are returned as *ContextEntry values whose Metadata carries a
// "bridged_from" key pointing at the originating session of the bridged entity.
//
// # Namespace safety
//
// Every bridged entity MUST belong to the same namespace as the seeds, which is
// expressed as a prefix match against `namespacePrefix`. Prefix matching is a
// literal string-prefix match on the entity's `Namespace` field, so "projA/"
// matches "projA/foo" and "projA/bar" but NOT "projA-mirror/foo". Callers that
// want to scope to an exact namespace should pass the full namespace string
// (e.g. "projA/foo"); callers that want the entire project tree should pass the
// prefix with a trailing separator (e.g. "projA/"). Cross-namespace hits are
// filtered out unconditionally — this is a load-bearing privacy guarantee
// exercised by TestBridgeWalk_NamespaceDeny.
package agentcontext

import (
	"context"
	"fmt"
	"strings"
)

// bridgeEdgeTypes is the fixed set of relation types the bridge walker will
// follow. Kept as a package-level variable so callers can't accidentally
// broaden the edge surface by misconfiguring a query.
var bridgeEdgeTypes = map[RelationType]bool{
	RelationType("derived_from"): true,
	RelationReferences:           true, // "references"
	RelationType("followup_of"):  true,
}

// BridgeWalk expands a seed set of entity IDs by walking up to `depth` hops
// along edges of type {derived_from, references, followup_of}, returning
// ContextEntry rows **strictly within the same namespace prefix**.
//
// Parameters:
//   - ctx: standard context; BridgeWalk honors cancellation between hops.
//   - seedIDs: entity IDs to start from. Unknown IDs are skipped, not errored.
//   - depth: max edge hops. depth<=0 returns an empty slice (seeds are NOT
//     emitted as bridged entries — the walker surfaces cross-session peers).
//   - namespacePrefix: required. An empty prefix is an error; callers must be
//     explicit about the namespace scope to avoid cross-tenant leakage.
//   - budget: caps the returned slice length. budget<=0 is treated as unlimited.
//
// Each returned ContextEntry has Metadata["bridged_from"] set to the session
// that produced the bridged entity (empty string if the entity had no
// SessionID attached).
func (g *KnowledgeGraph) BridgeWalk(
	ctx context.Context,
	seedIDs []string,
	depth int,
	namespacePrefix string,
	budget int,
) ([]ContextEntry, error) {
	if namespacePrefix == "" {
		return nil, fmt.Errorf("BridgeWalk: namespacePrefix is required")
	}
	if depth <= 0 {
		return []ContextEntry{}, nil
	}
	// Hard cap mirrors other traversals in this package to avoid unbounded
	// exploration on cyclic graphs.
	if depth > 10 {
		depth = 10
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	// Track entities we've already emitted so we don't double-bridge through
	// cycles. Seeds start visited so they don't self-emit.
	visited := make(map[string]bool, len(seedIDs))
	frontier := make([]string, 0, len(seedIDs))
	for _, id := range seedIDs {
		if id == "" || visited[id] {
			continue
		}
		if _, ok := g.entities[id]; !ok {
			continue
		}
		visited[id] = true
		frontier = append(frontier, id)
	}

	results := make([]ContextEntry, 0)

	for hop := 0; hop < depth && len(frontier) > 0; hop++ {
		// Honor cancellation between hops — traversals are cheap but we want
		// to be a good citizen in long-running recall paths.
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		nextFrontier := make([]string, 0)
		for _, srcID := range frontier {
			for relID := range g.outgoingRelations[srcID] {
				rel := g.relations[relID]
				if rel == nil {
					continue
				}
				if !bridgeEdgeTypes[rel.Type] {
					continue
				}
				targetID := rel.TargetID
				if visited[targetID] {
					continue
				}
				visited[targetID] = true

				target := g.entities[targetID]
				if target == nil {
					continue
				}
				// Namespace deny gate — strict prefix match on Namespace.
				// This check is the privacy guarantee advertised in the
				// function doc comment; do not weaken it without updating
				// TestBridgeWalk_NamespaceDeny.
				if !strings.HasPrefix(target.Namespace, namespacePrefix) {
					continue
				}

				results = append(results, bridgedEntryFromEntity(target))
				if budget > 0 && len(results) >= budget {
					return results, nil
				}
				nextFrontier = append(nextFrontier, targetID)
			}
		}
		frontier = nextFrontier
	}

	return results, nil
}

// bridgedEntryFromEntity projects a graph Entity into a ContextEntry suitable
// for merging into a recall result set. The "bridged_from" metadata key is
// required by the F4 contract so downstream recall can surface provenance.
func bridgedEntryFromEntity(e *Entity) ContextEntry {
	meta := map[string]any{
		"bridged_from":  e.SessionID,
		"source_entity": e.ID,
		"entity_type":   string(e.Type),
	}
	return ContextEntry{
		ID:            e.ID,
		SchemaVersion: SchemaVersion,
		AgentID:       e.AgentID,
		SessionID:     e.SessionID,
		Namespace:     e.Namespace,
		EntryType:     EntryTypeFinding,
		Timestamp:     e.UpdatedAt,
		Title:         e.Name,
		Content:       e.Description,
		ContentHash:   ContentHashFunc(e.Description),
		Metadata:      meta,
		FilePath:      e.FilePath,
		LineStart:     e.LineStart,
		LineEnd:       e.LineEnd,
		Tags:          e.Tags,
		Visibility:    VisibilityShared,
	}
}
