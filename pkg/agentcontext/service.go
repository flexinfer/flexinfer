package agentcontext

import (
	"context"
	"log/slog"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase/embed"
	"github.com/crb2nu/loom/pkg/httpclient"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const sessionsVectorSize = 4

// ServiceOption configures a Service.
type ServiceOption func(*Service)

// WithLogger sets the structured logger for the service.
func WithLogger(logger *slog.Logger) ServiceOption {
	return func(s *Service) { s.logger = logger }
}

// WithTracer sets the OpenTelemetry TracerProvider for the service.
func WithTracer(tp trace.TracerProvider) ServiceOption {
	return func(s *Service) { s.tracer = tp.Tracer("agentcontext") }
}

type Service struct {
	cfg     Config
	logger  *slog.Logger
	tracer  trace.Tracer
	metrics *Metrics

	qdrant *QdrantRegistry
	embed  embed.Embedder

	vectorSize int

	// Workflow orchestration
	workflowEngine *WorkflowEngine

	// Knowledge graph (with persistence)
	knowledgeGraph          *KnowledgeGraph
	persistedKnowledgeGraph *persistedGraph

	// Memory hierarchy (with persistence)
	memoryHierarchy          *MemoryHierarchy
	persistedMemoryHierarchy *persistedMemoryHierarchy

	// Workflow engine (with persistence)
	persistedWorkflowEngine *persistedWorkflowEngine

	// Domain sub-services
	presence  *PresenceSvc
	claims    *ClaimSvc
	worktrees *WorktreeSvc
	tasks     *TaskSvc
	sess      *SessionSvc

	// Context operations (entries, annotations, recall, search, summaries)
	ctxSvc *ContextSvc

	// Phase 2 domain extractions
	graph         *GraphSvc
	memory        *MemorySvc
	workflow      *WorkflowSvc
	sourceVersion *SourceVersionSvc
	handoffs      *HandoffSvc
	templates     *TemplateSvc

	// Nudge queue
	nudges *NudgeSvc

	// Background services
	compactionScheduler *CompactionScheduler
	memoryExporter      *MemoryExporter
	memoryImporter      *MemoryImporter
	bgCancel            context.CancelFunc
}

// Tracer returns the service's OTel tracer. Returns a noop tracer if none was configured.
func (s *Service) Tracer() trace.Tracer {
	return s.tracer
}

// OnPresenceEvent registers a callback invoked when an agent's presence
// state transitions (e.g., active → idle, idle → offline).
func (s *Service) OnPresenceEvent(fn func(eventType string, agentID string, oldStatus, newStatus PresenceStatus)) {
	s.presence.SetOnEvent(fn)
}

// AddNudge enqueues a nudge for the given agent, delivered on next heartbeat.
func (s *Service) AddNudge(agentID string, nudge *Nudge) { s.nudges.Add(agentID, nudge) }

// DrainNudges returns and clears all pending nudges for the given agent.
func (s *Service) DrainNudges(agentID string) []*Nudge { return s.nudges.Drain(agentID) }

// PendingNudgeCount returns the number of pending nudges for the given agent.
func (s *Service) PendingNudgeCount(agentID string) int { return s.nudges.PendingCount(agentID) }

func NewServiceFromEnv(opts ...ServiceOption) (*Service, error) {
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		return nil, err
	}

	hc := httpclient.NewDefault()

	// Select embedder based on provider configuration
	var embedder embed.Embedder
	switch cfg.EmbedProvider {
	case "flexinfer":
		baseURL := cfg.EmbedBaseURL
		if baseURL == "" || baseURL == "https://api.morphllm.com/v1" {
			baseURL = firstNonEmptyEnv([]string{"FLEXINFER_URL"}, "http://localhost:8080") + "/v1"
		}
		model := cfg.EmbedModel
		if model == "" || model == "morph-embedding-v3" {
			model = "BAAI/bge-large-en-v1.5"
		}
		embedder = embed.NewFlexInferClient(hc, baseURL, cfg.EmbedAPIKey, model)
	case "ollama":
		baseURL := cfg.EmbedBaseURL
		if baseURL == "" || baseURL == "https://api.morphllm.com/v1" {
			baseURL = "http://localhost:11434"
		}
		model := cfg.EmbedModel
		if model == "" || model == "morph-embedding-v3" {
			model = "nomic-embed-text"
		}
		embedder = embed.NewOllamaClient(hc, baseURL, model)
	case "dummy", "none":
		embedder = embed.NewDummyEmbedder(1)
	default:
		embedder = embed.NewMorphClient(hc, cfg.EmbedBaseURL, cfg.EmbedAPIKey, cfg.EmbedModel)
	}

	qdrantReg := NewQdrantRegistry(hc, cfg)

	svc := &Service{
		cfg:    cfg,
		qdrant: qdrantReg,
		embed:  embedder,

		nudges: NewNudgeSvc(),
	}

	// Best-effort: if the context collection already exists, remember its vector size
	// so we can avoid "unknown vector size" edge-cases on operations like share/summarize.
	if exists, size, err := svc.qdrant.Get(CollContext).GetCollectionVectorSize(context.Background()); err == nil && exists && size > 0 {
		svc.vectorSize = size
	}

	// Initialize workflow engine
	svc.workflowEngine = NewWorkflowEngine(nil) // Tool executor set by daemon

	// Initialize knowledge graph with persistence
	svc.knowledgeGraph = NewKnowledgeGraph()
	svc.persistedKnowledgeGraph = svc.knowledgeGraph.SetPersistence(&GraphPersistenceConfig{
		EntitiesQdrant:  svc.qdrant.Get(CollGraphEntities),
		RelationsQdrant: svc.qdrant.Get(CollGraphRelations),
		EmbedModel:      cfg.EmbedModel,
		VectorSize:      svc.vectorSize,
	})

	// Initialize memory hierarchy with persistence
	svc.memoryHierarchy = NewMemoryHierarchy()
	svc.persistedMemoryHierarchy = svc.memoryHierarchy.SetPersistence(&MemoryPersistenceConfig{
		MemoryQdrant: svc.qdrant.Get(CollMemory),
		EmbedModel:   cfg.EmbedModel,
		VectorSize:   svc.vectorSize,
	})

	// Initialize workflow engine with persistence
	svc.persistedWorkflowEngine = svc.workflowEngine.SetPersistence(&WorkflowPersistenceConfig{
		WorkflowsQdrant:    svc.qdrant.Get(CollWorkflows),
		WorkflowDefsQdrant: svc.qdrant.Get(CollWorkflowDefs),
	})

	// Apply functional options and set defaults.
	for _, opt := range opts {
		opt(svc)
	}
	if svc.logger == nil {
		svc.logger = slog.Default()
	}
	if svc.tracer == nil {
		svc.tracer = noop.NewTracerProvider().Tracer("agentcontext")
	}
	svc.metrics = GetMetrics()

	// Initialize domain sub-services
	svc.presence = NewPresenceSvc(qdrantReg.Get(CollPresence), cfg, svc.logger, svc.metrics)
	svc.claims = NewClaimSvc(qdrantReg.Get(CollFileClaims), svc.logger, svc.metrics)
	svc.worktrees = NewWorktreeSvc(qdrantReg.Get(CollWorktree), cfg, svc.logger, svc.metrics)
	svc.tasks = NewTaskSvc(qdrantReg.Get(CollTasks), svc.embed, cfg, svc.logger, &svc.vectorSize)

	// Wire cross-domain callbacks for presence cleanup
	svc.presence.releaseClaimsForAgent = func(agentID string) {
		svc.claims.ReleaseAllForAgent(agentID)
	}
	svc.presence.orphanWorktrees = svc.orphanWorktreesForAgent
	svc.presence.endSessionsForAgent = svc.endActiveSessionsForAgent
	svc.presence.detectConflicts = func(agentID string, files []string) []map[string]any {
		conflicts := svc.presence.DetectActiveFileConflicts(agentID, files)
		conflicts = append(conflicts, svc.claims.DetectConflicts(agentID, files)...)
		return conflicts
	}

	// Wire worktree ↔ presence callbacks
	svc.worktrees.setPresenceWorktreeID = svc.presence.SetWorktreeID
	svc.worktrees.clearPresenceWorktreeID = svc.presence.ClearWorktreeID

	// Initialize session sub-service
	svc.sess = NewSessionSvc(qdrantReg.Get(CollSessions), cfg, svc.logger, svc.metrics)

	// Wire session cleanup callbacks
	svc.sess.releaseClaimsForAgent = func(agentID string) int {
		return svc.claims.ReleaseAllForAgent(agentID)
	}
	svc.sess.removePresence = svc.presence.Remove
	svc.sess.deletePresenceFromQdrant = func(ctx context.Context, agentID string) error {
		if svc.qdrant.Get(CollPresence) == nil {
			return nil
		}
		return svc.qdrant.Get(CollPresence).DeleteByFilter(ctx, FilterMust(Match("agent_id", agentID)))
	}
	svc.sess.orphanWorktrees = svc.orphanWorktreesForAgent
	svc.sess.markTasksStale = svc.markSessionTasksStale
	svc.sess.enrichResult = svc.enrichSessionStartResult
	svc.sess.liveAgentIDs = svc.presence.LiveAgentIDs

	// Initialize context sub-service
	svc.ctxSvc = NewContextSvc(svc.qdrant, svc.embed, &svc.vectorSize, cfg, svc.logger, svc.metrics)
	svc.ctxSvc.persistedMemoryHierarchy = svc.persistedMemoryHierarchy
	svc.ctxSvc.knowledgeGraph = svc.knowledgeGraph
	svc.ctxSvc.getSession = svc.getSession
	svc.ctxSvc.persistSession = svc.persistSession
	svc.ctxSvc.getActiveTasks = svc.getActiveTasks
	svc.ctxSvc.addSessionEntryStats = func(session *Session, entries int, tokens int) {
		svc.sess.mu.Lock()
		session.EntryCount += entries
		session.TotalTokens += tokens
		svc.sess.mu.Unlock()
	}
	svc.ctxSvc.readSessionStats = func(session *Session) (int, int, *time.Time) {
		svc.sess.mu.RLock()
		defer svc.sess.mu.RUnlock()
		return session.EntryCount, session.TotalTokens, session.LastSummaryAt
	}
	svc.ctxSvc.markSessionSummarized = func(session *Session, t time.Time) {
		svc.sess.mu.Lock()
		session.LastSummaryAt = &t
		svc.sess.mu.Unlock()
	}

	// Initialize phase-2 domain sub-services.
	svc.graph = &GraphSvc{Service: svc}
	svc.memory = &MemorySvc{Service: svc}
	svc.workflow = &WorkflowSvc{Service: svc}
	svc.sourceVersion = &SourceVersionSvc{Service: svc}
	svc.handoffs = &HandoffSvc{Service: svc}
	svc.templates = &TemplateSvc{Service: svc}

	// Wire session summary callbacks to ContextSvc
	svc.sess.generateSummary = svc.ctxSvc.GenerateSummary
	svc.sess.runSummaryAsync = svc.runSessionSummaryAsync

	// Wire task callbacks
	svc.tasks.getSession = svc.getSession
	svc.tasks.upsertBatched = svc.ctxSvc.upsertBatched

	// Initialize compaction scheduler
	compactionConfig := DefaultCompactionConfig()
	compactionConfig.Enabled = cfg.CompactionEnabled
	if cfg.CompactionCheckInterval > 0 {
		compactionConfig.CheckInterval = time.Duration(cfg.CompactionCheckInterval) * time.Second
	}
	svc.compactionScheduler = NewCompactionScheduler(compactionConfig, svc.memoryHierarchy, nil, svc.logger)
	svc.compactionScheduler.SetPersistence(svc.persistedMemoryHierarchy)

	// Initialize task reconciler
	reconcilerConfig := DefaultTaskReconcilerConfig()
	reconcilerConfig.Enabled = cfg.TaskReconcilerEnabled
	if cfg.TaskReconcilerInterval > 0 {
		reconcilerConfig.CheckInterval = time.Duration(cfg.TaskReconcilerInterval) * time.Second
	}
	if cfg.TaskReconcilerCompletedRetention > 0 {
		reconcilerConfig.CompletedRetention = time.Duration(cfg.TaskReconcilerCompletedRetention) * time.Hour
	}
	if cfg.TaskReconcilerStaleTimeout > 0 {
		reconcilerConfig.StaleTimeout = time.Duration(cfg.TaskReconcilerStaleTimeout) * time.Hour
	}
	svc.tasks.reconciler = NewTaskReconciler(reconcilerConfig, svc.tasks, svc.logger)
	svc.tasks.reconciler.getSession = svc.getSession

	// Initialize worktree reconciler (stored on WorktreeSvc)
	wtReconcilerConfig := DefaultWorktreeReconcilerConfig()
	wtReconcilerConfig.Enabled = cfg.WorktreeReconcilerEnabled
	if cfg.WorktreeReconcilerInterval > 0 {
		wtReconcilerConfig.CheckInterval = time.Duration(cfg.WorktreeReconcilerInterval) * time.Second
	}
	if cfg.WorktreeOrphanGracePeriod > 0 {
		wtReconcilerConfig.OrphanGracePeriod = time.Duration(cfg.WorktreeOrphanGracePeriod) * time.Minute
	}
	wtReconcilerConfig.MaxTTLHours = cfg.WorktreeMaxTTLHours
	wtReconcilerConfig.ArtifactCleanupEnabled = cfg.WorktreeArtifactCleanupEnabled
	if cfg.WorktreeArtifactCleanupPatterns != "" {
		wtReconcilerConfig.ArtifactPatterns = parseArtifactPatterns(cfg.WorktreeArtifactCleanupPatterns)
	}
	wtReconcilerConfig.DiskScanEnabled = cfg.WorktreeDiskScanEnabled
	wtReconcilerConfig.DetectUntracked = cfg.WorktreeDetectUntracked
	svc.worktrees.reconciler = NewWorktreeReconciler(wtReconcilerConfig, svc.worktrees, svc.logger)

	// Initialize memory exporter/importer
	svc.memoryExporter = NewMemoryExporter(svc.memoryHierarchy, svc.knowledgeGraph, svc.workflowEngine)
	svc.memoryImporter = NewMemoryImporter(svc.memoryHierarchy, svc.knowledgeGraph, svc.workflowEngine)

	// Load persisted state on startup (best-effort)
	ctx := context.Background()
	if err := svc.loadPersistedState(ctx); err != nil {
		svc.logger.Warn("failed to load persisted state", "error", err)
	}

	return svc, nil
}

