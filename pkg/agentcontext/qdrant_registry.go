package agentcontext

import "github.com/crb2nu/loom/pkg/httpclient"

// Collection name constants used as keys in QdrantRegistry.
const (
	CollContext        = "context"
	CollSessions       = "sessions"
	CollTasks          = "tasks"
	CollAnnotations    = "annotations"
	CollHandoffs       = "handoffs"
	CollTemplates      = "templates"
	CollGraphEntities  = "graphEntities"
	CollGraphRelations = "graphRelations"
	CollWorkflows      = "workflows"
	CollWorkflowDefs   = "workflowDefs"
	CollMemory         = "memory"
	CollPresence       = "presence"
	CollFileClaims     = "fileClaims"
	CollWorktree       = "worktree"
)

// QdrantRegistry manages a set of named QdrantClient instances, one per collection.
// It replaces the 14 individual *QdrantClient fields that were on the Service struct.
type QdrantRegistry struct {
	clients map[string]*QdrantClient
}

// NewQdrantRegistry creates a QdrantRegistry with all collection clients
// derived from the given Config and shared HTTP client.
func NewQdrantRegistry(hc *httpclient.Client, cfg Config) *QdrantRegistry {
	mk := func(collection string) *QdrantClient {
		return NewQdrantClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, collection, cfg.QdrantDistance)
	}
	return &QdrantRegistry{
		clients: map[string]*QdrantClient{
			CollContext:        mk(cfg.ContextCollection),
			CollSessions:       mk(cfg.SessionsCollection),
			CollTasks:          mk(cfg.TasksCollection),
			CollAnnotations:    mk(cfg.AnnotationsCollection),
			CollHandoffs:       mk(cfg.HandoffsCollection),
			CollTemplates:      mk(cfg.TemplatesCollection),
			CollGraphEntities:  mk(cfg.GraphEntitiesCollection),
			CollGraphRelations: mk(cfg.GraphRelationsCollection),
			CollWorkflows:      mk(cfg.WorkflowsCollection),
			CollWorkflowDefs:   mk(cfg.WorkflowDefsCollection),
			CollMemory:         mk(cfg.MemoryCollection),
			CollPresence:       mk(cfg.PresenceCollection),
			CollFileClaims:     mk(cfg.FileClaimsCollection),
			CollWorktree:       mk(cfg.WorktreeCollection),
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
