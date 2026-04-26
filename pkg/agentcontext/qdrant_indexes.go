// qdrant_indexes.go -- Keyword payload indexes for agent-context collections.
//
// Without these indexes, every filtered list/scroll query (e.g. by session_id,
// status, agent_id) is a brute-force scan over the entire collection. Once a
// collection grows past a few hundred points the daemon's call lock starts
// timing out, which wedges the entire fleet (heartbeats, presence, task list).
//
// We register the canonical filter fields per collection kind here so that
// EnsureCollection can apply them idempotently on every startup.
package agentcontext

import (
	"context"
	"fmt"
	"net/http"
)

// keywordIndexesByKind lists the payload fields that need keyword indexes per
// collection kind. The keys are Coll* constants from qdrant_registry.go.
var keywordIndexesByKind = map[string][]string{
	CollContext:    {"session_id", "agent_id", "entry_type", "visibility", "file_path", "namespace"},
	CollSessions:   {"agent_id", "namespace", "status"},
	CollTasks:      {"session_id", "agent_id", "status", "namespace", "project", "file_path"},
	CollWorkflows:  {"status", "agent_id", "session_id"},
	CollHandoffs:   {"target_agent_id", "agent_id", "status"},
	CollMemory:     {"agent_id", "namespace", "tier"},
	CollPresence:   {"agent_id", "status"},
	CollFileClaims: {"agent_id", "status", "file_path"},
}

// EnsureKeywordIndex idempotently creates a keyword payload index on the named
// field. Qdrant's PUT /collections/{name}/index is idempotent: it succeeds if
// the index already exists with the same schema.
func (c *QdrantClient) EnsureKeywordIndex(ctx context.Context, field string) error {
	body := map[string]any{
		"field_name":   field,
		"field_schema": "keyword",
	}
	if err := c.doJSON(ctx, http.MethodPut, "/collections/"+c.collection+"/index", body, nil); err != nil {
		return fmt.Errorf("ensure index %s.%s: %w", c.collection, field, err)
	}
	return nil
}

// EnsureKeywordIndexes applies a list of keyword payload indexes. Stops at the
// first error.
func (c *QdrantClient) EnsureKeywordIndexes(ctx context.Context, fields ...string) error {
	for _, f := range fields {
		if err := c.EnsureKeywordIndex(ctx, f); err != nil {
			return err
		}
	}
	return nil
}

// ensureRegisteredIndexes applies the canonical keyword index set for the
// client's kind. No-op when kind is empty or unregistered.
func (c *QdrantClient) ensureRegisteredIndexes(ctx context.Context) error {
	if c == nil || c.kind == "" {
		return nil
	}
	fields, ok := keywordIndexesByKind[c.kind]
	if !ok || len(fields) == 0 {
		return nil
	}
	return c.EnsureKeywordIndexes(ctx, fields...)
}