// loadPersistedState loads all persisted data from Qdrant on startup
func (s *Service) loadPersistedState(ctx context.Context) error {
	// Load sessions
	if err := s.sess.LoadFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load sessions", "error", err)
	}

	// Load knowledge graph
	if err := s.persistedKnowledgeGraph.LoadGraphFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load knowledge graph", "error", err)
	}
	if err := s.persistedKnowledgeGraph.LoadReasoningChainsFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load reasoning chains", "error", err)
	}

	// Load memory hierarchy
	if err := s.persistedMemoryHierarchy.LoadMemoryFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load memory hierarchy", "error", err)
	}

	// Load workflows and definitions
	if err := s.persistedWorkflowEngine.LoadWorkflowsFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load workflows", "error", err)
	}
	if err := s.persistedWorkflowEngine.LoadDefinitionsFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load workflow definitions", "error", err)
	}

	// Load presence registry
	if err := s.presence.LoadFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load presence", "error", err)
	}

	// Load file claims
	if err := s.claims.LoadFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load file claims", "error", err)
	}

	// Load worktree assignments
	if err := s.worktrees.LoadFromQdrant(ctx); err != nil {
		s.logger.Warn("failed to load worktree assignments", "error", err)
	}

	return nil
}

// StartBackgroundServices starts background goroutines (compaction, presence cleanup)
func (s *Service) StartBackgroundServices(ctx context.Context) {
	bgCtx, cancel := context.WithCancel(ctx)
	s.bgCancel = cancel

	s.logger.Info("starting background services",
		"compaction_enabled", s.cfg.CompactionEnabled,
		"task_reconciler_enabled", s.cfg.TaskReconcilerEnabled,
		"worktree_reconciler_enabled", s.cfg.WorktreeReconcilerEnabled,
		"session_reaper_enabled", s.cfg.SessionReaperEnabled,
		"presence_cleanup_interval_s", s.cfg.PresenceCleanupInterval,
	)

	// Start compaction scheduler
	if s.compactionScheduler != nil && s.cfg.CompactionEnabled {
		if err := s.compactionScheduler.Start(bgCtx); err != nil {
			s.logger.Warn("failed to start compaction scheduler", "error", err)
		}
	}

	// Start task reconciler
	if s.cfg.TaskReconcilerEnabled {
		s.tasks.StartReconciler(bgCtx)
	}

	// Start worktree reconciler
	if s.cfg.WorktreeReconcilerEnabled {
		s.worktrees.StartReconciler(bgCtx)
	}

	// Start presence cleanup goroutine
	go s.presence.RunCleanup(bgCtx)

	// Start session reaper
	if s.cfg.SessionReaperEnabled {
		s.logger.Info("starting session reaper",
			"interval_s", s.cfg.SessionReaperInterval,
			"max_age_hours", s.cfg.SessionReaperMaxAge,
		)
		go s.sess.RunReaper(bgCtx)
	}
}

