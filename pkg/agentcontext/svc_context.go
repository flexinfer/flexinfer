package agentcontext

import (
	"context"
	"log/slog"
	"time"

	"github.com/crb2nu/loom/pkg/codebase/embed"
)

// ContextSvc manages context entries, annotations, search, and summary generation.
type ContextSvc struct {
	qdrant                   *QdrantRegistry
	embed                    embed.Embedder
	vectorSize               *int // shared mutable — pointer to Service.vectorSize
	cfg                      Config
	logger                   *slog.Logger
	metrics                  *Metrics
	persistedMemoryHierarchy *persistedMemoryHierarchy
	knowledgeGraph           *KnowledgeGraph

	// Cross-domain callbacks (wired by Service).
	getSession     func(ctx context.Context, sessionID string) (*Session, error)
	persistSession func(ctx context.Context, session *Session) error

	// Session state callbacks — SessionSvc owns the mutex.
	addSessionEntryStats  func(session *Session, entries int, tokens int)
	readSessionStats      func(session *Session) (entryCount, totalTokens int, lastSummary *time.Time)
	markSessionSummarized func(session *Session, t time.Time)
}

// NewContextSvc creates a new ContextSvc.
func NewContextSvc(qdrant *QdrantRegistry, embedr embed.Embedder, vectorSize *int, cfg Config, logger *slog.Logger, metrics *Metrics) *ContextSvc {
	return &ContextSvc{
		qdrant:     qdrant,
		embed:      embedr,
		vectorSize: vectorSize,
		cfg:        cfg,
		logger:     logger,
		metrics:    metrics,
	}
}
