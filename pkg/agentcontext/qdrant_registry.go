package agentcontext

import "github.com/crb2nu/loom/pkg/httpclient"

// Collection name constants used as keys in QdrantRegistry.
//
// SIMP-12 consolidation: annotations merged into context collection,
// templates collection deprecated (CLI-only after SIMP-7).
// Down from 14 → 12 active collections.
const (
	CollContext        = "context"
	CollSessions       = "sessions"
	CollTasks          = "tasks"
	CollHandoffs       = "handoffs"
	CollGraphEntities  = "graphEntities"
	CollGraphRelations = "graphRelations"
	CollWorkflows      = "workflows"
	CollWorkflowDefs   = "workflowDefs"
	CollMemory         = "memory"
	CollPresence       = "presence"
	CollFileClaims     = "fileClaims"
	CollWorktree       = "worktree"

	// CollAnnotations is an alias for CollContext.
	// Annotations now share the context collection with a _record_type discriminator.
	CollAnnotations = CollContext

	// CollTemplates is deprecated. Templates are CLI-only after SIMP-7.
	// Kept as constant for backward-compatible reads of existing data.
	CollTemplates = "templates"
)

// QdrantRegistry manages a set of named QdrantClient instances, one per collection.
type QdrantRegistry struct {
	clients map[string]*QdrantClient
}

// NewQdrantRegistry creates a QdrantRegistry with all active collection clients.
// After SIMP-12 consolidation: 12 active collections (annotations merged into context,
// templates deprecated).
func NewQdrantRegistry(hc *httpclient.Client, cfg Config) *QdrantRegistry {
	mk := func(collection string) *QdrantClient {
		return NewQdrantClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, collection, cfg.QdrantDistance)
	}

	contextClient := mk(cfg.ContextCollection)

	return &QdrantRegistry{
		clients: map[string]*QdrantClient{
			CollContext:        contextClient,
			CollSessions:       mk(cfg.SessionsCollection),
			CollTasks:          mk(cfg.TasksCollection),
			CollHandoffs:       mk(cfg.HandoffsCollection),
			CollGraphEntities:  mk(cfg.GraphEntitiesCollection),
			CollGraphRelations: mk(cfg.GraphRelationsCollection),
			CollWorkflows:      mk(cfg.WorkflowsCollection),
			CollWorkflowDefs:   mk(cfg.WorkflowDefsCollection),
			CollMemory:         mk(cfg.MemoryCollection),
			CollPresence:       mk(cfg.PresenceCollection),
			CollFileClaims:     mk(cfg.FileClaimsCollection),
			CollWorktree:       mk(cfg.WorktreeCollection),
			// Templates: deprecated but kept for backward-compatible reads.
			CollTemplates: mk(cfg.TemplatesCollection),
		},
	}
}

// Get returns the QdrantClient for the named collection, or nil if not found.
func (r *QdrantRegistry) Get(name string) *QdrantClient {
	if r == nil {
		return nil
	}
	return r.clients[name]
}

// Names returns all registered collection names.
func (r *QdrantRegistry) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.clients))
	for k := range r.clients {
		names = append(names, k)
	}
	return names
}