// StopBackgroundServices stops all background goroutines
func (s *Service) StopBackgroundServices() {
	s.logger.Info("stopping background services")
	if s.compactionScheduler != nil {
		s.compactionScheduler.Stop()
	}
	s.tasks.StopReconciler()
	s.worktrees.StopReconciler()
	if s.bgCancel != nil {
		s.bgCancel()
	}
}

// Session Handlers — thin delegation to SessionSvc.

func (s *Service) HandleSessionStart(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.sess.Start(ctx, args)
}

func (s *Service) HandleSessionEnd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.sess.End(ctx, args)
}

func (s *Service) HandleSessionList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.sess.List(ctx, args)
}

func (s *Service) HandleSessionDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.sess.Delete(ctx, args)
}

func (s *Service) HandleSessionPrune(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.sess.Prune(ctx, args)
}

// enrichSessionStartResult adds coordination info (pending handoffs, active agents).
// Stays on Service because it accesses CollHandoffs (not owned by SessionSvc).
func (s *Service) enrichSessionStartResult(ctx context.Context, result map[string]any, agentID, namespace string) {
	result["active_agents"] = len(s.presence.LiveAgentIDs())

	now := time.Now()
	var pendingHandoffs []map[string]any
	if s.qdrant.Get(CollHandoffs) != nil {
		conds := []any{
			Match("target_agent_id", agentID),
			Match("status", string(HandoffStatusPending)),
		}
		points, err := s.qdrant.Get(CollHandoffs).ScrollPoints(ctx, FilterMust(conds...), 50, false)
		if err == nil {
			for _, p := range points {
				h, err := payloadToHandoff(p.Payload)
				if err != nil || h == nil {
					continue
				}
				if h.ExpiresAt != nil && now.After(*h.ExpiresAt) {
					continue
				}
				pendingHandoffs = append(pendingHandoffs, map[string]any{
					"handoff_id":   h.ID,
					"source_agent": h.SourceAgentID,
					"instructions": h.Instructions,
					"summary":      h.Summary,
					"created_at":   h.CreatedAt.Format(time.RFC3339),
				})
			}
		}
	}
	result["pending_handoffs"] = pendingHandoffs
}

// runSessionSummaryAsync performs end-of-session summarization in background.
func (s *Service) runSessionSummaryAsync(session *Session) {
	bg, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := s.ctxSvc.GenerateSummary(bg, session); err != nil {
		s.logger.Warn("async session summarize failed",
			"session_id", session.ID,
			"agent_id", session.AgentID,
			"error", err,
		)
		return
	}

	session.Status = string(SessionStatusSummarized)
	if err := s.sess.Persist(bg, session); err != nil {
		s.logger.Warn("async session summarize persist failed",
			"session_id", session.ID,
			"agent_id", session.AgentID,
			"error", err,
		)
	}
}

// endActiveSessionsForAgent delegates to SessionSvc.
func (s *Service) endActiveSessionsForAgent(ctx context.Context, agentID string) {
	s.sess.EndActiveForAgent(ctx, agentID)
}

// getSession delegates to SessionSvc.Get.
func (s *Service) getSession(ctx context.Context, sessionID string) (*Session, error) {
	return s.sess.Get(ctx, sessionID)
}

// persistSession delegates to SessionSvc.Persist.
func (s *Service) persistSession(ctx context.Context, session *Session) error {
	return s.sess.Persist(ctx, session)
}

// Context handlers — thin delegation to ContextSvc.

func (s *Service) HandleContextAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.Add(ctx, args)
}

func (s *Service) HandleContextGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.Get(ctx, args)
}

func (s *Service) HandleContextDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.Delete(ctx, args)
}

func (s *Service) HandleContextSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.Search(ctx, args)
}

func (s *Service) HandleContextRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.Recall(ctx, args)
}

func (s *Service) HandleContextShare(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.Share(ctx, args)
}

func (s *Service) HandleContextQueryShared(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.QueryShared(ctx, args)
}

func (s *Service) HandleContextSummarize(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.Summarize(ctx, args)
}

func (s *Service) HandleContextStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.Stats(ctx, args)
}

func (s *Service) HandleContextLinkCodebase(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.LinkCodebase(ctx, args)
}

func (s *Service) HandleEnhancedRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.EnhancedRecall(ctx, args)
}

func (s *Service) HandleAnnotationAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.AnnotationAdd(ctx, args)
}

func (s *Service) HandleAnnotationsGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.AnnotationsGet(ctx, args)
}
